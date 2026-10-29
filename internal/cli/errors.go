package cli

import (
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
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
