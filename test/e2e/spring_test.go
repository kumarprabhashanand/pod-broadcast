//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func springPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": "spring"}},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: new(int64),
			Containers: []corev1.Container{{
				Name:            "app",
				Image:           springImage,
				ImagePullPolicy: corev1.PullNever,
				Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}, {Name: "management", ContainerPort: 8081}},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/actuator/health/readiness", Port: intstr.FromString("management")}},
					PeriodSeconds: 2,
				},
				Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("384Mi")}},
			}},
		},
	}
}

// values reads GET /value from every Spring pod: the number of times its cached value was loaded.
func values(t *testing.T, ns string) map[string]string {
	t.Helper()
	r := broadcast(t, ns, "-l", "app=spring", "--port", "http", "-X", "GET", "/value", "-o", "json")
	rep := parseReport(t, r)
	got := map[string]string{}
	for _, p := range rep.Pods {
		got[p.Name] = strings.TrimSpace(p.Body)
	}
	return got
}

func wantValues(t *testing.T, got map[string]string, want string) {
	t.Helper()
	for pod, v := range got {
		if v != want {
			t.Errorf("%s: value = %s, want %s", pod, v, want)
		}
	}
}

func TestSpringPresets(t *testing.T) {
	ns := newNamespace(t)
	createPods(t, ns, springPod("spring-a"), springPod("spring-b"))
	sel := []string{"-l", "app=spring"}
	run := func(a ...string) result {
		t.Helper()
		r := broadcast(t, ns, append(sel, a...)...)
		succeeded := strings.Contains(r.stdout, "2 targeted: 2 succeeded") || strings.Contains(r.stdout, `"succeeded": 2`)
		if r.code != 0 || !succeeded {
			t.Fatalf("%v: want 2 successes\n%s", a, r)
		}
		return r
	}

	if r := run("spring", "health"); !strings.Contains(r.stdout, `"status":"UP"`) {
		t.Errorf("health output\n%s", r)
	}
	run("spring", "info")

	wantValues(t, values(t, ns), "1")
	wantValues(t, values(t, ns), "1") // served from the cache
	run("spring", "evict-caches")
	wantValues(t, values(t, ns), "2")
	run("spring", "evict-caches", "--cache", "values")
	wantValues(t, values(t, ns), "3")

	run("spring", "set-log-level", "example.sample", "debug")
	r := run("--port", "management", "-X", "GET", "/actuator/loggers/example.sample", "-o", "json")
	for _, p := range parseReport(t, r).Pods {
		var level struct{ ConfiguredLevel string }
		if json.Unmarshal([]byte(p.Body), &level) != nil || level.ConfiguredLevel != "DEBUG" {
			t.Errorf("%s: logger = %s, want configuredLevel DEBUG", p.Name, p.Body)
		}
	}
	run("spring", "reset-log-level", "example.sample")
	r = run("--port", "management", "-X", "GET", "/actuator/loggers/example.sample", "-o", "json")
	for _, p := range parseReport(t, r).Pods {
		if strings.Contains(p.Body, "configuredLevel") {
			t.Errorf("%s: logger = %s, want no configured level", p.Name, p.Body)
		}
	}

	run("spring", "refresh")
}
