package dataloader

import (
	"context"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

// BatchFn is the function that fetches data in batch.
// keys: all collected keys in this batch.
// fields: merged recursive selection requested by this batch. The selection is
// immutable request metadata and must not be modified by the batch function.
// Returns a map from key to result.
type BatchFn[K comparable, V any] func(ctx context.Context, keys []K, fields []*selection.Field) (map[K]V, error)

// Loader batches and caches Load calls within a configurable time window.
// Thread-safe — multiple goroutines can call Load concurrently.
type Loader[K comparable, V any] struct {
	batchFn  BatchFn[K, V]
	wait     time.Duration
	maxBatch int

	mu      sync.Mutex
	batches map[<-chan struct{}]*batch[K, V]
}

// Config configures a Loader.
type Config struct {
	Wait     time.Duration // batch window (default 2ms)
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
		batches:  make(map[<-chan struct{}]*batch[K, V]),
	}
}

// Load adds a key + fields to the current batch and waits for the result.
// Respects ctx cancellation — returns immediately if context is done.
func (l *Loader[K, V]) Load(ctx context.Context, key K, fields []*selection.Field) (V, error) {
	l.mu.Lock()

	scope := ctx.Done()
	b := l.batches[scope]
	if b == nil {
		b = newBatch[K, V](ctx)
		l.batches[scope] = b
		time.AfterFunc(l.wait, func() { l.flushBatch(scope, b) })
	}
	req := b.add(key, fields)

	if l.maxBatch > 0 && b.size() >= l.maxBatch {
		delete(l.batches, scope)
		l.mu.Unlock()
		l.dispatchBatch(b)
	} else {
		l.mu.Unlock()
	}

	// Wait for result or context cancellation
	select {
	case <-req.done:
		return req.value, req.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

// LoadAll loads multiple keys immediately — bypasses batch window.
// Used by LIST handlers where all keys are known upfront.
// Zero wait, direct dispatch.
func (l *Loader[K, V]) LoadAll(ctx context.Context, keys []K, fields []*selection.Field) (map[K]V, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	return l.batchFn(ctx, keys, fields)
}

// flushBatch dispatches a batch after the wait window expires.
// Called by time.AfterFunc — no goroutine blocked during wait.
func (l *Loader[K, V]) flushBatch(scope <-chan struct{}, b *batch[K, V]) {
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
		keys := b.keys()
		fields := b.mergedSelection()
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		results, err := l.batchFn(ctx, keys, fields)

		for _, req := range b.requests {
			if err != nil {
				req.err = err
			} else {
				req.value = results[req.key]
			}
			close(req.done)
		}
	})
}

// batch collects load requests within a window.
type batch[K comparable, V any] struct {
	ctx        context.Context
	requests   []*request[K, V]
	fields     []*selection.Field
	hasRequest bool
	selectsAll bool
	once       sync.Once
}

type request[K comparable, V any] struct {
	key    K
	fields []*selection.Field
	value  V
	err    error
	done   chan struct{}
}

func newBatch[K comparable, V any](ctx context.Context) *batch[K, V] {
	return &batch[K, V]{
		ctx: ctx,
	}
}

func (b *batch[K, V]) add(key K, fields []*selection.Field) *request[K, V] {
	req := &request[K, V]{
		key:    key,
		fields: fields,
		done:   make(chan struct{}),
	}
	b.requests = append(b.requests, req)
	if !b.hasRequest {
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
	return req
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
	return len(b.requests)
}

func (b *batch[K, V]) keys() []K {
	seen := make(map[K]bool, len(b.requests))
	var keys []K
	for _, req := range b.requests {
		if !seen[req.key] {
			seen[req.key] = true
			keys = append(keys, req.key)
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
