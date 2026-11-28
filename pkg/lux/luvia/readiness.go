package luvia

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	readinessTimeout     = 750 * time.Millisecond
	readinessCachePeriod = time.Second
	maxReadinessChecks   = 8
)

// AddReadinessCheck registers a required dependency before Serve. The check
// must honor context cancellation and return an error when the dependency is unavailable.
func (g *Gateway) AddReadinessCheck(check func(context.Context) error) {
	if check != nil {
		g.readinessChecks = append(g.readinessChecks, check)
	}
}

type readinessLifecycle interface {
	http.Handler
	stop()
}

func (g *Gateway) startReadiness() readinessLifecycle {
	if len(g.readinessChecks) == 0 {
		return &statelessReadiness{}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &gatewayReadiness{
		lifetime: lifetime,
		cancel:   cancel,
		checks:   append([]func(context.Context) error(nil), g.readinessChecks...),
	}
}

type statelessReadiness struct{ stopped atomic.Bool }

func (r *statelessReadiness) stop() { r.stopped.Store(true) }

func (r *statelessReadiness) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	respondReady(w, req, !r.stopped.Load())
}

type readinessCheckResult struct {
	done         chan struct{}
	probeContext context.Context
	ready        bool
}

// A batch owns the shared worker inputs in the pending result's allocation.
type readinessCheckBatch struct {
	result        readinessCheckResult
	workerContext context.Context
	cancel        context.CancelFunc
}

type gatewayReadiness struct {
	lifetime context.Context
	cancel   context.CancelFunc
	checks   []func(context.Context) error
	mu       sync.Mutex
	expires  time.Time
	ready    bool
	running  *readinessCheckResult
}

func (r *gatewayReadiness) stop() { r.cancel() }

func (r *gatewayReadiness) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	respondReady(w, req, r.check(req.Context()))
}

func respondReady(w http.ResponseWriter, req *http.Request, ready bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if ready {
		handleHealth(w, req)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(`{"status":"unavailable"}`))
}

func (r *gatewayReadiness) check(ctx context.Context) bool {
	if ctx.Err() != nil || r.lifetime.Err() != nil {
		return false
	}
	r.mu.Lock()
	if time.Now().Before(r.expires) {
		ready := r.ready
		r.mu.Unlock()
		return ready
	}
	pending := r.running
	if pending == nil {
		pending = r.startProbeLocked()
	}
	r.mu.Unlock()

	select {
	case <-pending.done:
	case <-pending.probeContext.Done():
	case <-ctx.Done():
		return false
	}
	// A normal completion closes done before cancelling the timeout context.
	// On timeout, leave the worker registered until it actually returns.
	select {
	case <-pending.done:
		return pending.ready && ctx.Err() == nil && r.lifetime.Err() == nil
	default:
		return false
	}
}

func (r *gatewayReadiness) startProbeLocked() *readinessCheckResult {
	probeContext, cancel := context.WithTimeout(r.lifetime, readinessTimeout)
	done := make(chan struct{})
	if len(r.checks) == 1 {
		pending := &readinessCheckResult{done: done, probeContext: probeContext}
		r.running = pending
		go r.refreshSingle(pending, cancel)
		return pending
	}
	batch := &readinessCheckBatch{
		result: readinessCheckResult{done: done, probeContext: probeContext},
		cancel: cancel,
	}
	r.running = &batch.result
	go r.refreshBatch(batch)
	return &batch.result
}

func (r *gatewayReadiness) refreshSingle(pending *readinessCheckResult, cancel context.CancelFunc) {
	defer cancel()
	ready := false
	defer func() { r.publishProbe(pending, ready) }()
	ctx := pending.probeContext
	ready = ctx.Err() == nil && runReadinessCheck(ctx, r.checks[0], cancel) == nil && ctx.Err() == nil
}

func (r *gatewayReadiness) refreshBatch(batch *readinessCheckBatch) {
	defer batch.cancel()
	ready := false
	defer func() { r.publishProbe(&batch.result, ready) }()
	ready = r.probeBatch(batch)
}

func (r *gatewayReadiness) publishProbe(pending *readinessCheckResult, ready bool) {
	r.mu.Lock()
	r.ready = ready
	r.expires = time.Now().Add(readinessCachePeriod)
	r.running = nil
	pending.ready = ready
	close(pending.done)
	r.mu.Unlock()
}

func (r *gatewayReadiness) probeBatch(batch *readinessCheckBatch) bool {
	ctx := batch.result.probeContext
	if ctx.Err() != nil {
		return false
	}
	group, workerContext := errgroup.WithContext(ctx)
	batch.workerContext = workerContext
	group.SetLimit(maxReadinessChecks)
	for _, probe := range r.checks {
		if workerContext.Err() != nil {
			break
		}
		group.Go(func() error {
			if err := batch.workerContext.Err(); err != nil {
				return err
			}
			return runReadinessCheck(batch.workerContext, probe, batch.cancel)
		})
	}
	return group.Wait() == nil && ctx.Err() == nil
}

var errReadinessCheckPanic = errors.New("readiness check panicked")

func runReadinessCheck(ctx context.Context, probe func(context.Context) error, cancel context.CancelFunc) (err error) {
	returned := false
	defer func() {
		if returned {
			return
		}
		if recover() != nil {
			err = errReadinessCheckPanic
			return
		}
		// Goexit runs defers without returning an error to the probe group.
		cancel()
	}()
	err = probe(ctx)
	returned = true
	return err
}
