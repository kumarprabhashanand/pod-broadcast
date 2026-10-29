//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
)

var clearCaches = []string{"svc/orders", "--port", "management", "-X", "DELETE", "/actuator/caches"}

func threeEchoPods(t *testing.T, opts ...func(*corev1.Pod)) string {
	t.Helper()
	ns := newNamespace(t)
	createService(t, ns, ordersService(false))
	createPods(t, ns, echoPod("orders-a", opts...), echoPod("orders-b", opts...), echoPod("orders-c", opts...))
	waitEndpoints(t, ns, "orders", 3)
	return ns
}

func TestEveryPodGetsExactlyOneRequest(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	r := broadcast(t, ns, clearCaches...)
	if r.code != 0 || strings.Count(r.stdout, "Succeeded") != 3 || !strings.Contains(r.stdout, "3 targeted: 3 succeeded") {
		t.Fatalf("want 3 Succeeded rows and exit 0\n%s", r)
	}
	for _, pod := range []string{"orders-a", "orders-b", "orders-c"} {
		recs := requests(t, ns, pod)
		if len(recs) != 1 || recs[0].Method != http.MethodDelete || recs[0].Path != "/actuator/caches" {
			t.Errorf("%s received %+v, want one DELETE /actuator/caches", pod, recs)
		}
	}
}

func TestOneFailingPod(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createService(t, ns, ordersService(false))
	createPods(t, ns, echoPod("orders-a"), echoPod("orders-b"), echoPod("orders-c", env("STATUS", "500")))
	waitEndpoints(t, ns, "orders", 3)

	r := broadcast(t, ns, append(clearCaches, "-o", "json")...)
	rep := parseReport(t, r)
	if r.code != 1 || rep.Outcome != "PartiallyFailed" {
		t.Fatalf("want exit 1 and PartiallyFailed\n%s", r)
	}
	for _, p := range rep.Pods {
		failing := p.Name == "orders-c"
		if failing && (p.State != "Failed" || p.Code != 500 || p.Attempts != 3) {
			t.Errorf("orders-c = %+v, want Failed, 500, 3 attempts", p)
		}
		if !failing && p.State != "Succeeded" {
			t.Errorf("%s = %+v, want Succeeded", p.Name, p)
		}
	}
	wantRequests(t, ns, map[string]int{"orders-a": 1, "orders-b": 1, "orders-c": 3})
}

func TestEveryPodRefusesConnections(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t, args("-listen", ":8080"))
	r := broadcast(t, ns, append(clearCaches, "--backoff", "100ms", "-o", "json")...)
	rep := parseReport(t, r)
	if r.code != 3 || rep.Outcome != "Failed" {
		t.Fatalf("want exit 3\n%s", r)
	}
	for _, p := range rep.Pods {
		if p.State != "Failed" || !strings.Contains(p.Error, "connection refused") {
			t.Errorf("%s = %+v, want a connection error", p.Name, p)
		}
	}
}

func TestNotReadyPodsAreSkipped(t *testing.T) {
	t.Parallel()
	for _, publishNotReady := range []bool{false, true} {
		name := "not published"
		if publishNotReady {
			name = "publishNotReadyAddresses"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := newNamespace(t)
			createService(t, ns, ordersService(publishNotReady))
			createPods(t, ns, echoPod("orders-a"), echoPod("orders-b"), echoPod("orders-c", notReady))
			waitEndpoints(t, ns, "orders", 3)

			r := broadcast(t, ns, clearCaches...)
			if r.code != 0 || !strings.Contains(r.stdout, "2 targeted: 2 succeeded") {
				t.Fatalf("want 2 calls\n%s", r)
			}
			wantRequests(t, ns, map[string]int{"orders-a": 1, "orders-b": 1, "orders-c": 0})

			r = broadcast(t, ns, append(clearCaches, "--include-not-ready")...)
			if r.code != 0 || !strings.Contains(r.stdout, "3 targeted: 3 succeeded") {
				t.Fatalf("want 3 calls with --include-not-ready\n%s", r)
			}
			wantRequests(t, ns, map[string]int{"orders-a": 2, "orders-b": 2, "orders-c": 1})
		})
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	r := broadcastEnv(t, ns, []string{"E2E_TOKEN=env-secret-value"}, append(clearCaches, "--dry-run", "-H", "X-Trace: t1", "--header-from-env", "X-Api-Token=E2E_TOKEN")...)
	if r.code != 0 {
		t.Fatalf("dry run failed\n%s", r)
	}
	for _, want := range []string{"DELETE /actuator/caches", "X-Api-Token: <redacted>", "X-Trace: t1", "orders-a", "orders-b", "orders-c", "nothing was sent"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("dry run output lacks %q\n%s", want, r)
		}
	}
	if strings.Contains(r.stdout, "env-secret-value") {
		t.Errorf("dry run shows the secret\n%s", r)
	}
	wantRequests(t, ns, map[string]int{"orders-a": 0, "orders-b": 0, "orders-c": 0})
}

