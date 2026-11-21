package event

import (
	"errors"

	"github.com/nats-io/nats.go"
)

// busSubscription owns dispatch so Goexit cannot destroy NATS's internal
// callback worker. A failed event is not replayed; subsequent events continue.
type busSubscription struct {
	sub     *nats.Subscription
	handler nats.MsgHandler
	done    chan struct{}
	bus     *NATSBus
}

func (s *busSubscription) run() {
	returned := false
	defer func() {
		if returned {
			close(s.done)
			return
		}
		s.bus.reports.Submit(s.sub.Subject, "NATS listener terminated before returning; continuing subscription", nil)
		// A draining subscription must still finish its buffered messages.
		go s.run()
	}()
	s.consume()
	returned = true
}

func (s *busSubscription) consume() {
	for {
		message, err := s.sub.NextMsgWithContext(bgCtx)
		if err != nil {
			if errors.Is(err, nats.ErrSlowConsumer) {
				s.bus.reports.Submit(s.sub.Subject, "NATS slow consumer dropped messages; continuing subscription", nil)
				continue
			}
			return
		}
		s.handler(message)
	}
}
