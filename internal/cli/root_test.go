package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
	"github.com/kumarprabhashanand/pod-broadcast/internal/transport"
)

// recorder is a transport that answers per pod and remembers every request.
type recorder struct {
	mu       sync.Mutex
	answers  map[string]func() (request.Response, error)
	requests []sent
}

type sent struct {
	pod  string
	port int32
	path string
	spec request.Spec
}

func (r *recorder) Do(_ context.Context, ep target.Endpoint, spec request.Spec) (request.Response, error) {
	r.mu.Lock()
	path := spec.Path
	if ep.Path != "" {
		path = ep.Path
	}
	r.requests = append(r.requests, sent{pod: ep.Pod.Name, port: ep.Port, path: path, spec: spec})
	answer := r.answers[ep.Pod.Name]
	r.mu.Unlock()
	if answer != nil {
		return answer()
	}
	return request.Response{Code: 204}, nil
}

func fakeCluster(tr request.Transport, objects ...runtime.Object) clusterFunc {
	return fakeClusterWithAccess(tr, true, objects...)
}

// fakeClusterWithAccess answers access reviews for pods/proxy with allowed.
func fakeClusterWithAccess(tr request.Transport, allowed bool, objects ...runtime.Object) clusterFunc {
	return func(*genericclioptions.ConfigFlags) (*cluster, error) {
		cs := fake.NewClientset(objects...)
		cs.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			review := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
			review.Status.Allowed = allowed
			return true, review, nil
		})
		return &cluster{client: cs, namespace: "shop", transport: tr}, nil
	}
}

func noCluster(t *testing.T) clusterFunc {
	return func(*genericclioptions.ConfigFlags) (*cluster, error) {
		t.Fatal("the cluster was contacted")
		return nil, nil
	}
}

func ordersPod(name string, ready bool) *corev1.Pod {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", UID: "uid-" + "pod", Labels: map[string]string{"app": "orders"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "app",
			Ports: []corev1.ContainerPort{{Name: "management", ContainerPort: 8081}},
		}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      "10.0.0.1",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func ordersService(ports ...corev1.ServicePort) []runtime.Object {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "shop"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "orders"}, Ports: ports},
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "orders-1", Namespace: "shop", Labels: map[string]string{discoveryv1.LabelServiceName: "orders"}},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	for _, n := range []string{"orders-a", "orders-b", "orders-c"} {
		slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{
			Addresses: []string{"10.0.0.1"},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: n},
		})
	}
	return []runtime.Object{svc, slice}
}

func threePods() []runtime.Object {
	return append(ordersService(), ordersPod("orders-a", true), ordersPod("orders-b", true), ordersPod("orders-c", true))
}

type runResult struct {
	code   int
	stdout string
	stderr string
	rec    *recorder
}

func runWith(t *testing.T, rec *recorder, objects []runtime.Object, args ...string) runResult {
	t.Helper()
	if rec == nil {
		rec = &recorder{}
	}
	streams, _, out, errOut := genericiooptions.NewTestIOStreams()
	code := run(args, streams, fakeCluster(rec, objects...))
	return runResult{code: code, stdout: out.String(), stderr: errOut.String(), rec: rec}
}

