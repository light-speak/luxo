package rpc

import (
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type poolLifecycleConn struct {
	net.Conn
	closes  atomic.Int32
	onClose func()
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
