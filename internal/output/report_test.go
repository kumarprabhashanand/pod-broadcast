package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

func ep(name string, err error) target.Endpoint {
	return target.Endpoint{
		Pod:    &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, UID: "uid-" + "x"}, Status: corev1.PodStatus{PodIP: "10.0.0.1"}},
		Port:   8081,
		Scheme: "http",
		Err:    err,
	}
}

var (
	svc  = target.Ref{Kind: target.KindService, Name: "orders"}
	spec = request.Spec{
		Method:  "DELETE",
		Path:    "/actuator/caches",
		Headers: []request.Header{{Name: "X-Token", Value: "s3cret", Secret: true}, {Name: "X-Trace", Value: "t1"}},
	}
)

func results() []request.Result {
	return []request.Result{
		{Endpoint: ep("orders-a", nil), State: request.Succeeded, Code: 204, Duration: 41 * time.Millisecond, Attempts: 1},
		{Endpoint: ep("orders-b", nil), State: request.Failed, Code: 500, Duration: 5100 * time.Millisecond, Attempts: 3, Err: errors.New("HTTP 500"), Body: []byte("{\n \"error\": \"boom\"\n}")},
		{Endpoint: ep("orders-c", nil), State: request.Gone, Attempts: 1, Err: request.ErrGone},
	}
}

func TestOutcomeAndExitCode(t *testing.T) {
	ok := request.Result{Endpoint: ep("a", nil), State: request.Succeeded}
	bad := request.Result{Endpoint: ep("b", nil), State: request.Failed}
	gone := request.Result{Endpoint: ep("c", nil), State: request.Gone}
	tests := []struct {
		name    string
		results []request.Result
		outcome Outcome
		exit    int
	}{
		{"all succeeded", []request.Result{ok, ok}, OutcomeSucceeded, 0},
		{"succeeded and gone", []request.Result{ok, gone}, OutcomeSucceeded, 0},
		{"some failed", []request.Result{ok, bad}, OutcomePartiallyFailed, 1},
		{"all failed", []request.Result{bad, bad}, OutcomeFailed, 3},
		{"failed and gone", []request.Result{bad, gone}, OutcomeFailed, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReport("shop", svc, spec, tt.results)
			if r.Outcome != tt.outcome || r.ExitCode() != tt.exit {
				t.Errorf("outcome %s exit %d, want %s %d", r.Outcome, r.ExitCode(), tt.outcome, tt.exit)
			}
		})
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, "table", NewReport("shop", svc, spec, results())); err != nil {
		t.Fatal(err)
	}
	want := `POD       STATE      CODE  TIME  ATTEMPTS  ERROR        BODY
orders-a  Succeeded  204   41ms  1
orders-b  Failed     500   5.1s  3         HTTP 500     { "error": "boom" }
orders-c  Gone             0ms   1         pod is gone
3 targeted: 1 succeeded, 1 failed, 1 gone
`
	if buf.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestTableWithoutBodies(t *testing.T) {
	var buf bytes.Buffer
	r := NewReport("shop", svc, spec, results()[:1])
	if err := Write(&buf, "table", r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "BODY") {
		t.Errorf("BODY column shown without bodies:\n%s", buf.String())
	}
}

func TestDryRunTable(t *testing.T) {
	var buf bytes.Buffer
	eps := []target.Endpoint{ep("orders-a", nil), ep("orders-b", target.ErrPortNotFound)}
	if err := Write(&buf, "table", NewDryRunReport("shop", svc, spec, eps)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"DELETE /actuator/caches\n", "X-Token: <redacted>\n", "X-Trace: t1\n", "orders-a  10.0.0.1  8081  http", "port not found", "dry run: 1 of 2 pods would be called; nothing was sent"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("dry run shows a secret value:\n%s", out)
	}
}

func TestJSONAndYAMLMatchTheSchema(t *testing.T) {
	c := jsonschema.NewCompiler()
	schema, err := c.Compile("../../schemas/output-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	selector := target.Ref{Selector: "app=orders"}
	reports := map[string]Report{
		"results":  NewReport("shop", svc, spec, results()),
		"selector": NewReport("shop", selector, request.Spec{Method: "GET", Path: "/"}, results()),
		"dry run":  NewDryRunReport("shop", svc, spec, []target.Endpoint{ep("orders-a", nil), ep("orders-b", target.ErrPortNotFound)}),
		"empty":    NewReport("shop", svc, spec, nil),
	}
	for name, r := range reports {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, "json", r); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "s3cret") {
				t.Error("JSON output shows a secret value")
			}
			doc, err := jsonschema.UnmarshalJSON(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); err != nil {
				t.Errorf("schema validation: %v", err)
			}

			buf.Reset()
			if err := Write(&buf, "yaml", r); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "schemaVersion: v1") {
				t.Errorf("YAML output:\n%s", buf.String())
			}
		})
	}
}

func TestSchemaRejectsInvalidReports(t *testing.T) {
	c := jsonschema.NewCompiler()
	schema, err := c.Compile("../../schemas/output-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{}`,
		`{"schemaVersion":"v2","mode":"direct","dryRun":false,"target":{"namespace":"a","kind":"Service","name":"b"},"request":{"method":"GET","path":"/"},"outcome":"Succeeded","summary":{"targeted":0,"succeeded":0,"failed":0,"gone":0},"pods":[]}`,
		`{"schemaVersion":"v1","mode":"direct","dryRun":false,"target":{"namespace":"a","kind":"Service","name":"b","selector":"x"},"request":{"method":"GET","path":"/"},"outcome":"Succeeded","summary":{"targeted":0,"succeeded":0,"failed":0,"gone":0},"pods":[]}`,
	} {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		if schema.Validate(doc) == nil {
			t.Errorf("schema accepted %s", bad)
		}
	}
}

func TestDuration(t *testing.T) {
	for ms, want := range map[int64]string{0: "0ms", 41: "41ms", 999: "999ms", 1000: "1.0s", 5149: "5.1s"} {
		if got := duration(ms); got != want {
			t.Errorf("duration(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	if err := Write(&bytes.Buffer{}, "xml", Report{}); err == nil {
		t.Error("want an error for an unknown format")
	}
}
