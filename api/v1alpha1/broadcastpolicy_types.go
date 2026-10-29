package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type BroadcastPolicySpec struct {
	// A broadcast passes when its request, and its revert, each match one rule on target, port,
	// method and path together.
	// +kubebuilder:validation:MinItems=1
	Allow []Rule `json:"allow"`
	// Secrets broadcasts may use in headersFromSecrets.
	Secrets []string `json:"secrets,omitempty"`
	Limits  Limits   `json:"limits,omitempty"`
}

type Rule struct {
	// Targets such as svc/orders or deploy/orders; "*" matches any target, including selectors.
	// +kubebuilder:validation:MinItems=1
	Targets []string `json:"targets"`
	// Port names or numbers. Names are resolved on each pod, so a number matches the name it stands for.
	// +kubebuilder:validation:MinItems=1
	Ports []intstr.IntOrString `json:"ports"`
	// +kubebuilder:validation:MinItems=1
	Methods []string `json:"methods"`
	// RE2 patterns matched against the whole path and query.
	// +kubebuilder:validation:MinItems=1
	Paths []string `json:"paths"`
}

type Limits struct {
	MaxConcurrency  *int32           `json:"maxConcurrency,omitempty"`
	MaxRetries      *int32           `json:"maxRetries,omitempty"`
	DefaultDuration *metav1.Duration `json:"defaultDuration,omitempty"`
	MaxDuration     *metav1.Duration `json:"maxDuration,omitempty"`
}

// BroadcastPolicy limits what broadcasts in its namespace may call.
// +kubebuilder:object:root=true
type BroadcastPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec BroadcastPolicySpec `json:"spec"`
}

// +kubebuilder:object:root=true
type BroadcastPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BroadcastPolicy `json:"items"`
}
