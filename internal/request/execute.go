package request

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/kumarprabhashanand/pod-broadcast/internal/target"
)

type State string

const (
	Succeeded State = "Succeeded"
	Failed    State = "Failed"
	Gone      State = "Gone"
)

// Response is one HTTP response from a pod.
type Response struct {
	Code int
	Body []byte
}

// Transport sends one attempt to one pod. A returned error means no response came from the pod:
// ErrGone when the pod no longer exists, a *FatalError when the whole broadcast must stop, and any
// other error for a failed connection.
type Transport interface {
	Do(ctx context.Context, ep target.Endpoint, spec Spec) (Response, error)
}

var ErrGone = errors.New("pod is gone")

// FatalError stops the whole broadcast, for example when the caller lacks permission.
type FatalError struct{ Err error }

func (e *FatalError) Error() string { return e.Err.Error() }
func (e *FatalError) Unwrap() error { return e.Err }

type Options struct {
	Concurrency  int
	Timeout      time.Duration
	Retries      int
	Backoff      time.Duration
	SuccessCodes CodeSet
}

// retryableCodes are retried along with connection errors and timeouts.
var retryableCodes = map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true}

const maxBackoff = 30 * time.Second

type Result struct {
	Endpoint target.Endpoint
	State    State
	Code     int
	Duration time.Duration
	Attempts int
	Err      error
	Body     []byte
}

// Run calls every endpoint and returns one result per endpoint, in order. It returns a *FatalError
// when a transport reported one; pods not called by then are marked Failed.
func Run(ctx context.Context, t Transport, endpoints []target.Endpoint, spec Spec, opts Options) ([]Result, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	results := make([]Result, len(endpoints))
	work := make(chan int)
	var wg sync.WaitGroup
	for range min(max(opts.Concurrency, 1), len(endpoints)) {
		wg.Go(func() {
			for i := range work {
				results[i] = call(ctx, t, endpoints[i], spec, opts)
				var fatal *FatalError
				if errors.As(results[i].Err, &fatal) {
					cancel(fatal)
				}
			}
		})
	}
	for i, ep := range endpoints {
		select {
		case work <- i:
		case <-ctx.Done():
			results[i] = Result{Endpoint: ep, State: Failed, Err: context.Cause(ctx)}
		}
	}
	close(work)
	wg.Wait()

	var fatal *FatalError
	if errors.As(context.Cause(ctx), &fatal) {
		return results, fatal
	}
	return results, nil
}

func call(ctx context.Context, t Transport, ep target.Endpoint, spec Spec, opts Options) (r Result) {
	r = Result{Endpoint: ep, State: Failed}
	if ep.Err != nil {
		r.Err = ep.Err
		return r
	}
	start := time.Now()
	defer func() { r.Duration = time.Since(start) }()

	for {
		r.Attempts++
		attemptCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		resp, err := t.Do(attemptCtx, ep, spec)
		cancel()

		var fatal *FatalError
		retryable := false
		switch {
		case errors.Is(err, ErrGone):
			r.State, r.Code, r.Body, r.Err = Gone, 0, nil, err
			return r
		case errors.As(err, &fatal):
			r.Code, r.Body, r.Err = 0, nil, err
			return r
		case err != nil:
			r.Code, r.Body, r.Err = 0, nil, err
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				r.Err = fmt.Errorf("timed out after %s", opts.Timeout)
			}
			retryable = ctx.Err() == nil
		default:
			r.Code, r.Body, r.Err = resp.Code, resp.Body, nil
			if opts.SuccessCodes.Match(resp.Code) {
				r.State = Succeeded
				return r
			}
			r.Err = fmt.Errorf("HTTP %d", resp.Code)
			retryable = retryableCodes[resp.Code]
		}
		if !retryable || r.Attempts > opts.Retries || !sleep(ctx, backoff(opts.Backoff, r.Attempts)) {
			return r
		}
	}
}

// backoff is exponential with full jitter.
func backoff(base time.Duration, attempt int) time.Duration {
	d := min(base<<(attempt-1), maxBackoff)
	if d <= 0 {
		return 0
	}
	return rand.N(d + 1)
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
