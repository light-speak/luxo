package queue

import (
	"sync"

	"github.com/nats-io/nats.go/jetstream"
)

// A slot is reserved before reading, so blocked workers cannot cause prefetch.
// Batch intake separately bounds and renews messages waiting for a worker.
type parallelConsumer struct {
	jobs    chan jetstream.Msg
	slots   chan struct{}
	workers sync.WaitGroup
	handler jetstream.MessageHandler
	report  func(error)
}

func newParallelConsumer(handler jetstream.MessageHandler, count int, report func(error)) *parallelConsumer {
	p := &parallelConsumer{jobs: make(chan jetstream.Msg), slots: make(chan struct{}, count), handler: handler, report: report}
	p.workers.Add(count)
	for range count {
		go p.run()
	}
	return p
}

func (p *parallelConsumer) run() {
	returned := false
	defer func() {
		if returned {
			p.workers.Done()
			return
		}
		// Transfer the worker slot without replaying the interrupted task.
		p.report(queueDiagnostic{operation: "parallel worker terminated before returning; restarting worker"})
		go p.run()
	}()
	for message := range p.jobs {
		p.handle(message)
	}
	returned = true
}

func (p *parallelConsumer) handle(message jetstream.Msg) {
	defer func() { <-p.slots }()
	p.handler(message)
}
