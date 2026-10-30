//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

const controllerNamespace = "pod-broadcast-system"

func controller(a ...string) []string {
	return append(a, "--mode", "controller", "--wait-timeout", "2m")
}

func allowAll(t *testing.T, ns string, secrets ...string) {
	t.Helper()
	ports := []intstr.IntOrString{intstr.FromString("http"), intstr.FromString("management")}
	must(t, objects.Create(ctx(t), &v1alpha1.BroadcastPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: ns},
		Spec: v1alpha1.BroadcastPolicySpec{
			Allow:   []v1alpha1.Rule{{Targets: []string{"*"}, Ports: ports, Methods: []string{"GET", "POST", "DELETE"}, Paths: []string{".*"}}},
			Secrets: secrets,
		},
	}))
}

func getBroadcast(t *testing.T, ns, name string) *v1alpha1.Broadcast {
	t.Helper()
	var b v1alpha1.Broadcast
	must(t, objects.Get(ctx(t), ctrlclient.ObjectKey{Namespace: ns, Name: name}, &b))
	return &b
}

// loggerCalls counts the DEBUG and the reverting requests an echo pod received.
func loggerCalls(t *testing.T, ns, pod string) (applied, reverted int) {
	t.Helper()
	for _, r := range requests(t, ns, pod) {
		switch {
		case !strings.Contains(r.Path, "/loggers/"):
		case r.Body == "{}":
			reverted++
		default:
			applied++
		}
	}
	return applied, reverted
}

func runningPods(t *testing.T, ns, app string) []string {
	t.Helper()
	list, err := client.CoreV1().Pods(ns).List(ctx(t), metav1.ListOptions{LabelSelector: "app=" + app, FieldSelector: "status.phase=Running"})
	must(t, err)
	var names []string
	for _, p := range list.Items {
		if p.DeletionTimestamp == nil {
			names = append(names, p.Name)
		}
	}
	return names
}

// eventually polls cond until it holds or the timeout passes.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx(t), time.Second, timeout, true, func(context.Context) (bool, error) { return cond(), nil })
	if err != nil {
		t.Fatalf("waiting for %s: %v", what, err)
	}
}

func scale(t *testing.T, ns, name string, replicas int32) {
	t.Helper()
	s, err := client.AppsV1().Deployments(ns).GetScale(ctx(t), name, metav1.GetOptions{})
	must(t, err)
	s.Spec.Replicas = replicas
	_, err = client.AppsV1().Deployments(ns).UpdateScale(ctx(t), name, s, metav1.UpdateOptions{})
	must(t, err)
	waitDeployment(t, ns, name, replicas, 2*time.Minute)
}

func webDeployment(t *testing.T, ns string, replicas int32) {
	t.Helper()
	_, err := client.AppsV1().Deployments(ns).Create(ctx(t), deployment("web", replicas, map[string]string{"app": "web"}, echoPod("web")), metav1.CreateOptions{})
	must(t, err)
	waitDeployment(t, ns, "web", replicas, 2*time.Minute)
}

func TestControllerRunsOnce(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	allowAll(t, ns)
	r := broadcast(t, ns, controller(append(clearCaches, "--name", "clear", "-o", "json")...)...)
	rep := parseReport(t, r)
	if r.code != 0 || rep.Summary.Succeeded != 3 {
		t.Fatalf("want 3 successes\n%s", r)
	}
	wantRequests(t, ns, map[string]int{"orders-a": 1, "orders-b": 1, "orders-c": 1})

	schema, err := jsonschema.NewCompiler().Compile("../../schemas/output-v1.json")
	must(t, err)
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(r.stdout))
	must(t, err)
	if err := schema.Validate(doc); err != nil {
		t.Errorf("schema: %v", err)
	}

	events, err := client.EventsV1().Events(ns).List(ctx(t), metav1.ListOptions{})
	must(t, err)
	found := false
	for _, e := range events.Items {
		found = found || (e.Regarding.Name == "clear" && e.Reason == "Succeeded")
	}
	if !found {
		t.Error("no Succeeded event on the broadcast")
	}

	pods := runningPods(t, controllerNamespace, "pod-broadcast-controller")
	metrics, err := client.CoreV1().Pods(controllerNamespace).ProxyGet("http", pods[0], "8080", "/metrics", nil).DoRaw(ctx(t))
	must(t, err)
	if !strings.Contains(string(metrics), `pod_broadcast_broadcasts_finished_total{phase="Succeeded"}`) {
		t.Error("metrics lack pod_broadcast_broadcasts_finished_total")
	}
}

