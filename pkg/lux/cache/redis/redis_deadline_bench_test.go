package redis

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Warm real-Redis I/O only: no connection establishment, failure, contention,
// business handler or PG authority work. Both sides send identical commands.
func BenchmarkRedisContextDeadline(b *testing.B) {
	base := skipNoRedis(b)
	key := base.prefix + ":deadline"
	if err := base.client.Set(b.Context(), key, "value", time.Hour).Err(); err != nil {
		b.Fatal(err)
	}
	order := []bool{false, true}
	if os.Getenv("LUXO_CACHE_DEADLINE_REVERSE") == "1" {
		slices.Reverse(order)
	}
	for _, deadline := range []bool{false, true} {
		ctx := b.Context()
		if deadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Hour)
			defer cancel()
		}
		for _, enabled := range order {
			options := *base.client.Options()
			options.ContextTimeoutEnabled = enabled
			if options.MaintNotificationsConfig != nil {
				config := *options.MaintNotificationsConfig
				options.MaintNotificationsConfig = &config
			}
			client := redis.NewClient(&options)
			b.Cleanup(func() { _ = client.Close() })
			if err := client.Ping(ctx).Err(); err != nil {
				b.Fatal(err)
			}
			for _, count := range []int{1, 8} {
				b.Run(fmt.Sprintf("deadline=%t/enabled=%t/commands=%d", deadline, enabled, count), func(b *testing.B) {
					benchmarkDeadlineReads(b, ctx, client, key, count)
				})
			}
		}
	}
}

func benchmarkDeadlineReads(b *testing.B, ctx context.Context, client *redis.Client, key string, count int) {
	if count == 1 {
		b.ReportAllocs()
		for b.Loop() {
			value, err := client.Get(ctx, key).Result()
			if err != nil || value != "value" {
				b.Fatal(value, err)
			}
		}
		return
	}
	commands := make([]*redis.StringCmd, count)
	pipe := client.Pipeline()
	defer pipe.Discard()
	b.ReportAllocs()
	for b.Loop() {
		for i := range commands {
			commands[i] = pipe.Get(ctx, key)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			b.Fatal(err)
		}
		for _, command := range commands {
			if value, err := command.Result(); err != nil || value != "value" {
				b.Fatal(value, err)
			}
		}
	}
}
