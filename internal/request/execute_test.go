package request

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

// fakeTransport answers per pod name with a scripted sequence of responses.
type fakeTransport struct {
	mu       sync.Mutex
	script   map[string][]func(ctx context.Context) (Response, error)
	calls    map[string]int
	inFlight atomic.Int32
	peak     atomic.Int32
	delay    time.Duration
}

func (f *fakeTransport) Do(ctx context.Context, ep target.Endpoint, _ Spec) (Response, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(f.delay)

	f.mu.Lock()
	i := f.calls[ep.Pod.Name]
	f.calls[ep.Pod.Name]++
	steps := f.script[ep.Pod.Name]
	f.mu.Unlock()
	if len(steps) == 0 {
		return Response{Code: 204}, nil
	}
	return steps[min(i, len(steps)-1)](ctx)
}

func code(c int) func(context.Context) (Response, error) {
	return func(context.Context) (Response, error) { return Response{Code: c, Body: []byte("body")}, nil }
}

func fail(err error) func(context.Context) (Response, error) {
	return func(context.Context) (Response, error) { return Response{}, err }
}

func hang(ctx context.Context) (Response, error) {
	<-ctx.Done()
	return Response{}, ctx.Err()
}

func endpoints(names ...string) []target.Endpoint {
	eps := make([]target.Endpoint, len(names))
	for i, n := range names {
		eps[i] = target.Endpoint{Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: n}}, Port: 8080, Scheme: "http"}
	}
	return eps
}

func opts() Options {
	codes, _ := ParseCodeSet("2xx")
	return Options{Concurrency: 10, Timeout: time.Second, Retries: 2, Backoff: time.Millisecond, SuccessCodes: codes}
}

func newFake(script map[string][]func(context.Context) (Response, error)) *fakeTransport {
	return &fakeTransport{script: script, calls: map[string]int{}}
}

func TestRunStates(t *testing.T) {
	refused := errors.New("connection refused")
	ft := newFake(map[string][]func(context.Context) (Response, error){
		"ok":        {code(204)},
		"retried":   {code(503), fail(refused), code(200)},
		"always500": {code(500)},
		"client":    {code(404)},
		"refused":   {fail(refused)},
		"gone":      {fail(ErrGone)},
	})
	eps := endpoints("ok", "retried", "always500", "client", "refused", "gone")
	eps = append(eps, target.Endpoint{Pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "noport"}}, Err: target.ErrPortNotFound})

	results, err := Run(context.Background(), ft, eps, Spec{Method: "GET", Path: "/"}, opts())
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		state    State
		code     int
		attempts int
		err      string
	}{
		{Succeeded, 204, 1, ""},
		{Succeeded, 200, 3, ""},
		{Failed, 500, 3, "HTTP 500"},
		{Failed, 404, 1, "HTTP 404"},
		{Failed, 0, 3, "connection refused"},
		{Gone, 0, 1, "pod is gone"},
		{Failed, 0, 0, "port not found"},
	}
	for i, w := range want {
		r := results[i]
		gotErr := ""
		if r.Err != nil {
			gotErr = r.Err.Error()
		}
		if r.State != w.state || r.Code != w.code || r.Attempts != w.attempts || gotErr != w.err {
			t.Errorf("%s: got state=%s code=%d attempts=%d err=%q; want %s %d %d %q",
				eps[i].Pod.Name, r.State, r.Code, r.Attempts, gotErr, w.state, w.code, w.attempts, w.err)
		}
	}
}

func TestRunTimeoutIsRetried(t *testing.T) {
	ft := newFake(map[string][]func(context.Context) (Response, error){"slow": {hang, code(200)}})
	o := opts()
	o.Timeout = 20 * time.Millisecond
	results, _ := Run(context.Background(), ft, endpoints("slow"), Spec{}, o)
	if r := results[0]; r.State != Succeeded || r.Attempts != 2 {
		t.Errorf("got %+v, want success on the second attempt", r)
	}

	ft = newFake(map[string][]func(context.Context) (Response, error){"slow": {hang}})
	results, _ = Run(context.Background(), ft, endpoints("slow"), Spec{}, o)
	if r := results[0]; r.State != Failed || r.Attempts != 3 || r.Err.Error() != "timed out after 20ms" {
		t.Errorf("got %+v, want 3 timed-out attempts", r)
	}
}

func TestRunRespectsConcurrency(t *testing.T) {
	ft := newFake(nil)
	ft.delay = 20 * time.Millisecond
	o := opts()
	o.Concurrency = 3
	names := make([]string, 12)
	for i := range names {
		names[i] = string(rune('a' + i))
	}
	results, _ := Run(context.Background(), ft, endpoints(names...), Spec{}, o)
	if peak := ft.peak.Load(); peak != 3 {
		t.Errorf("peak concurrency = %d, want 3", peak)
	}
	for _, r := range results {
		if r.State != Succeeded {
			t.Errorf("%s: %s", r.Endpoint.Pod.Name, r.State)
		}
	}
}

func TestRunFatalErrorStopsTheBroadcast(t *testing.T) {
	forbidden := &FatalError{Err: errors.New("forbidden")}
	ft := newFake(map[string][]func(context.Context) (Response, error){"a": {fail(forbidden)}})
	o := opts()
	o.Concurrency = 1
	results, err := Run(context.Background(), ft, endpoints("a", "b", "c"), Spec{}, o)
	var fatal *FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("err = %v, want a FatalError", err)
	}
	if ft.calls["b"]+ft.calls["c"] > 1 {
		t.Errorf("calls after the fatal error: %v", ft.calls)
	}
	if results[0].Attempts != 1 {
		t.Errorf("fatal error was retried: %+v", results[0])
	}
}

func TestBackoffIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 12; attempt++ {
		if d := backoff(time.Second, attempt); d < 0 || d > maxBackoff {
			t.Errorf("backoff(1s, %d) = %s, outside [0, %s]", attempt, d, maxBackoff)
		}
	}
	if d := backoff(0, 1); d != 0 {
		t.Errorf("backoff(0, 1) = %s, want 0", d)
	}
}
