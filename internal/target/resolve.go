package target

import (
	"context"
	"fmt"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
func Resolve(ctx context.Context, c client.Reader, namespace string, ref Ref, opts ResolveOptions) (Set, error) {
	var (
		set  Set
		pods []*corev1.Pod
		err  error
	)
	switch ref.Kind {
	case "":
		pods, err = selectorPods(ctx, c, namespace, ref)
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
func servicePods(ctx context.Context, c client.Reader, ns, name string) ([]*corev1.Pod, []corev1.ServicePort, error) {
	var svc corev1.Service
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &svc); err != nil {
		return nil, nil, notFound(err, "service", name, ns)
	}
	var endpointSlices discoveryv1.EndpointSliceList
	if err := c.List(ctx, &endpointSlices, client.InNamespace(ns), client.MatchingLabels{discoveryv1.LabelServiceName: name}); err != nil {
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

func workloadPods(ctx context.Context, c client.Reader, ns string, ref Ref) ([]*corev1.Pod, error) {
	ls, err := workloadSelector(ctx, c, client.ObjectKey{Namespace: ns, Name: ref.Name}, ref.Kind)
	if err != nil {
		return nil, notFound(err, strings.ToLower(string(ref.Kind)), ref.Name, ns)
	}
	selector, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	if ls == nil || selector.Empty() {
		return nil, fmt.Errorf("%s has an empty selector, which matches every pod", ref)
	}
	return listPods(ctx, c, ns, selector)
}

func workloadSelector(ctx context.Context, c client.Reader, key client.ObjectKey, kind Kind) (*metav1.LabelSelector, error) {
	switch kind {
	case KindDeployment:
		var w appsv1.Deployment
		err := c.Get(ctx, key, &w)
		return w.Spec.Selector, err
	case KindStatefulSet:
		var w appsv1.StatefulSet
		err := c.Get(ctx, key, &w)
		return w.Spec.Selector, err
	case KindDaemonSet:
		var w appsv1.DaemonSet
		err := c.Get(ctx, key, &w)
		return w.Spec.Selector, err
	case KindReplicaSet:
		var w appsv1.ReplicaSet
		err := c.Get(ctx, key, &w)
		return w.Spec.Selector, err
	}
	return nil, fmt.Errorf("unsupported target kind %q", kind)
}

func selectorPods(ctx context.Context, c client.Reader, ns string, ref Ref) ([]*corev1.Pod, error) {
	if ref.Selector == nil || ref.Selector.Empty() {
		return nil, fmt.Errorf("an empty selector would match every pod")
	}
	return listPods(ctx, c, ns, ref.Selector)
}

func listPods(ctx context.Context, c client.Reader, ns string, selector labels.Selector) ([]*corev1.Pod, error) {
	var list corev1.PodList
	if err := c.List(ctx, &list, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: selector}); err != nil {
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
