package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type poolLifecycleConn struct {
	net.Conn
	closes  atomic.Int32
	onClose func()
}

func TestClientExchangeDeadlineFailuresDoNotRecycleConnections(t *testing.T) {
	for _, sample := range []struct {
		name      string
		deadline  bool
		fail      int
		wantError bool
	}{
		{"setting deadline", true, 1, true},
		{"resetting deadline", true, 2, false},
		{"resetting without caller deadline", false, 1, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var frame bytes.Buffer
			if err := WriteFrame(&frame, []byte{statusOK, 42}); err != nil {
				t.Fatal(err)
			}
			closed := &poolLifecycleConn{}
			conn := &prefaceTestConn{Conn: closed, reader: &frame, writer: io.Discard, failDeadline: sample.fail}
			client := NewClient("unused")
			defer client.Close()
			client.pool.Put(conn)
			ctx := context.Background()
			if sample.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Minute)
				defer cancel()
			}
			response, err := client.exchange(ctx, []byte{1})
			if sample.wantError {
				if !errors.Is(err, net.ErrClosed) || response != nil {
					t.Fatalf("deadline error = %v, response=%x", err, response)
				}
			} else if err != nil || !bytes.Equal(response, []byte{statusOK, 42}) {
				t.Fatalf("completed response lost during deadline cleanup: %x, %v", response, err)
			}
			if closed.closes.Load() != 1 || len(client.pool.conns) != 0 {
				t.Fatal("connection with failed deadline handling was retained")
			}
		})
	}
}

func TestClientExchangeCancellationInterruptsBlockedIO(t *testing.T) {
	for _, reading := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			conn, peer := net.Pipe()
			defer peer.Close()
			defer conn.Close()
			client := NewClient("unused")
			defer client.Close()
			client.pool.Put(conn)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			var response []byte
			var callErr error
			go func() {
				defer close(done)
				response, callErr = client.exchange(ctx, []byte{42})
			}()
			if reading {
				if request, err := ReadFrame(peer, nil); err != nil || !bytes.Equal(request, []byte{42}) {
					t.Fatalf("request = %x, err=%v", request, err)
				}
			}
			synctest.Wait()
			cancel()
			<-done
			if !errors.Is(callErr, context.Canceled) || response != nil || len(client.pool.conns) != 0 {
				t.Fatalf("canceled exchange (reading=%v) = %x, err=%v, idle=%d", reading, response, callErr, len(client.pool.conns))
			}
		})
	}
}

type cancelOnResponseConn struct {
	net.Conn
	remaining int
	cancel    context.CancelFunc
}

func (c *cancelOnResponseConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	c.remaining -= n
	if c.remaining == 0 {
		c.cancel()
		synctest.Wait()
	}
	return n, err
}

func TestClientExchangeCompletedResponseRacingCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, peer := net.Pipe()
		defer peer.Close()
		defer conn.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := NewClient("unused")
		defer client.Close()
		client.pool.Put(&cancelOnResponseConn{Conn: conn, remaining: 6, cancel: cancel})
		done := make(chan error, 1)
		go func() {
			if _, err := ReadFrame(peer, nil); err != nil {
				done <- err
				return
			}
			done <- WriteFrame(peer, []byte{statusOK, 42})
		}()
		response, err := client.exchange(ctx, []byte{1})
		if err != nil || !bytes.Equal(response, []byte{statusOK, 42}) || len(client.pool.conns) != 0 {
			t.Fatalf("completed response or pool ownership changed: %x, %v, idle=%d", response, err, len(client.pool.conns))
		}
		if err := <-done; err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
		if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("canceled connection remained open: %v", err)
		}
	})
}

func TestPoolIdleBorrowReturnAllocations(t *testing.T) {
	pool := NewPool("unused", 1)
	conn := &poolLifecycleConn{}
	pool.Put(conn)
	t.Cleanup(pool.Close)
	allocs := testing.AllocsPerRun(1000, func() {
		borrowed, err := pool.Get()
		if err != nil || borrowed != conn {
			t.Fatalf("idle borrow = %v / %v", borrowed, err)
		}
		pool.Put(borrowed)
	})
	if allocs != 0 {
		t.Fatalf("idle borrow/return allocated %v times, want zero", allocs)
	}
}

