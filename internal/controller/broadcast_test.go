package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

const ns = "shop"

// recorder is a transport that answers 200 and remembers each call as "pod path".
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) Do(_ context.Context, ep target.Endpoint, spec request.Spec) (request.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ep.Pod.Name+" "+spec.Path)
	return request.Response{Code: 200}, nil
}

func (r *recorder) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.HasSuffix(c, " "+path) {
			n++
		}
	}
	return n
}

func pod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name), Labels: map[string]string{"app": "web"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "app", Ports: []corev1.ContainerPort{{Name: "management", ContainerPort: 8081}},
		}}},
		Status: corev1.PodStatus{
			PodIP:      "10.0.0.1",
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

// newBroadcast has the defaults the API server would set.
func newBroadcast(mutate ...func(*v1alpha1.BroadcastSpec)) *v1alpha1.Broadcast {
	b := &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: ns},
		Spec: v1alpha1.BroadcastSpec{
			Target:  v1alpha1.Target{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
			Request: v1alpha1.Request{Port: new(intstr.FromString("management")), Method: "POST", Path: "/apply"},
			Mode:    v1alpha1.Once,
			Execution: v1alpha1.Execution{
				Concurrency: 10, Timeout: &metav1.Duration{Duration: time.Second}, Retries: new(int32(0)),
				Backoff: &metav1.Duration{}, SuccessCodes: []string{"2xx"},
			},
		},
	}
	for _, m := range mutate {
		m(&b.Spec)
	}
	return b
}

func continuous(s *v1alpha1.BroadcastSpec) {
	s.Mode = v1alpha1.Continuous
	s.Duration = &metav1.Duration{Duration: time.Hour}
	s.Revert = &v1alpha1.Revert{Method: "POST", Path: "/revert"}
}

type env struct {
	t   *testing.T
	c   client.Client
	r   *BroadcastReconciler
	pod *recorder
}

func newEnv(t *testing.T, requirePolicy bool, objs ...client.Object) *env {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(&v1alpha1.Broadcast{}).Build()
	rec := &recorder{}
	r := &BroadcastReconciler{
		Client: c, APIReader: c, Events: events.NewFakeRecorder(100), Transport: rec,
		RequirePolicy: requirePolicy, DefaultTTL: time.Hour, MaxDuration: 24 * time.Hour,
	}
	return &env{t: t, c: c, r: r, pod: rec}
}

// reconcile runs the reconciler n times, as a series of watch events would.
func (e *env) reconcile(n int) *v1alpha1.Broadcast {
	e.t.Helper()
	for range n {
		if _, err := e.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: "b"}}); err != nil {
			e.t.Fatalf("Reconcile: %v", err)
		}
	}
	return e.get()
}

func (e *env) get() *v1alpha1.Broadcast {
	e.t.Helper()
	var b v1alpha1.Broadcast
	if err := e.c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "b"}, &b); err != nil {
		e.t.Fatal(err)
	}
	return &b
}

func (e *env) create(objs ...client.Object) {
	e.t.Helper()
	for _, o := range objs {
		if err := e.c.Create(context.Background(), o); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) delete(objs ...client.Object) {
	e.t.Helper()
	for _, o := range objs {
		if err := e.c.Delete(context.Background(), o); err != nil {
			e.t.Fatal(err)
		}
	}
}

func complete(b *v1alpha1.Broadcast) *metav1.Condition {
	return meta.FindStatusCondition(b.Status.Conditions, v1alpha1.ConditionComplete)
}

func TestOnceCallsEveryPodOnce(t *testing.T) {
	e := newEnv(t, false, newBroadcast(), pod("a"), pod("b"), pod("c"))
	b := e.reconcile(3)
	if b.Status.Phase != v1alpha1.Succeeded || b.Status.Summary.Succeeded != 3 {
		t.Fatalf("phase %s, summary %+v; want Succeeded with 3", b.Status.Phase, b.Status.Summary)
	}
	if n := e.pod.count("/apply"); n != 3 {
		t.Errorf("%d calls, want 3 even after more reconciles", n)
	}
	if c := complete(b); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "Succeeded" {
		t.Errorf("Complete condition = %+v, want True with reason Succeeded", c)
	}
}

func TestOnceWithoutPodsFails(t *testing.T) {
	e := newEnv(t, false, newBroadcast())
	if b := e.reconcile(1); b.Status.Phase != v1alpha1.Failed || !strings.Contains(b.Status.Message, "no ready pods") {
		t.Errorf("phase %s, message %q; want Failed, no ready pods", b.Status.Phase, b.Status.Message)
	}
}

func TestRejected(t *testing.T) {
	policy := &v1alpha1.BroadcastPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns},
		Spec: v1alpha1.BroadcastPolicySpec{Allow: []v1alpha1.Rule{{
			Targets: []string{"*"}, Ports: []intstr.IntOrString{intstr.FromString("management")},
			Methods: []string{"POST"}, Paths: []string{"/apply(/.*)?"},
		}}},
	}
	tests := []struct {
		name    string
		objs    []client.Object
		path    string
		message string
	}{
		{"no policy", nil, "/apply", "requires one"},
		{"dot-dot segment", []client.Object{policy}, "/apply/../shutdown", `".." segment`},
		{"path not allowed", []client.Object{policy}, "/shutdown", "no BroadcastPolicy rule allows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append([]client.Object{pod("a"), newBroadcast(func(s *v1alpha1.BroadcastSpec) { s.Request.Path = tt.path })}, tt.objs...)
			e := newEnv(t, true, objs...)
			b := e.reconcile(1)
			if b.Status.Phase != v1alpha1.Rejected || !strings.Contains(b.Status.Message, tt.message) {
				t.Errorf("phase %s, message %q; want Rejected with %q", b.Status.Phase, b.Status.Message, tt.message)
			}
			if c := complete(b); c == nil || c.Status != metav1.ConditionTrue || c.Reason != "Rejected" {
				t.Errorf("Complete condition = %+v, want True with reason Rejected", c)
			}
			if len(e.pod.calls) != 0 {
				t.Errorf("a rejected broadcast called pods: %v", e.pod.calls)
			}
		})
	}
}

