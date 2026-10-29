package cli

import (
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
	"github.com/kumarprabhashanand/pod-broadcast/internal/transport"
)

func noPodsError(ref target.Ref, namespace string, notReady int, includeNotReady bool) error {
	switch {
	case includeNotReady:
		return fmt.Errorf("no pods matched %s in namespace %q", ref, namespace)
	case notReady > 0:
		return fmt.Errorf("no ready pods matched %s in namespace %q; %d not ready (add --include-not-ready to call them)", ref, namespace, notReady)
	}
	return fmt.Errorf("no ready pods matched %s in namespace %q", ref, namespace)
}

func noPortError(servicePorts []corev1.ServicePort) error {
	msg := "can't choose a port: use --port NAME|NUMBER, or annotate the pods with " + target.AnnotationPort
	if len(servicePorts) > 1 {
		ports := make([]string, len(servicePorts))
		for i, p := range servicePorts {
			tp := target.TargetPort(p)
			ports[i] = tp.String()
		}
		msg += "; the Service's target ports are " + strings.Join(ports, ", ")
	}
	return errors.New(msg)
}

// proxyVerbs are the RBAC verbs on pods/proxy that each HTTP method needs.
var proxyVerbs = map[string]string{
	"GET": "get", "HEAD": "get", "POST": "create", "PUT": "update", "PATCH": "patch", "DELETE": "delete",
}

func fatalError(err error, method string) error {
	var forbidden *transport.ForbiddenError
	if errors.As(err, &forbidden) {
		if verb, ok := proxyVerbs[method]; ok {
			return fmt.Errorf("%w (%s requests need the %q verb on pods/proxy)", err, method, verb)
		}
	}
	return err
}
