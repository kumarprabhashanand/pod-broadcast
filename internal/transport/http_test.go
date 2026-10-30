package transport

import (
	"net/http"
	"testing"
)

func TestDirectIgnoresProxyVariables(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.example:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:3128")
	tr := NewDirect().client.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("the direct transport uses a proxy; pod calls must go straight to the pod")
	}
	if !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("the direct transport verifies pod certificates")
	}
}
