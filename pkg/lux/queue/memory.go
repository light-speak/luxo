package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/light-speak/luxo/internal/diagnostics"
	"github.com/light-speak/luxo/internal/panicerror"
)

// MemoryQueue implements Queue using buffered Go channels.
// Tasks are not persisted — lost on restart. Suitable for dev / embedded mode.
type MemoryQueue struct {
	cfg             Config
	mu              sync.Mutex
	queues          map[string]*memQueue
	done            chan struct{}
	stopped         chan struct{}
	once            sync.Once
	wg              sync.WaitGroup
	work            sync.WaitGroup
	closed          bool
	closeErr        error // Published to all Close callers by once.
	reports         *diagnostics.Reporter
	reportsClosed   bool
	reportsRejected atomic.Uint64
}

// memQueue holds the channel and handler for one named queue.
type memQueue struct {
	ch      chan []byte
	handler Handler
}

// dlqSuffix is appended to the queue name for the dead-letter queue.
const dlqSuffix = DeadLetterSuffix

// shutdownTimeout is the maximum time Close waits for in-flight tasks.
const shutdownTimeout = 10 * time.Second

var _ Queue = (*MemoryQueue)(nil)

// NewMemoryQueue creates an in-memory queue with the given config.
func NewMemoryQueue(cfg Config) (*MemoryQueue, error) {
	cfg.SetDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &MemoryQueue{
		cfg:     cfg,
		queues:  make(map[string]*memQueue),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}, nil
}

// Publish sends a task payload to the named queue.
// Waits for buffer capacity until the context is canceled or the queue closes.
func (m *MemoryQueue) Publish(ctx context.Context, queue string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("queue: closed")
	}
	mq, ok := m.queues[queue]
	if ok {
		// Registration under mu prevents Add from racing shutdown admission.
		m.work.Add(1)
	}
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
		m.work.Done()
		return fmt.Errorf("queue: closed")
	case <-ctx.Done():
		m.work.Done()
		return ctx.Err()
	}
}

// Subscribe registers a handler for the named queue and starts worker goroutines.
// Only one handler per queue name is allowed.
func (m *MemoryQueue) Subscribe(queue string, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("queue: handler is required")
	}
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

// memoryTask retains an accepted task across abnormal worker termination.
// attempt counts started business calls, including calls terminated by Goexit.
type memoryTask struct {
	payload []byte
	attempt int
	delay   time.Duration
	active  bool
}

// worker reads tasks from the channel and executes the handler with retry.
// Channels stay open; accepted tasks and derived dead letters drain before stop.
func (m *MemoryQueue) worker(name string, mq *memQueue) {
	m.runWorker(name, mq, memoryTask{})
}

func (m *MemoryQueue) runWorker(name string, mq *memQueue, task memoryTask) {
	stopped := false
	defer func() {
		if stopped {
			m.wg.Done()
			return
		}
		// Goexit cannot be recovered. Transfer this worker's existing wait-group
		// reference and in-flight task to its replacement, without re-enqueuing it.
		go m.runWorker(name, mq, task)
	}()
	if task.active {
		m.finishTask(name, mq.handler, &task)
	}
	for {
		select {
		case payload := <-mq.ch:
			task = memoryTask{payload: payload, delay: m.cfg.RetryDelay, active: true}
			m.finishTask(name, mq.handler, &task)
		case <-m.stopped:
			stopped = true
			return
		}
	}
}

func (m *MemoryQueue) finishTask(name string, handler Handler, task *memoryTask) {
	m.executeTask(name, handler, task)
	m.work.Done()
	*task = memoryTask{}
}

// executeTask resumes after a failed attempt, preserving its retry budget.
func (m *MemoryQueue) executeTask(name string, handler Handler, task *memoryTask) {
	for task.attempt < m.cfg.MaxRetries {
		if task.attempt > 0 {
			select {
			case <-time.After(task.delay):
			case <-m.done:
				return
			}
			// Saturate before doubling; overflow must not turn backoff into an immediate retry.
			const longestDelay = time.Duration(1<<63 - 1)
			if task.delay > longestDelay/2 {
				task.delay = longestDelay
			} else {
				task.delay *= 2
			}
		}
		task.attempt++
		err := InvokeHandler(handler, context.Background(), task.payload)
		if err == nil {
			return
		}
		m.reportDiagnostic(memoryDiagnostic{queue: name, attempt: task.attempt, limit: m.cfg.MaxRetries, cause: err})
	}
	m.sendToDLQ(name, task.payload)
}

// sendToDLQ publishes the failed task to the dead-letter queue if it exists.
func (m *MemoryQueue) sendToDLQ(name string, payload []byte) {
	dlqName := name + dlqSuffix
	m.mu.Lock()
	mq, ok := m.queues[dlqName]
	m.mu.Unlock()

	if !ok {
		m.reportDiagnostic(memoryDiagnostic{queue: name, operation: "task dead-lettered (no DLQ subscriber)"})
		return
	}

	// The parent task still holds a work reference until this transfer finishes.
	m.work.Add(1)
	select {
	case mq.ch <- payload:
	default:
		m.work.Done()
		m.reportDiagnostic(memoryDiagnostic{queue: name, operation: "DLQ buffer full, task dropped"})
	}
}

// Close rejects new publications and waits for accepted work and its queued
// dead-letter transfers before stopping workers. Memory storage is not durable.
// Returns an error if shutdown times out or diagnostic reporting fails.
func (m *MemoryQueue) Close() error {
	m.once.Do(func() {
		deadline := time.Now().Add(shutdownTimeout)
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()

		close(m.done)

		// Do NOT close mq.ch here — Publish may still be in a select
		// racing to send on mq.ch. Workers stop only after accepted work drains.

		// Wait with timeout.
		ch := make(chan struct{})
		go func() {
			m.work.Wait()
			close(m.stopped)
			m.wg.Wait()
			close(ch)
		}()

		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
			m.closeErr = fmt.Errorf("queue: shutdown timed out after %v", shutdownTimeout)
		}
		if err := m.closeReports(deadline); err != nil {
			m.closeErr = errors.Join(m.closeErr, err)
		}
	})
	return m.closeErr
}

// InvokeHandler invokes the handler with panic recovery.
func InvokeHandler(h Handler, ctx context.Context, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicerror.New("queue handler panic: ", r)
		}
	}()
	return h(ctx, payload)
}
