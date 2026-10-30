package policy

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

func broadcast(mutate ...func(*v1alpha1.BroadcastSpec)) *v1alpha1.Broadcast {
	b := &v1alpha1.Broadcast{Spec: v1alpha1.BroadcastSpec{
		Target:    v1alpha1.Target{Service: "orders"},
		Request:   v1alpha1.Request{Method: "DELETE", Path: "/actuator/caches"},
		Mode:      v1alpha1.Once,
		Execution: v1alpha1.Execution{Concurrency: 10, Retries: new(int32(2))},
	}}
	for _, m := range mutate {
		m(&b.Spec)
	}
	return b
}

func continuous(s *v1alpha1.BroadcastSpec) {
	s.Mode = v1alpha1.Continuous
	s.Request = v1alpha1.Request{Method: "POST", Path: "/actuator/loggers/com.shop"}
	s.Revert = &v1alpha1.Revert{Method: "POST", Path: "/actuator/loggers/com.shop"}
}

func allow(targets string, paths ...string) v1alpha1.Rule {
	return v1alpha1.Rule{
		Targets: strings.Split(targets, ","),
		Ports:   []intstr.IntOrString{intstr.FromString("management")},
		Methods: []string{"post", "DELETE"},
		Paths:   paths,
	}
}

func policy(rules []v1alpha1.Rule, mutate ...func(*v1alpha1.BroadcastPolicySpec)) v1alpha1.BroadcastPolicy {
	p := v1alpha1.BroadcastPolicy{Spec: v1alpha1.BroadcastPolicySpec{Allow: rules}}
	for _, m := range mutate {
		m(&p.Spec)
	}
	return p
}

var actuator = []v1alpha1.Rule{allow("svc/orders,deploy/orders", `/actuator/(caches|loggers)(/.*)?`)}

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		policies []v1alpha1.BroadcastPolicy
		b        *v1alpha1.Broadcast
		wantErr  string
	}{
		{name: "allowed", policies: []v1alpha1.BroadcastPolicy{policy(actuator)}, b: broadcast()},
		{name: "allowed with revert", policies: []v1alpha1.BroadcastPolicy{policy(actuator)}, b: broadcast(continuous)},
		{name: "any target", policies: []v1alpha1.BroadcastPolicy{policy([]v1alpha1.Rule{allow("*", "/.*")})},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) {
				s.Target = v1alpha1.Target{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders"}}}
			})},
		{name: "method not allowed", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.Method = "PUT" }), wantErr: "no BroadcastPolicy rule allows PUT"},
		{name: "path not allowed", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.Path = "/actuator/shutdown" }), wantErr: "no BroadcastPolicy rule allows"},
		{name: "path must match whole", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.Path = "/x/actuator/caches" }), wantErr: "no BroadcastPolicy rule allows"},
		{name: "other target", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) { s.Target.Service = "billing" }), wantErr: "on service/billing"},
		{name: "selector needs a wildcard", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) {
				s.Target = v1alpha1.Target{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "orders"}}}
			}), wantErr: "no BroadcastPolicy rule allows"},
		{name: "revert not allowed", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(continuous, func(s *v1alpha1.BroadcastSpec) { s.Revert.Path = "/admin" }), wantErr: "the revert POST /admin"},
		{name: "rules match target, method and path together",
			policies: []v1alpha1.BroadcastPolicy{policy([]v1alpha1.Rule{allow("svc/orders", "/a"), allow("svc/billing", "/actuator/caches")})},
			b:        broadcast(), wantErr: "no BroadcastPolicy rule allows"},
		{name: "invalid pattern", policies: []v1alpha1.BroadcastPolicy{policy([]v1alpha1.Rule{allow("svc/orders", "(")})},
			b: broadcast(), wantErr: "BroadcastPolicy path"},
		{name: "concurrency over the strictest limit",
			policies: []v1alpha1.BroadcastPolicy{
				policy(actuator, func(p *v1alpha1.BroadcastPolicySpec) { p.Limits.MaxConcurrency = new(int32(20)) }),
				policy(nil, func(p *v1alpha1.BroadcastPolicySpec) { p.Limits.MaxConcurrency = new(int32(5)) }),
			},
			b: broadcast(), wantErr: "concurrency 10 is above the policy limit 5"},
		{name: "retries over the limit",
			policies: []v1alpha1.BroadcastPolicy{policy(actuator, func(p *v1alpha1.BroadcastPolicySpec) { p.Limits.MaxRetries = new(int32(1)) })},
			b:        broadcast(), wantErr: "retries 2 is above the policy limit 1"},
		{name: "secret listed",
			policies: []v1alpha1.BroadcastPolicy{policy(actuator, func(p *v1alpha1.BroadcastPolicySpec) { p.Secrets = []string{"auth"} })},
			b:        broadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.HeadersFromSecrets = []string{"auth"} })},
		{name: "secret not listed", policies: []v1alpha1.BroadcastPolicy{policy(actuator)},
			b: broadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.HeadersFromSecrets = []string{"auth"} }), wantErr: "the Secret auth isn't listed"},
		{name: "duration over the policy cap",
			policies: []v1alpha1.BroadcastPolicy{policy(actuator, func(p *v1alpha1.BroadcastPolicySpec) { p.Limits.MaxDuration = &metav1.Duration{Duration: time.Hour} })},
			b:        broadcast(continuous, func(s *v1alpha1.BroadcastSpec) { s.Duration = &metav1.Duration{Duration: 2 * time.Hour} }),
			wantErr:  "duration 2h0m0s is longer than the limit 1h0m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Check(tt.policies, tt.b, true, 24*time.Hour)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Check: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Check error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestCheckWithoutPolicies(t *testing.T) {
	if _, err := Check(nil, broadcast(), true, 24*time.Hour); err == nil {
		t.Error("required policy missing: want an error")
	}
	d, err := Check(nil, broadcast(continuous), false, 24*time.Hour)
	if err != nil || d.Duration != time.Hour || !d.AllowsPort(&corev1.Pod{}, 9999) {
		t.Errorf("no policy, not required: %+v, %v; want allowed, 1h", d, err)
	}
	if _, err := Check(nil, broadcast(continuous, func(s *v1alpha1.BroadcastSpec) { s.Duration = &metav1.Duration{Duration: 48 * time.Hour} }), false, 24*time.Hour); err == nil {
		t.Error("duration over --max-duration: want an error")
	}
}

func TestDefaultDuration(t *testing.T) {
	policies := []v1alpha1.BroadcastPolicy{
		policy(actuator, func(p *v1alpha1.BroadcastPolicySpec) {
			p.Limits.DefaultDuration = &metav1.Duration{Duration: 30 * time.Minute}
		}),
		policy(nil, func(p *v1alpha1.BroadcastPolicySpec) {
			p.Limits.DefaultDuration = &metav1.Duration{Duration: 10 * time.Minute}
		}),
	}
	d, err := Check(policies, broadcast(continuous), true, 24*time.Hour)
	if err != nil || d.Duration != 10*time.Minute {
		t.Errorf("duration = %s, %v; want the smallest default, 10m", d.Duration, err)
	}
}

func TestAllowsPortByNumberOnThePod(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Ports: []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080}, {Name: "management", ContainerPort: 8081},
	}}}}}
	d, err := Check([]v1alpha1.BroadcastPolicy{policy(actuator)}, broadcast(continuous), true, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !d.AllowsPort(pod, 8081) || d.AllowsPort(pod, 8080) {
		t.Errorf("AllowsPort: want 8081 (management) allowed and 8080 refused")
	}
}
