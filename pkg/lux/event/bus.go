// Package event provides a typed event system for the Luxo runtime.
// Embedded mode uses Go channels, multi-service mode uses NATS.
package event

import "context"

// Bus is the interface for publishing and subscribing to events.
// ChanBus (channel) and NATSBus implement this interface.
type Bus interface {
	// Emit publishes an event with the given name and payload.
	// payload can be any type — ChanBus passes it directly (zero serialization),
	// NATSBus serializes to JSON for wire transport.
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

// Handler processes an event payload.
// For ChanBus, payload is the original struct (zero-copy).
// For NATSBus, payload is []byte (raw JSON from wire).
type Handler func(ctx context.Context, payload any)
