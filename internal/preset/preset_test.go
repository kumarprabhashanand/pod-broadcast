package preset

import (
	"reflect"
	"testing"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
)

var jsonType = []request.Header{{Name: "Content-Type", Value: "application/json"}}

func TestPresetRequests(t *testing.T) {
	tests := []struct {
		preset string
		args   []string
		opts   Options
		want   request.Spec
	}{
		{"evict-caches", nil, Options{}, request.Spec{Method: "DELETE", Path: "/actuator/caches"}},
		{"evict-caches", nil, Options{Cache: "countries"}, request.Spec{Method: "DELETE", Path: "/actuator/caches/countries"}},
		{"evict-caches", nil, Options{Cache: "a b/c"}, request.Spec{Method: "DELETE", Path: "/actuator/caches/a%20b%2Fc"}},
		{"set-log-level", []string{"com.shop.orders", "debug"}, Options{}, request.Spec{Method: "POST", Path: "/actuator/loggers/com.shop.orders", Headers: jsonType, Body: []byte(`{"configuredLevel":"DEBUG"}`)}},
		{"set-log-level", []string{"ROOT", "WARN"}, Options{BasePath: "/manage/"}, request.Spec{Method: "POST", Path: "/manage/loggers/ROOT", Headers: jsonType, Body: []byte(`{"configuredLevel":"WARN"}`)}},
		{"reset-log-level", []string{"com.shop.orders"}, Options{}, request.Spec{Method: "POST", Path: "/actuator/loggers/com.shop.orders", Headers: jsonType, Body: []byte(`{}`)}},
		{"refresh", nil, Options{}, request.Spec{Method: "POST", Path: "/actuator/refresh"}},
		{"health", nil, Options{}, request.Spec{Method: "GET", Path: "/actuator/health"}},
		{"info", nil, Options{BasePath: "/"}, request.Spec{Method: "GET", Path: "/info"}},
	}
	for _, tt := range tests {
		t.Run(tt.preset, func(t *testing.T) {
			p, err := Find("spring", tt.preset)
			if err != nil {
				t.Fatal(err)
			}
			call, err := p.Build(tt.args, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(call.Spec, tt.want) {
				t.Errorf("spec = %+v\nwant   %+v", call.Spec, tt.want)
			}
			if call.Port != "management" {
				t.Errorf("port = %q, want management", call.Port)
			}
		})
	}
}

func TestSetLogLevelIsRevertedByResettingIt(t *testing.T) {
	set, _ := Find("spring", "set-log-level")
	reset, _ := Find("spring", "reset-log-level")
	call, err := set.Build([]string{"com.shop", "DEBUG"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := reset.Build([]string{"com.shop"}, Options{})
	if call.Revert == nil || !reflect.DeepEqual(*call.Revert, want.Spec) {
		t.Errorf("revert = %+v, want %+v", call.Revert, want.Spec)
	}
	if want.Revert != nil {
		t.Errorf("reset-log-level has a revert: %+v", want.Revert)
	}
}

func TestPresetErrors(t *testing.T) {
	tests := []struct {
		preset string
		args   []string
		opts   Options
	}{
		{"health", []string{"extra"}, Options{}},
		{"set-log-level", []string{"com.shop"}, Options{}},
		{"set-log-level", []string{"com.shop", "LOUD"}, Options{}},
		{"set-log-level", []string{"com/shop", "DEBUG"}, Options{}},
		{"set-log-level", []string{"", "DEBUG"}, Options{}},
		{"health", nil, Options{Cache: "x"}},
		{"health", nil, Options{BasePath: "actuator"}},
		{"health", nil, Options{BasePath: "/a?b"}},
	}
	for _, tt := range tests {
		p, err := Find("spring", tt.preset)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Build(tt.args, tt.opts); err == nil {
			t.Errorf("%s %v %+v: want error", tt.preset, tt.args, tt.opts)
		}
	}
	if _, err := Find("spring", "shutdown"); err == nil {
		t.Error("Find(spring shutdown): want error")
	}
}
