package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/kumarprabhashanand/pod-broadcast/internal/version"
)

func newVersionCommand(streams genericiooptions.IOStreams) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plugin version",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Fprintln(streams.Out, version.Line("kubectl-broadcast"))
		},
	}
}
