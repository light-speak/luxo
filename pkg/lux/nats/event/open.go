package event

import (
	"github.com/light-speak/luxo/pkg/lux/env"
	coreevent "github.com/light-speak/luxo/pkg/lux/event"
)

// NewFromEnv opens only NATS, never an in-process substitute.
func NewFromEnv() (coreevent.Bus, error) {
	url, err := env.Required("NATS_URL")
	if err != nil {
		return nil, err
	}
	bus, err := NewNATSBus(url)
	if err != nil {
		return nil, err
	}
	return bus, nil
}