func TestControllerExitCodesAndWait(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	allowAll(t, ns)
	createService(t, ns, ordersService(false))
	createPods(t, ns, echoPod("orders-a"), echoPod("orders-b", env("STATUS", "500")))
	waitEndpoints(t, ns, "orders", 2)

	if r := broadcast(t, ns, controller(append(clearCaches, "--backoff", "100ms")...)...); r.code != 1 {
		t.Errorf("one failing pod: want exit 1\n%s", r)
	}
	must(t, objects.Create(ctx(t), &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: "from-yaml", Namespace: ns},
		Spec: v1alpha1.BroadcastSpec{
			Target:  v1alpha1.Target{Service: "orders"},
			Request: v1alpha1.Request{Port: new(intstr.FromString("management")), Method: "DELETE", Path: "/actuator/caches"},
		},
	}))
	if r := broadcast(t, ns, "wait", "from-yaml"); r.code != 1 || !strings.Contains(r.stdout, "2 targeted: 1 succeeded, 1 failed") {
		t.Errorf("wait: want exit 1\n%s", r)
	}
}

func TestControllerDeletesAfterTTL(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	allowAll(t, ns)
	must(t, objects.Create(ctx(t), &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: "short-lived", Namespace: ns},
		Spec: v1alpha1.BroadcastSpec{
			Target:                  v1alpha1.Target{Service: "orders"},
			Request:                 v1alpha1.Request{Port: new(intstr.FromString("management")), Method: "DELETE", Path: "/actuator/caches"},
			TTLSecondsAfterFinished: new(int32(0)),
		},
	}))
	eventually(t, time.Minute, "the broadcast to be deleted", func() bool {
		var b v1alpha1.Broadcast
		return apierrors.IsNotFound(objects.Get(ctx(t), ctrlclient.ObjectKey{Namespace: ns, Name: "short-lived"}, &b))
	})
	wantRequests(t, ns, map[string]int{"orders-a": 1, "orders-b": 1, "orders-c": 1})
}

func TestControllerPolicy(t *testing.T) {
	t.Parallel()
	if r := broadcast(t, newNamespace(t), controller(clearCaches...)...); r.code != 2 || !strings.Contains(r.stderr, "the controller requires one") {
		t.Errorf("no policy: want exit 2\n%s", r)
	}

	ns := threeEchoPods(t)
	must(t, objects.Create(ctx(t), &v1alpha1.BroadcastPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "caches", Namespace: ns},
		Spec: v1alpha1.BroadcastPolicySpec{
			Allow: []v1alpha1.Rule{{
				Targets: []string{"svc/orders"}, Ports: []intstr.IntOrString{intstr.FromString("management")},
				Methods: []string{"DELETE"}, Paths: []string{"/actuator/caches"},
			}},
			Secrets: []string{"auth", "unlabeled"},
		},
	}))
	for name, labels := range map[string]map[string]string{"auth": {v1alpha1.GroupName + "/use": "true"}, "unlabeled": nil, "unlisted": {v1alpha1.GroupName + "/use": "true"}} {
		_, err := client.CoreV1().Secrets(ns).Create(ctx(t), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			Data:       map[string][]byte{"X-Token": []byte("from-" + name)},
		}, metav1.CreateOptions{})
		must(t, err)
	}

	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"allowed with a Secret", append(clearCaches, "--headers-from-secret", "auth"), 0, ""},
		{"path not allowed", []string{"svc/orders", "--port", "management", "-X", "DELETE", "/actuator/shutdown"}, 2, "no BroadcastPolicy rule allows"},
		{"number for a port the rule names", []string{"svc/orders", "--port", "8080", "-X", "DELETE", "/actuator/caches"}, 3, "port 8080 isn't allowed by a BroadcastPolicy"},
		{"unlabeled Secret", append(clearCaches, "--headers-from-secret", "unlabeled"), 2, "lacks the label"},
		{"unlisted Secret", append(clearCaches, "--headers-from-secret", "unlisted"), 2, "isn't listed in a BroadcastPolicy"},
	}
	for _, tt := range tests {
		r := broadcast(t, ns, controller(tt.args...)...)
		if r.code != tt.code || !strings.Contains(r.stdout+r.stderr, tt.want) {
			t.Errorf("%s: want exit %d and %q\n%s", tt.name, tt.code, tt.want, r)
		}
	}
	for _, pod := range []string{"orders-a", "orders-b", "orders-c"} {
		recs := requests(t, ns, pod)
		if len(recs) != 1 || strings.Join(recs[0].Headers["X-Token"], ",") != "from-auth" {
			t.Errorf("%s received %+v, want one request with the auth Secret's header", pod, recs)
		}
	}
}

