//go:build e2e

// Package e2e runs kubectl-broadcast against a real cluster. Run it with test/e2e/run.sh, which
// creates a kind cluster and loads the test images.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

const (
	echoImage   = "pod-broadcast-echo:e2e"
	springImage = "pod-broadcast-spring:e2e"
)

var (
	plugin      string
	pluginDir   string
	kubeContext = os.Getenv("E2E_CONTEXT")
	client      kubernetes.Interface
	// objects reads and writes Broadcasts and BroadcastPolicies.
	objects ctrlclient.Client
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "kubectl-broadcast-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	pluginDir = dir
	plugin = filepath.Join(dir, "kubectl-broadcast")
	if out, err := exec.CommandContext(context.Background(), "go", "build", "-o", plugin, "../../cmd/kubectl-broadcast").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the plugin: %v\n%s", err, out)
		return 1
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	config.QPS, config.Burst = 50, 100
	client = kubernetes.NewForConfigOrDie(config)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	if objects, err = ctrlclient.New(config, ctrlclient.Options{Scheme: scheme}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newNamespace(t *testing.T) string {
	t.Helper()
	ns, err := client.CoreV1().Namespaces().Create(ctx(t), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-"}}, metav1.CreateOptions{})
	must(t, err)
	t.Cleanup(func() {
		_ = client.CoreV1().Namespaces().Delete(context.Background(), ns.Name, metav1.DeleteOptions{})
	})
	return ns.Name
}

// echoPod runs the echo server on 8080 (http) and 8081 (management), TLS on 8443, stats on 9090.
func echoPod(name string, opts ...func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": "orders"}},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: new(int64),
			Containers: []corev1.Container{{
				Name:            "echo",
				Image:           echoImage,
				ImagePullPolicy: corev1.PullNever,
				Args:            []string{"-listen", ":8080,:8081", "-tls-listen", ":8443"},
				Ports: []corev1.ContainerPort{
					{Name: "http", ContainerPort: 8080},
					{Name: "management", ContainerPort: 8081},
					{Name: "https", ContainerPort: 8443},
					{Name: "stats", ContainerPort: 9090},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ready", Port: intstr.FromString("stats")}},
					PeriodSeconds: 1,
				},
			}},
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func env(name, value string) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Env = append(p.Spec.Containers[0].Env, corev1.EnvVar{Name: name, Value: value})
	}
}

func notReady(p *corev1.Pod) { env("READY", "false")(p) }

func args(a ...string) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Spec.Containers[0].Args = a }
}

func labeled(l map[string]string) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Labels = l }
}

func annotated(a map[string]string) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Annotations = a }
}

// createPods creates pods and waits until each is running and, unless it is meant to stay not
// ready, ready.
func createPods(t *testing.T, ns string, pods ...*corev1.Pod) {
	t.Helper()
	for _, p := range pods {
		_, err := client.CoreV1().Pods(ns).Create(ctx(t), p, metav1.CreateOptions{})
		must(t, err)
	}
	for _, p := range pods {
		wantReady := true
		for _, e := range p.Spec.Containers[0].Env {
			if e.Name == "READY" && e.Value == "false" {
				wantReady = false
			}
		}
		waitPod(t, ns, p.Name, wantReady)
	}
}

func waitPod(t *testing.T, ns, name string, wantReady bool) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx(t), 500*time.Millisecond, 3*time.Minute, true, func(c context.Context) (bool, error) {
		p, err := client.CoreV1().Pods(ns).Get(c, name, metav1.GetOptions{})
		if err != nil || p.Status.PodIP == "" || len(p.Status.ContainerStatuses) == 0 {
			return false, nil
		}
		if !wantReady {
			return p.Status.ContainerStatuses[0].Started != nil && *p.Status.ContainerStatuses[0].Started, nil
		}
		return p.Status.ContainerStatuses[0].Ready, nil
	})
	if err != nil {
		t.Fatalf("pod %s/%s: %v", ns, name, err)
	}
}

func createService(t *testing.T, ns string, svc *corev1.Service) {
	t.Helper()
	_, err := client.CoreV1().Services(ns).Create(ctx(t), svc, metav1.CreateOptions{})
	must(t, err)
}

func ordersService(publishNotReady bool) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "orders"},
		Spec: corev1.ServiceSpec{
			Selector:                 map[string]string{"app": "orders"},
			PublishNotReadyAddresses: publishNotReady,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromString("http")},
				{Name: "management", Port: 8081, TargetPort: intstr.FromString("management")},
			},
		},
	}
}

