package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// ProxyMethods are the methods direct mode sends: those the API server's pod proxy passes on and
// maps to an RBAC verb. Other methods, such as OPTIONS, map to no verb, so only a "*" rule allows them.
var ProxyMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
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
	base, _, err := rest.DefaultServerUrlFor(config)
	if err != nil {
		return nil, err
	}
	// HTTPClientFor may return http.DefaultClient, so noRedirects changes a copy.
	return &Proxy{client: noRedirects(*shared), base: strings.TrimSuffix(base.String(), "/"), namespace: namespace}, nil
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
	resp, err := send(ctx, p.client, p.URL(ep, spec.Path), spec)
	if err == nil {
		err = apiServerError(resp, spec.Method)
	}
	if err != nil {
		return request.Response{}, err
	}
	return resp.Response, nil
}

// apiServerError recognizes responses written by the API server itself rather than by the pod.
func apiServerError(resp response, method string) error {
	switch resp.Code {
	case http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusBadGateway, http.StatusServiceUnavailable:
	default:
		return nil
	}
	if !strings.HasPrefix(resp.header.Get("Content-Type"), "application/json") {
		return nil
	}
	var st metav1.Status
	if json.Unmarshal(resp.Body, &st) != nil || st.Kind != "Status" || st.APIVersion != "v1" {
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
