package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

const exitUsage = 2

type rootOptions struct {
	configFlags *genericclioptions.ConfigFlags
	selector    string
	method      string
}

// Run executes the plugin with args (without the program name) and returns the process exit code.
func Run(args []string, streams genericiooptions.IOStreams) int {
	cmd := newRootCommand(streams)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(streams.ErrOut, "error: %v\n", err)
		return exitUsage
	}
	return 0
}

func newRootCommand(streams genericiooptions.IOStreams) *cobra.Command {
	o := &rootOptions{configFlags: genericclioptions.NewConfigFlags(true)}
	cmd := &cobra.Command{
		Use:   "kubectl-broadcast TARGET (-X METHOD PATH | GROUP PRESET [ARGS])",
		Short: "Send one HTTP request to every ready pod of a workload",
		Example: `  # Clear the caches on every ready pod of the orders Service
  kubectl broadcast svc/orders -X DELETE /actuator/caches

  # Read health from every ready pod with the label app=orders
  kubectl broadcast -l app=orders -X GET /actuator/health`,
		Annotations:   map[string]string{cobra.CommandDisplayNameAnnotation: "kubectl broadcast"},
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && o.selector == "" {
				return cmd.Help()
			}
			if _, err := parseInvocation(args, o.selector, o.method); err != nil {
				return err
			}
			return errors.New("sending requests is not implemented yet")
		},
	}
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.ErrOut)

	cmd.Flags().StringVarP(&o.selector, "selector", "l", "", "Label selector for the target pods, instead of TARGET")
	cmd.Flags().StringVarP(&o.method, "request", "X", "", "HTTP method; needs a PATH argument")
	o.configFlags.AddFlags(cmd.PersistentFlags())

	cmd.AddCommand(newVersionCommand(streams))
	return cmd
}