func TestPoolClosedBorrowAllocations(t *testing.T) {
	pool := NewPool("unused", 1)
	pool.Close()
	ctx := context.Background()
	var lastErr error
	allocs := testing.AllocsPerRun(1000, func() {
		conn, err := pool.GetContext(ctx)
		if conn != nil || err == nil || err.Error() != "pool closed" {
			t.Fatalf("closed borrow = %v / %v", conn, err)
		}
		lastErr = err // Keep the caller-owned error observable beyond this call.
	})
	if lastErr == nil || allocs != 0 {
		t.Fatalf("closed borrow allocated %v times, want zero", allocs)
	}
}

func TestPoolClosePreservesBorrowedOwnership(t *testing.T) {
	pool := NewPool("unused", 1)
	conn := &poolLifecycleConn{}
	pool.Put(nil)
	pool.Put(conn)
	borrowed, err := pool.Get()
	if err != nil || borrowed != conn {
		t.Fatalf("borrow = %v / %v", borrowed, err)
	}
	pool.Close()
	if got := conn.closes.Load(); got != 0 {
		t.Fatalf("closed borrowed connection %d times", got)
	}
	if got, err := pool.Get(); got != nil || err == nil {
		t.Fatal("closed pool accepted a new borrow")
	}
	pool.Put(borrowed)
	pool.Close()
	if got := conn.closes.Load(); got != 1 || len(pool.conns) != 0 {
		t.Fatalf("late return = %d closes, %d idle", got, len(pool.conns))
	}
}

func TestPoolConcurrentDrainsDisposeOfLateReturns(t *testing.T) {
	pool := NewPool("unused", 16)
	pool.Close()
	var conns [16]poolLifecycleConn
	// Replay a Put that passed its first closed check before Close finished.
	// The still-open queue must accept it, and post-send draining owns disposal.
	for i := range conns {
		pool.conns <- &conns[i]
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(pool.closeIdle)
	}
	group.Wait()
	for i := range conns {
		if got := conns[i].closes.Load(); got != 1 {
			t.Errorf("late connection %d closed %d times", i, got)
		}
	}
}

func TestClientCloseAllowsInFlightResponse(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	router := api.NewRouter()
	router.Handle("wait", func(_ context.Context, req *api.Request) error {
		close(started)
		<-release
		req.Buf.B = append(req.Buf.B, 0)
		return nil
	})
	router.Registry.Register("wait", 1)
	router.Registry.RegisterParams("wait", nil)
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan struct{})
	var response []byte
	var callErr error
	t.Cleanup(func() { unblock(); cancel(); client.Close(); <-done })
	go func() {
		defer close(done)
		response, callErr = client.CallWithMaskContext(ctx, "", 1, nil, nil)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	client.Close()
	unblock()
	<-done
	if callErr != nil || !bytes.Equal(response, []byte{0}) || len(client.pool.conns) != 0 {
		t.Fatalf("in-flight response lost: %x / %v", response, callErr)
	}
	if _, err := client.Call(1, nil); err == nil {
		t.Fatal("closed client accepted another request")
	}
}

func (c *poolLifecycleConn) Close() error {
	c.closes.Add(1)
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

func TestPoolDrainHasBoundedWork(t *testing.T) {
	pool := NewPool("unused", 2)
	closed := 0
	var enqueue func()
	enqueue = func() {
		pool.conns <- &poolLifecycleConn{onClose: func() {
			closed++
			if closed < 8 {
				enqueue() // Model a late return replenishing the queue while draining.
			}
		}}
	}
	enqueue()
	pool.closeIdle()
	if closed != cap(pool.conns) {
		t.Errorf("one drain closed %d connections, want the capacity bound %d", closed, cap(pool.conns))
	}
	for len(pool.conns) > 0 {
		pool.closeIdle()
	}
	pool.Close()
}

func TestPoolConcurrentCloseAndReturn(t *testing.T) {
	for attempt := range 2000 {
		pool := NewPool("unused", 4)
		var conns [8]poolLifecycleConn
		var group sync.WaitGroup
		start := make(chan struct{})
		for i := range conns {
			group.Go(func() {
				// A recovered test panic is still a failure, never a production
				// fallback. Capture it so all workers can join and be inspected.
				defer func() {
					if value := recover(); value != nil {
						t.Errorf("attempt %d: Put panicked during Close: %v", attempt, value)
					}
				}()
				<-start
				pool.Put(&conns[i])
			})
		}
		for range 2 {
			group.Go(func() { <-start; pool.Close() })
		}
		close(start)
		group.Wait()
		for i := range conns {
			if got := conns[i].closes.Load(); got != 1 {
				t.Errorf("attempt %d: connection %d closed %d times, want once", attempt, i, got)
			}
		}
		if t.Failed() {
			return
		}
	}
}
