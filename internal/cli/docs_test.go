package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/kumarprabhashanand/pod-broadcast/internal/preset"
)

func TestCLIReferenceCoversEveryFlagAndPreset(t *testing.T) {
	doc, err := os.ReadFile("../../docs/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	streams, _, _, _ := genericiooptions.NewTestIOStreams()
	newRootCommand(streams, nil).LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !strings.Contains(string(doc), "`--"+f.Name+"`") {
			t.Errorf("docs/cli.md doesn't describe --%s", f.Name)
		}
	})
	for _, p := range preset.All() {
		if !strings.Contains(string(doc), "`"+p.Usage()+"`") {
			t.Errorf("docs/cli.md doesn't list the preset %q", p.Usage())
		}
	}
}
