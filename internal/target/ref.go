package target

import (
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

type Kind string

const (
	KindService     Kind = "Service"
	KindDeployment  Kind = "Deployment"
	KindStatefulSet Kind = "StatefulSet"
	KindDaemonSet   Kind = "DaemonSet"
	KindReplicaSet  Kind = "ReplicaSet"
)

// Ref names the pods to call: an object of Kind, or a label selector when Kind is empty.
type Ref struct {
	Kind     Kind
	Name     string
	Selector labels.Selector
}

var kindsByPrefix = map[string]Kind{
	"svc":         KindService,
	"service":     KindService,
	"deploy":      KindDeployment,
	"deployment":  KindDeployment,
	"sts":         KindStatefulSet,
	"statefulset": KindStatefulSet,
	"ds":          KindDaemonSet,
	"daemonset":   KindDaemonSet,
	"rs":          KindReplicaSet,
	"replicaset":  KindReplicaSet,
}

func (r Ref) String() string {
	if r.Kind == "" {
		return "selector " + r.Selector.String()
	}
	return strings.ToLower(string(r.Kind)) + "/" + r.Name
}

// ParseRef parses a KIND/NAME argument such as svc/orders or deploy/orders.
func ParseRef(s string) (Ref, error) {
	prefix, name, ok := strings.Cut(s, "/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return Ref{}, fmt.Errorf("target %q must look like KIND/NAME, for example svc/orders", s)
	}
	kind, ok := kindsByPrefix[prefix]
	if !ok {
		return Ref{}, fmt.Errorf("unknown target kind %q in %q: use svc, deploy, sts, ds or rs", prefix, s)
	}
	return Ref{Kind: kind, Name: name}, nil
}

// FromSpec converts a Broadcast's target.
func FromSpec(t v1alpha1.Target) (Ref, error) {
	switch {
	case t.Service != "":
		return Ref{Kind: KindService, Name: t.Service}, nil
	case t.WorkloadRef != nil:
		return Ref{Kind: Kind(t.WorkloadRef.Kind), Name: t.WorkloadRef.Name}, nil
	case t.Selector != nil:
		sel, err := metav1.LabelSelectorAsSelector(t.Selector)
		return Ref{Selector: sel}, err
	}
	return Ref{}, errors.New("target has no service, workloadRef or selector")
}
