package target

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	AnnotationDomain = "broadcast.kumarprabhashanand.github.io"
	AnnotationPort   = AnnotationDomain + "/port"
	AnnotationScheme = AnnotationDomain + "/scheme"
)

var (
	ErrNoPort        = errors.New("no port chosen")
	ErrPortNotFound  = errors.New("port not found")
	ErrAmbiguousPort = errors.New("ambiguous port")
)

// Endpoint is where one pod is called. When Err is set, the pod can't be called.
type Endpoint struct {
	Pod    *corev1.Pod
	Port   int32
	Scheme string
	Err    error
}

// PortChoice holds an explicitly requested port and scheme, and a preset's default port name.
type PortChoice struct {
	Port       *intstr.IntOrString
	Scheme     string
	PresetPort string
}

// Endpoints chooses a port and scheme for every pod in set. For the port the first rule that applies
// wins: the explicit port, the pod's port annotation, the preset's default port, the Service's only
// port. It returns ErrNoPort when no rule applies to any pod.
func Endpoints(set Set, choice PortChoice) ([]Endpoint, error) {
	var servicePort *intstr.IntOrString
	if len(set.ServicePorts) == 1 {
		p := TargetPort(set.ServicePorts[0])
		servicePort = &p
	}
	endpoints := make([]Endpoint, len(set.Pods))
	applied := 0
	for i, pod := range set.Pods {
		ep := Endpoint{Pod: pod}
		spec, ok, err := portSpec(pod, choice, servicePort)
		switch {
		case err != nil:
			applied++
			ep.Err = err
		case !ok:
			ep.Err = fmt.Errorf("%w: pod has no %s annotation", ErrNoPort, AnnotationPort)
		default:
			applied++
			ep.Port, ep.Err = containerPort(pod, spec)
		}
		if ep.Err == nil {
			ep.Scheme, ep.Err = scheme(pod, choice.Scheme)
		}
		endpoints[i] = ep
	}
	if applied == 0 && len(set.Pods) > 0 {
		return nil, ErrNoPort
	}
	return endpoints, nil
}

// ParsePort parses a port number or an IANA service name such as "management".
func ParsePort(s string) (intstr.IntOrString, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if errs := validation.IsValidPortNum(n); len(errs) > 0 {
			return intstr.IntOrString{}, fmt.Errorf("invalid port %q: %s", s, strings.Join(errs, ", "))
		}
		return intstr.FromInt32(int32(n)), nil
	}
	if errs := validation.IsValidPortName(s); len(errs) > 0 {
		return intstr.IntOrString{}, fmt.Errorf("invalid port %q: %s", s, strings.Join(errs, ", "))
	}
	return intstr.FromString(s), nil
}

func ValidateScheme(s string) error {
	if s == "http" || s == "https" {
		return nil
	}
	return fmt.Errorf("invalid scheme %q: use http or https", s)
}

func portSpec(pod *corev1.Pod, choice PortChoice, servicePort *intstr.IntOrString) (intstr.IntOrString, bool, error) {
	if choice.Port != nil {
		return *choice.Port, true, nil
	}
	if v, ok := pod.Annotations[AnnotationPort]; ok {
		p, err := ParsePort(v)
		if err != nil {
			return p, true, fmt.Errorf("annotation %s: %w", AnnotationPort, err)
		}
		return p, true, nil
	}
	if choice.PresetPort != "" {
		return intstr.FromString(choice.PresetPort), true, nil
	}
	if servicePort != nil {
		return *servicePort, true, nil
	}
	return intstr.IntOrString{}, false, nil
}

// TargetPort is the pod-side port of a Service port, which defaults to the port itself.
func TargetPort(p corev1.ServicePort) intstr.IntOrString {
	if p.TargetPort == (intstr.IntOrString{}) {
		return intstr.FromInt32(p.Port)
	}
	return p.TargetPort
}

// containerPort resolves a named port the way Services do: TCP ports of the regular containers and
// of sidecar (restartable init) containers. Unlike Services, a name found twice is an error.
func containerPort(pod *corev1.Pod, spec intstr.IntOrString) (int32, error) {
	if spec.Type == intstr.Int {
		return spec.IntVal, nil
	}
	var found []int32
	match := func(c *corev1.Container) {
		for _, p := range c.Ports {
			if p.Name == spec.StrVal && (p.Protocol == "" || p.Protocol == corev1.ProtocolTCP) {
				found = append(found, p.ContainerPort)
			}
		}
	}
	for i := range pod.Spec.Containers {
		match(&pod.Spec.Containers[i])
	}
	for i := range pod.Spec.InitContainers {
		c := &pod.Spec.InitContainers[i]
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			match(c)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("%w: no TCP container port named %q", ErrPortNotFound, spec.StrVal)
	case 1:
		return found[0], nil
	}
	return 0, fmt.Errorf("%w: %d container ports are named %q", ErrAmbiguousPort, len(found), spec.StrVal)
}

func scheme(pod *corev1.Pod, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	v, ok := pod.Annotations[AnnotationScheme]
	if !ok {
		return "http", nil
	}
	if err := ValidateScheme(v); err != nil {
		return "", fmt.Errorf("annotation %s: %w", AnnotationScheme, err)
	}
	return v, nil
}
