// Package cache provides a pluggable caching layer for @cache directive.
// Memory and Redis backends must be selected explicitly by the application.
package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errMemoryClosed = errors.New("cache: memory backend is closed")
var errNegativeTTL = errors.New("cache: TTL must not be negative")

// ValidateTTL enforces the shared backend contract before storing a value.
// Zero means no expiration; negative values must leave existing entries intact.
func ValidateTTL(ttl time.Duration) error {
	if ttl < 0 {
		return errNegativeTTL
	}
	return nil
}

// Cache is the pluggable cache interface used by @cache directive.
// Implementations must be safe for concurrent use.
// Backends return failures instead of disguising them as misses. The caller
// owns timeout, observability and failure policy; storage does not log secrets.
type Cache interface {
	// Get returns caller-owned data for the key. Returns nil, nil on cache miss.
	// Mutating a successful result must not change the stored value.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores data with a TTL. It must consume or copy value before returning;
	// the caller may immediately reuse its backing array. Zero TTL means no expiration.
	// Negative TTLs return an error without creating or replacing an entry.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Del removes specific keys.
	Del(ctx context.Context, keys ...string) error
	// Invalidate removes keys under a literal prefix (e.g., "luxo:api:getUser:").
	// It is a maintenance operation, not a barrier against concurrent fills.
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

// isClosedLocked requires mu to be held for reading or writing.
func (c *MemoryCache) isClosedLocked() bool {
	return c.items == nil
}

// Get returns cached data if not expired. Returns nil, nil on miss.
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	if c.isClosedLocked() {
		c.mu.RUnlock()
		return nil, errMemoryClosed
	}
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || (!e.expiresAt.IsZero() && !time.Now().Before(e.expiresAt)) {
		return nil, nil
	}
	// Return a copy to prevent caller mutation.
	cp := make([]byte, len(e.data))
	copy(cp, e.data)
	return cp, nil
}

// Set stores data with a TTL.
func (c *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ValidateTTL(ttl); err != nil {
		return err
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	c.mu.Lock()
	if c.isClosedLocked() {
		c.mu.Unlock()
		return errMemoryClosed
	}
	c.items[key] = entry{data: cp, expiresAt: expiresAt}
	c.mu.Unlock()
	return nil
}

// Del removes specific keys.
func (c *MemoryCache) Del(_ context.Context, keys ...string) error {
	c.mu.Lock()
	if c.isClosedLocked() {
		c.mu.Unlock()
		return errMemoryClosed
	}
	for _, k := range keys {
		delete(c.items, k)
	}
	c.mu.Unlock()
	return nil
}

// Invalidate removes all cache entries whose key starts with prefix.
func (c *MemoryCache) Invalidate(_ context.Context, prefix string) error {
	c.mu.Lock()
	if c.isClosedLocked() {
		c.mu.Unlock()
		return errMemoryClosed
	}
	for k := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
	return nil
}

// Close stops cleanup and releases all retained entries. Later operations fail;
// a read that already acquired a snapshot may still complete with its own copy.
func (c *MemoryCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closeCh:
		// Already closed.
	default:
		close(c.closeCh)
		c.items = nil
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
				if !e.expiresAt.IsZero() && !now.Before(e.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Nop is a no-op cache that never stores anything. Useful for disabling cache.
type Nop struct{}

func (Nop) Get(context.Context, string) ([]byte, error) { return nil, nil }
func (Nop) Set(_ context.Context, _ string, _ []byte, ttl time.Duration) error {
	return ValidateTTL(ttl)
}
func (Nop) Del(context.Context, ...string) error     { return nil }
func (Nop) Invalidate(context.Context, string) error { return nil }
func (Nop) Close() error                             { return nil }
