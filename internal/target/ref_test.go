package target

import "testing"

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "svc/orders", want: Ref{Kind: KindService, Name: "orders"}},
		{in: "service/orders", want: Ref{Kind: KindService, Name: "orders"}},
		{in: "deploy/orders", want: Ref{Kind: KindDeployment, Name: "orders"}},
		{in: "deployment/orders", want: Ref{Kind: KindDeployment, Name: "orders"}},
		{in: "sts/db", want: Ref{Kind: KindStatefulSet, Name: "db"}},
		{in: "statefulset/db", want: Ref{Kind: KindStatefulSet, Name: "db"}},
		{in: "ds/agent", want: Ref{Kind: KindDaemonSet, Name: "agent"}},
		{in: "daemonset/agent", want: Ref{Kind: KindDaemonSet, Name: "agent"}},
		{in: "rs/orders-7d9f", want: Ref{Kind: KindReplicaSet, Name: "orders-7d9f"}},
		{in: "replicaset/orders-7d9f", want: Ref{Kind: KindReplicaSet, Name: "orders-7d9f"}},
		{in: "orders", wantErr: true},
		{in: "svc/", wantErr: true},
		{in: "svc/a/b", wantErr: true},
		{in: "pod/orders-abc", wantErr: true},
		{in: "SVC/orders", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRef(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRef(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
