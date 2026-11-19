package dataloader

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/light-speak/luxo/internal/panicerror"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// ErrNonUniqueRelation rejects multiple rows for a single-object relation.
var ErrNonUniqueRelation = errors.New("dataloader: multiple rows for single relation")

var errNilBatchFunction = errors.New("dataloader: batch function is nil")
var errBatchInterrupted = errors.New("dataloader: batch callback terminated before returning")

// BatchFn is the function that fetches data in batch.
// keys: all collected keys in this batch.
// fields: merged recursive selection requested by this batch. The selection is
// immutable request metadata and must not be modified by the batch function.
// Returns a map from key to result.
type BatchFn[K comparable, V any] func(ctx context.Context, keys []K, fields []*selection.Field) (map[K]V, error)

// Loader batches and deduplicates Load calls within a configurable time window.
// Thread-safe — multiple goroutines can call Load concurrently.
type Loader[K comparable, V any] struct {
	batchFn  BatchFn[K, V]
	wait     time.Duration
	maxBatch int

	mu      sync.Mutex
	batches map[batchContextKey]*batch[K, V]
}

type batchContextKey struct {
	scope *Scope
	done  <-chan struct{}
}

// Config configures a Loader.
type Config struct {
	Wait     time.Duration // batch window (default 1ms)
	MaxBatch int           // max keys per batch (default 100, 0 = unlimited)
}

// DefaultConfig returns the default Loader configuration.
func DefaultConfig() Config {
	return Config{
		Wait:     1 * time.Millisecond,
		MaxBatch: 100,
	}
}

// New creates a Loader with the given batch function and config.
func New[K comparable, V any](fn BatchFn[K, V], cfg Config) *Loader[K, V] {
	if cfg.Wait <= 0 {
		cfg.Wait = 1 * time.Millisecond
	}
	return &Loader[K, V]{
		batchFn:  fn,
		wait:     cfg.Wait,
		maxBatch: cfg.MaxBatch,
	}
}

// Load adds a key + fields to the current batch and waits for the result.
// Batched loads stop waiting on caller cancellation. MaxBatch=1 executes inline;
// its callback must honor the supplied context before the call can return.
func (l *Loader[K, V]) Load(ctx context.Context, key K, fields []*selection.Field) (V, error) {
	scope, err := scopeForContext(ctx)
	if err != nil {
		var zero V
		return zero, err
	}
	if l.maxBatch == 1 {
		return l.loadImmediate(contextForScope(ctx, scope), key, fields)
	}
	if scope == nil {
		var zero V
		return zero, ErrMissingScope
	}
	return l.loadBatched(ctx, scope, key, fields)
}

func (l *Loader[K, V]) loadBatched(ctx context.Context, requestScope *Scope, key K, fields []*selection.Field) (V, error) {
	l.mu.Lock()

	scope := batchContextKey{scope: requestScope, done: ctx.Done()}
	b := l.batches[scope]
	if b == nil {
		if l.batches == nil {
			l.batches = make(map[batchContextKey]*batch[K, V])
		}
		b = newBatch[K, V](contextForScope(ctx, requestScope))
		l.batches[scope] = b
		b.run = func() { l.flushBatch(scope, b) }
		b.timer = time.AfterFunc(l.wait, b.run)
	}
	index := b.add(key, fields)

	if l.maxBatch > 0 && b.size() >= l.maxBatch {
		delete(l.batches, scope)
		l.mu.Unlock()
		// Reuse the captured callback instead of allocating a new goroutine closure.
		b.timer.Stop()
		go b.run()
	} else {
		l.mu.Unlock()
	}

	// Wait for result or context cancellation
	select {
	case <-b.done:
		if b.err != nil {
			var zero V
			return zero, b.err
		}
		return b.values[index], nil
	case <-ctx.Done():
		var zero V
		err := ctx.Err()
		l.cancelBatch(scope, b, err)
		return zero, err
	}
}

// Single-key mode skips batch state and executes inline so transactions wait
// for their backend call to finish before completing.
func (l *Loader[K, V]) loadImmediate(ctx context.Context, key K, fields []*selection.Field) (V, error) {
	dispatchCtx, cancel := batchReadContext(ctx)
	defer cancel()
	results, err := l.invokeBatchForCaller(ctx, dispatchCtx, []K{key}, fields)
	if err != nil {
		var zero V
		return zero, err
	}
	return results[key], nil
}

// LoadAll loads multiple keys immediately — bypasses batch window.
// Used by LIST handlers where all keys are known upfront.
// Zero wait, direct dispatch.
// Every dispatched read has the same 30-second cap as single-key batching;
// an earlier caller deadline is retained. Callbacks must honor the context.
func (l *Loader[K, V]) LoadAll(ctx context.Context, keys []K, fields []*selection.Field) (map[K]V, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	scope, err := scopeForContext(ctx)
	if err != nil {
		return nil, err
	}
	caller := contextForScope(ctx, scope)
	dispatchCtx, cancel := batchReadContext(caller)
	defer cancel()
	return l.invokeBatchForCaller(caller, dispatchCtx, keys, fields)
}

func batchReadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}

// The caller owns dispatchCtx and cancels it after consuming/snapshotting values.
func (l *Loader[K, V]) invokeBatchForCaller(ctx, dispatchCtx context.Context, keys []K, fields []*selection.Field) (map[K]V, error) {
	results, err := l.invokeBatch(dispatchCtx, keys, fields)
	// Custom cancellation registration may notify a derived timer asynchronously.
	// The caller's own cancellation must still prevent a successful return.
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return results, err
}

// Keep panic values opaque unless they are plain strings; their methods may exit.
func (l *Loader[K, V]) invokeBatch(ctx context.Context, keys []K, fields []*selection.Field) (results map[K]V, err error) {
	if l.batchFn == nil {
		return nil, errNilBatchFunction
	}
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	defer func() {
		if value := recover(); value != nil {
			results, err = nil, panicerror.New("dataloader callback panic: ", value)
		}
	}()
	results, err = l.batchFn(ctx, keys, fields)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return results, err
}

// A queued batch shares one request scope and cancellation channel. Dispatch removes its slot
// before invoking user code, so cancellation never completes an active batch.
func (l *Loader[K, V]) cancelBatch(scope batchContextKey, b *batch[K, V], err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.batches[scope] != b {
		return
	}
	delete(l.batches, scope)
	b.timer.Stop()
	b.once.Do(func() { b.complete(nil, err) })
}

// flushBatch dispatches a batch after the wait window expires.
// Called by time.AfterFunc — no goroutine blocked during wait.
func (l *Loader[K, V]) flushBatch(scope batchContextKey, b *batch[K, V]) {
	l.mu.Lock()
	if l.batches[scope] == b {
		delete(l.batches, scope)
	}
	l.mu.Unlock()

	l.dispatchBatch(b)
}

// dispatchBatch executes the batch function and distributes results.
func (l *Loader[K, V]) dispatchBatch(b *batch[K, V]) {
	b.once.Do(func() {
		// Publication under mu initializes the timer before either dispatcher runs.
		b.timer.Stop()
		keys := b.keys()
		fields := b.mergedSelection()
		ctx, cancel := batchReadContext(b.ctx)
		defer cancel()
		var results map[K]V
		err := errBatchInterrupted
		// Goexit bypasses ordinary returns but must still release every waiter.
		defer func() { b.complete(results, err) }()
		results, err = l.invokeBatchForCaller(b.ctx, ctx, keys, fields)
	})
}

func (b *batch[K, V]) complete(results map[K]V, err error) {
	b.err = err
	if err == nil {
		// Snapshot values in caller order before waking waiters.
		b.values = make([]V, len(b.requestedKeys))
		for i, key := range b.requestedKeys {
			b.values[i] = results[key]
		}
	}
	close(b.done)
}

// batch collects load requests within a window.
type batch[K comparable, V any] struct {
	ctx           context.Context
	timer         *time.Timer
	run           func()
	requestedKeys []K
	values        []V
	done          chan struct{}
	err           error
	fields        []*selection.Field
	hasRequest    bool
	selectsAll    bool
	once          sync.Once
}

func newBatch[K comparable, V any](ctx context.Context) *batch[K, V] {
	return &batch[K, V]{
		ctx: ctx,
	}
}

func (b *batch[K, V]) add(key K, fields []*selection.Field) int {
	index := len(b.requestedKeys)
	b.requestedKeys = append(b.requestedKeys, key)
	if !b.hasRequest {
		b.done = make(chan struct{})
		b.hasRequest = true
		b.selectsAll = fields == nil
		if fields != nil {
			b.fields = fields
		}
	} else if !b.selectsAll {
		if fields == nil {
			b.selectsAll = true
			b.fields = nil
		} else if !sameSelection(b.fields, fields) {
			b.fields = selection.Merge(b.fields, fields)
		}
	}
	return index
}

func sameSelection(left, right []*selection.Field) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] == nil || right[i] == nil {
			if left[i] != right[i] {
				return false
			}
			continue
		}
		if left[i].Name != right[i].Name || !sameSelection(left[i].Children, right[i].Children) {
			return false
		}
	}
	return true
}

func (b *batch[K, V]) size() int {
	return len(b.requestedKeys)
}

func (b *batch[K, V]) keys() []K {
	seen := make(map[K]bool, len(b.requestedKeys))
	var keys []K
	for _, key := range b.requestedKeys {
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func (b *batch[K, V]) mergedSelection() []*selection.Field {
	if b.selectsAll || !b.hasRequest {
		return nil
	}
	return b.fields
}
