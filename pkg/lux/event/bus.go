// Package event provides a typed event system for the Luxo runtime.
// Embedded mode uses Go channels, multi-service mode uses NATS.
package event

import "context"

// Bus is the interface for publishing and subscribing to events.
// ChanBus (channel) and NATSBus implement this interface.
type Bus interface {
	// Emit publishes an event with the given name and payload.
	Emit(ctx context.Context, name string, payload []byte) error

	// On registers a handler for an event name.
	// The handler runs in a separate goroutine.
	On(name string, handler Handler) error

	// Close shuts down the bus and all subscriptions.
	Close()
}

// Handler processes an event payload.
type Handler func(ctx context.Context, payload []byte)
