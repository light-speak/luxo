// Package event provides a typed event system for the Luxo runtime.
// An unset NATS_URL uses Go channels; configured NATS enables cross-process delivery.
package event

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/env"
)

var ErrBinaryPayloadRequired = errors.New("event: NATS payload must use Luxo binary or []byte")

// Bus is the interface for publishing and subscribing to events.
// ChanBus (channel) and NATSBus implement this interface.
type Bus interface {
	// Emit publishes an event with the given name and payload. Consumer handlers
	// run with a bus-owned context because delivery can outlive the caller.
	// payload can be any type — ChanBus passes it directly (zero serialization),
	// while NATSBus accepts LuxoMarshaler values or pre-encoded []byte only.
	Emit(ctx context.Context, name string, payload any) error

	// On registers a broadcast handler — every instance receives the event.
	// Use for cache invalidation, config refresh, etc.
	On(name string, handler Handler) error

	// OnQueue registers a queue handler — only one instance per group
	// receives each event. Different groups each get a copy.
	// group is typically the module name (auto-set by codegen).
	OnQueue(name string, group string, handler Handler) error

	// Close shuts down the bus and all subscriptions.
	Close()
}

// Handler processes an event payload and returns an error if processing fails.
// For ChanBus, payload is the original struct (zero-copy).
// For NATSBus, payload is []byte containing the published wire representation.
// Current backends log handler failures without retry or dead-letter delivery.
type Handler func(ctx context.Context, payload any) error

// NewFromEnv creates a Bus based on environment configuration.
// An explicitly configured NATS backend must initialize successfully.
// Only an unset NATS_URL selects the in-process ChanBus.
func NewFromEnv() (Bus, error) {
	if natsURL, ok := env.Get("NATS_URL"); ok {
		if strings.TrimSpace(natsURL) == "" {
			return nil, errors.New("event: NATS_URL is set but empty")
		}
		bus, err := NewNATSBus(natsURL)
		if err != nil {
			return nil, fmt.Errorf("event: initialize NATS: %w", err)
		}
		return bus, nil
	}
	return NewChanBus(256), nil
}
