package output

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"

	"github.com/kumarprabhashanand/pod-broadcast/api/v1alpha1"
	"github.com/kumarprabhashanand/pod-broadcast/internal/request"
	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

const SchemaVersion = "v1"

type Outcome string

const (
	OutcomeSucceeded       Outcome = "Succeeded"
	OutcomePartiallyFailed Outcome = "PartiallyFailed"
	OutcomeFailed          Outcome = "Failed"
	OutcomeDryRun          Outcome = "DryRun"
)

// Exit codes shared by every command.
const (
	ExitSucceeded       = 0
	ExitPartiallyFailed = 1
	ExitUsage           = 2
	ExitFailed          = 3
)

// statePending marks pods in a dry run that would be called.
const statePending = "Pending"

// Report is the result of one broadcast. Its JSON form is the versioned output schema.
type Report struct {
	SchemaVersion string  `json:"schemaVersion"`
	Mode          string  `json:"mode"`
	DryRun        bool    `json:"dryRun"`
	Target        Target  `json:"target"`
	Request       Request `json:"request"`
	Outcome       Outcome `json:"outcome"`
	Summary       Summary `json:"summary"`
	Pods          []Pod   `json:"pods"`
}

type Target struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Selector  string `json:"selector,omitempty"`
}

type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// Summary counts the pods by result; it is the same in both modes.
type Summary = v1alpha1.Summary

type Pod struct {
	Name       string `json:"name"`
	UID        string `json:"uid"`
	IP         string `json:"ip"`
	Port       int32  `json:"port"`
	Scheme     string `json:"scheme"`
	State      string `json:"state"`
	Code       int    `json:"code"`
	DurationMs int64  `json:"durationMs"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error"`
	Body       string `json:"body"`
}

func newReport(ns string, ref target.Ref, spec request.Spec) Report {
	r := Report{
		SchemaVersion: SchemaVersion,
		Mode:          "direct",
		Target:        Target{Namespace: ns, Kind: string(ref.Kind), Name: ref.Name},
		Request:       Request{Method: spec.Method, Path: spec.Path, Body: string(spec.Body)},
		Pods:          []Pod{},
	}
	if ref.Selector != nil {
		r.Target.Selector = ref.Selector.String()
	}
	if len(spec.Headers) > 0 {
		r.Request.Headers = map[string]string{}
		for _, h := range spec.Headers {
			r.Request.Headers[h.Name] = h.Display()
		}
	}
	return r
}

func podFor(ep target.Endpoint) Pod {
	return Pod{
		Name:   ep.Pod.Name,
		UID:    string(ep.Pod.UID),
		IP:     ep.Pod.Status.PodIP,
		Port:   ep.Port,
		Scheme: ep.Scheme,
	}
}

// NewReport builds the report for finished calls.
func NewReport(ns string, ref target.Ref, spec request.Spec, results []request.Result) Report {
	r := newReport(ns, ref, spec)
	for _, res := range results {
		p := podFor(res.Endpoint)
		p.State = string(res.State)
		p.Code = res.Code
		p.DurationMs = res.Duration.Milliseconds()
		p.Attempts = res.Attempts
		p.Body = string(res.Body)
		if res.Err != nil {
			p.Error = res.Err.Error()
		}
		r.Pods = append(r.Pods, p)
		switch res.State {
		case request.Succeeded:
			r.Summary.Succeeded++
		case request.Gone:
			r.Summary.Gone++
		default:
			r.Summary.Failed++
		}
	}
	r.Summary.Targeted = int32(len(results))
	r.Outcome = Outcome(r.Summary.Outcome())
	return r
}

// FromBroadcast builds the report of a Broadcast the controller ran.
func FromBroadcast(b *v1alpha1.Broadcast) Report {
	ref, _ := target.FromSpec(b.Spec.Target)
	req := b.Spec.Request
	spec := request.Spec{Method: req.Method, Path: req.Path, Body: []byte(req.Body)}
	for name, value := range req.Headers {
		spec.Headers = append(spec.Headers, request.Header{Name: name, Value: value})
	}
	r := newReport(b.Namespace, ref, spec)
	r.Mode = "controller"
	for _, p := range b.Status.Pods {
		r.Pods = append(r.Pods, Pod{
			Name: p.Name, UID: p.UID, IP: p.IP, Port: p.Port, Scheme: p.Scheme, State: p.State, Code: int(p.Code),
			DurationMs: p.DurationMs, Attempts: int(p.Attempts), Error: p.Error, Body: p.Body,
		})
	}
	r.Summary = b.Status.Summary
	r.Outcome = Outcome(r.Summary.Outcome())
	return r
}

// NewDryRunReport lists the pods that would be called. Pods that can't be called are Failed.
func NewDryRunReport(ns string, ref target.Ref, spec request.Spec, endpoints []target.Endpoint) Report {
	r := newReport(ns, ref, spec)
	r.DryRun = true
	r.Outcome = OutcomeDryRun
	for _, ep := range endpoints {
		p := podFor(ep)
		p.State = statePending
		if ep.Err != nil {
			p.State = string(request.Failed)
			p.Error = ep.Err.Error()
			r.Summary.Failed++
		}
		r.Pods = append(r.Pods, p)
	}
	r.Summary.Targeted = int32(len(endpoints))
	return r
}

// ExitCode maps an outcome to the process exit code.
func (r Report) ExitCode() int {
	switch r.Outcome {
	case OutcomeSucceeded, OutcomeDryRun:
		return ExitSucceeded
	case OutcomePartiallyFailed:
		return ExitPartiallyFailed
	}
	return ExitFailed
}

// Formats lists the values accepted by Write.
var Formats = []string{"table", "json", "yaml"}

func Write(w io.Writer, format string, r Report) error {
	switch format {
	case "table":
		return writeTable(w, r)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "yaml":
		b, err := yaml.Marshal(r)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	return fmt.Errorf("unknown output format %q", format)
}
