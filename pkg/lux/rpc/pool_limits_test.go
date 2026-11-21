package rpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func boundedTestPool(t *testing.T) *Pool {
	t.Helper()
	p, err := NewPoolWithOptions("unused", PoolOptions{MaxIdle: 1, MaxOpen: 1, MaxWaiters: 1, WaitTimeout: time.Second, DialTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	p.Put(&poolLifecycleConn{})
	return p
}

func TestPoolBoundedWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := boundedTestPool(t)
		conn, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			c, err := p.Get()
			if c != nil {
				p.Put(c)
			}
			done <- err
		}()
		synctest.Wait()
		if _, err := p.Get(); !errors.Is(err, ErrPoolExhausted) {
			t.Fatalf("queue overflow: %v", err)
		}
		p.Put(conn)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if p.open.Load() != 1 || p.waiters.Load() != 0 {
			t.Fatal("capacity leaked")
		}
	})
}

func TestPoolWaitTermination(t *testing.T) {
	for _, reason := range []string{"timeout", "cancel", "close"} {
		t.Run(reason, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := boundedTestPool(t)
				conn, _ := p.Get()
				defer conn.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := p.GetContext(ctx); done <- err }()
				synctest.Wait()
				want := ErrPoolWaitTimeout
				switch reason {
				case "cancel":
					cancel()
					want = context.Canceled
				case "close":
					p.Close()
					want = errPoolClosed
				case "timeout":
					time.Sleep(time.Second)
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
				if p.waiters.Load() != 0 {
					t.Fatal("waiter leaked")
				}
			})
		})
	}
}

func TestPoolClosedConnectionReleasesCapacityOnce(t *testing.T) {
	p := boundedTestPool(t)
	c, _ := p.Get()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c.Close()
	p.Put(c)
	if p.open.Load() != 0 || len(p.conns) != 0 {
		t.Fatal("closed connection retained")
	}
	p.Put(&poolLifecycleConn{})
	if p.open.Load() != 1 {
		t.Fatal("capacity not reusable")
	}
	c, _ = p.Get()
	p.Put(c)
	p.Put(c)
	if p.open.Load() != 1 || len(p.conns) != 1 {
		t.Fatal("duplicate return changed capacity")
	}
}

func TestPoolRejectsCanceledIdleBorrow(t *testing.T) {
	p := boundedTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(p.conns) != 1 {
		t.Fatal("canceled borrow consumed idle connection")
	}
}

func TestPoolDialingConsumesCapacityAndCloseCancelsHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	options := DefaultPoolOptions()
	options.MaxIdle, options.MaxOpen, options.MaxWaiters = 1, 1, 0
	p, err := NewPoolWithOptions(listener.Addr().String(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan error, 1)
	go func() { _, err := p.Get(); done <- err }()
	listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err := p.Get(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("dial exceeded limit: %v", err)
	}
	p.Close()
	select {
	case err := <-done:
		if !errors.Is(err, errPoolClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool close did not interrupt handshake")
	}
	if p.open.Load() != 0 {
		t.Fatal("dial reservation leaked")
	}
}

func TestPoolConcurrentBorrowLimit(t *testing.T) {
	p := boundedTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			for range 100 {
				conn, err := p.GetContext(ctx)
				if errors.Is(err, ErrPoolExhausted) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				if p.open.Load() > 1 {
					t.Error("open limit exceeded")
				}
				p.Put(conn)
			}
		})
	}
	group.Wait()
	if p.open.Load() != 1 || p.waiters.Load() != 0 {
		t.Fatal("concurrent capacity leaked")
	}
}

