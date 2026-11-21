package queue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Frozen publication path before admitted-publisher shutdown coordination.
func (m *MemoryQueue) publishBeforeCloseAdmission(ctx context.Context, queue string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("queue: closed")
	}
	mq, ok := m.queues[queue]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("queue: no subscriber for %q", queue)
	}

	// Copy payload to prevent caller mutation.
	cp := make([]byte, len(payload))
	copy(cp, payload)

	// Avoid registering cancellation waiters when capacity is already available.
	select {
	case mq.ch <- cp:
		return nil
	default:
	}

	select {
	case mq.ch <- cp:
		return nil
	case <-m.done:
		return fmt.Errorf("queue: closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func BenchmarkMemorySubscriptionLifecycle(b *testing.B) {
	handler := func(context.Context, []byte) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		q, err := NewMemoryQueue(Config{Concurrency: 1})
		if err != nil {
			b.Fatal(err)
		}
		if err := q.Subscribe("work", handler); err != nil {
			b.Fatal(err)
		}
		if err := q.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryPublishCloseAdmission(b *testing.B) {
	for _, size := range []int{32, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			q, err := NewMemoryQueue(DefaultConfig())
			if err != nil {
				b.Fatal(err)
			}
			channel := make(chan []byte, 1)
			q.queues["work"] = &memQueue{ch: channel}
			payload, ctx := make([]byte, size), context.Background()
			for _, version := range []struct {
				name string
				run  func() (int, error)
			}{
				{"Before", func() (int, error) {
					if err := q.publishBeforeCloseAdmission(ctx, "work", payload); err != nil {
						return 0, err
					}
					return len(<-channel), nil
				}},
				{"After", func() (int, error) {
					if err := q.Publish(ctx, "work", payload); err != nil {
						return 0, err
					}
					length := len(<-channel)
					q.work.Done() // Stand in for the worker's completion acknowledgement.
					return length, nil
				}},
			} {
				b.Run(version.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						length, err := version.run()
						if err != nil || length != size {
							b.Fatal("publication changed", err)
						}
					}
				})
			}
		})
	}
}

// Frozen pre-coordination object layout and construction, without workers.
type memoryQueueBeforeCoordination struct {
	cfg    Config
	mu     sync.Mutex
	queues map[string]*memQueue
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	closed bool
}

func newMemoryQueueBeforeCoordination(cfg Config) *memoryQueueBeforeCoordination {
	cfg.SetDefaults()
	return &memoryQueueBeforeCoordination{cfg: cfg, queues: make(map[string]*memQueue), done: make(chan struct{})}
}

var memoryQueueInitializationSink any

func BenchmarkMemoryCloseCoordinationInitialization(b *testing.B) {
	for _, version := range []struct {
		name string
		run  func() any
	}{
		{"Before", func() any { return newMemoryQueueBeforeCoordination(DefaultConfig()) }},
		{"After", func() any {
			q, err := NewMemoryQueue(DefaultConfig())
			if err != nil {
				b.Fatal(err)
			}
			return q
		}},
	} {
		b.Run(version.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				memoryQueueInitializationSink = version.run()
			}
		})
	}
}

func BenchmarkMemoryRepeatedClose(b *testing.B) {
	q, err := NewMemoryQueue(DefaultConfig())
	if err != nil {
		b.Fatal(err)
	}
	if err := q.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := q.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryCloseResultConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		q, err := NewMemoryQueue(DefaultConfig())
		if err != nil {
			b.Fatal(err)
		}
		memoryQueueInitializationSink = q
	}
}

func BenchmarkMemoryWorkerRoundTrip(b *testing.B) {
	q, err := NewMemoryQueue(Config{Concurrency: 1})
	if err != nil {
		b.Fatal(err)
	}
	done := make(chan struct{}, 1)
	if err := q.Subscribe("work", func(context.Context, []byte) error { done <- struct{}{}; return nil }); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := q.Close(); err != nil {
			b.Error(err)
		}
	})
	payload := []byte("accepted")
	b.ReportAllocs()
	for b.Loop() {
		if err := q.Publish(b.Context(), "work", payload); err != nil {
			b.Fatal(err)
		}
		<-done
	}
}

// Measure the resumed retry path, including its existing timer. The handler
// succeeds on this attempt so logging and another retry do not mask the branch.
func BenchmarkMemoryResumedRetry(b *testing.B) {
	q, err := NewMemoryQueue(Config{MaxRetries: 2})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := q.Close(); err != nil {
			b.Error(err)
		}
	})
	handler := func(context.Context, []byte) error { return nil }
	payload := []byte("retry")
	b.ReportAllocs()
	for b.Loop() {
		task := memoryTask{payload: payload, attempt: 1, delay: time.Nanosecond, active: true}
		q.executeTask("work", handler, &task)
		if task.attempt != 2 || task.delay != 2*time.Nanosecond {
			b.Fatal("retry not executed")
		}
	}
}