func TestMissingProxyPermission(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	user := grantRole(t, ns, "reader", readRules...)
	r := broadcast(t, ns, append(clearCaches, "--as", user)...)
	if r.code != 2 {
		t.Fatalf("want exit 2\n%s", r)
	}
	for _, want := range []string{`missing permission: DELETE requests need the "delete" verb on pods/proxy in namespace "` + ns + `"`} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr lacks %q\n%s", want, r)
		}
	}
	wantRequests(t, ns, map[string]int{"orders-a": 0, "orders-b": 0, "orders-c": 0})
}

func TestNoPodsMatched(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	r := broadcast(t, ns, "-l", "app=nothing", "--port", "8080", "-X", "GET", "/")
	if r.code != 2 || !strings.Contains(r.stderr, "no ready pods matched selector app=nothing") {
		t.Fatalf("want exit 2 and the target in the message\n%s", r)
	}
}

func TestJSONOutputMatchesTheSchema(t *testing.T) {
	t.Parallel()
	ns := threeEchoPods(t)
	schema, err := jsonschema.NewCompiler().Compile("../../schemas/output-v1.json")
	must(t, err)
	for _, a := range [][]string{
		append(clearCaches, "-o", "json"),
		append(clearCaches, "-o", "json", "--dry-run"),
		{"-l", "app=orders", "--port", "8080", "-X", "GET", "/x", "-o", "json"},
	} {
		r := broadcast(t, ns, a...)
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(r.stdout))
		if err != nil {
			t.Fatalf("%v\n%s", err, r)
		}
		if err := schema.Validate(doc); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
}

func TestHundredPodsFinishQuickly(t *testing.T) {
	if os.Getenv("E2E_SKIP_SCALE") != "" {
		t.Skip("E2E_SKIP_SCALE is set")
	}
	ns := newNamespace(t)
	labels := map[string]string{"app": "fleet"}
	pod := echoPod("fleet")
	pod.Spec.Containers[0].ReadinessProbe.PeriodSeconds = 5
	_, err := client.AppsV1().Deployments(ns).Create(ctx(t), deployment("fleet", 100, labels, pod), metav1.CreateOptions{})
	must(t, err)
	waitDeployment(t, ns, "fleet", 100, 5*time.Minute)

	// The first run right after 100 pods start mostly measures an API server that is still busy
	// with them, so it is only logged; the second run is the one held to the target.
	for _, timed := range []bool{false, true} {
		start := time.Now()
		r := broadcast(t, ns, "deploy/fleet", "--port", "http", "-X", "GET", "/")
		elapsed := time.Since(start)
		if r.code != 0 || !strings.Contains(r.stdout, "100 targeted: 100 succeeded") {
			t.Fatalf("want 100 successes\n%s", r)
		}
		t.Logf("100 pods in %s (timed: %v)", elapsed.Round(time.Millisecond), timed)
		if timed && elapsed > 10*time.Second {
			t.Errorf("took %s, want under 10s", elapsed)
		}
	}
}

