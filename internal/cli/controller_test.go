package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

// createBroadcast runs the plugin with --wait=false and returns the Broadcast it created.
func createBroadcast(t *testing.T, args ...string) v1alpha1.Broadcast {
	t.Helper()
	var cl *cluster
	newCluster := func(f *genericclioptions.ConfigFlags) (*cluster, error) {
		c, err := fakeCluster(&recorder{})(f)
		cl = c
		return c, err
	}
	streams, _, out, errOut := genericiooptions.NewTestIOStreams()
	if code := run(append(args, "--wait=false"), streams, newCluster); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	var b v1alpha1.Broadcast
	key := client.ObjectKey{Namespace: "shop", Name: strings.TrimSpace(out.String())}
	if err := cl.client.Get(context.Background(), key, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestControllerModeCreatesABroadcast(t *testing.T) {
	b := createBroadcast(t, "svc/orders", "spring", "evict-caches", "--mode", "controller",
		"-H", "X-Trace: t1", "--headers-from-secret", "auth", "--concurrency", "5", "--success-codes", "2xx, 404")
	s := b.Spec
	if !strings.HasPrefix(b.Name, "orders-") || s.Target.Service != "orders" || s.Mode != v1alpha1.Once {
		t.Errorf("object = %+v", b)
	}
	r := s.Request
	if r.Method != http.MethodDelete || r.Path != "/actuator/caches" || r.Port != nil || r.DefaultPort != "management" ||
		r.Headers["X-Trace"] != "t1" || len(r.HeadersFromSecrets) != 1 || r.HeadersFromSecrets[0] != "auth" {
		t.Errorf("request = %+v", r)
	}
	if e := s.Execution; e.Concurrency != 5 || *e.Retries != 2 || e.Timeout.Duration.String() != "5s" || strings.Join(e.SuccessCodes, ",") != "2xx,404" {
		t.Errorf("execution = %+v", e)
	}
}

func TestControllerModeRefusesDirectOnlyFlags(t *testing.T) {
	for _, flag := range [][]string{{"--dry-run"}, {"--header-from-env", "X-T=HOME"}} {
		r := runWith(t, nil, threePods(), append([]string{"svc/orders", "--port", "8081", "-X", "GET", "/", "--mode", "controller"}, flag...)...)
		if r.code != 2 || !strings.Contains(r.stderr, "direct mode only") {
			t.Errorf("%v: exit %d, stderr %q", flag, r.code, r.stderr)
		}
	}
}

func finished(name string, phase v1alpha1.Phase, summary v1alpha1.Summary, message string) *v1alpha1.Broadcast {
	return &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop"},
		Spec: v1alpha1.BroadcastSpec{
			Target:  v1alpha1.Target{Service: "orders"},
			Request: v1alpha1.Request{Port: new(intstr.FromInt32(8081)), Method: "DELETE", Path: "/actuator/caches"},
		},
		Status: v1alpha1.BroadcastStatus{
			Phase: phase, Message: message, Summary: summary,
			Pods: []v1alpha1.PodResult{
				{Name: "orders-a", UID: "a", State: v1alpha1.PodSucceeded, Code: 204, Attempts: 1},
				{Name: "orders-b", UID: "b", State: v1alpha1.PodFailed, Code: 500, Attempts: 3, Error: "HTTP 500"},
			},
		},
	}
}

func TestWait(t *testing.T) {
	objects := []runtime.Object{
		finished("partly", v1alpha1.PartiallyFailed, v1alpha1.Summary{Targeted: 2, Succeeded: 1, Failed: 1}, ""),
		finished("refused", v1alpha1.Rejected, v1alpha1.Summary{}, "no BroadcastPolicy rule allows DELETE /actuator/caches on service/orders"),
	}
	r := runWith(t, nil, objects, "wait", "partly")
	if r.code != 1 || !strings.Contains(r.stdout, "orders-b  Failed     500") || !strings.Contains(r.stdout, "2 targeted: 1 succeeded, 1 failed") {
		t.Errorf("wait partly: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = runWith(t, nil, objects, "wait", "refused")
	if r.code != 2 || !strings.Contains(r.stderr, "broadcast refused rejected: no BroadcastPolicy rule allows") {
		t.Errorf("wait refused: exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestActiveBroadcastEndsWithTheStopHint(t *testing.T) {
	b := finished("debug", v1alpha1.Active, v1alpha1.Summary{Targeted: 2, Succeeded: 1, Failed: 1}, "")
	b.Spec.Mode = v1alpha1.Continuous
	b.Status.ExpiresAt = new(metav1.Now())
	r := runWith(t, nil, []runtime.Object{b}, "wait", "debug")
	if r.code != 1 || !strings.HasSuffix(strings.TrimSpace(r.stderr), "kubectl broadcast stop debug -n shop") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestForMakesAContinuousBroadcast(t *testing.T) {
	s := createBroadcast(t, "deploy/orders", "spring", "set-log-level", "com.shop", "DEBUG", "--for", "30m").Spec
	if s.Mode != v1alpha1.Continuous || s.Duration.Duration.String() != "30m0s" || s.Target.WorkloadRef.Kind != "Deployment" {
		t.Errorf("spec = %+v", s)
	}
	if rv := s.Revert; rv == nil || rv.Method != http.MethodPost || rv.Path != "/actuator/loggers/com.shop" || rv.Body != "{}" {
		t.Errorf("revert = %+v", s.Revert)
	}
}

func TestStop(t *testing.T) {
	stopped := finished("debug", v1alpha1.Completed, v1alpha1.Summary{Targeted: 1, Succeeded: 1, Reverted: 1}, "stopped early")
	stopped.Spec.Mode = v1alpha1.Continuous
	stopped.Status.Pods = stopped.Status.Pods[:1]
	stopped.Status.Pods[0].State = v1alpha1.PodReverted
	once := finished("once", v1alpha1.Succeeded, v1alpha1.Summary{Targeted: 2, Succeeded: 2}, "")
	once.Spec.Mode = v1alpha1.Once

	r := runWith(t, nil, []runtime.Object{stopped, once}, "stop", "debug")
	if r.code != 0 || !strings.Contains(r.stdout, "orders-a  Reverted") || !strings.Contains(r.stdout, "1 succeeded, 1 reverted") {
		t.Errorf("stop debug: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	r = runWith(t, nil, []runtime.Object{stopped, once}, "stop", "once")
	if r.code != 2 || !strings.Contains(r.stderr, "only Continuous broadcasts can be stopped") {
		t.Errorf("stop once: exit %d, stderr %q", r.code, r.stderr)
	}
	r = runWith(t, nil, nil, "stop", "nope")
	if r.code != 2 || !strings.Contains(r.stderr, `broadcast "nope" not found in namespace "shop"`) {
		t.Errorf("stop nope: exit %d, stderr %q", r.code, r.stderr)
	}
}
