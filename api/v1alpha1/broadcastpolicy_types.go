package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// BroadcastPolicySpec says what broadcasts in the namespace may call.
type BroadcastPolicySpec struct {
	// A broadcast passes when its request, and its revert, each match one rule on target, port,
	// method and path together.
	// +kubebuilder:validation:MinItems=1
	Allow []Rule `json:"allow"`
	// Secrets broadcasts may use in headersFromSecrets.
	Secrets []string `json:"secrets,omitempty"`
	// Limits caps execution settings and durations; with several policies, the strictest wins.
	Limits Limits `json:"limits,omitempty"`
}

// Rule allows a set of methods and paths on a set of targets and ports.
type Rule struct {
	// Targets such as svc/orders or deploy/orders; "*" matches any target, including selectors.
	// +kubebuilder:validation:MinItems=1
	Targets []string `json:"targets"`
	// Port names or numbers. Names are resolved on each pod, so a number matches the name it stands for.
	// +kubebuilder:validation:MinItems=1
	Ports []intstr.IntOrString `json:"ports"`
	// HTTP methods, compared without regard to case.
	// +kubebuilder:validation:MinItems=1
	Methods []string `json:"methods"`
	// RE2 patterns, each matched against the whole path and query, and against the path without its
	// query, so that a wildcard can't reach into the query; allow an optional query with (\?.*)?.
	// Paths with "." or ".." segments, ";", "#", a backslash, or percent-encoded ".", "/", backslash
	// or "%" are refused, so that no server reads them as a path the pattern doesn't allow.
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`
}

// Limits caps what broadcasts may ask for.
type Limits struct {
	// MaxConcurrency is the highest execution.concurrency allowed.
	// +kubebuilder:validation:Minimum=1
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`
	// MaxRetries is the highest execution.retries allowed.
	// +kubebuilder:validation:Minimum=0
	MaxRetries *int32 `json:"maxRetries,omitempty"`
	// DefaultDuration of a Continuous broadcast that sets none.
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="defaultDuration must be positive"
	DefaultDuration *metav1.Duration `json:"defaultDuration,omitempty"`
	// MaxDuration is the longest Continuous broadcast allowed.
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="maxDuration must be positive"
	MaxDuration *metav1.Duration `json:"maxDuration,omitempty"`
}

// BroadcastPolicy limits what broadcasts in its namespace may call.
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=bcp,categories=pod-broadcast
type BroadcastPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec BroadcastPolicySpec `json:"spec"`
}

// BroadcastPolicyList is a list of BroadcastPolicies.
// +kubebuilder:object:root=true
type BroadcastPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BroadcastPolicy `json:"items"`
}