func TestContinuousReachesNewPodsAndStops(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	allowAll(t, ns)
	webDeployment(t, ns, 2)

	r := broadcast(t, ns, controller("deploy/web", "spring", "set-log-level", "com.shop", "debug", "--for", "5m", "--name", "debug")...)
	if r.code != 0 || !strings.Contains(r.stdout, "2 targeted: 2 succeeded") || !strings.Contains(r.stderr, "kubectl broadcast stop debug") {
		t.Fatalf("first pass\n%s", r)
	}
	scale(t, ns, "web", 3)
	eventually(t, time.Minute, "the new pod to be reached", func() bool { return getBroadcast(t, ns, "debug").Status.Summary.Succeeded == 3 })

	if r := broadcast(t, ns, "stop", "debug"); r.code != 0 || strings.Count(r.stdout, "Reverted") != 3 {
		t.Fatalf("stop\n%s", r)
	}
	for _, pod := range runningPods(t, ns, "web") {
		if applied, reverted := loggerCalls(t, ns, pod); applied != 1 || reverted != 1 {
			t.Errorf("%s: applied %d, reverted %d; want 1 and 1", pod, applied, reverted)
		}
	}
	if b := getBroadcast(t, ns, "debug"); b.Status.Phase != v1alpha1.Completed || len(b.Finalizers) != 0 {
		t.Errorf("after stop: phase %s, finalizers %v", b.Status.Phase, b.Finalizers)
	}
}

// TestContinuousSurvivesRestartAndExpires restarts the shared controller, so it doesn't run in parallel.
func TestContinuousSurvivesRestartAndExpires(t *testing.T) {
	ns := newNamespace(t)
	allowAll(t, ns)
	webDeployment(t, ns, 2)
	must(t, objects.Create(ctx(t), &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: "debug", Namespace: ns},
		Spec: v1alpha1.BroadcastSpec{
			Target:   v1alpha1.Target{WorkloadRef: &v1alpha1.WorkloadRef{Kind: "Deployment", Name: "web"}},
			Mode:     v1alpha1.Continuous,
			Duration: &metav1.Duration{Duration: 45 * time.Second},
			Request:  v1alpha1.Request{Port: new(intstr.FromString("management")), Method: "POST", Path: "/actuator/loggers/com.shop", Body: `{"configuredLevel":"DEBUG"}`},
			Revert:   &v1alpha1.Revert{Method: "POST", Path: "/actuator/loggers/com.shop", Body: "{}"},
		},
	}))
	if r := broadcast(t, ns, "wait", "debug"); r.code != 0 {
		t.Fatalf("first pass\n%s", r)
	}

	controllers, err := client.CoreV1().Pods(controllerNamespace).List(ctx(t), metav1.ListOptions{LabelSelector: "app=pod-broadcast-controller"})
	must(t, err)
	for _, p := range controllers.Items {
		must(t, client.CoreV1().Pods(controllerNamespace).Delete(ctx(t), p.Name, metav1.DeleteOptions{}))
	}
	scale(t, ns, "web", 3)
	eventually(t, time.Minute, "the new pod to be reached after the restart", func() bool {
		return getBroadcast(t, ns, "debug").Status.Summary.Succeeded == 3
	})
	eventually(t, 90*time.Second, "the broadcast to expire", func() bool {
		return getBroadcast(t, ns, "debug").Status.Phase == v1alpha1.Completed
	})
	for _, pod := range runningPods(t, ns, "web") {
		if applied, reverted := loggerCalls(t, ns, pod); applied != 1 || reverted != 1 {
			t.Errorf("%s: applied %d, reverted %d; want 1 and 1", pod, applied, reverted)
		}
	}
}

