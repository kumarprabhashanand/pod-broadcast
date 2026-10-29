package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kumarprabhashanand/pod-broadcast/internal/output"
	"github.com/kumarprabhashanand/pod-broadcast/internal/preset"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
	"github.com/kumarprabhashanand/pod-broadcast/internal/transport"
	"github.com/kumarprabhashanand/pod-broadcast/internal/version"
)

// cluster is what a broadcast needs from Kubernetes.
type cluster struct {
	client    kubernetes.Interface
	namespace string
	transport request.Transport
}

type clusterFunc func(*genericclioptions.ConfigFlags) (*cluster, error)

// silentExit ends the command with a code after the output has already explained the result.
type silentExit int

func (e silentExit) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

type rootOptions struct {
	configFlags *genericclioptions.ConfigFlags
	newCluster  clusterFunc
	streams     genericiooptions.IOStreams

	selector        string
	method          string
	port            string
	scheme          string
	includeNotReady bool
	headers         []string
	envHeaders      []string
	secretHeaders   []string
	data            string
	concurrency     int
	timeout         time.Duration
	retries         int
	backoff         time.Duration
	successCodes    string
	dryRun          bool
	output          string
	cache           string
	basePath        string
}

// Run executes the plugin with args (without the program name) and returns the process exit code.
func Run(args []string, streams genericiooptions.IOStreams) int {
	return run(args, streams, kubeCluster)
}

func run(args []string, streams genericiooptions.IOStreams, newCluster clusterFunc) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd := newRootCommand(streams, newCluster)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	var exit silentExit
	switch {
	case err == nil:
		return output.ExitSucceeded
	case errors.As(err, &exit):
		return int(exit)
	}
	fmt.Fprintf(streams.ErrOut, "error: %v\n", err)
	return output.ExitUsage
}

