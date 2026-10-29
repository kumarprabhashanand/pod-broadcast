package request

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseHeader(t *testing.T) {
	tests := []struct {
		in      string
		want    Header
		wantErr bool
	}{
		{in: "Content-Type: application/json", want: Header{Name: "Content-Type", Value: "application/json"}},
		{in: "x-trace-id:abc", want: Header{Name: "X-Trace-Id", Value: "abc"}},
		{in: "X-Empty:", want: Header{Name: "X-Empty", Value: ""}},
		{in: "X-Colon: a:b", want: Header{Name: "X-Colon", Value: "a:b"}},
		{in: "no colon", wantErr: true},
		{in: "Bad Name: v", wantErr: true},
		{in: "X-Bad: a\nb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseHeader(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseHeader(%q) = %+v, want error", tt.in, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("ParseHeader(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestEnvHeader(t *testing.T) {
	env := map[string]string{"TOKEN": "s3cret", "EMPTY": ""}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	got, err := EnvHeader("X-Api-Token=TOKEN", lookup)
	if err != nil || got != (Header{Name: "X-Api-Token", Value: "s3cret", Secret: true}) {
		t.Errorf("EnvHeader = %+v, %v", got, err)
	}
	for _, in := range []string{"X-A=MISSING", "X-A=EMPTY", "X-A", "X-A="} {
		if _, err := EnvHeader(in, lookup); err == nil {
			t.Errorf("EnvHeader(%q): want error", in)
		}
	}
}

func TestSecretHeaders(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth"},
		Data:       map[string][]byte{"X-Token": []byte("abc\n"), "X-Client": []byte("orders")},
	}
	got, err := SecretHeaders(secret)
	if err != nil {
		t.Fatal(err)
	}
	want := []Header{{Name: "X-Client", Value: "orders", Secret: true}, {Name: "X-Token", Value: "abc", Secret: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SecretHeaders = %+v, want %+v", got, want)
	}

	secret.Data = map[string][]byte{"bad key": []byte("v")}
	if _, err := SecretHeaders(secret); err == nil {
		t.Error("SecretHeaders with an invalid key: want error")
	}
}

func TestMergeLaterWins(t *testing.T) {
	got := Merge(
		[]Header{{Name: "Content-Type", Value: "text/plain"}, {Name: "X-A", Value: "1"}},
		[]Header{{Name: "content-type", Value: "application/json", Secret: true}},
	)
	want := []Header{{Name: "content-type", Value: "application/json", Secret: true}, {Name: "X-A", Value: "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Merge = %+v, want %+v", got, want)
	}
}

func TestDisplayRedacts(t *testing.T) {
	tests := []struct {
		h    Header
		want string
	}{
		{Header{Name: "X-Plain", Value: "v"}, "v"},
		{Header{Name: "X-Plain", Value: "v", Secret: true}, redacted},
		{Header{Name: "Cookie", Value: "v"}, redacted},
		{Header{Name: "X-Auth-Token", Value: "v"}, redacted},
		{Header{Name: "X-Api-Key", Value: "v"}, redacted},
	}
	for _, tt := range tests {
		if got := tt.h.Display(); got != tt.want {
			t.Errorf("%+v.Display() = %q, want %q", tt.h, got, tt.want)
		}
	}
}

func TestCodeSet(t *testing.T) {
	set, err := ParseCodeSet("2xx, 404")
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[int]bool{200: true, 204: true, 299: true, 404: true, 301: false, 403: false, 500: false} {
		if got := set.Match(code); got != want {
			t.Errorf("Match(%d) = %v, want %v", code, got, want)
		}
	}
	for _, bad := range []string{"", "6xx", "2x", "99", "600", "abc", "2xx,"} {
		if _, err := ParseCodeSet(bad); err == nil {
			t.Errorf("ParseCodeSet(%q): want error", bad)
		}
	}
}
