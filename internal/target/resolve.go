package target

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// Set is a resolved target.
type Set struct {
	// Pods are the pods to call, sorted by name.
	Pods []*corev1.Pod
	// NotReady counts matching pods left out only because they aren't ready.
	NotReady int
	// ServicePorts are the target Service's ports; empty for other kinds.
	ServicePorts []corev1.ServicePort
}

type ResolveOptions struct {
	IncludeNotReady bool
}

// Resolve finds the pods of ref in namespace. Readiness always comes from the Pod itself.
func Resolve(ctx context.Context, c kubernetes.Interface, namespace string, ref Ref, opts ResolveOptions) (Set, error) {
	var (
		set  Set
		pods []*corev1.Pod
		err  error
	)
	switch ref.Kind {
	case "":
		pods, err = selectorPods(ctx, c, namespace, ref.Selector)
	case KindService:
		pods, set.ServicePorts, err = servicePods(ctx, c, namespace, ref.Name)
	default:
		pods, err = workloadPods(ctx, c, namespace, ref)
	}
	if err != nil {
		return Set{}, err
	}
	for _, p := range pods {
		switch {
		case !callable(p):
		case opts.IncludeNotReady || isReady(p):
			set.Pods = append(set.Pods, p)
		default:
			set.NotReady++
		}
	}
	slices.SortFunc(set.Pods, func(a, b *corev1.Pod) int { return strings.Compare(a.Name, b.Name) })
	return set, nil
}

// callable reports whether a pod can take a call at all. A finished pod is excluded because its IP
// may already belong to another pod.
func callable(p *corev1.Pod) bool {
	return p.Status.PodIP != "" && p.DeletionTimestamp == nil &&
		p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed
}

func isReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// servicePods takes membership from the Service's EndpointSlices. Their ready flag is ignored: it is
// true for not-ready pods when the Service publishes not-ready addresses.
func servicePods(ctx context.Context, c kubernetes.Interface, ns, name string) ([]*corev1.Pod, []corev1.ServicePort, error) {
	svc, err := c.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, notFound(err, "service", name, ns)
	}
	endpointSlices, err := c.DiscoveryV1().EndpointSlices(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{discoveryv1.LabelServiceName: name}.String(),
	})
	if err != nil {
		return nil, nil, err
	}
	// Dual-stack Services list each pod once per address family.
	members := map[string]bool{}
	for _, s := range endpointSlices.Items {
		for _, e := range s.Endpoints {
			r := e.TargetRef
			if r != nil && r.Kind == "Pod" && (r.Namespace == "" || r.Namespace == ns) {
				members[r.Name] = true
			}
		}
	}
	if len(members) == 0 {
		return nil, svc.Spec.Ports, nil
	}
	all, err := listPods(ctx, c, ns, labels.SelectorFromSet(svc.Spec.Selector))
	if err != nil {
		return nil, nil, err
	}
	var pods []*corev1.Pod
	for _, p := range all {
		if members[p.Name] {
			pods = append(pods, p)
		}
	}
	return pods, svc.Spec.Ports, nil
}

func workloadPods(ctx context.Context, c kubernetes.Interface, ns string, ref Ref) ([]*corev1.Pod, error) {
	ls, err := workloadSelector(ctx, c, ns, ref)
	if err != nil {
		return nil, notFound(err, strings.ToLower(string(ref.Kind)), ref.Name, ns)
	}
	if ls == nil {
		return nil, fmt.Errorf("%s has no selector", ref)
	}
	selector, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	if selector.Empty() {
		return nil, fmt.Errorf("%s has an empty selector, which matches every pod", ref)
	}
	return listPods(ctx, c, ns, selector)
}

func workloadSelector(ctx context.Context, c kubernetes.Interface, ns string, ref Ref) (*metav1.LabelSelector, error) {
	apps, get := c.AppsV1(), metav1.GetOptions{}
	switch ref.Kind {
	case KindDeployment:
		w, err := apps.Deployments(ns).Get(ctx, ref.Name, get)
		if err != nil {
			return nil, err
		}
		return w.Spec.Selector, nil
	case KindStatefulSet:
		w, err := apps.StatefulSets(ns).Get(ctx, ref.Name, get)
		if err != nil {
			return nil, err
		}
		return w.Spec.Selector, nil
	case KindDaemonSet:
		w, err := apps.DaemonSets(ns).Get(ctx, ref.Name, get)
		if err != nil {
			return nil, err
		}
		return w.Spec.Selector, nil
	case KindReplicaSet:
		w, err := apps.ReplicaSets(ns).Get(ctx, ref.Name, get)
		if err != nil {
			return nil, err
		}
		return w.Spec.Selector, nil
	}
	return nil, fmt.Errorf("unsupported target kind %q", ref.Kind)
}

func selectorPods(ctx context.Context, c kubernetes.Interface, ns, s string) ([]*corev1.Pod, error) {
	selector, err := labels.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid selector %q: %w", s, err)
	}
	if selector.Empty() {
		return nil, fmt.Errorf("selector %q matches every pod", s)
	}
	return listPods(ctx, c, ns, selector)
}

func listPods(ctx context.Context, c kubernetes.Interface, ns string, selector labels.Selector) ([]*corev1.Pod, error) {
	list, err := c.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	pods := make([]*corev1.Pod, len(list.Items))
	for i := range list.Items {
		pods[i] = &list.Items[i]
	}
	return pods, nil
}

func notFound(err error, kind, name, ns string) error {
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%s %q not found in namespace %q", kind, name, ns)
	}
	return err
}
