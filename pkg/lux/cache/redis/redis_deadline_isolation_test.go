package redis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// This is a synchronous RESP fixture, not a Redis backend. It removes socket
// scheduling, syscalls and server work while retaining the actual driver path.
type deadlineMemoryConn struct {
	reply                       [512]byte
	read, size                  int
	readDeadline, writeDeadline time.Time
	reads, writes               int
}

func (c *deadlineMemoryConn) Read(p []byte) (int, error) {
	if c.read == c.size {
		return 0, io.EOF
	}
	n := copy(p, c.reply[c.read:c.size])
	c.read += n
	c.reads++
	return n, nil
}

func (c *deadlineMemoryConn) Write(p []byte) (int, error) {
	if c.read != c.size {
		return 0, errors.New("unconsumed fixture reply")
	}
	c.read, c.size = 0, 0
	remaining := p
	for len(remaining) > 0 {
		command, reply := deadlineFixtureReply(remaining)
		if command == "" || len(reply) > len(c.reply)-c.size {
			return 0, errors.New("unexpected fixture command or oversized pipeline")
		}
		c.size += copy(c.reply[c.size:], reply)
		remaining = remaining[len(command):]
	}
	c.writes++
	return len(p), nil
}

func deadlineFixtureReply(command []byte) (string, string) {
	for _, pair := range [...]struct{ command, reply string }{
		{"*2\r\n$5\r\nhello\r\n$1\r\n2\r\n", "-ERR unsupported fixture command\r\n"},
		{"*2\r\n$5\r\nhello\r\n$1\r\n3\r\n", "%0\r\n"},
		{"*1\r\n$4\r\nping\r\n", "+PONG\r\n"},
		{"*2\r\n$3\r\nget\r\n$8\r\ndeadline\r\n", "$5\r\nvalue\r\n"},
	} {
		if bytes.HasPrefix(command, []byte(pair.command)) {
			return pair.command, pair.reply
		}
	}
	return "", ""
}

func (c *deadlineMemoryConn) Close() error         { return nil }
func (c *deadlineMemoryConn) LocalAddr() net.Addr  { return &net.TCPAddr{} }
func (c *deadlineMemoryConn) RemoteAddr() net.Addr { return &net.TCPAddr{} }
func (c *deadlineMemoryConn) SetDeadline(d time.Time) error {
	c.readDeadline, c.writeDeadline = d, d
	return nil
}
func (c *deadlineMemoryConn) SetReadDeadline(d time.Time) error  { c.readDeadline = d; return nil }
func (c *deadlineMemoryConn) SetWriteDeadline(d time.Time) error { c.writeDeadline = d; return nil }

func deadlineMemoryClient(t testing.TB, enabled bool, protocol int) (*redis.Client, *deadlineMemoryConn) {
	t.Helper()
	conn := &deadlineMemoryConn{}
	client := redis.NewClient(&redis.Options{
		Addr: "memory-fixture:1", Protocol: protocol, DisableIdentity: true,
		ContextTimeoutEnabled: enabled, PoolSize: 1, MaxRetries: -1,
		ReadTimeout: 2 * time.Hour, WriteTimeout: 2 * time.Hour,
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
		Dialer:                   func(context.Context, string, string) (net.Conn, error) { return conn, nil },
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	return client, conn
}

func TestRedisDeadlineIsolationUsesActualDriver(t *testing.T) {
	for _, protocol := range []int{2, 3} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("RESP%d/%t", protocol, enabled), func(t *testing.T) {
				verifyDeadlineMemoryClient(t, enabled, protocol)
			})
		}
	}
}

func verifyDeadlineMemoryClient(t *testing.T, enabled bool, protocol int) {
	client, conn := deadlineMemoryClient(t, enabled, protocol)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	for _, count := range []int{1, 8} {
		reads, writes := conn.reads, conn.writes
		pipe := client.Pipeline()
		for range count {
			pipe.Get(ctx, "deadline")
		}
		commands, err := pipe.Exec(ctx)
		if err != nil || len(commands) != count {
			t.Fatalf("pipeline: count=%d error=%v", len(commands), err)
		}
		for _, command := range commands {
			value, err := command.(*redis.StringCmd).Result()
			if err != nil || value != "value" {
				t.Fatal(value, err)
			}
		}
		if conn.reads != reads+1 || conn.writes != writes+1 {
			t.Fatal("driver skipped or repeated fixture I/O")
		}
		if enabled && (!conn.readDeadline.Equal(deadline) || !conn.writeDeadline.Equal(deadline)) {
			t.Fatal("driver failed to propagate request deadline")
		}
		if !enabled && (!conn.readDeadline.After(deadline) || !conn.writeDeadline.After(deadline)) {
			t.Fatal("control unexpectedly honored request deadline")
		}
	}
}

func BenchmarkRedisContextDeadlineIsolated(b *testing.B) {
	for _, protocol := range []int{2, 3} {
		b.Run(fmt.Sprintf("RESP%d", protocol), func(b *testing.B) { benchmarkIsolatedDeadline(b, protocol) })
	}
}

func benchmarkIsolatedDeadline(b *testing.B, protocol int) {
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
			client, _ := deadlineMemoryClient(b, enabled, protocol)
			for _, count := range []int{1, 8} {
				b.Run(fmt.Sprintf("deadline=%t/enabled=%t/commands=%d", deadline, enabled, count), func(b *testing.B) {
					benchmarkDeadlineReads(b, ctx, client, "deadline", count)
				})
			}
		}
	}
}
