// Package cache provides a simple in-memory TTL cache for @cache directive.
package cache

import (
	"sync"
	"time"
)

type entry struct {
	data      []byte
	expiresAt time.Time
}

// Cache is a concurrency-safe in-memory cache with TTL.
type Cache struct {
	mu    sync.RWMutex
	items map[string]entry
}

// New creates a new Cache.
func New() *Cache {
	c := &Cache{items: make(map[string]entry)}
	go c.cleanup()
	return c
}

// Get returns cached data if not expired. Returns nil if miss.
func (c *Cache) Get(key string) []byte {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return nil
	}
	return e.data
}

// Set stores data with a TTL.
func (c *Cache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	c.items[key] = entry{data: data, expiresAt: time.Now().Add(ttl)}
	c.mu.Unlock()
}

// Invalidate removes all cache entries whose key starts with prefix.
// Used by write operations (create/update/delete) to clear stale read caches.
func (c *Cache) Invalidate(prefix string) {
	c.mu.Lock()
	for k := range c.items {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}

// InvalidateAll clears all cached entries.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	c.items = make(map[string]entry)
	c.mu.Unlock()
}

// cleanup periodically removes expired entries.
func (c *Cache) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for k, e := range c.items {
			if now.After(e.expiresAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}
