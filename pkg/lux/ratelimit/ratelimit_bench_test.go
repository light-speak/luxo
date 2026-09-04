package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkLimiterAllowSameKey(b *testing.B) {
	limiter := New(b.N+1, time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow("client-1")
	}
}

func BenchmarkLimiterAllowShardedKeys(b *testing.B) {
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = "client-" + strconv.Itoa(i)
	}
	limiter := New(b.N+1, time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			limiter.Allow(keys[i&(len(keys)-1)])
			i++
		}
	})
}
