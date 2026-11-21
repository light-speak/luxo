package ratelimit

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
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

func TestLimiterRefillDoesNotOverflow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(math.MaxInt, time.Nanosecond)
	l.now = func() time.Time { return now }
	if !l.Allow("key") {
		t.Fatal("initial request denied")
	}
	now = now.Add(2 * time.Nanosecond)
	if !l.Allow("key") {
		t.Fatal("refill overflow denied a request with available capacity")
	}
}

func TestLimiterLongWindowCleanupDoesNotResetQuota(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(1, time.Duration(math.MaxInt64))
	l.now = func() time.Time { return now }
	if !l.Allow("key") {
		t.Fatal("initial request denied")
	}
	for i := 2; i <= 512; i++ {
		if l.Allow("key") {
			t.Fatalf("request %d bypassed quota without elapsed time", i)
		}
	}
}

func TestLimiterConcurrentQuotaAndRefill(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"shared", []string{"key"}},
		{"same_shard", limiterTestKeys(true)},
		{"separate_shards", limiterTestKeys(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			limiter := New(17, time.Second)
			limiter.now = func() time.Time { return now }
			checkLimiterWave(t, limiter, tc.keys, 17)
			checkLimiterWave(t, limiter, tc.keys, 0)
			now = now.Add(time.Second)
			checkLimiterWave(t, limiter, tc.keys, 17)
		})
	}
}

func limiterTestKeys(sameShard bool) []string {
	keys := make([]string, 0, 8)
	seen := make(map[uint32]bool)
	for i := 0; len(keys) < 8; i++ {
		key := "key-" + strconv.Itoa(i)
		index := shardIndex(key)
		if sameShard && index != 0 || !sameShard && seen[index] {
			continue
		}
		seen[index] = true
		keys = append(keys, key)
	}
	return keys
}

func checkLimiterWave(t *testing.T, l *Limiter, keys []string, want int32) {
	t.Helper()
	accepted := make([]atomic.Int32, len(keys))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			for range 32 {
				for i, key := range keys {
					if l.Allow(key) {
						accepted[i].Add(1)
					}
				}
			}
		})
	}
	close(start)
	workers.Wait()
	for i, key := range keys {
		if got := accepted[i].Load(); got != want {
			t.Errorf("%s accepted %d requests, want %d", key, got, want)
		}
	}
}
