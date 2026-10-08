// Package event provides a typed event system for the Luxo runtime.
// Applications select an in-process or independent network adapter at build time.
package event

import "context"

// Bus is the interface for publishing and subscribing to events.
// ChanBus (channel) and NATSBus implement this interface.
type Bus interface {
	// Emit publishes an event with the given name and payload. Consumer handlers
	// run with a bus-owned context because delivery can outlive the caller.
	// Generated payloads implement SnapshotSource for ChanBus and LuxoMarshaler
	// for NATSBus. Untyped in-memory values are rejected, never silently shared.
	// Low-level pre-encoded []byte is decoded by the registered listener.
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
// For ChanBus, payload is an independently owned, statically copied struct.
// For NATSBus, payload is []byte containing the published wire representation.
// Current backends log handler failures without retry or dead-letter delivery.
type Handler func(ctx context.Context, payload any) error
