package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/output"
	"github.com/kumarprabhashanand/pod-broadcast/internal/preset"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// viaController creates a Broadcast for the controller to run, and waits for its result.
func (o *rootOptions) viaController(ctx context.Context, cl *cluster, inv invocation, spec request.Spec, call *preset.Call, choice target.PortChoice) error {
	switch {
	case o.dryRun:
		return errors.New("--dry-run works in direct mode only")
	case len(o.envHeaders) > 0:
		return errors.New("--header-from-env works in direct mode only, because the value would be stored in the Broadcast; use --headers-from-secret")
	}
	b, err := o.newBroadcast(cl.namespace, inv, spec, choice)
	if err != nil {
		return err
	}
	if o.forDuration > 0 {
		b.Spec.Mode = v1alpha1.Continuous
		b.Spec.Duration = &metav1.Duration{Duration: o.forDuration}
		if call != nil && call.Revert != nil {
			rv := call.Revert
			b.Spec.Revert = &v1alpha1.Revert{Method: rv.Method, Path: rv.Path, Headers: headerMap(rv.Headers), Body: string(rv.Body)}
		}
	}
	if err := cl.client.Create(ctx, b); err != nil {
		if meta.IsNoMatchError(err) {
			return errors.New("the Broadcast API isn't installed in this cluster; install the pod-broadcast controller first")
		}
		return err
	}
	if !o.wait {
		_, err := fmt.Fprintln(o.streams.Out, b.Name)
		return err
	}
	fmt.Fprintf(o.streams.ErrOut, "broadcast %s created\n", b.Name)
	return report(ctx, cl.client, client.ObjectKeyFromObject(b), o.waitTimeout, o.output, o.streams.Out, o.streams.ErrOut)
}

func (o *rootOptions) newBroadcast(ns string, inv invocation, spec request.Spec, choice target.PortChoice) (*v1alpha1.Broadcast, error) {
	b := &v1alpha1.Broadcast{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: o.name}}
	if o.name == "" {
		b.GenerateName = strings.ToLower(inv.target.Name) + "-"
		if inv.target.Kind == "" {
			b.GenerateName = "broadcast-"
		}
	}

	t := &b.Spec.Target
	t.IncludeNotReady = o.includeNotReady
	switch inv.target.Kind {
	case "":
		ls, err := metav1.ParseToLabelSelector(inv.target.Selector.String())
		if err != nil {
			return nil, err
		}
		t.Selector = ls
	case target.KindService:
		t.Service = inv.target.Name
	default:
		t.WorkloadRef = &v1alpha1.WorkloadRef{Kind: string(inv.target.Kind), Name: inv.target.Name}
	}

	b.Spec.Request = v1alpha1.Request{
		Port:               choice.Port,
		DefaultPort:        choice.DefaultPort,
		Scheme:             choice.Scheme,
		Method:             spec.Method,
		Path:               spec.Path,
		Body:               string(spec.Body),
		HeadersFromSecrets: o.secretHeaders,
	}
	b.Spec.Request.Headers = headerMap(spec.Headers)

	codes := strings.Split(o.successCodes, ",")
	for i := range codes {
		codes[i] = strings.TrimSpace(codes[i])
	}
	b.Spec.Mode = v1alpha1.Once
	retries := int32(o.retries)
	b.Spec.Execution = v1alpha1.Execution{
		Concurrency:  int32(o.concurrency),
		Timeout:      &metav1.Duration{Duration: o.timeout},
		Retries:      &retries,
		Backoff:      &metav1.Duration{Duration: o.backoff},
		SuccessCodes: codes,
	}
	return b, nil
}

