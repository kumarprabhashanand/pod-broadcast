package cli

import (
	"fmt"
	"runtime"

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
			fmt.Fprintf(streams.Out, "kubectl-broadcast %s (%s %s/%s)\n", version.String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}
