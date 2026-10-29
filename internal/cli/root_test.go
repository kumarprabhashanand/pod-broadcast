package cli

import (
	"strings"
	"testing"

	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "kubectl-broadcast "},
		{name: "no arguments prints help", args: nil, wantCode: 0, wantStdout: "kubectl broadcast TARGET"},
		{name: "help", args: []string{"--help"}, wantCode: 0, wantStdout: "--selector"},
		{name: "unknown command", args: []string{"orders"}, wantCode: exitUsage, wantStderr: `unknown command "orders"`},
		{name: "unknown flag", args: []string{"svc/orders", "--bogus"}, wantCode: exitUsage, wantStderr: "unknown flag: --bogus"},
		{name: "missing operation", args: []string{"svc/orders"}, wantCode: exitUsage, wantStderr: "missing operation"},
		{name: "kubeconfig flags are accepted", args: []string{"svc/orders", "-n", "shop", "--context", "dev"}, wantCode: exitUsage, wantStderr: "missing operation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streams, _, out, errOut := genericiooptions.NewTestIOStreams()
			code := Run(tt.args, streams)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}
