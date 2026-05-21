// Package cache provides a pluggable caching layer for @cache directive.
// Default implementation is in-memory with TTL. Redis backend available.
package cache

import (
	"context"
	"sync"
	"time"
)

// Cache is the pluggable cache interface used by @cache directive.
// Implementations must be safe for concurrent use.
// All methods are best-effort: cache failures should not break the application.
type Cache interface {
	// Get returns cached data for the key. Returns nil, nil on cache miss.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores data with a TTL. Zero TTL means no expiration.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes specific keys.
	Del(ctx context.Context, keys ...string) error
	// Invalidate removes all keys matching a prefix (e.g., "luxo:api:getUser:").
	Invalidate(ctx context.Context, prefix string) error
	// Close releases resources.
	Close() error
}

// entry stores cached data with expiration time.
type entry struct {
	data      []byte
	expiresAt time.Time
}

// MemoryCache is a concurrency-safe in-memory cache with TTL.
// Suitable for single-instance deployments or development.
type MemoryCache struct {
	mu      sync.RWMutex
	items   map[string]entry
	closeCh chan struct{}
}

// NewMemory creates a new in-memory cache with background cleanup.
func NewMemory() *MemoryCache {
	c := &MemoryCache{
		items:   make(map[string]entry),
		closeCh: make(chan struct{}),
	}
	go c.cleanup()
	return c
}

// Get returns cached data if not expired. Returns nil, nil on miss.
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || (!e.expiresAt.IsZero() && time.Now().After(e.expiresAt)) {
		return nil, nil
	}
	// Return a copy to prevent caller mutation.
	cp := make([]byte, len(e.data))
	copy(cp, e.data)
	return cp, nil
}

// Set stores data with a TTL.
func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	cp := make([]byte, len(value))
	copy(cp, value)
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = entry{data: cp, expiresAt: expiresAt}
	c.mu.Unlock()
	return nil
}

// Del removes specific keys.
func (c *MemoryCache) Del(_ context.Context, keys ...string) error {
	c.mu.Lock()
	for _, k := range keys {
		delete(c.items, k)
	}
	c.mu.Unlock()
	return nil
}

// Invalidate removes all cache entries whose key starts with prefix.
func (c *MemoryCache) Invalidate(_ context.Context, prefix string) error {
	c.mu.Lock()
	for k := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
	return nil
}

// Close stops the background cleanup goroutine.
func (c *MemoryCache) Close() error {
	select {
	case <-c.closeCh:
		// Already closed.
	default:
		close(c.closeCh)
	}
	return nil
}

// Len returns the number of entries (for testing).
func (c *MemoryCache) Len() int {
	c.mu.RLock()
	n := len(c.items)
	c.mu.RUnlock()
	return n
}

// cleanup periodically removes expired entries.
func (c *MemoryCache) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeCh:
			return
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, e := range c.items {
				if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Nop is a no-op cache that never stores anything. Useful for disabling cache.
type Nop struct{}

func (Nop) Get(context.Context, string) ([]byte, error)              { return nil, nil }
func (Nop) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (Nop) Del(context.Context, ...string) error                     { return nil }
func (Nop) Invalidate(context.Context, string) error                 { return nil }
func (Nop) Close() error                                             { return nil }
