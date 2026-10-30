// Package controller runs Broadcast objects.
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/policy"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

const (
	finalizer   = v1alpha1.GroupName + "/revert"
	secretLabel = v1alpha1.GroupName + "/use"
	chunk       = 50
	bodyBytes   = 256
)

// maxPods keeps a Broadcast's status far below etcd's request size limit: it caps both the pods of
// one pass and the entries a Continuous broadcast collects over its life. Tests lower it.
var maxPods = 1000

var (
	finishedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pod_broadcast_broadcasts_finished_total", Help: "Broadcasts that finished, by phase.",
	}, []string{"phase"})
	callsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pod_broadcast_pod_calls_total", Help: "Calls to pods, by the pod's resulting state.",
	}, []string{"state"})
)

func init() {
	metrics.Registry.MustRegister(finishedTotal, callsTotal)
}

type BroadcastReconciler struct {
	client.Client
	// APIReader reads without the cache: Secrets, so that not every Secret is cached, and a Broadcast
	// right before calling pods.
	APIReader     client.Reader
	Events        events.EventRecorder
	Transport     request.Transport
	RequirePolicy bool
	DefaultTTL    time.Duration
	MaxDuration   time.Duration
}

func (r *BroadcastReconciler) SetupWithManager(mgr ctrl.Manager) error {
	active := handler.EnqueueRequestsFromMapFunc(r.activeInNamespace)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Broadcast{}).
		Watches(&corev1.Pod{}, active).
		Watches(&discoveryv1.EndpointSlice{}, active).
		Watches(&v1alpha1.BroadcastPolicy{}, active).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(r)
}

