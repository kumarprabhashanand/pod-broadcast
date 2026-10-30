// Package v1alpha1 contains the Broadcast and BroadcastPolicy API.
// +kubebuilder:object:generate=true
// +groupName=broadcast.prabhash.net
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is also the prefix of every annotation, label and finalizer the project uses.
const GroupName = "broadcast.prabhash.net"

// StopAnnotation on a Continuous broadcast ends it early: the controller reverts it and keeps the
// object, so its result stays readable.
const StopAnnotation = GroupName + "/stop"

var GroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

// AddToScheme registers the API's types.
func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Broadcast{}, &BroadcastList{}, &BroadcastPolicy{}, &BroadcastPolicyList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
