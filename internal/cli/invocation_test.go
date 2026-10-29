package cli

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

var ordersSelector = labels.SelectorFromSet(labels.Set{"app": "orders"})

func TestParseInvocation(t *testing.T) {
	orders := target.Ref{Kind: target.KindService, Name: "orders"}
	tests := []struct {
		name     string
		args     []string
		selector string
		method   string
		want     invocation
		wantErr  bool
	}{
		{
			name:   "raw request on a service",
			args:   []string{"svc/orders", "/actuator/caches"},
			method: "DELETE",
			want:   invocation{target: orders, method: "DELETE", path: "/actuator/caches"},
		},
		{
			name:   "path keeps its query",
			args:   []string{"svc/orders", "/actuator/caches?name=countries"},
			method: "DELETE",
			want:   invocation{target: orders, method: "DELETE", path: "/actuator/caches?name=countries"},
		},
		{
			name: "preset with arguments",
			args: []string{"deploy/orders", "spring", "set-log-level", "com.shop", "DEBUG"},
			want: invocation{
				target: target.Ref{Kind: target.KindDeployment, Name: "orders"},
				preset: presetCall{group: "spring", name: "set-log-level", args: []string{"com.shop", "DEBUG"}},
			},
		},
		{
			name:     "selector with raw request",
			args:     []string{"/actuator/health"},
			selector: "app=orders",
			method:   "GET",
			want:     invocation{target: target.Ref{Selector: ordersSelector}, method: "GET", path: "/actuator/health"},
		},
		{
			name:     "selector with preset",
			args:     []string{"spring", "health"},
			selector: "app=orders",
			want: invocation{
				target: target.Ref{Selector: ordersSelector},
				preset: presetCall{group: "spring", name: "health", args: []string{}},
			},
		},
		{name: "target and selector together", args: []string{"svc/orders", "/x"}, selector: "app=orders", method: "GET", wantErr: true},
		{name: "first argument is not a target", args: []string{"orders", "/x"}, method: "GET", wantErr: true},
		{name: "unknown target kind", args: []string{"pod/orders-abc", "/x"}, method: "GET", wantErr: true},
		{name: "no operation", args: []string{"svc/orders"}, wantErr: true},
		{name: "preset group without preset", args: []string{"svc/orders", "spring"}, wantErr: true},
		{name: "method without path", args: []string{"svc/orders"}, method: "GET", wantErr: true},
		{name: "method with two paths", args: []string{"svc/orders", "/a", "/b"}, method: "GET", wantErr: true},
		{name: "path without leading slash", args: []string{"svc/orders", "actuator/caches"}, method: "DELETE", wantErr: true},
		{name: "selector without operation", selector: "app=orders", wantErr: true},
		{name: "invalid selector", args: []string{"/x"}, selector: "app in (orders", method: "GET", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInvocation(tt.args, tt.selector, tt.method)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseInvocation(%q) = %+v, want error", tt.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseInvocation(%q): %v", tt.args, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseInvocation(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
