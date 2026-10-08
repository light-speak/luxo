// Package queue provides a pluggable task queue for the Luxo runtime.
// NATS JetStream is the production backend; an in-memory channel-based
// implementation is provided for embedded / dev mode.
package queue

import (
	"context"
	"time"
)

// Queue is the interface for publishing and consuming tasks.
// NATSQueue and MemoryQueue implement this interface.
type Queue interface {
	// Publish sends a task to the named queue.
	Publish(ctx context.Context, queue string, payload []byte) error

	// Subscribe registers a handler for tasks on the named queue.
	// Handler receives task payload; returning nil acknowledges the task,
	// returning an error triggers retry (up to MaxRetries).
	Subscribe(queue string, handler Handler) error

	// Close gracefully shuts down the queue, draining in-flight tasks
	// with a reasonable timeout.
	Close() error
}

// Handler processes a task payload.
type Handler func(ctx context.Context, payload []byte) error

// DeadLetterSuffix names the portable dead-letter destination of a task queue.
const DeadLetterSuffix = ".dlq"

// Config controls queue behavior.
type Config struct {
	MaxRetries  int           // maximum delivery attempts (default 3)
	RetryDelay  time.Duration // base retry delay, doubled each attempt (default 5s; NATS caps at 1m)
	Concurrency int           // concurrent workers per queue (default 4)
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxRetries:  3,
		RetryDelay:  5 * time.Second,
		Concurrency: 4,
	}
}

// SetDefaults fills zero-value fields with defaults.
func (c *Config) SetDefaults() {
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 5 * time.Second
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
}