func TestPoolOptionsAndAdoption(t *testing.T) {
	for _, field := range []string{"open", "idle", "idle-over-open", "waiters", "wait-timeout", "dial-timeout"} {
		options := DefaultPoolOptions()
		switch field {
		case "open":
			options.MaxOpen = 0
		case "idle":
			options.MaxIdle = -1
		case "idle-over-open":
			options.MaxIdle = options.MaxOpen + 1
		case "waiters":
			options.MaxWaiters = -1
		case "wait-timeout":
			options.WaitTimeout = 0
		case "dial-timeout":
			options.DialTimeout = 0
		}
		if _, err := NewNamedClientWithOptions("test", "unused", options); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
	p := boundedTestPool(t)
	extra := &poolLifecycleConn{}
	p.Put(extra)
	if extra.closes.Load() != 1 || p.open.Load() != 1 {
		t.Fatal("adoption exceeded open limit")
	}
	other := boundedTestPool(t)
	c, _ := other.Get()
	p.Put(c)
	if other.open.Load() != 0 || p.open.Load() != 1 {
		t.Fatal("foreign ownership leaked")
	}
}

func TestPoolFailedDialReleasesReservation(t *testing.T) {
	opts := DefaultPoolOptions()
	opts.MaxOpen, opts.MaxIdle = 1, 0
	p, err := NewPoolWithOptions("invalid address", opts)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for range 2 {
		if _, err := p.Get(); err == nil {
			t.Fatal("dial unexpectedly succeeded")
		}
		if p.open.Load() != 0 {
			t.Fatal("failed dial leaked capacity")
		}
	}
	p.Put(&poolLifecycleConn{})
	if p.open.Load() != 0 || len(p.conns) != 0 {
		t.Fatal("zero-idle pool retained connection")
	}
}

func TestPoolStreamOccupiesSlotUntilTermination(t *testing.T) {
	router, _ := newStreamIsolationRouter(t)
	opts := DefaultPoolOptions()
	opts.MaxIdle, opts.MaxOpen, opts.MaxWaiters = 1, 1, 0
	client, err := NewNamedClientWithOptions("test", startTestRPCServer(t, NewServer(router)), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub := openIsolationStream(t, ctx, client)
	if _, err := client.CallWithMaskContext(ctx, "", 2, nil, nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("stream did not reserve capacity: %v", err)
	} else {
		var app *luxerrors.AppError
		if !errors.As(err, &app) || app.Code != 503 {
			t.Fatalf("lost overload status: %v", err)
		}
	}
	sub.Close()
	// The reader may be concurrently closing the same socket. Wait for its
	// terminal notification before asserting that physical Close has finished.
	for range sub.Messages() {
	}
	if _, err := client.CallWithMaskContext(ctx, "", 2, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPoolWaiterWakeupChain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := DefaultPoolOptions()
		opts.MaxIdle, opts.MaxOpen, opts.MaxWaiters = 1, 1, 8
		p, err := NewPoolWithOptions("unused", opts)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		p.Put(&poolLifecycleConn{})
		c, _ := p.Get()
		done := make(chan error, 8)
		for range 8 {
			go func() {
				conn, err := p.Get()
				if conn != nil {
					p.Put(conn)
				}
				done <- err
			}()
		}
		synctest.Wait()
		p.Put(c)
		for range 8 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if p.waiters.Load() != 0 || p.open.Load() != 1 {
			t.Fatal("wake chain leaked capacity")
		}
	})
}

func TestPoolCompletedDialRechecksOwnership(t *testing.T) {
	for _, closed := range []bool{false, true} {
		p := NewPool("unused", 1)
		p.open.Add(1)
		conn := &poolLifecycleConn{}
		ctx, cancel := context.WithCancel(context.Background())
		want := error(context.Canceled)
		if closed {
			p.Close()
			want = errPoolClosed
		} else {
			cancel()
		}
		got, err := p.acceptDial(ctx, conn)
		cancel()
		p.Close()
		if got != nil || !errors.Is(err, want) || p.open.Load() != 0 || conn.closes.Load() != 1 {
			t.Fatalf("late dial ownership: %v, %v, open=%d closes=%d", got, err, p.open.Load(), conn.closes.Load())
		}
	}
}

type poolClosingContext struct {
	context.Context
	pool *Pool
}

func (c poolClosingContext) Err() error {
	// Reproduce shutdown publishing its flag between the first check and the
	// idle dequeue; Close's bounded drain has not yet taken this connection.
	c.pool.closed.Store(true)
	return nil
}

func TestPoolIdleAcquisitionRechecksState(t *testing.T) {
	p := boundedTestPool(t)
	ctx := poolClosingContext{Context: context.Background(), pool: p}
	if c, err := p.GetContext(ctx); c != nil || !errors.Is(err, errPoolClosed) {
		t.Fatal(c, err)
	}
	if p.open.Load() != 0 {
		t.Fatal("shutdown dequeue leaked capacity")
	}
	other := boundedTestPool(t)
	c, _ := other.Get()
	other.Put(c)
	c.Close()
	if c, err, ready := other.tryAcquire(context.Background()); c != nil || err != nil || !ready {
		t.Fatalf("closed idle entry was reused: %v, %v, %v", c, err, ready)
	}
	// tryAcquire returned a reservation instead of the stale idle connection.
	replacement, err := other.acceptDial(context.Background(), &poolLifecycleConn{})
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
}
