package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// MaxBody is how much of a response is read.
const MaxBody = 1 << 20

// Direct calls pods by IP, as the controller does from inside the cluster.
type Direct struct {
	client *http.Client
}

// NewDirect returns a transport that, like the API server's pod proxy, doesn't verify pod
// certificates: they rarely name the pod's IP.
func NewDirect() *Direct {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &Direct{client: noRedirects(http.Client{Transport: t})}
}

func (d *Direct) Do(ctx context.Context, ep target.Endpoint, spec request.Spec) (request.Response, error) {
	addr := net.JoinHostPort(ep.Pod.Status.PodIP, strconv.Itoa(int(ep.Port)))
	resp, err := send(ctx, d.client, ep.Scheme+"://"+addr+spec.Path, spec)
	return resp.Response, err
}

// noRedirects stops a pod from sending the call elsewhere.
func noRedirects(c http.Client) *http.Client {
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

type response struct {
	request.Response
	header http.Header
}

// send makes one request and reads at most MaxBody of the response.
func send(ctx context.Context, c *http.Client, rawURL string, spec request.Spec) (response, error) {
	req, err := http.NewRequestWithContext(ctx, spec.Method, rawURL, bytes.NewReader(spec.Body))
	if err != nil {
		return response{}, &request.FatalError{Err: err}
	}
	for _, h := range spec.Headers {
		if strings.EqualFold(h.Name, "Host") {
			req.Host = h.Value
			continue
		}
		req.Header.Set(h.Name, h.Value)
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return response{}, err
	}
	return response{Response: request.Response{Code: resp.StatusCode, Body: body}, header: resp.Header}, nil
}
