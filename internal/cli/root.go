package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

const exitUsage = 2

// clientFunc returns a Kubernetes client and the namespace to work in.
type clientFunc func(*genericclioptions.ConfigFlags) (kubernetes.Interface, string, error)

type rootOptions struct {
	configFlags     *genericclioptions.ConfigFlags
	newClient       clientFunc
	selector        string
	method          string
	port            string
	scheme          string
	includeNotReady bool
}

// Run executes the plugin with args (without the program name) and returns the process exit code.
func Run(args []string, streams genericiooptions.IOStreams) int {
	return run(args, streams, kubeClient)
}

func run(args []string, streams genericiooptions.IOStreams, newClient clientFunc) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := newRootCommand(streams, newClient)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(streams.ErrOut, "error: %v\n", err)
		return exitUsage
	}
	return 0
}

func newRootCommand(streams genericiooptions.IOStreams, newClient clientFunc) *cobra.Command {
	o := &rootOptions{configFlags: genericclioptions.NewConfigFlags(true), newClient: newClient}
	cmd := &cobra.Command{
		Use:   "kubectl-broadcast TARGET (-X METHOD PATH | GROUP PRESET [ARGS])",
		Short: "Send one HTTP request to every ready pod of a workload",
		Example: `  # Clear the caches on every ready pod of the orders Service
  kubectl broadcast svc/orders --port management -X DELETE /actuator/caches

  # Read health from every ready pod with the label app=orders
  kubectl broadcast -l app=orders --port 8081 -X GET /actuator/health`,
		Annotations:   map[string]string{cobra.CommandDisplayNameAnnotation: "kubectl broadcast"},
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && o.selector == "" {
				return cmd.Help()
			}
			return o.broadcast(cmd.Context(), args)
		},
	}
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.ErrOut)

	f := cmd.Flags()
	f.StringVarP(&o.selector, "selector", "l", "", "Label selector for the target pods, instead of TARGET")
	f.StringVarP(&o.method, "request", "X", "", "HTTP method; needs a PATH argument")
	f.StringVar(&o.port, "port", "", "Container port name or number (default: the pod annotation "+target.AnnotationPort+", then the Service's only port)")
	f.StringVar(&o.scheme, "scheme", "", "http or https (default: the pod annotation "+target.AnnotationScheme+", then http)")
	f.BoolVar(&o.includeNotReady, "include-not-ready", false, "Also call pods that aren't ready")
	o.configFlags.AddFlags(cmd.PersistentFlags())

	cmd.AddCommand(newVersionCommand(streams))
	return cmd
}

func (o *rootOptions) broadcast(ctx context.Context, args []string) error {
	inv, err := parseInvocation(args, o.selector, o.method)
	if err != nil {
		return err
	}
	if inv.method == "" {
		return fmt.Errorf("unknown preset %q", inv.preset.group+" "+inv.preset.name)
	}
	choice, err := o.portChoice()
	if err != nil {
		return err
	}
	client, namespace, err := o.newClient(o.configFlags)
	if err != nil {
		return err
	}
	set, err := target.Resolve(ctx, client, namespace, inv.target, target.ResolveOptions{IncludeNotReady: o.includeNotReady})
	if err != nil {
		return err
	}
	if len(set.Pods) == 0 {
		return noPodsError(inv.target, namespace, set.NotReady, o.includeNotReady)
	}
	endpoints, err := target.Endpoints(set, choice)
	if errors.Is(err, target.ErrNoPort) {
		return noPortError(set.ServicePorts)
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("resolved %d pods; sending requests is not implemented yet", len(endpoints))
}

func (o *rootOptions) portChoice() (target.PortChoice, error) {
	var choice target.PortChoice
	if o.port != "" {
		p, err := target.ParsePort(o.port)
		if err != nil {
			return choice, err
		}
		choice.Port = &p
	}
	if o.scheme != "" {
		if err := target.ValidateScheme(o.scheme); err != nil {
			return choice, err
		}
		choice.Scheme = o.scheme
	}
	return choice, nil
}

func kubeClient(f *genericclioptions.ConfigFlags) (kubernetes.Interface, string, error) {
	namespace, _, err := f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return nil, "", err
	}
	config, err := f.ToRESTConfig()
	if err != nil {
		return nil, "", err
	}
	client, err := kubernetes.NewForConfig(config)
	return client, namespace, err
}
