package target

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const ns = "shop"

var ordersLabels = map[string]string{"app": "orders"}

func newPod(name string, opts ...func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID(name + "-uid"), Labels: ordersLabels},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      "10.0.0.1",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func notReady(p *corev1.Pod) { p.Status.Conditions[0].Status = corev1.ConditionFalse }

func withLabels(l map[string]string) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Labels = l }
}

func annotated(key, value string) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		p.Annotations[key] = value
	}
}

func container(ports ...corev1.ContainerPort) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		name := fmt.Sprintf("c%d", len(p.Spec.Containers))
		p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: name, Ports: ports})
	}
}

func port(name string, number int32) corev1.ContainerPort {
	return corev1.ContainerPort{Name: name, ContainerPort: number, Protocol: corev1.ProtocolTCP}
}

func newService(name string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.ServiceSpec{Selector: ordersLabels, Ports: ports},
	}
}

// newSlice reports every endpoint as ready, as a Service with publishNotReadyAddresses does.
func newSlice(name, service string, family discoveryv1.AddressType, podNames ...string) *discoveryv1.EndpointSlice {
	s := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{discoveryv1.LabelServiceName: service}},
		AddressType: family,
	}
	ready := true
	for _, n := range podNames {
		s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.0.0.1"},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: n},
		})
	}
	return s
}

func podNames(pods []*corev1.Pod) []string {
	names := make([]string, len(pods))
	for i, p := range pods {
		names[i] = p.Name
	}
	return names
}
