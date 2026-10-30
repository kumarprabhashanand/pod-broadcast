// Package deploy holds the install manifests; its tests keep them consistent.
package deploy

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
)

// devImage is the image the manifests reference; the release workflow replaces this exact line.
const devImage = "image: pod-broadcast-controller:dev"

type manifest struct {
	raw        string
	rules      []rbacv1.PolicyRule
	deployment appsv1.Deployment
}

func load(t *testing.T, path string) manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest{raw: string(raw)}
	for _, doc := range bytes.Split(raw, []byte("\n---\n")) {
		var meta struct{ Kind string }
		if err := yaml.Unmarshal(doc, &meta); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		switch meta.Kind {
		case "ClusterRole", "Role":
			var r rbacv1.ClusterRole
			must(t, yaml.UnmarshalStrict(doc, &r))
			m.rules = r.Rules
		case "Deployment":
			must(t, yaml.UnmarshalStrict(doc, &m.deployment))
		}
	}
	return m
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestBothInstallsMatch(t *testing.T) {
	cluster, namespaced := load(t, "controller.yaml"), load(t, "controller-namespaced.yaml")
	if len(cluster.rules) == 0 || !reflect.DeepEqual(cluster.rules, namespaced.rules) {
		t.Errorf("the ClusterRole and the Role grant different rules:\n%+v\n%+v", cluster.rules, namespaced.rules)
	}
	for _, m := range []manifest{cluster, namespaced} {
		if strings.Count(m.raw, devImage) != 1 {
			t.Errorf("want exactly one %q line, which the release replaces", devImage)
		}
		for _, r := range m.rules {
			for _, g := range r.APIGroups {
				if strings.HasPrefix(g, "broadcast.") && g != v1alpha1.GroupName {
					t.Errorf("rule for API group %q, want %q", g, v1alpha1.GroupName)
				}
			}
		}
	}
	// The namespaced install only adds its namespace and the policy default.
	c, n := cluster.deployment.Spec.Template.Spec, namespaced.deployment.Spec.Template.Spec
	n.Containers[0].Args, n.Containers[0].Env = nil, nil
	if !reflect.DeepEqual(c, n) {
		t.Errorf("the two Deployments differ beyond args and env:\n%+v\n%+v", c, n)
	}
}

func TestUserRolesUseTheAPIGroup(t *testing.T) {
	raw, err := os.ReadFile("user-roles.yaml")
	must(t, err)
	for _, doc := range bytes.Split(raw, []byte("\n---\n")) {
		var r rbacv1.ClusterRole
		must(t, yaml.UnmarshalStrict(doc, &r))
		for _, rule := range r.Rules {
			if !reflect.DeepEqual(rule.APIGroups, []string{v1alpha1.GroupName}) {
				t.Errorf("%s: rule for %v, want %s", r.Name, rule.APIGroups, v1alpha1.GroupName)
			}
		}
	}
}
