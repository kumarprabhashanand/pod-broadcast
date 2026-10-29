package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// checkProxyAccess fails before any call when the caller may not use the pod proxy with this
// method. A response to HEAD has no body, so the API server's refusal couldn't be recognized later.
// When the review itself fails, the calls are left to report it.
func checkProxyAccess(ctx context.Context, c client.Client, ns, method string) error {
	verb, ok := proxyVerbs[method]
	if !ok {
		return nil
	}
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
			Namespace: ns, Verb: verb, Resource: "pods", Subresource: "proxy",
		}},
	}
	if err := c.Create(ctx, review); err != nil || review.Status.Allowed {
		return nil
	}
	return fmt.Errorf("missing permission: %s requests need the %q verb on pods/proxy in namespace %q", method, verb, ns)
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
