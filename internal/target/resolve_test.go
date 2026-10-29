package target

import (
	"context"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

var svcOrders = Ref{Kind: KindService, Name: "orders"}

func resolve(t *testing.T, ref Ref, opts ResolveOptions, objects ...runtime.Object) Set {
	t.Helper()
	set, err := Resolve(context.Background(), fake.NewClientset(objects...), ns, ref, opts)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", ref, err)
	}
	return set
}

func wantPods(t *testing.T, set Set, want ...string) {
	t.Helper()
	if got := podNames(set.Pods); !slices.Equal(got, want) {
		t.Errorf("pods = %v, want %v", got, want)
	}
}

func TestResolveService(t *testing.T) {
	ports := []corev1.ServicePort{{Name: "http", Port: 80}}
	set := resolve(t, svcOrders, ResolveOptions{},
		newService("orders", ports...),
		newSlice("orders-1", "orders", discoveryv1.AddressTypeIPv4, "c", "a", "b"),
		newPod("a"), newPod("b"), newPod("c"))
	wantPods(t, set, "a", "b", "c")
	if set.NotReady != 0 {
		t.Errorf("NotReady = %d, want 0", set.NotReady)
	}
	if !slices.Equal(set.ServicePorts, ports) {
		t.Errorf("ServicePorts = %v, want %v", set.ServicePorts, ports)
	}
}

func TestResolveReadinessComesFromThePod(t *testing.T) {
	objects := []runtime.Object{
		newService("orders"),
		newSlice("orders-1", "orders", discoveryv1.AddressTypeIPv4, "a", "b", "c"),
		newPod("a"), newPod("b", notReady), newPod("c"),
	}

	set := resolve(t, svcOrders, ResolveOptions{}, objects...)
	wantPods(t, set, "a", "c")
	if set.NotReady != 1 {
		t.Errorf("NotReady = %d, want 1", set.NotReady)
	}

	set = resolve(t, svcOrders, ResolveOptions{IncludeNotReady: true}, objects...)
	wantPods(t, set, "a", "b", "c")
}

func TestResolveDualStackListsEachPodOnce(t *testing.T) {
	set := resolve(t, svcOrders, ResolveOptions{},
		newService("orders"),
		newSlice("orders-v4", "orders", discoveryv1.AddressTypeIPv4, "a", "b"),
		newSlice("orders-v6", "orders", discoveryv1.AddressTypeIPv6, "a", "b"),
		newPod("a"), newPod("b"))
	wantPods(t, set, "a", "b")
}

func TestResolveServiceMembershipComesFromItsSlices(t *testing.T) {
	foreign := newSlice("orders-2", "orders", discoveryv1.AddressTypeIPv4, "e")
	foreign.Endpoints[0].TargetRef.Namespace = "other"
	odd := newSlice("orders-3", "orders", discoveryv1.AddressTypeIPv4, "f", "g")
	odd.Endpoints[0].TargetRef = nil
	odd.Endpoints[1].TargetRef.Kind = "Node"

	set := resolve(t, svcOrders, ResolveOptions{},
		newService("orders"),
		newSlice("orders-1", "orders", discoveryv1.AddressTypeIPv4, "a"),
		newSlice("billing-1", "billing", discoveryv1.AddressTypeIPv4, "b"),
		foreign, odd,
		newPod("a"), newPod("b"), newPod("d"), newPod("e"), newPod("f"), newPod("g"))
	wantPods(t, set, "a")
}

func TestResolveSkipsPodsThatCannotTakeCalls(t *testing.T) {
	now := metav1.Now()
	set := resolve(t, svcOrders, ResolveOptions{IncludeNotReady: true},
		newService("orders"),
		newSlice("orders-1", "orders", discoveryv1.AddressTypeIPv4, "ok", "deleting", "no-ip", "done", "crashed"),
		newPod("ok"),
		newPod("deleting", func(p *corev1.Pod) { p.DeletionTimestamp = &now; p.Finalizers = []string{"x"} }),
		newPod("no-ip", notReady, func(p *corev1.Pod) { p.Status.PodIP = "" }),
		newPod("done", notReady, func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }),
		newPod("crashed", notReady, func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed }))
	wantPods(t, set, "ok")
	if set.NotReady != 0 {
		t.Errorf("NotReady = %d, want 0", set.NotReady)
	}
}

func TestResolveServiceWithoutEndpoints(t *testing.T) {
	set := resolve(t, svcOrders, ResolveOptions{}, newService("orders", corev1.ServicePort{Port: 80}), newPod("a"))
	wantPods(t, set)
	if len(set.ServicePorts) != 1 {
		t.Errorf("ServicePorts = %v, want the Service's port", set.ServicePorts)
	}
}

func TestResolveWorkloads(t *testing.T) {
	selector := &metav1.LabelSelector{MatchLabels: ordersLabels}
	meta := metav1.ObjectMeta{Name: "orders", Namespace: ns}
	tests := []struct {
		kind     Kind
		workload runtime.Object
	}{
		{KindDeployment, &appsv1.Deployment{ObjectMeta: meta, Spec: appsv1.DeploymentSpec{Selector: selector}}},
		{KindStatefulSet, &appsv1.StatefulSet{ObjectMeta: meta, Spec: appsv1.StatefulSetSpec{Selector: selector}}},
		{KindDaemonSet, &appsv1.DaemonSet{ObjectMeta: meta, Spec: appsv1.DaemonSetSpec{Selector: selector}}},
		{KindReplicaSet, &appsv1.ReplicaSet{ObjectMeta: meta, Spec: appsv1.ReplicaSetSpec{Selector: selector}}},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			set := resolve(t, Ref{Kind: tt.kind, Name: "orders"}, ResolveOptions{},
				tt.workload, newPod("b"), newPod("a"), newPod("x", withLabels(map[string]string{"app": "billing"})))
			wantPods(t, set, "a", "b")
		})
	}
}

func TestResolveSelector(t *testing.T) {
	set := resolve(t, Ref{Selector: "app=orders"}, ResolveOptions{},
		newPod("a"), newPod("x", withLabels(map[string]string{"app": "billing"})))
	wantPods(t, set, "a")
}

func TestResolveErrors(t *testing.T) {
	empty := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{}},
	}
	tests := []struct {
		name    string
		ref     Ref
		objects []runtime.Object
		want    string
	}{
		{name: "missing service", ref: svcOrders, want: `service "orders" not found in namespace "shop"`},
		{name: "missing deployment", ref: Ref{Kind: KindDeployment, Name: "orders"}, want: `deployment "orders" not found in namespace "shop"`},
		{name: "empty workload selector", ref: Ref{Kind: KindDeployment, Name: "orders"}, objects: []runtime.Object{empty}, want: "matches every pod"},
		{name: "invalid selector", ref: Ref{Selector: "app in (orders"}, want: "invalid selector"},
		{name: "blank selector", ref: Ref{Selector: " "}, want: "matches every pod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(context.Background(), fake.NewClientset(tt.objects...), ns, tt.ref, ResolveOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Resolve(%s) error = %v, want it to contain %q", tt.ref, err, tt.want)
			}
		})
	}
}
