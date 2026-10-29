package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

func endpoint(scheme string) target.Endpoint {
	return target.Endpoint{Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orders-a"}}, Port: 8081, Scheme: scheme}
}

func newProxy(t *testing.T, h http.HandlerFunc) *Proxy {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := NewProxy(&rest.Config{Host: srv.URL + "/prefix"}, "shop")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProxyRequest(t *testing.T) {
	var got *http.Request
	var gotBody string
	p := newProxy(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("done"))
	})
	spec := request.Spec{
		Method:  "POST",
		Path:    "/actuator/loggers/com.shop?x=a%20b",
		Headers: []request.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "Host", Value: "orders.local"}},
		Body:    []byte(`{"configuredLevel":"DEBUG"}`),
	}
	resp, err := p.Do(context.Background(), endpoint("http"), spec)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != http.StatusAccepted || string(resp.Body) != "done" {
		t.Errorf("response = %d %q", resp.Code, resp.Body)
	}
	if got.Method != http.MethodPost || got.URL.EscapedPath() != "/prefix/api/v1/namespaces/shop/pods/orders-a:8081/proxy/actuator/loggers/com.shop" {
		t.Errorf("request = %s %s", got.Method, got.URL.EscapedPath())
	}
	if got.URL.RawQuery != "x=a%20b" {
		t.Errorf("query = %q", got.URL.RawQuery)
	}
	if got.Header.Get("Content-Type") != "application/json" || got.Host != "orders.local" || gotBody != `{"configuredLevel":"DEBUG"}` {
		t.Errorf("headers = %v, host = %q, body = %q", got.Header, got.Host, gotBody)
	}
}

func TestProxyURL(t *testing.T) {
	p := &Proxy{base: "https://api:6443", namespace: "shop"}
	if got := p.URL(endpoint("https"), "/x"); got != "https://api:6443/api/v1/namespaces/shop/pods/https:orders-a:8081/proxy/x" {
		t.Errorf("URL = %q", got)
	}
}

func TestProxyDoesNotFollowRedirects(t *testing.T) {
	p := newProxy(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
	})
	resp, err := p.Do(context.Background(), endpoint("http"), request.Spec{Method: "GET", Path: "/"})
	if err != nil || resp.Code != http.StatusFound {
		t.Errorf("got %d, %v; want the 302 itself", resp.Code, err)
	}
}

func TestProxyReadsAtMostMaxBody(t *testing.T) {
	p := newProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", MaxBody+100)))
	})
	resp, err := p.Do(context.Background(), endpoint("http"), request.Spec{Method: "GET", Path: "/"})
	if err != nil || len(resp.Body) != MaxBody {
		t.Errorf("body length = %d, %v; want %d", len(resp.Body), err, MaxBody)
	}
}

func status(code int32, reason metav1.StatusReason, message, detailsKind string) string {
	st := metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure, Message: message, Reason: reason, Code: code,
	}
	if detailsKind != "" {
		st.Details = &metav1.StatusDetails{Name: "orders-a:8081", Kind: detailsKind}
	}
	b, _ := json.Marshal(st)
	return string(b)
}

func TestProxyClassifiesAPIServerResponses(t *testing.T) {
	tests := []struct {
		name        string
		code        int
		contentType string
		body        string
		check       func(t *testing.T, resp request.Response, err error)
	}{
		{
			name: "forbidden", code: 403, contentType: "application/json",
			body: status(403, "Forbidden", `pods "orders-a:8081" is forbidden: User "u" cannot create resource "pods/proxy"`, "pods"),
			check: func(t *testing.T, _ request.Response, err error) {
				var fatal *request.FatalError
				var forbidden *ForbiddenError
				if !errors.As(err, &fatal) || !errors.As(err, &forbidden) || !strings.Contains(err.Error(), "cannot create resource") {
					t.Errorf("err = %v, want a fatal ForbiddenError", err)
				}
			},
		},
		{
			name: "pod gone", code: 404, contentType: "application/json",
			body: status(404, "NotFound", `pods "orders-a" not found`, "pods"),
			check: func(t *testing.T, _ request.Response, err error) {
				if !errors.Is(err, request.ErrGone) {
					t.Errorf("err = %v, want ErrGone", err)
				}
			},
		},
		{
			name: "method not allowed", code: 405, contentType: "application/json",
			body: status(405, "MethodNotAllowed", "the server does not allow this method on the requested resource", ""),
			check: func(t *testing.T, _ request.Response, err error) {
				var fatal *request.FatalError
				if !errors.As(err, &fatal) {
					t.Errorf("err = %v, want a FatalError", err)
				}
			},
		},
		{
			name: "pod unreachable", code: 503, contentType: "application/json",
			body: status(503, "ServiceUnavailable", "error trying to reach service: dial tcp 10.0.0.1:8081: connect: connection refused", ""),
			check: func(t *testing.T, _ request.Response, err error) {
				if err == nil || err.Error() != "dial tcp 10.0.0.1:8081: connect: connection refused" {
					t.Errorf("err = %v, want the dial error", err)
				}
			},
		},
		{
			name: "pod's own 404", code: 404, contentType: "text/plain", body: "no such page",
			check: func(t *testing.T, resp request.Response, err error) {
				if err != nil || resp.Code != 404 {
					t.Errorf("got %d, %v; want the pod's 404", resp.Code, err)
				}
			},
		},
		{
			name: "pod's own JSON 503", code: 503, contentType: "application/json", body: `{"status":"DOWN"}`,
			check: func(t *testing.T, resp request.Response, err error) {
				if err != nil || resp.Code != 503 {
					t.Errorf("got %d, %v; want the pod's 503", resp.Code, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newProxy(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			})
			resp, err := p.Do(context.Background(), endpoint("http"), request.Spec{Method: "POST", Path: "/"})
			tt.check(t, resp, err)
		})
	}
}
