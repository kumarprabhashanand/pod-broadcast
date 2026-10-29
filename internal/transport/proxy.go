package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// MaxBody is how much of a response is read.
const MaxBody = 1 << 20

// ProxyMethods are the methods the API server's pod proxy passes on.
var ProxyMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// ForbiddenError means the caller may not use the pod proxy for this method.
type ForbiddenError struct{ Message string }

func (e *ForbiddenError) Error() string { return e.Message }

// Proxy calls pods through the API server's pod proxy with the caller's credentials.
type Proxy struct {
	client    *http.Client
	base      string
	namespace string
}

func NewProxy(config *rest.Config, namespace string) (*Proxy, error) {
	shared, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, err
	}
	// HTTPClientFor may return http.DefaultClient, so change a copy. Redirects are never followed:
	// a pod must not be able to send the call elsewhere.
	client := *shared
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, _, err := rest.DefaultServerUrlFor(config)
	if err != nil {
		return nil, err
	}
	return &Proxy{client: &client, base: strings.TrimSuffix(base.String(), "/"), namespace: namespace}, nil
}

// URL is the pod proxy URL for one pod. Named ports must already be resolved to numbers.
func (p *Proxy) URL(ep target.Endpoint, path string) string {
	pod := ep.Pod.Name + ":" + strconv.Itoa(int(ep.Port))
	if ep.Scheme == "https" {
		pod = "https:" + pod
	}
	return p.base + "/api/v1/namespaces/" + p.namespace + "/pods/" + pod + "/proxy" + path
}

func (p *Proxy) Do(ctx context.Context, ep target.Endpoint, spec request.Spec) (request.Response, error) {
	path := spec.Path
	if ep.Path != "" {
		path = ep.Path
	}
	req, err := http.NewRequestWithContext(ctx, spec.Method, p.URL(ep, path), bytes.NewReader(spec.Body))
	if err != nil {
		return request.Response{}, &request.FatalError{Err: err}
	}
	for _, h := range spec.Headers {
		if strings.EqualFold(h.Name, "Host") {
			req.Host = h.Value
			continue
		}
		req.Header.Set(h.Name, h.Value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return request.Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return request.Response{}, err
	}
	if err := apiServerError(resp, body, spec.Method); err != nil {
		return request.Response{}, err
	}
	return request.Response{Code: resp.StatusCode, Body: body}, nil
}

// apiServerError recognizes responses written by the API server itself rather than by the pod.
func apiServerError(resp *http.Response, body []byte, method string) error {
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusBadGateway, http.StatusServiceUnavailable:
	default:
		return nil
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	var st metav1.Status
	if json.Unmarshal(body, &st) != nil || st.Kind != "Status" || st.APIVersion != "v1" {
		return nil
	}
	podDetails := st.Details != nil && st.Details.Kind == "pods"
	switch {
	case st.Reason == metav1.StatusReasonForbidden && podDetails:
		return &request.FatalError{Err: &ForbiddenError{Message: st.Message}}
	case st.Reason == metav1.StatusReasonNotFound && podDetails:
		return request.ErrGone
	case st.Reason == metav1.StatusReasonMethodNotAllowed:
		return &request.FatalError{Err: fmt.Errorf("the API server's pod proxy refused method %s", method)}
	case strings.HasPrefix(st.Message, "error trying to reach service: "):
		return errors.New(strings.TrimPrefix(st.Message, "error trying to reach service: "))
	}
	return nil
}
