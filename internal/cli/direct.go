package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kumarprabhashanand/pod-broadcast/internal/output"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
	"github.com/kumarprabhashanand/pod-broadcast/internal/transport"
)

// direct calls the pods itself, through the API server's pod proxy.
func (o *rootOptions) direct(ctx context.Context, cl *cluster, inv invocation, spec request.Spec, choice target.PortChoice, opts request.Options) error {
	if !slices.Contains(transport.ProxyMethods, spec.Method) {
		return fmt.Errorf("method %s can't be sent in direct mode, which sends only %s", spec.Method, strings.Join(transport.ProxyMethods, ", "))
	}
	for _, name := range o.secretHeaders {
		var secret corev1.Secret
		if err := cl.client.Get(ctx, client.ObjectKey{Namespace: cl.namespace, Name: name}, &secret); err != nil {
			return err
		}
		headers, err := request.SecretHeaders(&secret)
		if err != nil {
			return err
		}
		spec.Headers = request.Merge(spec.Headers, headers)
	}
	// The API server deletes the Authorization header after authenticating the caller.
	for _, h := range spec.Headers {
		if strings.EqualFold(h.Name, "Authorization") {
			return errors.New("the Authorization header can't reach pods: the API server removes it from proxied requests")
		}
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

	if o.dryRun {
		return output.Write(o.streams.Out, o.output, output.NewDryRunReport(cl.namespace, inv.target, spec, endpoints))
	}
	if err := checkProxyAccess(ctx, cl.client, cl.namespace, spec.Method); err != nil {
		return err
	}
	results, err := request.Run(ctx, cl.transport, endpoints, spec, opts)
	if err != nil {
		return fatalError(err, spec.Method)
	}
	return writeReport(o.streams.Out, o.output, output.NewReport(cl.namespace, inv.target, spec, results))
}

func writeReport(w io.Writer, format string, r output.Report) error {
	if err := output.Write(w, format, r); err != nil {
		return err
	}
	if code := r.ExitCode(); code != output.ExitSucceeded {
		return silentExit(code)
	}
	return nil
}