// activeInNamespace returns the Continuous broadcasts that a change in obj's namespace may concern:
// a pod became ready, or a policy changed.
func (r *BroadcastReconciler) activeInNamespace(ctx context.Context, obj client.Object) []reconcile.Request {
	var list v1alpha1.BroadcastList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Status.Phase == v1alpha1.Active {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return reqs
}

func (r *BroadcastReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var b v1alpha1.Broadcast
	if err := r.Get(ctx, req.NamespacedName, &b); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	st := b.Status
	_, stopped := b.Annotations[v1alpha1.StopAnnotation]
	switch {
	case st.Phase.Finished():
		return r.done(ctx, &b)
	case b.Spec.Mode == v1alpha1.Continuous && stopped:
		return r.revert(ctx, &b, "stopped early")
	case st.Phase == v1alpha1.Reverting || b.DeletionTimestamp != nil ||
		(st.Phase == v1alpha1.Active && time.Now().After(st.ExpiresAt.Time)):
		return r.revert(ctx, &b, "")
	case b.Spec.Mode == v1alpha1.Once:
		return r.runOnce(ctx, &b)
	case st.Phase == v1alpha1.Pending:
		return r.start(ctx, &b)
	}
	return r.apply(ctx, &b)
}

// runOnce calls every pod of the target once. It also resumes a broadcast that a restart left Running.
func (r *BroadcastReconciler) runOnce(ctx context.Context, b *v1alpha1.Broadcast) (ctrl.Result, error) {
	d, c, rejection, err := r.prepare(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if rejection != "" {
		return r.finish(ctx, b, v1alpha1.Rejected, rejection)
	}
	if b.Status.Phase == v1alpha1.Pending {
		now := metav1.Now()
		b.Status.StartedAt = &now
		setPhase(b, v1alpha1.Running)
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
		r.event(b, "started")
	}
	eps, problem := r.endpoints(ctx, b, d)
	if problem != "" {
		return r.finish(ctx, b, v1alpha1.Failed, problem)
	}
	if err := r.call(ctx, b, notCalled(eps, b.Status.Pods), c.spec, c.opts, false); err != nil {
		return ctrl.Result{}, err
	}
	return r.finish(ctx, b, b.Status.Summary.Outcome(), "")
}

// start checks a new Continuous broadcast and makes its first pass. The phase stays Pending during
// the first pass and becomes Active after it, so waiting for Active means waiting for the first pass.
func (r *BroadcastReconciler) start(ctx context.Context, b *v1alpha1.Broadcast) (ctrl.Result, error) {
	d, _, rejection, err := r.prepare(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if rejection != "" {
		return r.finish(ctx, b, v1alpha1.Rejected, rejection)
	}
	if err := r.setFinalizer(ctx, b, true); err != nil {
		return ctrl.Result{}, err
	}
	if b.Status.ExpiresAt == nil {
		now := metav1.Now()
		b.Status.StartedAt = &now
		b.Status.ExpiresAt = &metav1.Time{Time: now.Add(d.Duration)}
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.apply(ctx, b)
}

// apply calls the target's pods that the request hasn't reached yet, until the broadcast expires.
func (r *BroadcastReconciler) apply(ctx context.Context, b *v1alpha1.Broadcast) (ctrl.Result, error) {
	d, rejection, err := r.check(ctx, b)
	if err != nil {
		return ctrl.Result{}, err
	}
	if rejection != "" {
		return r.revert(ctx, b, "no longer allowed: "+rejection)
	}
	eps, problem := r.endpoints(ctx, b, d)
	switch {
	case problem != "" && len(b.Status.Pods) == 0:
		return r.finish(ctx, b, v1alpha1.Failed, problem)
	case problem != "":
		return r.revert(ctx, b, problem)
	}
	todo := notCalled(eps, b.Status.Pods)
	if len(todo) > 0 {
		if err := r.refresh(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
		todo = notCalled(eps, b.Status.Pods)
	}
	if len(b.Status.Pods)+len(todo) > maxPods {
		if err := r.dropGone(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
		if len(b.Status.Pods)+len(todo) > maxPods {
			return r.revert(ctx, b, fmt.Sprintf("more than %d pods to keep track of; a Broadcast keeps at most %d", maxPods, maxPods))
		}
	}
	if len(todo) > 0 {
		c, rejection, err := r.requests(ctx, b)
		if err != nil {
			return ctrl.Result{}, err
		}
		if rejection != "" {
			return r.revert(ctx, b, rejection)
		}
		if err := r.call(ctx, b, todo, c.spec, c.opts, false); err != nil {
			return ctrl.Result{}, err
		}
	}
	if b.Status.Phase != v1alpha1.Active {
		setPhase(b, v1alpha1.Active)
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
		r.event(b, "")
	}
	return ctrl.Result{RequeueAfter: max(time.Until(b.Status.ExpiresAt.Time), time.Second)}, nil
}

// revert sends the revert request to every pod the request reached, then completes the broadcast.
func (r *BroadcastReconciler) revert(ctx context.Context, b *v1alpha1.Broadcast, message string) (ctrl.Result, error) {
	if err := r.refresh(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	if b.Status.Phase.Finished() {
		return r.done(ctx, b)
	}
	if b.Status.Phase != v1alpha1.Reverting {
		setPhase(b, v1alpha1.Reverting)
		if message != "" {
			b.Status.Message = message
		}
		if err := r.Status().Update(ctx, b); err != nil {
			return ctrl.Result{}, err
		}
		r.event(b, message)
	}
	if b.Spec.Revert != nil {
		c, _, err := r.requests(ctx, b)
		if err != nil {
			return ctrl.Result{}, err
		}
		eps, err := r.reached(ctx, b)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := r.call(ctx, b, eps, c.revert, c.opts, true); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.finish(ctx, b, v1alpha1.Completed, "")
}

// done removes the finalizer of a finished broadcast and deletes it once its TTL has passed.
func (r *BroadcastReconciler) done(ctx context.Context, b *v1alpha1.Broadcast) (ctrl.Result, error) {
	if err := r.setFinalizer(ctx, b, false); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if b.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}
	ttl := r.DefaultTTL
	if b.Spec.TTLSecondsAfterFinished != nil {
		ttl = time.Duration(*b.Spec.TTLSecondsAfterFinished) * time.Second
	}
	if wait := time.Until(b.Status.FinishedAt.Add(ttl)); wait > 0 {
		return ctrl.Result{RequeueAfter: wait}, nil
	}
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, b))
}

// refresh reads b from the API server. The cache can lag behind the last status write, and calling
// pods from a stale status would reach some of them twice.
func (r *BroadcastReconciler) refresh(ctx context.Context, b *v1alpha1.Broadcast) error {
	return r.APIReader.Get(ctx, client.ObjectKeyFromObject(b), b)
}

// setFinalizer patches only the metadata: an update would resend the spec, and a duration written
// as "5m" comes back as "5m0s", which the API server refuses as a spec change.
func (r *BroadcastReconciler) setFinalizer(ctx context.Context, b *v1alpha1.Broadcast, present bool) error {
	patch := client.MergeFrom(b.DeepCopy())
	changed := controllerutil.RemoveFinalizer(b, finalizer)
	if present {
		changed = controllerutil.AddFinalizer(b, finalizer)
	}
	if !changed {
		return nil
	}
	return r.Patch(ctx, b, patch)
}

func (r *BroadcastReconciler) finish(ctx context.Context, b *v1alpha1.Broadcast, phase v1alpha1.Phase, message string) (ctrl.Result, error) {
	if message != "" {
		b.Status.Message = message
	}
	setPhase(b, phase)
	b.Status.Summary = summarize(&b.Status)
	now := metav1.Now()
	b.Status.FinishedAt = &now
	if err := r.Status().Update(ctx, b); err != nil {
		return ctrl.Result{}, err
	}
	finishedTotal.WithLabelValues(string(phase)).Inc()
	r.event(b, message)
	return r.done(ctx, b)
}

// setPhase sets the phase and the Complete condition that mirrors it.
func setPhase(b *v1alpha1.Broadcast, phase v1alpha1.Phase) {
	b.Status.Phase = phase
	c := metav1.Condition{Type: v1alpha1.ConditionComplete, Status: metav1.ConditionFalse, Reason: string(phase), ObservedGeneration: b.Generation}
	if phase.Finished() {
		c.Status, c.Message = metav1.ConditionTrue, b.Status.Message
	}
	meta.SetStatusCondition(&b.Status.Conditions, c)
}

func (r *BroadcastReconciler) event(b *v1alpha1.Broadcast, note string) {
	s := b.Status.Summary
	kind := corev1.EventTypeNormal
	if b.Status.Phase == v1alpha1.Rejected || s.Failed > 0 {
		kind = corev1.EventTypeWarning
	}
	if note == "" {
		note = fmt.Sprintf("%d targeted: %d succeeded, %d failed, %d gone", s.Targeted, s.Succeeded, s.Failed, s.Gone)
	}
	r.Events.Eventf(b, nil, kind, string(b.Status.Phase), "Broadcast", "%s", note)
}

// check returns why the namespace's policies don't allow b, if they don't.
func (r *BroadcastReconciler) check(ctx context.Context, b *v1alpha1.Broadcast) (policy.Decision, string, error) {
	var policies v1alpha1.BroadcastPolicyList
	if err := r.List(ctx, &policies, client.InNamespace(b.Namespace)); err != nil {
		return policy.Decision{}, "", err
	}
	d, err := policy.Check(policies.Items, b, r.RequirePolicy, r.MaxDuration)
	if err != nil {
		return d, err.Error(), nil
	}
	return d, "", nil
}

// prepare checks the policies and builds the requests. A rejection says why b can't run.
func (r *BroadcastReconciler) prepare(ctx context.Context, b *v1alpha1.Broadcast) (policy.Decision, calls, string, error) {
	d, rejection, err := r.check(ctx, b)
	if err != nil || rejection != "" {
		return d, calls{}, rejection, err
	}
	c, rejection, err := r.requests(ctx, b)
	return d, c, rejection, err
}

// calls holds the requests of one broadcast.
type calls struct {
	spec   request.Spec
	revert request.Spec
	opts   request.Options
}

// requests builds the request and the revert. A rejection says why they can't be sent as written;
// they are built anyway, so a revert still goes out when a Secret has disappeared.
func (r *BroadcastReconciler) requests(ctx context.Context, b *v1alpha1.Broadcast) (calls, string, error) {
	var c calls
	var rejection string
	var secretHeaders []request.Header
	req := b.Spec.Request
	for _, name := range req.HeadersFromSecrets {
		var s corev1.Secret
		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: name}, &s)
		switch {
		case err != nil && !apierrors.IsNotFound(err):
			return c, "", err
		case err != nil || s.Labels[secretLabel] != "true":
			rejection = fmt.Sprintf("the Secret %s doesn't exist or lacks the label %s=true", name, secretLabel)
			continue
		}
		h, err := request.SecretHeaders(&s)
		if err != nil {
			rejection = err.Error()
			continue
		}
		secretHeaders = request.Merge(secretHeaders, h)
	}
	c.spec = request.Spec{Method: req.Method, Path: req.Path, Headers: request.Merge(headers(req.Headers), secretHeaders), Body: []byte(req.Body)}
	if rv := b.Spec.Revert; rv != nil {
		c.revert = request.Spec{Method: rv.Method, Path: rv.Path, Headers: request.Merge(secretHeaders, headers(rv.Headers)), Body: []byte(rv.Body)}
	}
	e := b.Spec.Execution
	codes, err := request.ParseCodeSet(strings.Join(e.SuccessCodes, ","))
	if err != nil && rejection == "" {
		rejection = err.Error()
	}
	// The API server sets every execution default, so the pointers aren't nil.
	c.opts = request.Options{Concurrency: int(e.Concurrency), Timeout: e.Timeout.Duration, Retries: int(*e.Retries), Backoff: e.Backoff.Duration, SuccessCodes: codes}
	return c, rejection, nil
}

// endpoints resolves the target. A problem means the broadcast can't go ahead; no pods is not one.
func (r *BroadcastReconciler) endpoints(ctx context.Context, b *v1alpha1.Broadcast, d policy.Decision) ([]target.Endpoint, string) {
	ref, err := target.FromSpec(b.Spec.Target)
	if err != nil {
		return nil, err.Error()
	}
	set, err := target.Resolve(ctx, r.Client, b.Namespace, ref, target.ResolveOptions{IncludeNotReady: b.Spec.Target.IncludeNotReady})
	switch {
	case err != nil:
		return nil, err.Error()
	case len(set.Pods) == 0 && b.Spec.Mode == v1alpha1.Once:
		return nil, fmt.Sprintf("no ready pods matched %s", ref)
	case len(set.Pods) > maxPods:
		return nil, fmt.Sprintf("%d pods matched %s; a Broadcast calls at most %d", len(set.Pods), ref, maxPods)
	}
	req := b.Spec.Request
	eps, err := target.Endpoints(set, target.PortChoice{Port: req.Port, Scheme: req.Scheme, DefaultPort: req.DefaultPort})
	if err != nil {
		return nil, "no port: set request.port, or annotate the pods with " + target.AnnotationPort
	}
	for i := range eps {
		if eps[i].Err == nil && !d.AllowsPort(eps[i].Pod, eps[i].Port) {
			eps[i].Err = fmt.Errorf("port %d isn't allowed by a BroadcastPolicy", eps[i].Port)
		}
	}
	return eps, ""
}

// reached returns the pods the request succeeded on, marking those deleted since as Gone.
func (r *BroadcastReconciler) reached(ctx context.Context, b *v1alpha1.Broadcast) ([]target.Endpoint, error) {
	var eps []target.Endpoint
	for i := range b.Status.Pods {
		rec := &b.Status.Pods[i]
		if rec.State != v1alpha1.PodSucceeded {
			continue
		}
		var pod corev1.Pod
		err := r.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: rec.Name}, &pod)
		switch {
		case apierrors.IsNotFound(err) || (err == nil && string(pod.UID) != rec.UID):
			rec.State = v1alpha1.PodGone
		case err != nil:
			return nil, err
		default:
			eps = append(eps, target.Endpoint{Pod: &pod, Port: rec.Port, Scheme: rec.Scheme})
		}
	}
	return eps, nil
}

// call sends spec to eps in chunks, writing the results to the status after each chunk.
func (r *BroadcastReconciler) call(ctx context.Context, b *v1alpha1.Broadcast, eps []target.Endpoint, spec request.Spec, opts request.Options, revert bool) error {
	size := max(chunk, opts.Concurrency)
	for start := 0; start < len(eps); start += size {
		results, err := request.Run(ctx, r.Transport, eps[start:min(start+size, len(eps))], spec, opts)
		if err != nil {
			return err
		}
		for _, res := range results {
			b.Status.Pods = upsert(b.Status.Pods, r.podResult(ctx, res, revert))
		}
		b.Status.Summary = summarize(&b.Status)
		if err := r.Status().Update(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

// dropGone removes the entries of pods that no longer exist, so that a Continuous broadcast on a
// workload that keeps replacing its pods stays within maxPods. They still count as gone.
func (r *BroadcastReconciler) dropGone(ctx context.Context, b *v1alpha1.Broadcast) error {
	kept := b.Status.Pods[:0]
	for _, p := range b.Status.Pods {
		var pod corev1.Pod
		err := r.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: p.Name}, &pod)
		switch {
		case apierrors.IsNotFound(err) || (err == nil && string(pod.UID) != p.UID):
			b.Status.DroppedPods++
		case err != nil:
			return err
		default:
			kept = append(kept, p)
		}
	}
	if len(kept) == len(b.Status.Pods) {
		return nil
	}
	b.Status.Pods = kept
	b.Status.Summary = summarize(&b.Status)
	return r.Status().Update(ctx, b)
}

func (r *BroadcastReconciler) podResult(ctx context.Context, res request.Result, revert bool) v1alpha1.PodResult {
	pod := res.Endpoint.Pod
	state := string(res.State)
	if res.State == request.Failed && res.Code == 0 && r.gone(ctx, pod) {
		state = v1alpha1.PodGone
	}
	if revert {
		switch state {
		case v1alpha1.PodSucceeded:
			state = v1alpha1.PodReverted
		case v1alpha1.PodFailed:
			state = v1alpha1.PodRevertFailed
		}
	}
	callsTotal.WithLabelValues(state).Inc()
	p := v1alpha1.PodResult{
		Name: pod.Name, UID: string(pod.UID), IP: pod.Status.PodIP, Port: res.Endpoint.Port, Scheme: res.Endpoint.Scheme,
		State: state, Code: int32(res.Code), DurationMs: res.Duration.Milliseconds(), Attempts: int32(res.Attempts),
	}
	if res.Err != nil {
		p.Error = res.Err.Error()
	}
	if len(res.Body) > bodyBytes {
		res.Body = res.Body[:bodyBytes]
	}
	p.Body = string(res.Body)
	return p
}

func (r *BroadcastReconciler) gone(ctx context.Context, pod *corev1.Pod) bool {
	var current corev1.Pod
	err := r.Get(ctx, client.ObjectKeyFromObject(pod), &current)
	return apierrors.IsNotFound(err) || (err == nil && current.UID != pod.UID)
}

func headers(m map[string]string) []request.Header {
	var hs []request.Header
	for name, value := range m {
		hs = append(hs, request.Header{Name: name, Value: value})
	}
	return hs
}

func notCalled(eps []target.Endpoint, pods []v1alpha1.PodResult) []target.Endpoint {
	called := map[string]bool{}
	for _, p := range pods {
		called[p.UID] = true
	}
	var out []target.Endpoint
	for _, ep := range eps {
		if !called[string(ep.Pod.UID)] {
			out = append(out, ep)
		}
	}
	return out
}

func upsert(pods []v1alpha1.PodResult, p v1alpha1.PodResult) []v1alpha1.PodResult {
	for i := range pods {
		if pods[i].UID == p.UID {
			pods[i] = p
			return pods
		}
	}
	return append(pods, p)
}

func summarize(st *v1alpha1.BroadcastStatus) v1alpha1.Summary {
	s := v1alpha1.Summary{Targeted: int32(len(st.Pods)) + st.DroppedPods, Gone: st.DroppedPods}
	for _, p := range st.Pods {
		switch p.State {
		case v1alpha1.PodSucceeded:
			s.Succeeded++
		case v1alpha1.PodReverted:
			s.Succeeded++
			s.Reverted++
		case v1alpha1.PodRevertFailed:
			s.Failed++
			s.RevertFailed++
		case v1alpha1.PodGone:
			s.Gone++
		default:
			s.Failed++
		}
	}
	return s
}
