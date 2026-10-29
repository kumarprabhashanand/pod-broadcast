package target

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func ptr(p intstr.IntOrString) *intstr.IntOrString { return &p }

func TestEndpointsPortRules(t *testing.T) {
	management := container(port("management", 8081), port("http", 8080))
	tests := []struct {
		name   string
		pod    *corev1.Pod
		svc    []corev1.ServicePort
		choice PortChoice
		want   int32
	}{
		{
			name:   "explicit number wins over the annotation",
			pod:    newPod("a", management, annotated(AnnotationPort, "9000")),
			choice: PortChoice{Port: ptr(intstr.FromInt32(8081))},
			want:   8081,
		},
		{
			name:   "explicit name is resolved on the pod",
			pod:    newPod("a", management),
			choice: PortChoice{Port: ptr(intstr.FromString("management"))},
			want:   8081,
		},
		{name: "annotation number", pod: newPod("a", annotated(AnnotationPort, "9000")), want: 9000},
		{name: "annotation name", pod: newPod("a", management, annotated(AnnotationPort, "http")), want: 8080},
		{
			name:   "annotation wins over the preset",
			pod:    newPod("a", management, annotated(AnnotationPort, "http")),
			choice: PortChoice{PresetPort: "management"},
			want:   8080,
		},
		{name: "preset default name", pod: newPod("a", management), choice: PortChoice{PresetPort: "management"}, want: 8081},
		{
			name:   "preset wins over the Service port",
			pod:    newPod("a", management),
			svc:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("http")}},
			choice: PortChoice{PresetPort: "management"},
			want:   8081,
		},
		{name: "Service target port number", pod: newPod("a"), svc: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(8080)}}, want: 8080},
		{name: "Service target port name", pod: newPod("a", management), svc: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromString("http")}}, want: 8080},
		{name: "Service port without target port", pod: newPod("a"), svc: []corev1.ServicePort{{Port: 80}}, want: 80},
		{
			name: "sidecar container port",
			pod: newPod("a", func(p *corev1.Pod) {
				always := corev1.ContainerRestartPolicyAlways
				p.Spec.InitContainers = []corev1.Container{{Name: "proxy", RestartPolicy: &always, Ports: []corev1.ContainerPort{port("admin", 15000)}}}
			}),
			choice: PortChoice{Port: ptr(intstr.FromString("admin"))},
			want:   15000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eps, err := Endpoints(Set{Pods: []*corev1.Pod{tt.pod}, ServicePorts: tt.svc}, tt.choice)
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			if eps[0].Err != nil {
				t.Fatalf("pod error: %v", eps[0].Err)
			}
			if eps[0].Port != tt.want {
				t.Errorf("port = %d, want %d", eps[0].Port, tt.want)
			}
		})
	}
}

func TestEndpointsWithoutAnyRule(t *testing.T) {
	tests := []struct {
		name string
		svc  []corev1.ServicePort
	}{
		{name: "workload target"},
		{name: "Service with several ports", svc: []corev1.ServicePort{{Name: "http", Port: 80}, {Name: "management", Port: 8081}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Endpoints(Set{Pods: []*corev1.Pod{newPod("a"), newPod("b")}, ServicePorts: tt.svc}, PortChoice{})
			if !errors.Is(err, ErrNoPort) {
				t.Errorf("error = %v, want ErrNoPort", err)
			}
		})
	}
}

func TestEndpointsPodErrors(t *testing.T) {
	management := container(port("management", 8081))
	byName := PortChoice{Port: ptr(intstr.FromString("management"))}
	tests := []struct {
		name   string
		pod    *corev1.Pod
		choice PortChoice
		want   error
	}{
		{name: "no annotation while another pod has one", pod: newPod("b"), want: ErrNoPort},
		{name: "named port missing", pod: newPod("b", container(port("http", 8080))), choice: byName, want: ErrPortNotFound},
		{name: "named port in two containers", pod: newPod("b", management, management), choice: byName, want: ErrAmbiguousPort},
		{
			name:   "UDP port with the name",
			pod:    newPod("b", container(corev1.ContainerPort{Name: "management", ContainerPort: 8081, Protocol: corev1.ProtocolUDP})),
			choice: byName,
			want:   ErrPortNotFound,
		},
		{
			name: "port on a plain init container",
			pod: newPod("b", func(p *corev1.Pod) {
				p.Spec.InitContainers = []corev1.Container{{Name: "setup", Ports: []corev1.ContainerPort{port("management", 8081)}}}
			}),
			choice: byName,
			want:   ErrPortNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok := newPod("a", management, annotated(AnnotationPort, "8081"))
			eps, err := Endpoints(Set{Pods: []*corev1.Pod{ok, tt.pod}}, tt.choice)
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			if eps[0].Err != nil || eps[0].Port != 8081 {
				t.Errorf("healthy pod = %+v, want port 8081 and no error", eps[0])
			}
			if !errors.Is(eps[1].Err, tt.want) {
				t.Errorf("pod error = %v, want %v", eps[1].Err, tt.want)
			}
		})
	}
}

func TestEndpointsInvalidPortAnnotation(t *testing.T) {
	eps, err := Endpoints(Set{Pods: []*corev1.Pod{newPod("a", annotated(AnnotationPort, "99999"))}}, PortChoice{})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if eps[0].Err == nil {
		t.Error("pod error = nil, want an invalid annotation error")
	}
}

func TestEndpointsScheme(t *testing.T) {
	numeric := PortChoice{Port: ptr(intstr.FromInt32(8080))}
	tests := []struct {
		name     string
		pod      *corev1.Pod
		explicit string
		want     string
		wantErr  bool
	}{
		{name: "default", pod: newPod("a"), want: "http"},
		{name: "annotation", pod: newPod("a", annotated(AnnotationScheme, "https")), want: "https"},
		{name: "explicit wins", pod: newPod("a", annotated(AnnotationScheme, "https")), explicit: "http", want: "http"},
		{name: "invalid annotation", pod: newPod("a", annotated(AnnotationScheme, "ftp")), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choice := numeric
			choice.Scheme = tt.explicit
			eps, err := Endpoints(Set{Pods: []*corev1.Pod{tt.pod}}, choice)
			if err != nil {
				t.Fatalf("Endpoints: %v", err)
			}
			if tt.wantErr {
				if eps[0].Err == nil {
					t.Error("pod error = nil, want an error")
				}
				return
			}
			if eps[0].Err != nil || eps[0].Scheme != tt.want {
				t.Errorf("endpoint = %+v, want scheme %q", eps[0], tt.want)
			}
		})
	}
}

func TestEndpointsEmptySet(t *testing.T) {
	eps, err := Endpoints(Set{}, PortChoice{})
	if err != nil || len(eps) != 0 {
		t.Errorf("Endpoints(empty) = %v, %v; want no endpoints and no error", eps, err)
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		in      string
		want    intstr.IntOrString
		wantErr bool
	}{
		{in: "8081", want: intstr.FromInt32(8081)},
		{in: "65535", want: intstr.FromInt32(65535)},
		{in: "management", want: intstr.FromString("management")},
		{in: "0", wantErr: true},
		{in: "65536", wantErr: true},
		{in: "4294975377", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "Management", wantErr: true},
		{in: "a-name-longer-than-15", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParsePort(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParsePort(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("ParsePort(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
			}
		})
	}
}
