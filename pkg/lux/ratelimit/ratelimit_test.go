package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter_Allow(t *testing.T) {
	l := New(3, time.Second)
	// First 3 should pass
	for i := 0; i < 3; i++ {
		if !l.Allow("key") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
	// 4th should be blocked
	if l.Allow("key") {
		t.Error("4th request should be rate limited")
	}
}

func TestLimiter_DifferentKeys(t *testing.T) {
	l := New(1, time.Second)
	if !l.Allow("a") {
		t.Error("first key should pass")
	}
	if !l.Allow("b") {
		t.Error("different key should pass")
	}
	if l.Allow("a") {
		t.Error("same key should be limited")
	}
}

func TestLimiter_Refill(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := New(2, time.Second)
	l.now = func() time.Time { return now }
	l.Allow("key") // use one token
	l.Allow("key") // use the second token
	if l.Allow("key") {
		t.Error("should be limited")
	}
	now = now.Add(500 * time.Millisecond)
	if !l.Allow("key") {
		t.Error("should be allowed after refill")
	}
	if l.Allow("key") {
		t.Error("fractional refill should add exactly one token")
	}

	now = now.Add(10 * time.Second)
	if !l.Allow("key") || !l.Allow("key") {
		t.Error("refill must cap the bucket at max tokens")
	}
}

func TestLimiter_InvalidConfigurationDeniesRequests(t *testing.T) {
	for _, limiter := range []*Limiter{New(0, time.Second), New(1, 0)} {
		if limiter.Allow("key") {
			t.Error("invalid limiter configuration must deny requests")
		}
	}
}

func TestLimiter_UsesMinimumRefillInterval(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := New(2, time.Nanosecond)
	l.now = func() time.Time { return now }
	l.Allow("key")
	l.Allow("key")
	if l.Allow("key") {
		t.Fatal("bucket should be empty")
	}
	now = now.Add(time.Nanosecond)
	if !l.Allow("key") {
		t.Error("minimum refill interval should restore a token")
	}
}

func TestLimiter_CleansUpInactiveBuckets(t *testing.T) {
	now := time.Unix(1_000, 0)
	l := New(1, time.Second)
	l.now = func() time.Time { return now }
	shard := &l.shards[shardIndex("fresh")]
	shard.buckets["stale"] = &bucket{lastSeen: now.Add(-11 * time.Second)}
	shard.buckets["boundary"] = &bucket{lastSeen: now.Add(-10 * time.Second)}
	shard.checks = 255

	if !l.Allow("fresh") {
		t.Fatal("fresh key should be allowed")
	}
	if _, exists := shard.buckets["stale"]; exists {
		t.Error("inactive bucket should be removed")
	}
	if _, exists := shard.buckets["boundary"]; !exists {
		t.Error("bucket exactly at the cutoff should be retained")
	}
}
