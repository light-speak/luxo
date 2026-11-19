package queue

import (
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// messageConsumer owns the dispatch goroutine; business Goexit cannot kill a
// library-owned callback loop. Single-worker delivery has no channel handoff.
type messageConsumer struct {
	messages jetstream.MessagesContext
	handler  jetstream.MessageHandler
	report   func(error)
	stopped  chan struct{}
	done     chan struct{}
	once     sync.Once
	parallel *parallelConsumer
}

func newMessageConsumer(messages jetstream.MessagesContext, handler jetstream.MessageHandler, report func(error)) *messageConsumer {
	c := &messageConsumer{messages: messages, handler: handler, report: report, stopped: make(chan struct{}), done: make(chan struct{})}
	return c
}

// Start is called exactly once, after registration or after a losing close race.
func (c *messageConsumer) Start(workers int) {
	if workers > 1 {
		c.parallel = newParallelConsumer(c.handler, workers, c.report)
	}
	go c.run()
}

func (c *messageConsumer) Stop() {
	c.once.Do(func() { close(c.stopped); c.messages.Stop() })
}

func (c *messageConsumer) Done() <-chan struct{} { return c.done }

func (c *messageConsumer) run() {
	returned := false
	defer func() {
		if returned {
			c.finish()
			return
		}
		select {
		case <-c.stopped:
			c.finish()
		default:
			c.report(queueDiagnostic{operation: "consumer terminated before returning; restarting delivery loop"})
			go c.run()
		}
	}()
	c.consume()
	returned = true
}

func (c *messageConsumer) consume() {
	handle := c.handler
	if c.parallel != nil {
		handle = c.dispatchParallel
	}
	for {
		select {
		case <-c.stopped:
			return
		default:
		}
		if !c.reserveWorker() {
			return
		}
		message, err := c.messages.Next()
		if err != nil {
			c.releaseWorker()
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return
			}
			// Avoid a spin loop on repeated iterator errors; Stop interrupts backoff.
			c.report(queueDiagnostic{operation: "message iterator failed; retrying", cause: err})
			select {
			case <-c.stopped:
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		select {
		case <-c.stopped:
			c.discardMessage(message)
			return
		default:
		}
		handle(message)
	}
}

func (c *messageConsumer) finish() {
	if c.parallel != nil {
		close(c.parallel.jobs)
		c.parallel.workers.Wait()
	}
	close(c.done)
}

func (c *messageConsumer) dispatchParallel(message jetstream.Msg) {
	select {
	case c.parallel.jobs <- message:
	case <-c.stopped:
		c.discardMessage(message)
	}
}

func (c *messageConsumer) reserveWorker() bool {
	if c.parallel == nil {
		return true
	}
	select {
	case c.parallel.slots <- struct{}{}:
		return true
	case <-c.stopped:
		return false
	}
}

func (c *messageConsumer) releaseWorker() {
	if c.parallel != nil {
		<-c.parallel.slots
	}
}

func (c *messageConsumer) discardMessage(message jetstream.Msg) {
	if held, ok := message.(*renewedMessage); ok {
		held.release()
	}
	c.releaseWorker()
}