func headerMap(headers []request.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	m := map[string]string{}
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

func newWaitCommand(o *rootOptions) *cobra.Command {
	var timeout time.Duration
	var format string
	cmd := &cobra.Command{
		Use:   "wait NAME",
		Short: "Wait for a Broadcast to finish, then report it with the usual exit codes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := o.newCluster(o.configFlags)
			if err != nil {
				return err
			}
			key := client.ObjectKey{Namespace: cl.namespace, Name: args[0]}
			return report(cmd.Context(), cl.client, key, timeout, format, o.streams.Out, o.streams.ErrOut)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "How long to wait")
	cmd.Flags().StringVarP(&format, "output", "o", "table", "Output format: table, json or yaml")
	return cmd
}

func newStopCommand(o *rootOptions) *cobra.Command {
	var timeout time.Duration
	var format string
	cmd := &cobra.Command{
		Use:   "stop NAME",
		Short: "End a Continuous broadcast early, revert it, and report the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := o.newCluster(o.configFlags)
			if err != nil {
				return err
			}
			key := client.ObjectKey{Namespace: cl.namespace, Name: args[0]}
			var b v1alpha1.Broadcast
			if err := cl.client.Get(cmd.Context(), key, &b); err != nil {
				return err
			}
			if b.Spec.Mode != v1alpha1.Continuous {
				return fmt.Errorf("broadcast %s runs once; only Continuous broadcasts can be stopped", b.Name)
			}
			patch := client.MergeFrom(b.DeepCopy())
			metav1.SetMetaDataAnnotation(&b.ObjectMeta, v1alpha1.StopAnnotation, "true")
			if err := cl.client.Patch(cmd.Context(), &b, patch); err != nil {
				return err
			}
			return report(cmd.Context(), cl.client, key, timeout, format, o.streams.Out, o.streams.ErrOut)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "How long to wait for the revert")
	cmd.Flags().StringVarP(&format, "output", "o", "table", "Output format: table, json or yaml")
	return cmd
}

// report waits until the controller has finished the Broadcast, or for a Continuous one that is
// still running, its first pass, then prints it.
func report(ctx context.Context, c client.Reader, key client.ObjectKey, timeout time.Duration, format string, out, errOut io.Writer) error {
	b, err := waitDone(ctx, c, key, timeout, errOut)
	if err != nil {
		return err
	}
	st := b.Status
	if st.Phase == v1alpha1.Rejected || (st.Phase == v1alpha1.Failed && st.Summary.Targeted == 0) {
		return fmt.Errorf("broadcast %s %s: %s", b.Name, strings.ToLower(string(st.Phase)), st.Message)
	}
	if st.Phase == v1alpha1.Active {
		fmt.Fprintf(errOut, "broadcast %s keeps applying to new pods until %s; end it early with: kubectl broadcast stop %s\n",
			b.Name, st.ExpiresAt.Local().Format(time.Kitchen), b.Name)
	}
	if st.Summary.Targeted == 0 {
		return output.Write(out, format, output.FromBroadcast(b))
	}
	return writeReport(out, format, output.FromBroadcast(b))
}

func waitDone(ctx context.Context, c client.Reader, key client.ObjectKey, timeout time.Duration, errOut io.Writer) (*v1alpha1.Broadcast, error) {
	deadline := time.Now().Add(timeout)
	warnAt := time.Now().Add(10 * time.Second)
	for {
		var b v1alpha1.Broadcast
		if err := c.Get(ctx, key, &b); err != nil {
			return nil, err
		}
		_, stopping := b.Annotations[v1alpha1.StopAnnotation]
		if b.Status.Phase.Finished() || (b.Status.Phase == v1alpha1.Active && !stopping) {
			return &b, nil
		}
		now := time.Now()
		if now.After(deadline) {
			return nil, fmt.Errorf("timed out waiting for broadcast %s (phase %q)", key.Name, b.Status.Phase)
		}
		if b.Status.StartedAt == nil && now.After(warnAt) {
			fmt.Fprintf(errOut, "warning: broadcast %s has no status yet; is the pod-broadcast controller running?\n", key.Name)
			warnAt = deadline
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