func TestRunWithoutCluster(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantStdout: "kubectl-broadcast "},
		{name: "no arguments prints help", wantStdout: "kubectl broadcast TARGET"},
		{name: "help", args: []string{"--help"}, wantStdout: "Exit codes: 0 every pod succeeded"},
		{name: "unknown command", args: []string{"orders"}, wantCode: 2, wantStderr: `unknown command "orders"`},
		{name: "unknown flag", args: []string{"svc/orders", "--bogus"}, wantCode: 2, wantStderr: "unknown flag: --bogus"},
		{name: "missing operation", args: []string{"svc/orders", "-n", "shop", "--context", "dev"}, wantCode: 2, wantStderr: "missing operation"},
		{name: "unknown preset", args: []string{"svc/orders", "spring", "nope"}, wantCode: 2, wantStderr: `unknown preset "spring nope": run kubectl broadcast presets`},
		{name: "preset with wrong arguments", args: []string{"svc/orders", "spring", "set-log-level", "com.shop"}, wantCode: 2, wantStderr: "usage: kubectl broadcast TARGET spring set-log-level LOGGER LEVEL"},
		{name: "invalid log level", args: []string{"svc/orders", "spring", "set-log-level", "com.shop", "LOUD"}, wantCode: 2, wantStderr: `invalid log level "LOUD"`},
		{name: "cache flag on another preset", args: []string{"svc/orders", "spring", "health", "--cache", "x"}, wantCode: 2, wantStderr: "--cache only applies to spring evict-caches"},
		{name: "body with a preset", args: []string{"svc/orders", "spring", "refresh", "-d", "x"}, wantCode: 2, wantStderr: "-d can't be used with a preset"},
		{name: "base path on a raw request", args: []string{"svc/orders", "--base-path", "/m", "-X", "GET", "/"}, wantCode: 2, wantStderr: "only apply to presets"},
		{name: "invalid base path", args: []string{"svc/orders", "spring", "health", "--base-path", "m"}, wantCode: 2, wantStderr: "must start with /"},
		{name: "presets command", args: []string{"presets"}, wantStdout: "spring evict-caches [--cache NAME]  DELETE {base}/caches[/NAME]"},
		{name: "invalid port", args: []string{"svc/orders", "--port", "70000", "-X", "GET", "/"}, wantCode: 2, wantStderr: `invalid port "70000"`},
		{name: "invalid scheme", args: []string{"svc/orders", "--scheme", "ftp", "-X", "GET", "/"}, wantCode: 2, wantStderr: `invalid scheme "ftp"`},
		{name: "method the proxy refuses", args: []string{"svc/orders", "-X", "BAN", "/"}, wantCode: 2, wantStderr: "method BAN can't be sent"},
		{name: "authorization header", args: []string{"svc/orders", "-H", "Authorization: Bearer x", "-X", "GET", "/"}, wantCode: 2, wantStderr: "the Authorization header can't reach pods"},
		{name: "bad header", args: []string{"svc/orders", "-H", "nocolon", "-X", "GET", "/"}, wantCode: 2, wantStderr: "must look like 'Name: value'"},
		{name: "missing env var", args: []string{"svc/orders", "--header-from-env", "X-T=PB_TEST_UNSET", "-X", "GET", "/"}, wantCode: 2, wantStderr: "PB_TEST_UNSET"},
		{name: "bad concurrency", args: []string{"svc/orders", "--concurrency", "0", "-X", "GET", "/"}, wantCode: 2, wantStderr: "--concurrency"},
		{name: "bad timeout", args: []string{"svc/orders", "--timeout", "0s", "-X", "GET", "/"}, wantCode: 2, wantStderr: "--timeout"},
		{name: "bad retries", args: []string{"svc/orders", "--retries", "11", "-X", "GET", "/"}, wantCode: 2, wantStderr: "--retries"},
		{name: "bad success codes", args: []string{"svc/orders", "--success-codes", "2x", "-X", "GET", "/"}, wantCode: 2, wantStderr: "invalid status code"},
		{name: "bad output", args: []string{"svc/orders", "-o", "xml", "-X", "GET", "/"}, wantCode: 2, wantStderr: `unknown output format "xml"`},
		{name: "missing body file", args: []string{"svc/orders", "-d", "@/does/not/exist", "-X", "POST", "/"}, wantCode: 2, wantStderr: "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, out, errOut := genericiooptions.NewTestIOStreams()
			code := run(tt.args, streams, noCluster(t))
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

func TestEveryReadyPodIsCalledOnce(t *testing.T) {
	r := runWith(t, nil, append(threePods(), ordersPod("orders-d", false)),
		"svc/orders", "--port", "management", "-X", "delete", "/actuator/caches")
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr %q", r.code, r.stderr)
	}
	if len(r.rec.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(r.rec.requests))
	}
	for _, s := range r.rec.requests {
		if s.port != 8081 || s.spec.Method != http.MethodDelete || s.spec.Path != "/actuator/caches" {
			t.Errorf("request = %+v", s)
		}
	}
	if !strings.Contains(r.stdout, "3 targeted: 3 succeeded") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
}

func TestExitCodes(t *testing.T) {
	refused := func() (request.Response, error) { return request.Response{}, errors.New("connection refused") }
	status500 := func() (request.Response, error) { return request.Response{Code: 500}, nil }
	tests := []struct {
		name    string
		answers map[string]func() (request.Response, error)
		want    int
	}{
		{"all succeed", nil, 0},
		{"one fails", map[string]func() (request.Response, error){"orders-b": status500}, 1},
		{"all fail", map[string]func() (request.Response, error){"orders-a": refused, "orders-b": refused, "orders-c": refused}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWith(t, &recorder{answers: tt.answers}, threePods(), "svc/orders", "--port", "8081", "--backoff", "0s", "-X", "GET", "/")
			if r.code != tt.want {
				t.Errorf("exit code = %d, want %d\n%s%s", r.code, tt.want, r.stdout, r.stderr)
			}
		})
	}
}