func TestContinuousReachesNewPodsAndRevertsOnStop(t *testing.T) {
	e := newEnv(t, false, newBroadcast(continuous), pod("a"), pod("b"))
	b := e.reconcile(1)
	if b.Status.Phase != v1alpha1.Active || len(b.Finalizers) != 1 {
		t.Fatalf("phase %s, finalizers %v; want Active with the revert finalizer", b.Status.Phase, b.Finalizers)
	}
	if c := complete(b); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Complete condition = %+v, want False while active", c)
	}

	e.create(pod("c"))
	if b = e.reconcile(2); b.Status.Summary.Succeeded != 3 || e.pod.count("/apply") != 3 {
		t.Fatalf("summary %+v, %d calls; want the new pod reached once", b.Status.Summary, e.pod.count("/apply"))
	}

	metav1.SetMetaDataAnnotation(&b.ObjectMeta, v1alpha1.StopAnnotation, "true")
	if err := e.c.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	b = e.reconcile(2)
	if b.Status.Phase != v1alpha1.Completed || b.Status.Summary.Reverted != 3 || e.pod.count("/revert") != 3 {
		t.Errorf("phase %s, summary %+v, %d reverts; want Completed with 3 reverted once", b.Status.Phase, b.Status.Summary, e.pod.count("/revert"))
	}
	if len(b.Finalizers) != 0 {
		t.Errorf("finalizers %v left after completing", b.Finalizers)
	}
}

func TestContinuousDropsEntriesOfGonePods(t *testing.T) {
	defer func(n int) { maxPods = n }(maxPods)
	maxPods = 3
	a, b0 := pod("a"), pod("b")
	e := newEnv(t, false, newBroadcast(continuous), a, b0)
	e.reconcile(1)

	// A rollout replaces both pods: the two old entries are dropped to make room.
	e.delete(a, b0)
	e.create(pod("c"), pod("d"))
	b := e.reconcile(1)
	if len(b.Status.Pods) != 2 || b.Status.DroppedPods != 2 {
		t.Fatalf("%d entries, %d dropped; want 2 and 2", len(b.Status.Pods), b.Status.DroppedPods)
	}
	if s := b.Status.Summary; s.Targeted != 4 || s.Succeeded != 2 || s.Gone != 2 {
		t.Errorf("summary %+v; want 4 targeted, 2 succeeded, 2 gone", s)
	}

	// Pods that still exist are never dropped, even when not ready, so past the cap the broadcast
	// ends and reverts.
	var d corev1.Pod
	if err := e.c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "d"}, &d); err != nil {
		t.Fatal(err)
	}
	d.Status.Conditions[0].Status = corev1.ConditionFalse
	if err := e.c.Status().Update(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	e.create(pod("e"), pod("f"))
	b = e.reconcile(1)
	if b.Status.Phase != v1alpha1.Completed || !strings.Contains(b.Status.Message, "more than 3 pods") {
		t.Errorf("phase %s, message %q; want Completed, more than 3 pods", b.Status.Phase, b.Status.Message)
	}
	if n := e.pod.count("/revert"); n != 2 {
		t.Errorf("%d reverts, want 2 (pods c and d, which still exist)", n)
	}
}

func TestContinuousRevertsWhenItExpires(t *testing.T) {
	e := newEnv(t, false, newBroadcast(continuous), pod("a"))
	b := e.reconcile(1)
	b.Status.ExpiresAt = &metav1.Time{Time: time.Now().Add(-time.Second)}
	if err := e.c.Status().Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if b = e.reconcile(1); b.Status.Phase != v1alpha1.Completed || e.pod.count("/revert") != 1 {
		t.Errorf("phase %s, %d reverts; want Completed with 1", b.Status.Phase, e.pod.count("/revert"))
	}
}
