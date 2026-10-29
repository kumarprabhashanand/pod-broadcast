package request

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/net/http/httpguts"
	corev1 "k8s.io/api/core/v1"
)

// Spec is the request sent to every pod.
type Spec struct {
	Method  string
	Path    string
	Headers []Header
	Body    []byte
}

// Header is one request header. Secret values never appear in output.
type Header struct {
	Name   string
	Value  string
	Secret bool
}

const redacted = "<redacted>"

// Display returns the value to show to users.
func (h Header) Display() string {
	if h.Secret || sensitive(h.Name) {
		return redacted
	}
	return h.Value
}

func sensitive(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie":
		return true
	}
	for _, s := range []string{"token", "secret", "password", "api-key", "apikey"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// ParseHeader parses "Name: value".
func ParseHeader(s string) (Header, error) {
	name, value, ok := strings.Cut(s, ":")
	if !ok {
		return Header{}, fmt.Errorf("header %q must look like 'Name: value'", s)
	}
	return newHeader(strings.TrimSpace(name), strings.TrimSpace(value), false)
}

// EnvHeader parses "Name=VAR" and takes the value from the environment variable VAR.
func EnvHeader(s string, lookupEnv func(string) (string, bool)) (Header, error) {
	name, env, ok := strings.Cut(s, "=")
	if !ok || env == "" {
		return Header{}, fmt.Errorf("header from environment %q must look like Name=VAR", s)
	}
	value, ok := lookupEnv(env)
	if !ok || value == "" {
		return Header{}, fmt.Errorf("environment variable %s for header %s is not set", env, name)
	}
	return newHeader(name, value, true)
}

// SecretHeaders turns every key of secret into a header.
func SecretHeaders(secret *corev1.Secret) ([]Header, error) {
	headers := make([]Header, 0, len(secret.Data)+len(secret.StringData))
	for k, v := range secret.Data {
		h, err := newHeader(k, strings.TrimSpace(string(v)), true)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", secret.Name, err)
		}
		headers = append(headers, h)
	}
	slices.SortFunc(headers, func(a, b Header) int { return strings.Compare(a.Name, b.Name) })
	return headers, nil
}

func newHeader(name, value string, secret bool) (Header, error) {
	if !httpguts.ValidHeaderFieldName(name) {
		return Header{}, fmt.Errorf("invalid header name %q", name)
	}
	if !httpguts.ValidHeaderFieldValue(value) {
		return Header{}, fmt.Errorf("invalid value for header %s", name)
	}
	return Header{Name: http.CanonicalHeaderKey(name), Value: value, Secret: secret}, nil
}

// Merge combines header groups in order. A later header replaces an earlier one with the same name.
func Merge(groups ...[]Header) []Header {
	var merged []Header
	index := map[string]int{}
	for _, g := range groups {
		for _, h := range g {
			key := http.CanonicalHeaderKey(h.Name)
			if i, ok := index[key]; ok {
				merged[i] = h
				continue
			}
			index[key] = len(merged)
			merged = append(merged, h)
		}
	}
	return merged
}