func TestForbiddenStopsWithTheMissingVerb(t *testing.T) {
	forbidden := func() (request.Response, error) {
		return request.Response{}, &request.FatalError{Err: &transport.ForbiddenError{
			Message: `pods "orders-a:8081" is forbidden: User "dev" cannot create resource "pods/proxy" in API group "" in the namespace "shop"`,
		}}
	}
	answers := map[string]func() (request.Response, error){"orders-a": forbidden, "orders-b": forbidden, "orders-c": forbidden}
	r := runWith(t, &recorder{answers: answers}, threePods(), "svc/orders", "--port", "8081", "-X", "POST", "/refresh")
	if r.code != 2 {
		t.Errorf("exit code = %d, want 2", r.code)
	}
	for _, want := range []string{`cannot create resource "pods/proxy"`, `POST requests need the "create" verb on pods/proxy`} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", r.stderr, want)
		}
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want nothing", r.stdout)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	t.Setenv("PB_TEST_TOKEN", "env-secret")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: "shop"},
		Data:       map[string][]byte{"X-Client-Key": []byte("secret-value")},
	}
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			r := runWith(t, nil, append(threePods(), secret),
				"svc/orders", "--port", "8081", "--dry-run", "-o", format,
				"-H", "X-Trace: t1", "--header-from-env", "X-Api-Token=PB_TEST_TOKEN", "--headers-from-secret", "auth",
				"-X", "POST", "/refresh", "-d", `{"a":1}`)
			if r.code != 0 {
				t.Fatalf("exit code = %d, stderr %q", r.code, r.stderr)
			}
			if len(r.rec.requests) != 0 {
				t.Errorf("dry run sent %d requests", len(r.rec.requests))
			}
			for _, leaked := range []string{"env-secret", "secret-value"} {
				if strings.Contains(r.stdout, leaked) {
					t.Errorf("output shows %q:\n%s", leaked, r.stdout)
				}
			}
			for _, want := range []string{"orders-a", "orders-c", "X-Trace", "t1", "X-Api-Token", "X-Client-Key", `{\"a\":1}`} {
				if format == "table" {
					want = strings.ReplaceAll(want, `\"`, `"`)
				}
				if !strings.Contains(r.stdout, want) {
					t.Errorf("output lacks %q:\n%s", want, r.stdout)
				}
			}
		})
	}
}

func TestHeadersAndBodyAreSent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "body.json")
	if err := os.WriteFile(file, []byte(`{"configuredLevel":"DEBUG"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PB_TEST_TOKEN", "env-secret")
	r := runWith(t, nil, threePods(),
		"svc/orders", "--port", "8081", "-H", "Content-Type: text/plain", "-H", "content-type: application/json",
		"--header-from-env", "X-Api-Token=PB_TEST_TOKEN", "-X", "POST", "/actuator/loggers/com.shop", "-d", "@"+file)
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr %q", r.code, r.stderr)
	}
	s := r.rec.requests[0].spec
	if string(s.Body) != `{"configuredLevel":"DEBUG"}` {
		t.Errorf("body = %q", s.Body)
	}
	got := map[string]string{}
	for _, h := range s.Headers {
		got[h.Name] = h.Value
	}
	if got["Content-Type"] != "application/json" || got["X-Api-Token"] != "env-secret" || len(got) != 2 {
		t.Errorf("headers = %v", got)
	}
}

func TestJSONOutput(t *testing.T) {
	status500 := func() (request.Response, error) { return request.Response{Code: 500, Body: []byte("boom")}, nil }
	r := runWith(t, &recorder{answers: map[string]func() (request.Response, error){"orders-b": status500}}, threePods(),
		"svc/orders", "--port", "8081", "--retries", "0", "-X", "GET", "/", "-o", "json")
	if r.code != 1 {
		t.Fatalf("exit code = %d, stderr %q", r.code, r.stderr)
	}
	var report struct {
		SchemaVersion string
		Outcome       string
		Target        struct{ Namespace, Kind, Name string }
		Summary       struct{ Targeted, Succeeded, Failed int }
		Pods          []struct {
			Name, State, Error, Body string
			Code, Attempts           int
		}
	}
	if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	if report.SchemaVersion != "v1" || report.Outcome != "PartiallyFailed" || report.Target.Kind != "Service" || report.Summary.Failed != 1 {
		t.Errorf("report = %+v", report)
	}
	if b := report.Pods[1]; b.Name != "orders-b" || b.State != "Failed" || b.Code != 500 || b.Error != "HTTP 500" || b.Body != "boom" || b.Attempts != 1 {
		t.Errorf("orders-b = %+v", b)
	}
}

func TestResolutionErrors(t *testing.T) {
	twoPorts := ordersService(
		corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
		corev1.ServicePort{Name: "management", Port: 8081, TargetPort: intstr.FromString("management")},
	)
	tests := []struct {
		name    string
		objects []runtime.Object
		args    []string
		want    string
	}{
		{"target not found", nil, []string{"svc/orders", "-X", "GET", "/"}, `service "orders" not found in namespace "shop"`},
		{"no ready pods", append(ordersService(), ordersPod("orders-a", false)), []string{"svc/orders", "-X", "GET", "/"}, `no ready pods matched service/orders in namespace "shop"; 1 not ready`},
		{"no pods for selector", nil, []string{"-l", "app=nothing", "--port", "1", "-X", "GET", "/"}, `no ready pods matched selector app=nothing`},
		{"port can't be chosen", append(twoPorts, ordersPod("orders-a", true)), []string{"svc/orders", "-X", "GET", "/"}, "the Service's target ports are 8080, management"},
		{"missing secret", threePods(), []string{"svc/orders", "--headers-from-secret", "nope", "-X", "GET", "/"}, `secrets "nope" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWith(t, nil, tt.objects, tt.args...)
			if r.code != 2 || !strings.Contains(r.stderr, tt.want) {
				t.Errorf("exit %d, stderr %q; want 2 and %q", r.code, r.stderr, tt.want)
			}
		})
	}
}

