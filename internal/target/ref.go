package target

import (
	"fmt"
	"strings"
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
	Selector string
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
