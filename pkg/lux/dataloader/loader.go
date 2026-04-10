package dataloader

import (
	"context"
	"sync"
	"time"
)

// BatchFn is the function that fetches data in batch.
// keys: all collected keys in this batch.
// fields: union of all requested fields in this batch.
// Returns a map from key to result.
type BatchFn[K comparable, V any] func(ctx context.Context, keys []K, fields []string) (map[K]V, error)

// Loader batches and caches Load calls within a configurable time window.
// Thread-safe — multiple goroutines can call Load concurrently.
type Loader[K comparable, V any] struct {
	batchFn  BatchFn[K, V]
	wait     time.Duration
	maxBatch int

	mu    sync.Mutex
	batch *batch[K, V]
}

// Config configures a Loader.
type Config struct {
	Wait     time.Duration // batch window (default 2ms)
	MaxBatch int           // max keys per batch (default 100, 0 = unlimited)
}

// DefaultConfig returns the default Loader configuration.
func DefaultConfig() Config {
	return Config{
		Wait:     2 * time.Millisecond,
		MaxBatch: 100,
	}
}

// New creates a Loader with the given batch function and config.
func New[K comparable, V any](fn BatchFn[K, V], cfg Config) *Loader[K, V] {
	if cfg.Wait <= 0 {
		cfg.Wait = 2 * time.Millisecond
	}
	return &Loader[K, V]{
		batchFn:  fn,
		wait:     cfg.Wait,
		maxBatch: cfg.MaxBatch,
	}
}

// Load adds a key + fields to the current batch and waits for the result.
// Multiple concurrent Load calls within the wait window are batched together.
// fields are merged (union) across all calls in the same batch.
func (l *Loader[K, V]) Load(ctx context.Context, key K, fields []string) (V, error) {
	l.mu.Lock()

	// Create batch if needed, start timer
	if l.batch == nil {
		l.batch = newBatch[K, V]()
		go l.scheduleBatch(l.batch)
	}
	b := l.batch

	// Add request to batch
	req := b.add(key, fields)

	// If max batch reached, dispatch immediately
	if l.maxBatch > 0 && b.size() >= l.maxBatch {
		l.batch = nil // next Load creates a new batch
		l.mu.Unlock()
		l.dispatchBatch(b)
	} else {
		l.mu.Unlock()
	}

	// Wait for result
	<-req.done
	return req.value, req.err
}

// LoadMany loads multiple keys with the same fields.
func (l *Loader[K, V]) LoadMany(ctx context.Context, keys []K, fields []string) (map[K]V, error) {
	results := make(map[K]V, len(keys))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for _, key := range keys {
		wg.Add(1)
		go func(k K) {
			defer wg.Done()
			v, err := l.Load(ctx, k, fields)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			results[k] = v
			mu.Unlock()
		}(key)
	}

	wg.Wait()
	return results, firstErr
}

// scheduleBatch waits for the batch window then dispatches.
func (l *Loader[K, V]) scheduleBatch(b *batch[K, V]) {
	time.Sleep(l.wait)

	l.mu.Lock()
	// Only dispatch if this batch is still the current one
	if l.batch == b {
		l.batch = nil
	}
	l.mu.Unlock()

	l.dispatchBatch(b)
}

// dispatchBatch executes the batch function and distributes results.
func (l *Loader[K, V]) dispatchBatch(b *batch[K, V]) {
	b.once.Do(func() {
		keys := b.keys()
		fields := b.mergedFields()
		results, err := l.batchFn(context.Background(), keys, fields)

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
	requests []*request[K, V]
	fieldSet map[string]bool // union of all requested fields
	once     sync.Once
}

type request[K comparable, V any] struct {
	key    K
	fields []string
	value  V
	err    error
	done   chan struct{}
}

func newBatch[K comparable, V any]() *batch[K, V] {
	return &batch[K, V]{
		fieldSet: make(map[string]bool),
	}
}

func (b *batch[K, V]) add(key K, fields []string) *request[K, V] {
	req := &request[K, V]{
		key:    key,
		fields: fields,
		done:   make(chan struct{}),
	}
	b.requests = append(b.requests, req)
	for _, f := range fields {
		b.fieldSet[f] = true
	}
	return req
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

func (b *batch[K, V]) mergedFields() []string {
	if len(b.fieldSet) == 0 {
		return nil // nil = SELECT *
	}
	fields := make([]string, 0, len(b.fieldSet))
	for f := range b.fieldSet {
		fields = append(fields, f)
	}
	return fields
}