func TestSecretAuthorizationHeaderIsRefused(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: "shop"},
		Data:       map[string][]byte{"Authorization": []byte("Bearer x")},
	}
	r := runWith(t, nil, append(threePods(), secret), "svc/orders", "--port", "8081", "--headers-from-secret", "auth", "-X", "GET", "/")
	if r.code != 2 || !strings.Contains(r.stderr, "the Authorization header can't reach pods") || len(r.rec.requests) != 0 {
		t.Errorf("exit %d, stderr %q, %d requests", r.code, r.stderr, len(r.rec.requests))
	}
}

func TestPresetUsesTheManagementPortAndBasePath(t *testing.T) {
	custom := ordersPod("orders-c", true)
	custom.Annotations = map[string]string{target.AnnotationActuatorBasePath: "/manage"}
	objects := append(ordersService(), ordersPod("orders-a", true), ordersPod("orders-b", true), custom)

	r := runWith(t, nil, objects, "svc/orders", "spring", "set-log-level", "com.shop", "debug")
	if r.code != 0 {
		t.Fatalf("exit code = %d, stderr %q", r.code, r.stderr)
	}
	paths := map[string]string{}
	for _, s := range r.rec.requests {
		if s.port != 8081 || s.spec.Method != http.MethodPost || string(s.spec.Body) != `{"configuredLevel":"DEBUG"}` {
			t.Errorf("request = %+v", s)
		}
		paths[s.pod] = s.path
	}
	if paths["orders-a"] != "/actuator/loggers/com.shop" || paths["orders-c"] != "/manage/loggers/com.shop" {
		t.Errorf("paths = %v", paths)
	}

	r = runWith(t, nil, objects, "svc/orders", "spring", "health", "--dry-run")
	for _, want := range []string{"GET /actuator/health", "PATH", "orders-a  10.0.0.1  8081  http    /actuator/health", "/manage/health"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("dry run lacks %q:\n%s", want, r.stdout)
		}
	}
}

func TestMissingProxyPermissionStopsBeforeAnyCall(t *testing.T) {
	for _, method := range []string{"HEAD", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			rec := &recorder{}
			streams, _, out, errOut := genericiooptions.NewTestIOStreams()
			code := run([]string{"svc/orders", "--port", "8081", "-X", method, "/"}, streams, fakeClusterWithAccess(rec, false, threePods()...))
			verb := proxyVerbs[method]
			want := `missing permission: ` + method + ` requests need the "` + verb + `" verb on pods/proxy in namespace "shop"`
			if code != 2 || !strings.Contains(errOut.String(), want) || len(rec.requests) != 0 || out.Len() != 0 {
				t.Errorf("exit %d, stderr %q, %d requests; want 2, %q, none", code, errOut.String(), len(rec.requests), want)
			}
		})
	}
}
