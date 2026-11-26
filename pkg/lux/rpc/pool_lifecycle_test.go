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

// Test the raw I/O lifecycle independently of response decoding.
func (c *Client) exchange(ctx context.Context, payload []byte) ([]byte, error) {
	reply, err := c.exchangeRequest(ctx, payload, nil)
	c.releaseReply(reply.conn, nil)
	return reply.frame, err
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
			response, err := client.exchange(ctx, []byte{0, 0, 0, 0, 1})
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
				response, callErr = client.exchange(ctx, []byte{0, 0, 0, 0, 42})
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

func TestCanceledUnaryCallReturnsAfterReleasingPoolSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		options := PoolOptions{MaxIdle: 1, MaxOpen: 1, DialTimeout: time.Second}
		client, err := NewNamedClientWithOptions("test", "unused", options)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		conn, peer := net.Pipe()
		defer conn.Close()
		defer peer.Close()
		closing, release := make(chan struct{}), make(chan struct{})
		allowClose := sync.OnceFunc(func() { close(release) })
		defer allowClose()
		tracked := &poolLifecycleConn{Conn: conn, onClose: func() {
			conn.Close()
			close(closing)
			<-release
		}}
		client.pool.Put(tracked)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, callErr := client.CallWithMaskContext(ctx, "", 1, nil, []byte{42})
			done <- callErr
		}()
		if _, err := ReadFrame(peer, nil); err != nil {
			t.Fatal(err)
		}
		cancel()
		<-closing
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("canceled call returned before its pool slot was released: %v", err)
		default:
		}
		allowClose()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Call error = %v", err)
		}
		if open := client.pool.open.Load(); open != 0 {
			t.Errorf("pool has %d open connections after canceled Call returned", open)
		}
		if n := tracked.closes.Load(); n != 1 {
			t.Errorf("underlying Close called %d times", n)
		}
	})
}

func TestPoolCloseWaitersTrackEachConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := NewPool("unused", 2)
		defer pool.Close()
		makeHeld := func() (net.Conn, func()) {
			closing := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			pool.Put(&poolLifecycleConn{onClose: func() {
				close(closing)
				<-release
			}})
			conn, err := pool.Get()
			if err != nil {
				t.Fatal(err)
			}
			go conn.Close()
			<-closing
			return conn, unblock
		}
		first, releaseFirst := makeHeld()
		defer releaseFirst()
		second, releaseSecond := makeHeld()
		defer releaseSecond()
		firstDone, secondDone := make(chan error, 2), make(chan error, 1)
		go func() { firstDone <- first.Close() }()
		go func() { firstDone <- first.Close() }()
		go func() { secondDone <- second.Close() }()
		synctest.Wait()
		if open := pool.open.Load(); open != 2 || len(firstDone) != 0 || len(secondDone) != 0 {
			t.Fatalf("close returned early: open=%d first=%d second=%d", open, len(firstDone), len(secondDone))
		}
		releaseFirst()
		synctest.Wait()
		if open := pool.open.Load(); open != 1 || len(firstDone) != 2 || len(secondDone) != 0 {
			t.Fatalf("wrong waiters released: open=%d first=%d second=%d", open, len(firstDone), len(secondDone))
		}
		for range 2 {
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
		}
		releaseSecond()
		if err := <-secondDone; err != nil || pool.open.Load() != 0 {
			t.Fatalf("second close did not release its slot: %v", err)
		}
		if len(pool.closingWaiters) != 0 {
			t.Fatal("pool retained waiters for closed connections")
		}
	})
}

func TestFullPoolRejectsRawConnWithoutReservingSlot(t *testing.T) {
	pool := NewPool("unused", 1)
	defer pool.Close()
	idle := &poolLifecycleConn{}
	pool.Put(idle)
	rejected := &poolLifecycleConn{onClose: func() {
		if open := pool.open.Load(); open != 1 {
			t.Errorf("rejected connection changed open reservation count to %d", open)
		}
	}}
	pool.Put(rejected)
	if rejected.closes.Load() != 1 || idle.closes.Load() != 0 || len(pool.conns) != 1 {
		t.Fatal("full pool did not close only the rejected connection")
	}
}

func TestPoolCloseFinishesBeforeWaiterRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := NewPool("unused", 1)
		defer pool.Close()
		raw := &poolLifecycleConn{}
		pool.Put(raw)
		conn, err := pool.Get()
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		// Replay a duplicate caller that saw closing before the owner finished,
		// and reached waiter registration after the socket and slot were released.
		pool.waitForClose(conn.(*poolConn))
		if raw.closes.Load() != 1 || pool.open.Load() != 0 || pool.closingWaiters != nil {
			t.Fatal("late close waiter acquired a slot or left a waiter allocation")
		}
	})
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
		response, err := client.exchange(ctx, []byte{0, 0, 0, 0, 1})
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

func TestClientExchangePreservesPreparedRequestBody(t *testing.T) {
	client, _ := traceLocalClient(t, false)
	body := []byte{21, 42, 84}
	frame := append(make([]byte, 4), body...)
	for range 2 {
		if _, err := client.exchange(context.Background(), frame); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(frame[4:], body) {
			t.Fatalf("prepared request body changed: %x", frame[4:])
		}
	}
}

func TestPoolIdleBorrowReturnAllocations(t *testing.T) {
	pool := NewPool("unused", 1)
	conn := &poolLifecycleConn{}
	pool.Put(conn)
	t.Cleanup(pool.Close)
	allocs := testing.AllocsPerRun(1000, func() {
		borrowed, err := pool.Get()
		if err != nil || borrowed.(*poolConn).Conn != conn {
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
	if err != nil || borrowed.(*poolConn).Conn != conn {
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
		group.Go(pool.drainAfterReturn)
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