// waitEndpoints waits until the Service's EndpointSlices list n pods, ready or not.
func waitEndpoints(t *testing.T, ns, service string, n int) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx(t), 500*time.Millisecond, 2*time.Minute, true, func(c context.Context) (bool, error) {
		slices, err := client.DiscoveryV1().EndpointSlices(ns).List(c, metav1.ListOptions{LabelSelector: "kubernetes.io/service-name=" + service})
		if err != nil {
			return false, nil
		}
		pods := map[string]bool{}
		for _, s := range slices.Items {
			for _, e := range s.Endpoints {
				if e.TargetRef != nil {
					pods[e.TargetRef.Name] = true
				}
			}
		}
		return len(pods) == n, nil
	})
	if err != nil {
		t.Fatalf("endpoints of %s/%s: %v", ns, service, err)
	}
}

type result struct {
	code   int
	stdout string
	stderr string
}

// broadcast runs the plugin in namespace ns.
func broadcast(t *testing.T, ns string, a ...string) result {
	t.Helper()
	return broadcastEnv(t, ns, nil, a...)
}

// broadcastEnv runs the plugin with extra environment variables such as "NAME=value".
func broadcastEnv(t *testing.T, ns string, env []string, a ...string) result {
	t.Helper()
	a = append(a, "-n", ns)
	if kubeContext != "" {
		a = append(a, "--context", kubeContext)
	}
	cmd := exec.CommandContext(ctx(t), plugin, a...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (r result) String() string {
	return "exit " + strconv.Itoa(r.code) + "\nstdout:\n" + r.stdout + "stderr:\n" + r.stderr
}

type report struct {
	Outcome string
	Summary struct{ Targeted, Succeeded, Failed, Gone int }
	Pods    []struct {
		Name, State, Error, Body string
		Code, Attempts           int
	}
}

func parseReport(t *testing.T, r result) report {
	t.Helper()
	var rep report
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("parsing JSON output: %v\n%s", err, r)
	}
	return rep
}

type record struct {
	Method  string
	Path    string
	Host    string
	Headers map[string][]string
	Body    string
}

// requests returns what an echo pod received, read from its stats port.
func requests(t *testing.T, ns, pod string) []record {
	t.Helper()
	raw, err := client.CoreV1().Pods(ns).ProxyGet("http", pod, "9090", "/requests", nil).DoRaw(ctx(t))
	must(t, err)
	var recs []record
	must(t, json.Unmarshal(raw, &recs))
	return recs
}

func wantRequests(t *testing.T, ns string, want map[string]int) {
	t.Helper()
	for pod, n := range want {
		if got := len(requests(t, ns, pod)); got != n {
			t.Errorf("pod %s received %d requests, want %d", pod, got, n)
		}
	}
}

// grantRole creates a ServiceAccount with the rules, waits until they apply, and returns the --as
// flag value for it.
func grantRole(t *testing.T, ns, name string, rules ...rbacv1.PolicyRule) string {
	t.Helper()
	c := ctx(t)
	_, err := client.CoreV1().ServiceAccounts(ns).Create(c, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
	must(t, err)
	_, err = client.RbacV1().Roles(ns).Create(c, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: rules}, metav1.CreateOptions{})
	must(t, err)
	_, err = client.RbacV1().RoleBindings(ns).Create(c, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: name, Namespace: ns}},
	}, metav1.CreateOptions{})
	must(t, err)
	user := "system:serviceaccount:" + ns + ":" + name
	// The authorizer sees new RBAC objects a moment later; once the first rule applies, all do.
	first := rules[0]
	eventually(t, 30*time.Second, "the role to apply", func() bool {
		review, err := client.AuthorizationV1().SubjectAccessReviews().Create(c, &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{User: user, ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: ns, Group: first.APIGroups[0], Resource: first.Resources[0], Verb: first.Verbs[0],
			}},
		}, metav1.CreateOptions{})
		return err == nil && review.Status.Allowed
	})
	return user
}

// readRules are what a direct-mode user needs besides pods/proxy.
var readRules = []rbacv1.PolicyRule{
	{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get"}},
	{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"list"}},
	{APIGroups: []string{"discovery.k8s.io"}, Resources: []string{"endpointslices"}, Verbs: []string{"list"}},
	{APIGroups: []string{"apps"}, Resources: []string{"deployments", "statefulsets", "daemonsets", "replicasets"}, Verbs: []string{"get"}},
}

func proxyRule(verbs ...string) rbacv1.PolicyRule {
	return rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/proxy"}, Verbs: verbs}
}

func deployment(name string, replicas int32, labels map[string]string, pod *corev1.Pod) *appsv1.Deployment {
	pod.Labels = labels
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: pod.ObjectMeta, Spec: pod.Spec},
		},
	}
}

func waitDeployment(t *testing.T, ns, name string, replicas int32, timeout time.Duration) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx(t), time.Second, timeout, true, func(c context.Context) (bool, error) {
		d, err := client.AppsV1().Deployments(ns).Get(c, name, metav1.GetOptions{})
		return err == nil && d.Status.ReadyReplicas == replicas, nil
	})
	if err != nil {
		t.Fatalf("deployment %s/%s: %v", ns, name, err)
	}
}
