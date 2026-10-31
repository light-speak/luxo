package redis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// commandHook exercises the real client's command API without a Redis process.
type commandHook func(context.Context, redis.Cmder) error

func (h commandHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("unexpected Redis network operation")
	}
}

func (h commandHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return redis.ProcessHook(h)
}

func (h commandHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error {
		for _, command := range commands {
			if err := h(ctx, command); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestRedisGetPreservesFailure(t *testing.T) {
	want := errors.New("backend unavailable")
	c := mustRedis(t, "unused:6379")
	t.Cleanup(func() { _ = c.Close() })
	c.client.AddHook(commandHook(func(_ context.Context, command redis.Cmder) error {
		command.SetErr(want)
		return want
	}))
	data, err := c.Get(context.Background(), "private-key")
	if data != nil || !errors.Is(err, want) {
		t.Fatalf("Get = (%q, %v), want (nil, backend error)", data, err)
	}
}

func TestRedisInvalidateLiteralPrefixAndBoundedBatches(t *testing.T) {
	c := mustRedis(t, "unused:6379", WithPrefix("team[1]*"))
	t.Cleanup(func() { _ = c.Close() })
	const prefix = "team[1]*:User?"
	scans, removed := 0, 0
	c.client.AddHook(commandHook(func(_ context.Context, command redis.Cmder) error {
		args := command.Args()
		switch cmd := command.(type) {
		case *redis.ScanCmd:
			scans++
			if args[3] != `team\[1\]\*:User\?*` {
				t.Errorf("pattern = %v; prefix must be literal", args[3])
			}
			if scans == 1 {
				keys := make([]string, 261)
				for i := range 260 {
					keys[i] = fmt.Sprintf("%s:%d", prefix, i)
				}
				keys[260] = "another-namespace:key"
				cmd.SetVal(keys, 23)
			} else {
				if args[1] != uint64(23) {
					t.Errorf("cursor = %v, want 23", args[1])
				}
				cmd.SetVal(nil, 0)
			}
		case *redis.IntCmd:
			if cmd.Name() != "unlink" || len(args) > 129 {
				t.Fatalf("expected bounded UNLINK, got %s/%d", cmd.Name(), len(args))
			}
			for _, arg := range args[1:] {
				if !strings.HasPrefix(arg.(string), prefix) {
					t.Fatalf("refusing foreign key %q", arg)
				}
				removed++
			}
			cmd.SetVal(int64(len(args) - 1))
		default:
			t.Fatalf("unexpected command %s; no whole-keyspace Lua", cmd.Name())
		}
		return nil
	}))
	if err := c.Invalidate(context.Background(), "User?"); err != nil {
		t.Fatal(err)
	}
	if scans != 2 || removed != 260 {
		t.Fatalf("scans/removed = %d/%d, want 2/260", scans, removed)
	}
}

func TestRedisInvalidateStopsOnFailure(t *testing.T) {
	for _, failing := range []string{"scan", "unlink", "cancel"} {
		t.Run(failing, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := errors.New("Redis failure")
			c := mustRedis(t, "unused:6379", WithPrefix("app"))
			t.Cleanup(func() { _ = c.Close() })
			c.client.AddHook(commandHook(func(_ context.Context, command redis.Cmder) error {
				if command.Name() == failing {
					command.SetErr(want)
					return want
				}
				if scan, ok := command.(*redis.ScanCmd); ok {
					scan.SetVal([]string{"app:User:1"}, 0)
					if failing == "cancel" {
						cancel()
					}
					return nil
				}
				t.Fatalf("unexpected command %s", command.Name())
				return nil
			}))
			err := c.Invalidate(ctx, "User:")
			if failing == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("Invalidate = %v, want %v", err, want)
			}
		})
	}
}

func TestRedisInvalidateAlreadyCanceled(t *testing.T) {
	c := mustRedis(t, "unused:6379", WithPrefix("app"))
	t.Cleanup(func() { _ = c.Close() })
	c.client.AddHook(commandHook(func(context.Context, redis.Cmder) error {
		t.Fatal("canceled invalidation must not send a command")
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Invalidate(ctx, "User:"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Invalidate = %v, want cancellation", err)
	}
}

func TestRedisInvalidateContinuesPastEmptyPage(t *testing.T) {
	c := mustRedis(t, "unused:6379", WithPrefix("app"))
	t.Cleanup(func() { _ = c.Close() })
	pages := 0
	c.client.AddHook(commandHook(func(_ context.Context, command redis.Cmder) error {
		scan, ok := command.(*redis.ScanCmd)
		if !ok {
			t.Fatalf("unexpected command %s", command.Name())
		}
		pages++
		if pages == 1 {
			scan.SetVal(nil, 9)
		} else {
			scan.SetVal(nil, 0)
		}
		return nil
	}))
	if err := c.Invalidate(context.Background(), "User:"); err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Fatalf("read %d pages, want 2", pages)
	}
}
