// Package event provides a typed event system for the Luxo runtime.
// Embedded mode uses Go channels, multi-service mode uses NATS.
package event

import (
	"context"
	"fmt"
	"os"

	"github.com/light-speak/luxo/pkg/lux/env"
)

// Bus is the interface for publishing and subscribing to events.
// ChanBus (channel) and NATSBus implement this interface.
type Bus interface {
	// Emit publishes an event with the given name and payload.
	// payload can be any type — ChanBus passes it directly (zero serialization),
	// NATSBus uses Luxo binary for generated events and JSON for custom payloads.
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
// Returning an error signals a delivery failure — the bus may retry or dead-letter.
type Handler func(ctx context.Context, payload any) error

// NewFromEnv creates a Bus based on environment configuration.
// If NATS_URL is set, connects to NATS (falls back to ChanBus on failure).
// Otherwise uses ChanBus with default buffer size 256.
func NewFromEnv() Bus {
	if natsURL, ok := env.Get("NATS_URL"); ok {
		bus, err := NewNATSBus(natsURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: NATS connect failed, using channel bus: %v\n", err)
			return NewChanBus(256)
		}
		return bus
	}
	return NewChanBus(256)
}
