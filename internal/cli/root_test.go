package cli

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeClient(objects ...runtime.Object) clientFunc {
	return func(*genericclioptions.ConfigFlags) (kubernetes.Interface, string, error) {
		return fake.NewClientset(objects...), "shop", nil
	}
}

func noClient(t *testing.T) clientFunc {
	return func(*genericclioptions.ConfigFlags) (kubernetes.Interface, string, error) {
		t.Fatal("the cluster was contacted")
		return nil, "", nil
	}
}

func ordersPod(name string, ready bool) *corev1.Pod {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop", Labels: map[string]string{"app": "orders"}},
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
	for _, n := range []string{"orders-a", "orders-b"} {
		slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{
			Addresses: []string{"10.0.0.1"},
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: n},
		})
	}
	return []runtime.Object{svc, slice}
}

func TestRun(t *testing.T) {
	twoPorts := ordersService(
		corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080)},
		corev1.ServicePort{Name: "management", Port: 8081, TargetPort: intstr.FromString("management")},
	)
	tests := []struct {
		name       string
		args       []string
		client     clientFunc
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "kubectl-broadcast "},
		{name: "no arguments prints help", args: nil, wantCode: 0, wantStdout: "kubectl broadcast TARGET"},
		{name: "help", args: []string{"--help"}, wantCode: 0, wantStdout: "--include-not-ready"},
		{name: "unknown command", args: []string{"orders"}, wantCode: exitUsage, wantStderr: `unknown command "orders"`},
		{name: "unknown flag", args: []string{"svc/orders", "--bogus"}, wantCode: exitUsage, wantStderr: "unknown flag: --bogus"},
		{name: "missing operation", args: []string{"svc/orders"}, wantCode: exitUsage, wantStderr: "missing operation"},
		{
			name:       "kubeconfig flags are accepted",
			args:       []string{"svc/orders", "-n", "shop", "--context", "dev"},
			wantCode:   exitUsage,
			wantStderr: "missing operation",
		},
		{
			name:       "unknown preset",
			args:       []string{"svc/orders", "spring", "evict-caches"},
			wantCode:   exitUsage,
			wantStderr: `unknown preset "spring evict-caches"`,
		},
		{
			name:       "invalid port is rejected before contacting the cluster",
			args:       []string{"svc/orders", "--port", "70000", "-X", "GET", "/"},
			wantCode:   exitUsage,
			wantStderr: `invalid port "70000"`,
		},
		{
			name:       "invalid scheme is rejected before contacting the cluster",
			args:       []string{"svc/orders", "--scheme", "ftp", "-X", "GET", "/"},
			wantCode:   exitUsage,
			wantStderr: `invalid scheme "ftp"`,
		},
		{
			name:       "target not found",
			args:       []string{"svc/orders", "-X", "GET", "/"},
			client:     fakeClient(),
			wantCode:   exitUsage,
			wantStderr: `service "orders" not found in namespace "shop"`,
		},
		{
			name:       "no ready pods",
			args:       []string{"svc/orders", "-X", "GET", "/"},
			client:     fakeClient(append(ordersService(), ordersPod("orders-a", false))...),
			wantCode:   exitUsage,
			wantStderr: `no ready pods matched service/orders in namespace "shop"; 1 not ready (add --include-not-ready`,
		},
		{
			name:       "port can't be chosen",
			args:       []string{"svc/orders", "-X", "GET", "/"},
			client:     fakeClient(append(twoPorts, ordersPod("orders-a", true))...),
			wantCode:   exitUsage,
			wantStderr: "can't choose a port: use --port NAME|NUMBER, or annotate the pods with broadcast.kumarprabhashanand.github.io/port; the Service's target ports are 8080, management",
		},
		{
			name:       "resolves ready pods",
			args:       []string{"svc/orders", "--port", "management", "-X", "GET", "/"},
			client:     fakeClient(append(twoPorts, ordersPod("orders-a", true), ordersPod("orders-b", true))...),
			wantCode:   exitUsage,
			wantStderr: "resolved 2 pods",
		},
		{
			name:       "includes not-ready pods on request",
			args:       []string{"svc/orders", "--include-not-ready", "--port", "8081", "-X", "GET", "/"},
			client:     fakeClient(append(ordersService(), ordersPod("orders-a", true), ordersPod("orders-b", false))...),
			wantCode:   exitUsage,
			wantStderr: "resolved 2 pods",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := tt.client
			if client == nil {
				client = noClient(t)
			}
			streams, _, out, errOut := genericiooptions.NewTestIOStreams()
			code := run(tt.args, streams, client)
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
