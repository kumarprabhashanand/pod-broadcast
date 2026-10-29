package preset

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
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

func pod(name, basePath string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if basePath != "" {
		p.Annotations = map[string]string{target.AnnotationActuatorBasePath: basePath}
	}
	return p
}

func TestApplyBasePath(t *testing.T) {
	p, _ := Find("spring", "health")
	call, _ := p.Build(nil, Options{})
	eps := []target.Endpoint{
		{Pod: pod("plain", "")},
		{Pod: pod("custom", "/manage")},
		{Pod: pod("same", "/actuator/")},
		{Pod: pod("bad", "manage")},
	}
	call.ApplyBasePath(eps)
	if eps[0].Path != "" || eps[1].Path != "/manage/health" || eps[2].Path != "" || eps[3].Err == nil {
		t.Errorf("endpoints = %+v", eps)
	}

	flagged, _ := p.Build(nil, Options{BasePath: "/ops"})
	eps = []target.Endpoint{{Pod: pod("custom", "/manage")}}
	flagged.ApplyBasePath(eps)
	if eps[0].Path != "" || flagged.Spec.Path != "/ops/health" {
		t.Errorf("--base-path should win: %+v, spec path %q", eps, flagged.Spec.Path)
	}
}

func TestUsage(t *testing.T) {
	want := map[string]string{
		"evict-caches":  "spring evict-caches [--cache NAME]",
		"set-log-level": "spring set-log-level LOGGER LEVEL",
		"health":        "spring health",
	}
	for name, usage := range want {
		p, _ := Find("spring", name)
		if p.Usage() != usage {
			t.Errorf("Usage() = %q, want %q", p.Usage(), usage)
		}
	}
}
