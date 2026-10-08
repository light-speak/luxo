package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func deadlineRedisFixture(t *testing.T, address string, fromURL bool) *RedisCache {
	t.Helper()
	if !fromURL {
		c := mustRedis(t, address, WithTimeout(5*time.Second))
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	c, err := newRedisFromURL("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRedisFactoryEnablesContextDeadline(t *testing.T) {
	for _, fromURL := range []bool{false, true} {
		t.Run(fmt.Sprint(fromURL), func(t *testing.T) {
			c := deadlineRedisFixture(t, "localhost:1", fromURL)
			if !c.client.Options().ContextTimeoutEnabled {
				t.Fatal("cache-owned Redis client ignores command deadlines")
			}
		})
	}
}

func TestRedisGetBlockedReplyRespectsDeadline(t *testing.T) {
	for _, fromURL := range []bool{false, true} {
		t.Run(fmt.Sprint(fromURL), func(t *testing.T) {
			address, received := stalledRedisPeer(t)
			c := deadlineRedisFixture(t, address, fromURL)
			if err := c.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			result, done := make(chan error, 1), make(chan struct{})
			go func() {
				defer close(done)
				_, err := c.Get(ctx, "deadline")
				result <- err
			}()
			t.Cleanup(func() { _ = c.Close(); <-done })
			select {
			case <-received:
			case <-ctx.Done():
				t.Fatal("GET did not reach the stalled peer")
			}
			select {
			case err := <-result:
				var timeout net.Error
				if !errors.Is(err, context.DeadlineExceeded) && (!errors.As(err, &timeout) || !timeout.Timeout()) {
					t.Fatalf("blocked GET lost deadline error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("TTL cache GET outlived its request deadline")
			}
		})
	}
}

// Only the owned loopback peer stalls. No CLIENT PAUSE, shared keys, server-wide
// configuration or detached I/O goroutines are used to manufacture cancellation.
func stalledRedisPeer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop, done, received := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var workers sync.WaitGroup
	var receivedOnce sync.Once
	go func() {
		defer close(done)
		defer workers.Wait()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("owned Redis accept: %v", err)
				}
				return
			}
			workers.Go(func() {
				defer conn.Close()
				err := serveStalledRedis(conn, stop, func() { receivedOnce.Do(func() { close(received) }) })
				if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					t.Errorf("owned Redis peer: %v", err)
				}
			})
		}
	}()
	t.Cleanup(func() {
		close(stop)
		_ = listener.Close()
		<-done
	})
	return listener.Addr().String(), received
}

func serveStalledRedis(conn net.Conn, stop <-chan struct{}, received func()) error {
	reader := bufio.NewReader(conn)
	for {
		command, err := readRedisTestCommand(reader)
		if err != nil {
			return err
		}
		if command == "get" {
			received()
			<-stop
			return nil
		}
		reply := "-ERR unsupported test command\r\n"
		if command == "ping" {
			reply = "+PONG\r\n"
		} else if command == "command" {
			reply = "*0\r\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return err
		}
	}
}

// The fixture accepts only bounded arrays of bulk strings emitted by go-redis.
func readRedisTestCommand(reader *bufio.Reader) (string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	count, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(header, "*"), "\r\n"))
	if err != nil || count < 1 || count > 128 {
		return "", fmt.Errorf("invalid test command header %q", header)
	}
	var command string
	for i := range count {
		header, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		size, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(header, "$"), "\r\n"))
		if err != nil || size < 0 || size > 1<<16 {
			return "", fmt.Errorf("invalid test argument header %q", header)
		}
		value := make([]byte, size+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		if i == 0 {
			command = strings.ToLower(string(value[:size]))
		}
	}
	return command, nil
}