func newRootCommand(streams genericiooptions.IOStreams, newCluster clusterFunc) *cobra.Command {
	o := &rootOptions{configFlags: genericclioptions.NewConfigFlags(true), newCluster: newCluster, streams: streams}
	cmd := &cobra.Command{
		Use:   "kubectl-broadcast TARGET (-X METHOD PATH | GROUP PRESET [ARGS])",
		Short: "Send one HTTP request to every ready pod of a workload",
		Long: `Send one HTTP request to every ready pod of a Service, workload or label selector,
and report each pod's result. Requests go through the Kubernetes API server's pod
proxy with your own credentials, so nothing needs to be installed in the cluster.

Exit codes: 0 every pod succeeded, 1 some pods failed, 2 usage error or no pods,
3 every pod failed.`,
		Example: `  # Clear the caches on every ready pod of the orders Service
  kubectl broadcast svc/orders --port management -X DELETE /actuator/caches

  # Read health from every ready pod with the label app=orders, as JSON
  kubectl broadcast -l app=orders --port 8081 -X GET /actuator/health -o json

  # Show the pods and the request without sending anything
  kubectl broadcast deploy/orders --port 8081 -X POST /refresh --dry-run`,
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
	f.StringVarP(&o.method, "request", "X", "", "HTTP method: GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS; needs a PATH argument")
	f.StringVar(&o.port, "port", "", "Container port name or number (default: the pod annotation "+target.AnnotationPort+", then the Service's only port)")
	f.StringVar(&o.scheme, "scheme", "", "http or https (default: the pod annotation "+target.AnnotationScheme+", then http)")
	f.BoolVar(&o.includeNotReady, "include-not-ready", false, "Also call pods that aren't ready")
	f.StringArrayVarP(&o.headers, "header", "H", nil, "Request header 'Name: value'; repeatable")
	f.StringArrayVar(&o.envHeaders, "header-from-env", nil, "Request header Name=VAR with the value of environment variable VAR, never shown; repeatable")
	f.StringArrayVar(&o.secretHeaders, "headers-from-secret", nil, "Secret whose keys become request headers, never shown; needs get on secrets; repeatable")
	f.StringVarP(&o.data, "data", "d", "", "Request body, @FILE to read a file, or @- to read standard input")
	f.IntVar(&o.concurrency, "concurrency", 10, "Pods called at the same time")
	f.DurationVar(&o.timeout, "timeout", 5*time.Second, "Timeout for each attempt")
	f.IntVar(&o.retries, "retries", 2, "Retries after a connection error, timeout or status 429, 500, 502, 503 or 504")
	f.DurationVar(&o.backoff, "backoff", time.Second, "Base delay between retries; it doubles each time, with jitter")
	f.StringVar(&o.successCodes, "success-codes", "2xx", "Status codes that count as success, as classes or codes, e.g. 2xx,404")
	f.BoolVar(&o.dryRun, "dry-run", false, "Print the pods and the request without sending anything")
	f.StringVarP(&o.output, "output", "o", "table", "Output format: table, json or yaml")
	f.StringVar(&o.cache, "cache", "", "Cache to evict, for spring evict-caches")
	f.StringVar(&o.basePath, "base-path", "", "Actuator base path for spring presets (default: the pod annotation "+target.AnnotationActuatorBasePath+", then "+preset.DefaultBasePath+")")
	o.configFlags.AddFlags(cmd.PersistentFlags())

	cmd.AddCommand(newVersionCommand(streams), newPresetsCommand(streams))
	return cmd
}

func (o *rootOptions) broadcast(ctx context.Context, args []string) error {
	inv, err := parseInvocation(args, o.selector, o.method)
	if err != nil {
		return err
	}
	spec, call, err := o.spec(inv)
	if err != nil {
		return err
	}
	choice, err := o.portChoice()
	if err != nil {
		return err
	}
	if call != nil {
		choice.PresetPort = call.Port
	}
	opts, err := o.executeOptions()
	if err != nil {
		return err
	}
	if !slices.Contains(output.Formats, o.output) {
		return fmt.Errorf("unknown output format %q: use %s", o.output, strings.Join(output.Formats, ", "))
	}

	cl, err := o.newCluster(o.configFlags)
	if err != nil {
		return err
	}
	for _, name := range o.secretHeaders {
		secret, err := cl.client.CoreV1().Secrets(cl.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		headers, err := request.SecretHeaders(secret)
		if err != nil {
			return err
		}
		spec.Headers = request.Merge(spec.Headers, headers)
	}
	if err := checkProxyHeaders(spec.Headers); err != nil {
		return err
	}

	set, err := target.Resolve(ctx, cl.client, cl.namespace, inv.target, target.ResolveOptions{IncludeNotReady: o.includeNotReady})
	if err != nil {
		return err
	}
	if len(set.Pods) == 0 {
		return noPodsError(inv.target, cl.namespace, set.NotReady, o.includeNotReady)
	}
	endpoints, err := target.Endpoints(set, choice)
	if errors.Is(err, target.ErrNoPort) {
		return noPortError(set.ServicePorts)
	}
	if err != nil {
		return err
	}
	if call != nil {
		call.ApplyBasePath(endpoints)
	}

	if o.dryRun {
		return output.Write(o.streams.Out, o.output, output.NewDryRunReport(cl.namespace, inv.target, spec, endpoints))
	}
	results, err := request.Run(ctx, cl.transport, endpoints, spec, opts)
	if err != nil {
		return fatalError(err, spec.Method)
	}
	report := output.NewReport(cl.namespace, inv.target, spec, results)
	if err := output.Write(o.streams.Out, o.output, report); err != nil {
		return err
	}
	if code := report.ExitCode(); code != output.ExitSucceeded {
		return silentExit(code)
	}
	return nil
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

func (o *rootOptions) executeOptions() (request.Options, error) {
	switch {
	case o.concurrency < 1:
		return request.Options{}, fmt.Errorf("--concurrency must be at least 1, got %d", o.concurrency)
	case o.timeout <= 0:
		return request.Options{}, fmt.Errorf("--timeout must be positive, got %s", o.timeout)
	case o.retries < 0 || o.retries > 10:
		return request.Options{}, fmt.Errorf("--retries must be between 0 and 10, got %d", o.retries)
	case o.backoff < 0:
		return request.Options{}, fmt.Errorf("--backoff can't be negative, got %s", o.backoff)
	}
	codes, err := request.ParseCodeSet(o.successCodes)
	if err != nil {
		return request.Options{}, err
	}
	return request.Options{Concurrency: o.concurrency, Timeout: o.timeout, Retries: o.retries, Backoff: o.backoff, SuccessCodes: codes}, nil
}

// spec builds the request from -X METHOD PATH or from a preset; call is set for presets.
func (o *rootOptions) spec(inv invocation) (spec request.Spec, call *preset.Call, err error) {
	if inv.method != "" {
		if o.cache != "" || o.basePath != "" {
			return spec, nil, errors.New("--cache and --base-path only apply to presets")
		}
		spec = request.Spec{Method: strings.ToUpper(inv.method), Path: inv.path}
		if spec.Body, err = o.body(); err != nil {
			return spec, nil, err
		}
	} else {
		if o.data != "" {
			return spec, nil, errors.New("-d can't be used with a preset, which sets its own body; use -X METHOD PATH instead")
		}
		p, err := preset.Find(inv.preset.group, inv.preset.name)
		if err != nil {
			return spec, nil, err
		}
		c, err := p.Build(inv.preset.args, preset.Options{Cache: o.cache, BasePath: o.basePath})
		if err != nil {
			return spec, nil, err
		}
		spec, call = c.Spec, &c
	}
	if !slices.Contains(transport.ProxyMethods, spec.Method) {
		return spec, nil, fmt.Errorf("method %s can't be sent: the API server's pod proxy passes only %s", spec.Method, strings.Join(transport.ProxyMethods, ", "))
	}

	var flagHeaders, envHeaders []request.Header
	for _, s := range o.headers {
		h, err := request.ParseHeader(s)
		if err != nil {
			return spec, nil, err
		}
		flagHeaders = append(flagHeaders, h)
	}
	for _, s := range o.envHeaders {
		h, err := request.EnvHeader(s, os.LookupEnv)
		if err != nil {
			return spec, nil, err
		}
		envHeaders = append(envHeaders, h)
	}
	spec.Headers = request.Merge(spec.Headers, flagHeaders, envHeaders)
	if c := call; c != nil {
		c.Spec = spec
	}
	return spec, call, checkProxyHeaders(spec.Headers)
}

func (o *rootOptions) body() ([]byte, error) {
	switch {
	case o.data == "@-":
		return io.ReadAll(o.streams.In)
	case strings.HasPrefix(o.data, "@"):
		return os.ReadFile(o.data[1:])
	case o.data != "":
		return []byte(o.data), nil
	}
	return nil, nil
}

// checkProxyHeaders refuses headers the pod would never receive. The API server deletes the
// Authorization header after authenticating the caller.
func checkProxyHeaders(headers []request.Header) error {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Authorization") {
			return errors.New("the Authorization header can't reach pods: the API server removes it from proxied requests")
		}
	}
	return nil
}

func kubeCluster(f *genericclioptions.ConfigFlags) (*cluster, error) {
	namespace, _, err := f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return nil, err
	}
	config, err := f.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	config = rest.CopyConfig(config)
	config.QPS, config.Burst = 50, 100
	config.UserAgent = "kubectl-broadcast/" + version.String()
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	proxy, err := transport.NewProxy(config, namespace)
	if err != nil {
		return nil, err
	}
	return &cluster{client: client, namespace: namespace, transport: proxy}, nil
}
