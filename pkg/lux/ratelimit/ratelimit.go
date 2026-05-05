// Package ratelimit provides a simple per-key rate limiter for @rateLimit directive.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens   int
	lastFill time.Time
}

// Limiter enforces per-key rate limits using token bucket.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	max     int
	window  time.Duration
}

// New creates a Limiter. max = tokens per window.
func New(max int, window time.Duration) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		max:     max,
		window:  window,
	}
	go l.cleanup()
	return l
}

// Allow checks if a request for the given key is allowed.
// Returns true if allowed, false if rate limited.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.max - 1, lastFill: now}
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastFill)
	refill := int(elapsed / l.window * time.Duration(l.max))
	if refill > 0 {
		b.tokens += refill
		if b.tokens > l.max {
			b.tokens = l.max
		}
		b.lastFill = now
	}

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// cleanup periodically removes stale buckets.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-10 * l.window)
		for k, b := range l.buckets {
			if b.lastFill.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}
