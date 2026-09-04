// Package ratelimit provides a simple per-key rate limiter for @rateLimit directive.
package ratelimit

import (
	"sync"
	"time"
)

const shardCount = 32

type bucket struct {
	tokens   int
	lastFill time.Time
	lastSeen time.Time
}

type limiterShard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	checks  uint32
}

// Limiter enforces per-key rate limits using token bucket.
type Limiter struct {
	shards [shardCount]limiterShard
	max    int
	window time.Duration
	now    func() time.Time
}

// New creates a Limiter. max = tokens per window.
func New(max int, window time.Duration) *Limiter {
	l := &Limiter{max: max, window: window, now: time.Now}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	return l
}

// Allow checks if a request for the given key is allowed.
// Returns true if allowed, false if rate limited.
func (l *Limiter) Allow(key string) bool {
	if l.max <= 0 || l.window <= 0 {
		return false
	}
	shard := &l.shards[shardIndex(key)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	now := l.now()
	shard.checks++
	if shard.checks%256 == 0 {
		l.cleanupShard(shard, now)
	}
	b, ok := shard.buckets[key]
	if !ok {
		shard.buckets[key] = &bucket{tokens: l.max - 1, lastFill: now, lastSeen: now}
		return true
	}
	b.lastSeen = now

	interval := l.window / time.Duration(l.max)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	refill := int(now.Sub(b.lastFill) / interval)
	if refill > 0 {
		b.tokens += refill
		if b.tokens > l.max {
			b.tokens = l.max
		}
		b.lastFill = b.lastFill.Add(time.Duration(refill) * interval)
	}

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func shardIndex(key string) uint32 {
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for i := 0; i < len(key); i++ {
		hash ^= uint32(key[i])
		hash *= prime32
	}
	return hash % shardCount
}

func (l *Limiter) cleanupShard(shard *limiterShard, now time.Time) {
	cutoff := now.Add(-10 * l.window)
	for key, current := range shard.buckets {
		if current.lastSeen.Before(cutoff) {
			delete(shard.buckets, key)
		}
	}
}