// TestNamespacedInstall replaces the cluster-wide controller for a while, so it doesn't run in parallel.
func TestNamespacedInstall(t *testing.T) {
	deployments := client.AppsV1().Deployments(controllerNamespace)
	setReplicas := func(n int32) {
		s, err := deployments.GetScale(context.Background(), "pod-broadcast-controller", metav1.GetOptions{})
		must(t, err)
		s.Spec.Replicas = n
		_, err = deployments.UpdateScale(context.Background(), "pod-broadcast-controller", s, metav1.UpdateOptions{})
		must(t, err)
	}
	setReplicas(0)
	t.Cleanup(func() {
		setReplicas(1)
		waitDeployment(t, controllerNamespace, "pod-broadcast-controller", 1, 2*time.Minute)
	})
	eventually(t, time.Minute, "the cluster-wide controller to stop", func() bool {
		return len(runningPods(t, controllerNamespace, "pod-broadcast-controller")) == 0
	})

	ns := threeEchoPods(t)
	cmd := exec.CommandContext(ctx(t), "kubectl", "apply", "-n", ns, "-f", "../../deploy/controller-namespaced.yaml")
	if kubeContext != "" {
		cmd.Args = append(cmd.Args, "--context", kubeContext)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubectl apply: %v\n%s", err, out)
	}
	waitDeployment(t, ns, "pod-broadcast-controller", 1, 2*time.Minute)

	if r := broadcast(t, ns, controller(clearCaches...)...); r.code != 0 {
		t.Errorf("namespaced controller without a policy: want exit 0\n%s", r)
	}
	other := newNamespace(t)
	must(t, objects.Create(ctx(t), &v1alpha1.Broadcast{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: other},
		Spec:       v1alpha1.BroadcastSpec{Target: v1alpha1.Target{Service: "orders"}, Request: v1alpha1.Request{Port: new(intstr.FromInt32(8080)), Method: "GET", Path: "/"}},
	}))
	time.Sleep(5 * time.Second)
	if b := getBroadcast(t, other, "elsewhere"); b.Status.Phase != v1alpha1.Pending {
		t.Errorf("a broadcast in another namespace got phase %q; the namespaced controller should ignore it", b.Status.Phase)
	}
}

// TestDefaultUserRolesCoverBroadcasts checks deploy/user-roles.yaml: Kubernetes' view, edit and
// admin roles gain the documented Broadcast permissions, and edit is enough to run one.
func TestDefaultUserRolesCoverBroadcasts(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	allowAll(t, ns)
	users := map[string]string{}
	for _, role := range []string{"view", "edit", "admin"} {
		_, err := client.CoreV1().ServiceAccounts(ns).Create(ctx(t), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: role}}, metav1.CreateOptions{})
		must(t, err)
		_, err = client.RbacV1().RoleBindings(ns).Create(ctx(t), &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: role},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: role},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: role, Namespace: ns}},
		}, metav1.CreateOptions{})
		must(t, err)
		users[role] = "system:serviceaccount:" + ns + ":" + role
	}
	checks := []struct {
		user, verb, resource string
		allowed              bool
	}{
		{"view", "list", "broadcasts", true},
		{"view", "create", "broadcasts", false},
		{"edit", "create", "broadcasts", true},
		{"edit", "patch", "broadcasts", true},
		{"edit", "list", "broadcastpolicies", true},
		{"edit", "create", "broadcastpolicies", false},
		{"admin", "create", "broadcastpolicies", true},
	}
	eventually(t, time.Minute, "the aggregated roles to apply", func() bool {
		for _, c := range checks {
			review, err := client.AuthorizationV1().SubjectAccessReviews().Create(ctx(t), &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{User: users[c.user], ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: ns, Group: v1alpha1.GroupName, Resource: c.resource, Verb: c.verb,
				}},
			}, metav1.CreateOptions{})
			if err != nil || review.Status.Allowed != c.allowed {
				return false
			}
		}
		return true
	})
	if r := broadcast(t, ns, controller(append(clearCaches, "--as", users["edit"])...)...); r.code != 0 {
		t.Errorf("a broadcast as a user with the edit role: want exit 0\n%s", r)
	}
}
