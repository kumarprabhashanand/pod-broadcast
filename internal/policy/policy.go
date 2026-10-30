// Package policy decides what BroadcastPolicies allow.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

const defaultDuration = time.Hour

// Decision is what a namespace's policies allow for one broadcast.
type Decision struct {
	// Duration of a Continuous broadcast after defaults and caps.
	Duration time.Duration

	restricted bool
	request    []v1alpha1.Rule
	revert     []v1alpha1.Rule
}

// Check returns an error when policies don't allow b. Without policies everything is allowed,
// unless requirePolicy is set. maxDuration caps Continuous broadcasts when no policy does.
func Check(policies []v1alpha1.BroadcastPolicy, b *v1alpha1.Broadcast, requirePolicy bool, maxDuration time.Duration) (Decision, error) {
	if len(policies) == 0 && requirePolicy {
		return Decision{}, errors.New("no BroadcastPolicy in this namespace, and the controller requires one")
	}
	def := defaultDuration
	secrets := map[string]bool{}
	var rules []v1alpha1.Rule
	var maxConcurrency, maxRetries *int32
	for _, p := range policies {
		rules = append(rules, p.Spec.Allow...)
		for _, s := range p.Spec.Secrets {
			secrets[s] = true
		}
		l := p.Spec.Limits
		maxConcurrency = lower(maxConcurrency, l.MaxConcurrency)
		maxRetries = lower(maxRetries, l.MaxRetries)
		if l.MaxDuration != nil && l.MaxDuration.Duration < maxDuration {
			maxDuration = l.MaxDuration.Duration
		}
		if l.DefaultDuration != nil && l.DefaultDuration.Duration < def {
			def = l.DefaultDuration.Duration
		}
	}

	d := Decision{Duration: min(def, maxDuration), restricted: len(policies) > 0}
	spec := b.Spec
	if spec.Duration != nil {
		d.Duration = spec.Duration.Duration
	}
	if spec.Mode == v1alpha1.Continuous && d.Duration > maxDuration {
		return d, fmt.Errorf("duration %s is longer than the limit %s", d.Duration, maxDuration)
	}
	if !d.restricted {
		return d, nil
	}

	ref, err := target.FromSpec(spec.Target)
	if err != nil {
		return d, err
	}
	if d.request, err = matching(rules, ref, spec.Request.Method, spec.Request.Path); err != nil {
		return d, err
	}
	if len(d.request) == 0 {
		return d, fmt.Errorf("no BroadcastPolicy rule allows %s %s on %s", spec.Request.Method, spec.Request.Path, ref)
	}
	if r := spec.Revert; r != nil {
		if d.revert, err = matching(rules, ref, r.Method, r.Path); err != nil {
			return d, err
		}
		if len(d.revert) == 0 {
			return d, fmt.Errorf("no BroadcastPolicy rule allows the revert %s %s on %s", r.Method, r.Path, ref)
		}
	}
	if maxConcurrency != nil && spec.Execution.Concurrency > *maxConcurrency {
		return d, fmt.Errorf("concurrency %d is above the policy limit %d", spec.Execution.Concurrency, *maxConcurrency)
	}
	if r := spec.Execution.Retries; maxRetries != nil && r != nil && *r > *maxRetries {
		return d, fmt.Errorf("retries %d is above the policy limit %d", *r, *maxRetries)
	}
	for _, s := range spec.Request.HeadersFromSecrets {
		if !secrets[s] {
			return d, fmt.Errorf("the Secret %s isn't listed in a BroadcastPolicy", s)
		}
	}
	return d, nil
}

// AllowsPort reports whether the request, and the revert if any, may use port on pod. Policy port
// names are resolved on the pod, so a number can't bypass a rule written with a name.
func (d Decision) AllowsPort(pod *corev1.Pod, port int32) bool {
	if !d.restricted {
		return true
	}
	return allowsPort(d.request, pod, port) && (d.revert == nil || allowsPort(d.revert, pod, port))
}

func allowsPort(rules []v1alpha1.Rule, pod *corev1.Pod, port int32) bool {
	for _, r := range rules {
		for _, p := range r.Ports {
			if n, err := target.ContainerPort(pod, p); err == nil && n == port {
				return true
			}
		}
	}
	return false
}

// matching returns the rules that allow method and path on ref, ignoring ports.
func matching(rules []v1alpha1.Rule, ref target.Ref, method, path string) ([]v1alpha1.Rule, error) {
	var out []v1alpha1.Rule
	for _, r := range rules {
		ok, err := allows(r, ref, method, path)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func allows(r v1alpha1.Rule, ref target.Ref, method, path string) (bool, error) {
	if !slices.ContainsFunc(r.Targets, func(t string) bool { return matchesTarget(t, ref) }) ||
		!slices.ContainsFunc(r.Methods, func(m string) bool { return strings.EqualFold(m, method) }) {
		return false, nil
	}
	for _, p := range r.Paths {
		re, err := regexp.Compile(`^(?:` + p + `)$`)
		if err != nil {
			return false, fmt.Errorf("BroadcastPolicy path %q: %w", p, err)
		}
		if re.MatchString(path) {
			return true, nil
		}
	}
	return false, nil
}

func matchesTarget(entry string, ref target.Ref) bool {
	if entry == "*" {
		return true
	}
	r, err := target.ParseRef(entry)
	return err == nil && ref.Kind != "" && r.Kind == ref.Kind && r.Name == ref.Name
}

func lower(a, b *int32) *int32 {
	if a == nil || (b != nil && *b < *a) {
		return b
	}
	return a
}
