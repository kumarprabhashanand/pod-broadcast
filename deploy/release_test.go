package deploy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"sigs.k8s.io/yaml"
)

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	must(t, err)
	return string(raw)
}

func TestGoVersionIsPinnedTheSameEverywhere(t *testing.T) {
	m := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindStringSubmatch(read(t, "../go.mod"))
	if m == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	for _, dockerfile := range []string{"../Dockerfile", "../test/apps/echo/Dockerfile"} {
		want := "golang:" + m[1] + "-alpine@sha256:"
		if !strings.Contains(read(t, dockerfile), want) {
			t.Errorf("%s doesn't build from %q, the toolchain in go.mod", dockerfile, want)
		}
	}
}

func TestEveryActionIsPinnedToACommit(t *testing.T) {
	workflows, err := filepath.Glob("../.github/workflows/*.yml")
	must(t, err)
	uses, pinned := regexp.MustCompile(`(?m)^\s*(?:- )?uses: (\S+)`), regexp.MustCompile(`@[0-9a-f]{40}$`)
	found := 0
	for _, w := range workflows {
		for _, m := range uses.FindAllStringSubmatch(read(t, w), -1) {
			found++
			if !pinned.MatchString(m[1]) {
				t.Errorf("%s: %s isn't pinned to a full commit hash", w, m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("found no actions to check")
	}
}

type workflow struct {
	Permissions map[string]string `json:"permissions"`
	Jobs        map[string]struct {
		Needs       string            `json:"needs"`
		Permissions map[string]string `json:"permissions"`
		Steps       []struct {
			Uses string `json:"uses"`
		} `json:"steps"`
	} `json:"jobs"`
}

// The bot is third-party code, so it must not share a job with the release's write permissions.
func TestKrewBotRunsAfterTheReleaseWithoutWriteAccess(t *testing.T) {
	var w workflow
	must(t, yaml.Unmarshal([]byte(read(t, "../.github/workflows/release.yml")), &w))
	for name, job := range w.Jobs {
		runsBot := false
		for _, s := range job.Steps {
			runsBot = runsBot || strings.HasPrefix(s.Uses, "rajatjindal/krew-release-bot@")
		}
		if runsBot != (name == "krew") {
			t.Errorf("job %q: runs the krew bot = %v, want only the krew job to", name, runsBot)
		}
	}
	krew, ok := w.Jobs["krew"]
	if !ok {
		t.Fatal("release.yml has no krew job")
	}
	if krew.Needs != "release" {
		t.Errorf("the krew job needs %q, want it to wait for the release job", krew.Needs)
	}
	permissions := w.Permissions
	if krew.Permissions != nil {
		permissions = krew.Permissions
	}
	if len(permissions) == 0 {
		t.Error("the krew job has no explicit permissions")
	}
	for scope, level := range permissions {
		if level != "read" && level != "none" {
			t.Errorf("the krew job has %s: %s, want it read-only", scope, level)
		}
	}
}

// The release workflow builds the archives and the bot downloads them by name, so both must agree.
func TestKrewTemplateMatchesTheReleaseBuild(t *testing.T) {
	const tag = "v9.8.7"
	release := read(t, "../.github/workflows/release.yml")
	for _, name := range []string{".tar.gz", ".zip"} {
		if !strings.Contains(release, "kubectl-broadcast_${VERSION}_${os}_${arch}"+name) {
			t.Errorf("release.yml no longer builds kubectl-broadcast_VERSION_OS_ARCH%s archives", name)
		}
	}
	m := regexp.MustCompile(`for target in ([a-z0-9/ ]+); do`).FindStringSubmatch(release)
	if m == nil {
		t.Fatal("release.yml has no list of build targets")
	}
	targets := strings.Fields(m[1])

	// Mirrors the bot's addURIAndSha, which downloads the archive and prints its hash.
	funcs := template.FuncMap{"addURIAndSha": func(url, tag string) (string, error) {
		var uri bytes.Buffer
		u, err := template.New("url").Parse(url)
		if err != nil {
			return "", err
		}
		if err := u.Execute(&uri, struct{ TagName string }{tag}); err != nil {
			return "", err
		}
		return fmt.Sprintf("uri: %s\n    sha256: %s", uri.String(), strings.Repeat("a", 64)), nil
	}}
	tpl, err := template.New(".krew.yaml").Funcs(funcs).ParseFiles("../.krew.yaml")
	must(t, err)
	var rendered bytes.Buffer
	must(t, tpl.Execute(&rendered, struct{ TagName string }{tag}))

	var plugin struct {
		Metadata struct{ Name string } `json:"metadata"`
		Spec     struct {
			Version          string `json:"version"`
			ShortDescription string `json:"shortDescription"`
			Platforms        []struct {
				Selector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"selector"`
				URI    string `json:"uri"`
				Sha256 string `json:"sha256"`
				Bin    string `json:"bin"`
				Files  []struct {
					From string `json:"from"`
					To   string `json:"to"`
				} `json:"files"`
			} `json:"platforms"`
		} `json:"spec"`
	}
	must(t, yaml.Unmarshal(rendered.Bytes(), &plugin))

	if plugin.Metadata.Name != "broadcast" || plugin.Spec.Version != tag {
		t.Errorf("plugin %q at version %q, want broadcast at %s", plugin.Metadata.Name, plugin.Spec.Version, tag)
	}
	// The krew-index review rejects longer descriptions.
	if n := len(plugin.Spec.ShortDescription); n == 0 || n > 50 {
		t.Errorf("shortDescription is %d characters, want 1 to 50", n)
	}
	if len(plugin.Spec.Platforms) != len(targets) {
		t.Errorf("%d platforms, want one for each of the %d build targets", len(plugin.Spec.Platforms), len(targets))
	}
	sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, target := range targets {
		goos, goarch, _ := strings.Cut(target, "/")
		ext, bin := "tar.gz", "kubectl-broadcast"
		if goos == "windows" {
			ext, bin = "zip", bin+".exe"
		}
		archive := fmt.Sprintf("kubectl-broadcast_%s_%s_%s.%s", tag, goos, goarch, ext)
		found := false
		for _, p := range plugin.Spec.Platforms {
			if p.Selector.MatchLabels["os"] != goos || p.Selector.MatchLabels["arch"] != goarch {
				continue
			}
			found = true
			if !strings.HasPrefix(p.URI, "https://github.com/") || !strings.HasSuffix(p.URI, "/releases/download/"+tag+"/"+archive) {
				t.Errorf("%s: uri %q, want the release asset %s", target, p.URI, archive)
			}
			if !sha.MatchString(p.Sha256) {
				t.Errorf("%s: sha256 %q isn't a hash", target, p.Sha256)
			}
			if p.Bin != bin {
				t.Errorf("%s: bin %q, want %q", target, p.Bin, bin)
			}
			files := map[string]string{}
			for _, f := range p.Files {
				files[f.From] = f.To
			}
			if files[bin] != "." || files["LICENSE"] != "." {
				t.Errorf("%s: files %v, want %s and LICENSE copied to .", target, p.Files, bin)
			}
		}
		if !found {
			t.Errorf("%s: no platform in .krew.yaml", target)
		}
	}
}

// A release names its version in the changelog, the install docs and the verify example.
func TestReleaseVersionReferencesAgree(t *testing.T) {
	first := func(path, pattern string) string {
		m := regexp.MustCompile(pattern).FindStringSubmatch(read(t, path))
		if m == nil {
			t.Fatalf("%s doesn't match %s", path, pattern)
		}
		return m[1]
	}
	latest := first("../CHANGELOG.md", `(?m)^## (\d+\.\d+\.\d+)`)
	for path, got := range map[string]string{
		"README.md":          first("../README.md", `kubectl-broadcast_v(\S+?)_linux_amd64\.tar\.gz`),
		"docs/controller.md": first("../docs/controller.md", `(?m)^VERSION=v(\S+)$`),
	} {
		if got != latest {
			t.Errorf("%s names v%s, but the newest changelog section is %s", path, got, latest)
		}
	}
}
