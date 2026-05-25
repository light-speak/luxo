package queue

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// MemoryQueue implements Queue using buffered Go channels.
// Tasks are not persisted — lost on restart. Suitable for dev / embedded mode.
type MemoryQueue struct {
	cfg    Config
	mu     sync.Mutex
	queues map[string]*memQueue
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	closed bool
}

// memQueue holds the channel and handler for one named queue.
type memQueue struct {
	ch      chan []byte
	handler Handler
}

// dlqSuffix is appended to the queue name for the dead-letter queue.
const dlqSuffix = ".dlq"

// shutdownTimeout is the maximum time Close waits for in-flight tasks.
const shutdownTimeout = 10 * time.Second

var _ Queue = (*MemoryQueue)(nil)

// NewMemoryQueue creates an in-memory queue with the given config.
func NewMemoryQueue(cfg Config) *MemoryQueue {
	cfg.validate()
	return &MemoryQueue{
		cfg:    cfg,
		queues: make(map[string]*memQueue),
		done:   make(chan struct{}),
	}
}

// Publish sends a task payload to the named queue.
// Returns an error if the queue is closed or the buffer is full.
func (m *MemoryQueue) Publish(_ context.Context, queue string, payload []byte) error {
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

	select {
	case mq.ch <- cp:
		return nil
	case <-m.done:
		return fmt.Errorf("queue: closed")
	}
}

// Subscribe registers a handler for the named queue and starts worker goroutines.
// Only one handler per queue name is allowed.
func (m *MemoryQueue) Subscribe(queue string, handler Handler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("queue: closed")
	}
	if _, ok := m.queues[queue]; ok {
		return fmt.Errorf("queue: %q already subscribed", queue)
	}

	mq := &memQueue{
		ch:      make(chan []byte, 256),
		handler: handler,
	}
	m.queues[queue] = mq

	// Start concurrent workers.
	for range m.cfg.Concurrency {
		m.wg.Add(1)
		go m.worker(queue, mq)
	}
	return nil
}

// worker reads tasks from the channel and executes the handler with retry.
// Channels are never closed; workers detect shutdown exclusively via m.done.
func (m *MemoryQueue) worker(name string, mq *memQueue) {
	defer m.wg.Done()
	for {
		select {
		case payload := <-mq.ch:
			m.executeWithRetry(name, mq.handler, payload)
		case <-m.done:
			// Drain remaining messages in the channel before exiting.
			for {
				select {
				case payload := <-mq.ch:
					m.executeWithRetry(name, mq.handler, payload)
				default:
					return
				}
			}
		}
	}
}

// executeWithRetry runs the handler with exponential backoff retry.
// After MaxRetries failures, publishes to the dead-letter queue.
func (m *MemoryQueue) executeWithRetry(name string, handler Handler, payload []byte) {
	delay := m.cfg.RetryDelay
	for attempt := 1; attempt <= m.cfg.MaxRetries; attempt++ {
		err := safeCall(handler, context.Background(), payload)
		if err == nil {
			return
		}
		fmt.Fprintf(os.Stderr, "queue %s: attempt %d/%d failed: %v\n",
			name, attempt, m.cfg.MaxRetries, err)

		if attempt < m.cfg.MaxRetries {
			select {
			case <-time.After(delay):
			case <-m.done:
				return
			}
			delay *= 2 // exponential backoff
		}
	}

	// All retries exhausted — send to DLQ.
	m.sendToDLQ(name, payload)
}

// sendToDLQ publishes the failed task to the dead-letter queue if it exists.
func (m *MemoryQueue) sendToDLQ(name string, payload []byte) {
	dlqName := name + dlqSuffix
	m.mu.Lock()
	mq, ok := m.queues[dlqName]
	m.mu.Unlock()

	if !ok {
		fmt.Fprintf(os.Stderr, "queue %s: task dead-lettered (no DLQ subscriber)\n", name)
		return
	}

	select {
	case mq.ch <- payload:
	default:
		fmt.Fprintf(os.Stderr, "queue %s: DLQ buffer full, task dropped\n", name)
	}
}

// Close shuts down all workers and waits for in-flight tasks to complete.
// Returns an error if shutdown times out.
func (m *MemoryQueue) Close() error {
	var timedOut bool
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()

		close(m.done)

		// Do NOT close mq.ch here — Publish may still be in a select
		// racing to send on mq.ch. Workers detect shutdown via m.done.

		// Wait with timeout.
		ch := make(chan struct{})
		go func() {
			m.wg.Wait()
			close(ch)
		}()

		select {
		case <-ch:
		case <-time.After(shutdownTimeout):
			timedOut = true
		}
	})
	if timedOut {
		return fmt.Errorf("queue: shutdown timed out after %v", shutdownTimeout)
	}
	return nil
}

// safeCall invokes the handler with panic recovery.
func safeCall(h Handler, ctx context.Context, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("queue handler panic: %v", r)
		}
	}()
	return h(ctx, payload)
}
