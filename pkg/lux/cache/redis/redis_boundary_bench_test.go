package redis

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

var redisBoundaryValueSink []byte

func BenchmarkRedisEmptyBoundary(b *testing.B) {
	for _, sample := range []struct {
		name, value string
		missing     bool
	}{
		{"empty", "", false},
		{"short", "payload", false},
		{"bytes4KiB", strings.Repeat("a", 4096), false},
		{"missing", "", true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			client := mustRedis(b, "unused:6379")
			b.Cleanup(func() { _ = client.Close() })
			client.client.AddHook(commandHook(func(_ context.Context, command redis.Cmder) error {
				value := command.(*redis.StringCmd)
				if sample.missing {
					value.SetErr(redis.Nil)
					return redis.Nil
				}
				value.SetVal(sample.value)
				return nil
			}))
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				var err error
				redisBoundaryValueSink, err = client.Get(ctx, "key")
				if err != nil || string(redisBoundaryValueSink) != sample.value {
					b.Fatal("Redis command result changed", err)
				}
			}
		})
	}
}