// TestPodInSeveralSlicesIsCalledOnce covers dual-stack Services, which list each pod once per
// address family. On a dual-stack cluster it uses a real dual-stack Service; elsewhere it writes
// the two EndpointSlices itself.
func TestPodInSeveralSlicesIsCalledOnce(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createPods(t, ns, echoPod("orders-a"), echoPod("orders-b"))

	if os.Getenv("E2E_DUAL_STACK") != "" {
		svc := ordersService(false)
		policy := corev1.IPFamilyPolicyRequireDualStack
		svc.Spec.IPFamilyPolicy = &policy
		svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
		createService(t, ns, svc)
		waitEndpoints(t, ns, "orders", 2)
		slices, err := client.DiscoveryV1().EndpointSlices(ns).List(ctx(t), metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=orders"})
		must(t, err)
		if len(slices.Items) < 2 {
			t.Fatalf("dual-stack Service has %d EndpointSlices, want 2", len(slices.Items))
		}
	} else {
		createService(t, ns, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "orders"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)}}},
		})
		for family, addr := range map[discoveryv1.AddressType]string{discoveryv1.AddressTypeIPv4: "10.255.0.1", discoveryv1.AddressTypeIPv6: "fd00::1"} {
			slice := &discoveryv1.EndpointSlice{
				ObjectMeta:  metav1.ObjectMeta{GenerateName: "orders-", Labels: map[string]string{discoveryv1.LabelServiceName: "orders"}},
				AddressType: family,
			}
			for _, pod := range []string{"orders-a", "orders-b"} {
				slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{
					Addresses: []string{addr},
					TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: pod},
				})
			}
			_, err := client.DiscoveryV1().EndpointSlices(ns).Create(ctx(t), slice, metav1.CreateOptions{})
			must(t, err)
		}
	}

	r := broadcast(t, ns, "svc/orders", "--port", "http", "-X", "GET", "/")
	if r.code != 0 || !strings.Contains(r.stdout, "2 targeted: 2 succeeded") {
		t.Fatalf("want 2 calls\n%s", r)
	}
	wantRequests(t, ns, map[string]int{"orders-a": 1, "orders-b": 1})
}

func TestRBACVerbForEachMethod(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createPods(t, ns, echoPod("orders-a"))
	for method, verb := range map[string]string{"GET": "get", "HEAD": "get", "POST": "create", "PUT": "update", "PATCH": "patch", "DELETE": "delete"} {
		t.Run(method, func(t *testing.T) {
			allowed := grantRole(t, ns, "allow-"+strings.ToLower(method), append(readRules, proxyRule(verb))...)
			r := broadcast(t, ns, "-l", "app=orders", "--port", "http", "-X", method, "/rbac", "--as", allowed)
			if r.code != 0 {
				t.Errorf("%s with the %q verb failed\n%s", method, verb, r)
			}
			other := "get"
			if verb == "get" {
				other = "create"
			}
			denied := grantRole(t, ns, "deny-"+strings.ToLower(method), append(readRules, proxyRule(other))...)
			r = broadcast(t, ns, "-l", "app=orders", "--port", "http", "-X", method, "/rbac", "--as", denied)
			if r.code != 2 || !strings.Contains(r.stderr, `need the "`+verb+`" verb`) {
				t.Errorf("%s with only the %q verb: want exit 2 naming %q\n%s", method, other, verb, r)
			}
		})
	}
}

func TestRequestReachesThePodUnchanged(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createPods(t, ns, echoPod("orders-a"))
	_, err := client.CoreV1().Secrets(ns).Create(ctx(t), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth"},
		Data:       map[string][]byte{"X-Client-Key": []byte("secret-value")},
	}, metav1.CreateOptions{})
	must(t, err)
	r := broadcastEnv(t, ns, []string{"E2E_TOKEN=env-secret-value"}, "-l", "app=orders", "--port", "http", "-X", "PATCH", "/a/b?q=x%20y&n=1",
		"-H", "Content-Type: application/json", "-H", "X-Trace: t1", "-H", "Host: orders.internal",
		"--header-from-env", "X-Api-Token=E2E_TOKEN", "--headers-from-secret", "auth", "-d", `{"k":1}`)
	if r.code != 0 {
		t.Fatalf("PATCH failed\n%s", r)
	}
	for _, leaked := range []string{"env-secret-value", "secret-value"} {
		if strings.Contains(r.stdout+r.stderr, leaked) {
			t.Errorf("output shows %q\n%s", leaked, r)
		}
	}
	recs := requests(t, ns, "orders-a")
	if len(recs) != 1 {
		t.Fatalf("received %d requests, want 1", len(recs))
	}
	got := recs[0]
	h := func(name string) string { return strings.Join(got.Headers[name], ",") }
	if got.Method != http.MethodPatch || got.Path != "/a/b?q=x%20y&n=1" || got.Body != `{"k":1}` || got.Host != "orders.internal" {
		t.Errorf("pod received %s %s host=%s body=%q", got.Method, got.Path, got.Host, got.Body)
	}
	if h("Content-Type") != "application/json" || h("X-Trace") != "t1" || h("X-Api-Token") != "env-secret-value" || h("X-Client-Key") != "secret-value" {
		t.Errorf("pod received headers %v", got.Headers)
	}
	if !strings.HasPrefix(h("User-Agent"), "kubectl-broadcast/") {
		t.Errorf("User-Agent = %q", h("User-Agent"))
	}
}

