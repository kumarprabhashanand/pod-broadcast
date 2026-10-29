package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/kumarprabhashanand/pod-broadcast/internal/output"
	"github.com/kumarprabhashanand/pod-broadcast/internal/preset"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

func newPresetsCommand(streams genericiooptions.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: "List presets and the requests they send",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			rows := []string{"PRESET\tREQUEST"}
			for _, p := range preset.All() {
				rows = append(rows, p.Usage()+"\t"+p.Request)
			}
			if err := output.WriteRows(streams.Out, rows); err != nil {
				return err
			}
			_, err := fmt.Fprintf(streams.Out, `
{base} is --base-path if given, else the pod annotation
%s, else %s.
Spring presets call the container port named management, unless --port or the pod
annotation %s says otherwise.
`, target.AnnotationActuatorBasePath, preset.DefaultBasePath, target.AnnotationPort)
			return err
		},
	}
}