func TestHTTPSAndAnnotations(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	tls := map[string]string{
		"broadcast.kumarprabhashanand.github.io/port":   "https",
		"broadcast.kumarprabhashanand.github.io/scheme": "https",
	}
	createPods(t, ns, echoPod("tls-a", annotated(tls)), echoPod("tls-b", annotated(tls)))
	r := broadcast(t, ns, "-l", "app=orders", "-X", "GET", "/secure", "-o", "json")
	rep := parseReport(t, r)
	if r.code != 0 || rep.Summary.Succeeded != 2 {
		t.Fatalf("want 2 successes over HTTPS\n%s", r)
	}
	wantRequests(t, ns, map[string]int{"tls-a": 1, "tls-b": 1})
}

func TestLargeResponseIsCut(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createPods(t, ns, echoPod("orders-a"))
	r := broadcast(t, ns, "-l", "app=orders", "--port", "http", "-X", "GET", "/__big?bytes=3145728", "-o", "json")
	rep := parseReport(t, r)
	if r.code != 0 || len(rep.Pods) != 1 || len(rep.Pods[0].Body) != 1<<20 {
		t.Fatalf("want success with a 1 MiB body, got %d bytes\n%s", len(rep.Pods[0].Body), r.stderr)
	}
}

func TestWorkloadTargets(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	labels := map[string]string{"app": "web"}
	_, err := client.AppsV1().Deployments(ns).Create(ctx(t), deployment("web", 2, labels, echoPod("web")), metav1.CreateOptions{})
	must(t, err)
	waitDeployment(t, ns, "web", 2, 3*time.Minute)
	createPods(t, ns, echoPod("stray", labeled(map[string]string{"app": "other"})))

	for _, target := range []string{"deploy/web", "deployment/web"} {
		r := broadcast(t, ns, target, "--port", "8080", "-X", "GET", "/")
		if r.code != 0 || !strings.Contains(r.stdout, "2 targeted: 2 succeeded") {
			t.Errorf("%s: want the 2 Deployment pods\n%s", target, r)
		}
	}
	rs, err := client.AppsV1().ReplicaSets(ns).List(ctx(t), metav1.ListOptions{LabelSelector: "app=web"})
	must(t, err)
	r := broadcast(t, ns, "rs/"+rs.Items[0].Name, "--port", "8080", "-X", "GET", "/")
	if r.code != 0 || !strings.Contains(r.stdout, "2 targeted: 2 succeeded") {
		t.Errorf("ReplicaSet target\n%s", r)
	}
	wantRequests(t, ns, map[string]int{"stray": 0})
}

func TestUnsupportedRequestsAreRefused(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	for _, a := range [][]string{
		{"-l", "app=orders", "--port", "http", "-X", "BAN", "/"},
		{"-l", "app=orders", "--port", "http", "-H", "Authorization: Bearer x", "-X", "GET", "/"},
	} {
		if r := broadcast(t, ns, a...); r.code != 2 {
			t.Errorf("%v: want exit 2\n%s", a, r)
		}
	}
}

func TestRunsAsAKubectlPlugin(t *testing.T) {
	kubectl, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not found")
	}
	cmd := exec.CommandContext(ctx(t), kubectl, "broadcast", "version")
	cmd.Env = append(os.Environ(), "PATH="+pluginDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "kubectl-broadcast ") {
		t.Errorf("kubectl broadcast version: %v\n%s", err, out)
	}
}

func TestExampleRoleIsEnough(t *testing.T) {
	t.Parallel()
	ns := newNamespace(t)
	createPods(t, ns, echoPod("orders-a"))
	raw, err := os.ReadFile("../../examples/rbac/direct-user.yaml")
	must(t, err)
	var role rbacv1.Role
	must(t, yaml.UnmarshalStrict(raw, &role))
	user := grantRole(t, ns, "example-user", role.Rules...)
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		if r := broadcast(t, ns, "-l", "app=orders", "--port", "http", "-X", method, "/", "--as", user); r.code != 0 {
			t.Errorf("%s as the example Role\n%s", method, r)
		}
	}
	svc := ordersService(false)
	createService(t, ns, svc)
	waitEndpoints(t, ns, "orders", 1)
	if r := broadcast(t, ns, "svc/orders", "--port", "http", "-X", "GET", "/", "--as", user); r.code != 0 {
		t.Errorf("Service target as the example Role\n%s", r)
	}
}
