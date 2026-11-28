package rpc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestPoolReadinessWaitUsesCallerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := boundedTestPool(t)
		conn, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		start := time.Now()
		if err := p.checkReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("readiness wait returned %v", err)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Millisecond {
			t.Fatalf("readiness exceeded caller deadline: %v", elapsed)
		}
		if open, waiting := p.open.Load(), p.waiters.Load(); open != 1 || waiting != 0 {
			t.Fatalf("readiness changed a borrowed slot: open=%d waiting=%d", open, waiting)
		}
	})
}

func TestPoolReplacementRetainsReservationThroughClose(t *testing.T) {
	p := boundedTestPool(t)
	conn, err := p.Get()
	if err != nil {
		t.Fatal(err)
	}
	replaced := conn.(*poolConn)
	if err := replaced.closeForReplacement(); err != nil {
		t.Fatal(err)
	}
	if err := replaced.closeForReplacement(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("repeated replacement returned %v", err)
	}
	if err := replaced.Close(); err != nil || p.open.Load() != 1 {
		t.Fatalf("closing the retired socket released the replacement's slot: %v", err)
	}
	replacement, err := p.acceptDial(t.Context(), &poolLifecycleConn{})
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil || p.open.Load() != 0 {
		t.Fatalf("replacement leaked the slot: %v", err)
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
	for _, field := range []string{"open", "idle", "idle-over-open", "waiters", "wait-timeout", "dial-timeout", "reserve", "reserve-over-open"} {
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
		case "reserve":
			options.UnaryReserve = -1
		case "reserve-over-open":
			options.UnaryReserve = options.MaxOpen + 1
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

func TestLoadGatewayClientsFromEnv(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.env")
	for _, tc := range []struct {
		name, maxOpen, reserve, invalidKey string
		wantOpen, wantReserve              int
	}{
		{name: "default", wantOpen: 32, wantReserve: 8},
		{name: "smaller", maxOpen: "12", reserve: "4", wantOpen: 12, wantReserve: 4},
		{name: "larger", maxOpen: "64", reserve: "0", wantOpen: 64, wantReserve: 16},
		{name: "no-streams", reserve: "32", wantOpen: 32, wantReserve: 32},
		{name: "bad-open", maxOpen: "slow", invalidKey: "LRPC_MAX_OPEN"},
		{name: "bad-reserve", reserve: "fast", invalidKey: "LRPC_UNARY_RESERVE"},
		{name: "zero-open", maxOpen: "0", invalidKey: "LRPC_MAX_OPEN"},
		{name: "negative-open", maxOpen: "-1", invalidKey: "LRPC_MAX_OPEN"},
		{name: "negative-reserve", reserve: "-1", invalidKey: "LRPC_UNARY_RESERVE"},
		{name: "oversized-reserve", maxOpen: "12", reserve: "13", invalidKey: "LRPC_UNARY_RESERVE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LRPC_MAX_OPEN", tc.maxOpen)
			t.Setenv("LRPC_UNARY_RESERVE", tc.reserve)
			t.Setenv("EMPTY_SERVICE_ADDR", "")
			clients, err := LoadGatewayClients(missing, map[string]string{"empty": "fallback:9000", "other": "other:9000"})
			for _, client := range clients {
				t.Cleanup(client.Close)
			}
			if tc.invalidKey != "" {
				if clients != nil || err == nil || !strings.Contains(err.Error(), tc.invalidKey) {
					t.Fatalf("invalid configuration returned %d clients, error %v", len(clients), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := DefaultPoolOptions()
			want.MaxOpen, want.UnaryReserve = tc.wantOpen, tc.wantReserve
			want.MaxIdle = min(want.MaxIdle, want.MaxOpen)
			if len(clients) != 2 || clients["empty"] == nil || clients["other"] == nil ||
				clients["empty"].pool.options != want || clients["other"].pool.options != want {
				t.Fatalf("clients did not use validated pool options: %+v", want)
			}
			if clients["empty"].pool.addr != "" {
				t.Fatal("an explicitly empty service address must keep dotenv lookup semantics")
			}
		})
	}
}

func TestLoadGatewayClientsKeepTargetPoolsIndependent(t *testing.T) {
	t.Setenv("LRPC_MAX_OPEN", "2")
	t.Setenv("LRPC_UNARY_RESERVE", "1")
	t.Setenv("FIRST_SERVICE_ADDR", "ignored")
	if err := os.Unsetenv("FIRST_SERVICE_ADDR"); err != nil {
		t.Fatal(err)
	}
	router, _ := newStreamIsolationRouter(t)
	addr := startTestRPCServer(t, NewServer(router))
	t.Setenv("SECOND_SERVICE_ADDR", addr)
	clients, err := LoadGatewayClients(filepath.Join(t.TempDir(), "missing.env"),
		map[string]string{"first": addr, "second": "unused:9000"})
	if err != nil {
		t.Fatal(err)
	}
	first, second := clients["first"], clients["second"]
	if first == nil || second == nil {
		t.Fatal("factory did not construct both clients")
	}
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	if first.name != "first" || second.name != "second" || first.pool == second.pool ||
		first.pool.addr != addr || second.pool.addr != addr {
		t.Fatal("named clients did not keep distinct pools or resolve the service address")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream := openIsolationStream(t, ctx, first)
	extra, err := first.SubscribeContext(ctx, "", 1, nil, nil)
	if extra != nil {
		extra.Close()
	}
	if extra != nil || !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("first pool accepted a second stream: %v", err)
	}
	requirePooledPing(t, ctx, first)
	openIsolationStream(t, ctx, second)
	stream.Close()
	first.Close()
	requirePooledPing(t, ctx, second)
}

func TestLoadGatewayClientsUsesExplicitDotenv(t *testing.T) {
	t.Setenv("LRPC_MAX_OPEN", "3")
	t.Setenv("LRPC_UNARY_RESERVE", "1")
	t.Setenv("DOTENV_SERVICE_ADDR", "restore-after-test")
	if err := os.Unsetenv("DOTENV_SERVICE_ADDR"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "cluster.env")
	content := []byte("LRPC_MAX_OPEN=8\nLRPC_UNARY_RESERVE=4\nDOTENV_SERVICE_ADDR=localhost:9234\n")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	clients, err := LoadGatewayClients(file, map[string]string{"dotenv": "fallback:9000"})
	if err != nil {
		t.Fatal(err)
	}
	client := clients["dotenv"]
	if client == nil {
		t.Fatal("factory did not construct the dotenv client")
	}
	t.Cleanup(client.Close)
	if pool := client.pool; pool.addr != "localhost:9234" ||
		pool.options.MaxOpen != 3 || pool.options.UnaryReserve != 1 {
		t.Fatal("startup did not load the explicit dotenv file with process values taking precedence")
	}
}

func TestLoadGatewayClientsRejectsMalformedDotenv(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cluster.env")
	if err := os.WriteFile(file, []byte("invalid config without an equals sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clients, err := LoadGatewayClients(file, map[string]string{"test": "localhost:9234"})
	if clients != nil || err == nil || !strings.HasPrefix(err.Error(), "load "+file+":") {
		t.Fatalf("malformed dotenv returned %d clients, error %v", len(clients), err)
	}
}

func TestPoolStreamLeavesUnaryReserve(t *testing.T) {
	router, started := newStreamIsolationRouter(t)
	client := NewNamedClient("test", startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	streams := make([]*Subscription, 0, 24)
	for range cap(streams) {
		streams = append(streams, openIsolationStream(t, ctx, client))
		<-started
	}

	overflow, err := client.SubscribeContext(ctx, "", 1, nil, nil)
	if overflow != nil {
		overflow.Close()
	}
	if overflow != nil || !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("25th stream = %v, %v; want immediate pool busy", overflow != nil, err)
	}
	var app *luxerrors.AppError
	if !errors.As(err, &app) || app.Code != 503 {
		t.Fatalf("lost overload status: %v", err)
	}

	requirePooledPing(t, ctx, client)
	borrowed := make([]net.Conn, 0, 8)
	for range cap(borrowed) {
		conn, err := client.pool.GetContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.pool.Put(conn) })
		borrowed = append(borrowed, conn)
	}
	if client.pool.open.Load() != 32 || client.pool.waiters.Load() != 0 {
		t.Fatal("streams and unary calls did not share the 32-connection limit")
	}

	streams[0].Close()
	for range streams[0].Messages() {
	}
	openIsolationStream(t, ctx, client)
	<-started
	if client.pool.open.Load() != 32 {
		t.Fatal("reopening a stream exceeded the shared limit")
	}
}

func TestPoolStreamConfiguredUnaryReserve(t *testing.T) {
	router, _ := newStreamIsolationRouter(t)
	opts := DefaultPoolOptions()
	opts.MaxIdle, opts.MaxOpen, opts.UnaryReserve = 3, 3, 2
	client, err := NewNamedClientWithOptions("test", startTestRPCServer(t, NewServer(router)), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub := openIsolationStream(t, ctx, client)
	for range 2 {
		if extra, err := client.SubscribeContext(ctx, "", 1, nil, nil); extra != nil || !errors.Is(err, ErrPoolExhausted) {
			t.Fatalf("stream exceeded the configured budget: %v, %v", extra != nil, err)
		}
	}
	requirePooledPing(t, ctx, client)
	sub.Close()
	openIsolationStream(t, ctx, client)
	requirePooledPing(t, ctx, client)
}

func TestPoolStreamDoesNotWaitAtSharedLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := boundedTestPool(t)
		unary, err := p.Get()
		if err != nil {
			t.Fatal(err)
		}
		defer unary.Close()
		// A one-connection pool reserves the only slot for unary RPC.
		start := time.Now()
		if conn, err := p.getStreamContext(t.Context()); conn != nil || !errors.Is(err, ErrPoolExhausted) {
			t.Fatalf("single-connection pool admitted a stream: %v, %v", conn, err)
		}
		if time.Since(start) != 0 || p.waiters.Load() != 0 {
			t.Fatal("stream waited for the unary-only slot")
		}

		opts := DefaultPoolOptions()
		opts.MaxOpen, opts.MaxIdle = 2, 2
		p, err = NewPoolWithOptions("unused", opts)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		for range 2 {
			p.Put(&poolLifecycleConn{})
			conn, err := p.Get()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
		}
		start = time.Now()
		if conn, err := p.getStreamContext(t.Context()); conn != nil || !errors.Is(err, ErrPoolExhausted) {
			t.Fatalf("stream entered the full pool: %v, %v", conn, err)
		}
		if time.Since(start) != 0 || p.waiters.Load() != 0 || p.streams.Load() != 0 {
			t.Fatal("stream waited or leaked its reservation at the shared limit")
		}
	})
}

func TestPoolStreamDialingConsumesBudget(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	opts := DefaultPoolOptions()
	opts.MaxOpen, opts.MaxIdle = 2, 2
	p, err := NewPoolWithOptions(listener.Addr().String(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := p.getStreamContext(ctx)
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	peer, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if conn, err := p.getStreamContext(t.Context()); conn != nil || !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("another stream acquired a slot during handshake: %v, %v", conn, err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || p.open.Load() != 0 || p.streams.Load() != 0 {
		t.Fatalf("canceled handshake leaked quota: err=%v, open=%d, streams=%d", err, p.open.Load(), p.streams.Load())
	}
}

func TestPoolStreamAcquisitionPreservesErrors(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		p := NewPool("unused", 1)
		defer p.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if conn, err := p.getStreamContext(ctx); conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled stream got %v, %v", conn, err)
		}
	})
	t.Run("closed", func(t *testing.T) {
		p := NewPool("unused", 1)
		p.Close()
		if conn, err := p.getStreamContext(t.Context()); conn != nil || !errors.Is(err, errPoolClosed) {
			t.Fatalf("stream from closed pool got %v, %v", conn, err)
		}
	})
	t.Run("shutdown after admission", func(t *testing.T) {
		p := NewPool("unused", 1)
		defer p.Close()
		ctx := poolClosingContext{Context: t.Context(), pool: p}
		if conn, err := p.getStreamContext(ctx); conn != nil || !errors.Is(err, errPoolClosed) {
			t.Fatalf("stream during shutdown got %v, %v", conn, err)
		}
		if p.streams.Load() != 0 {
			t.Fatal("shutdown leaked stream quota")
		}
	})
}

// Closing the pipe can wake the reader before the underlying Close returns.
type pausedStreamCloseConn struct {
	net.Conn
	started chan struct{}
	release chan struct{}
}

func (c *pausedStreamCloseConn) Close() error {
	err := c.Conn.Close()
	close(c.started)
	<-c.release
	return err
}

func TestPoolStreamTerminalWaitsForSlotRelease(t *testing.T) {
	for _, source := range []string{"parent cancellation", "explicit close"} {
		t.Run(source, func(t *testing.T) {
			armResult, resultDeadline := poolCloseWatchdog(t, 5*time.Second)
			armObservation, observationDone := poolCloseWatchdog(t, 100*time.Millisecond)
			synctest.Test(t, func(t *testing.T) {
				opts := DefaultPoolOptions()
				opts.MaxIdle, opts.MaxOpen, opts.MaxWaiters = 2, 2, 0
				client, err := NewNamedClientWithOptions("test", "unused", opts)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				client.pool.Put(&poolLifecycleConn{})
				unaryHold, err := client.pool.Get()
				if err != nil {
					t.Fatal(err)
				}
				defer unaryHold.Close()
				conn, peer := net.Pipe()
				defer peer.Close()
				closeStarted, allowClose := make(chan struct{}), make(chan struct{})
				release := sync.OnceFunc(func() { close(allowClose) })
				defer release()
				client.pool.Put(&pausedStreamCloseConn{Conn: conn, started: closeStarted, release: allowClose})
				streamPeer := replyToPoolRequest(peer, []byte{statusOK})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				sub, err := client.SubscribeContext(ctx, "", 1, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := <-streamPeer; err != nil {
					t.Fatal(err)
				}
				synctest.Wait()

				explicit := make(chan struct{})
				if source == "explicit close" {
					go func() {
						sub.Close()
						close(explicit)
					}()
				} else {
					cancel()
				}
				<-closeStarted
				close(armResult)
				for sub.Err() == nil {
					select {
					case <-resultDeadline:
						t.Fatal("terminal result not published while physical close was paused")
					default:
						runtime.Gosched()
					}
				}
				if !errors.Is(sub.Err(), context.Canceled) {
					t.Fatalf("lost early cancellation result: %v", sub.Err())
				}
				if _, err := client.CallWithMaskContext(t.Context(), "", 2, nil, nil); !errors.Is(err, ErrPoolExhausted) {
					t.Fatalf("slot released during physical close: %v", err)
				}
				if client.pool.streams.Load() != 1 {
					t.Fatal("stream quota released during physical close")
				}
				close(armObservation)
				assertPoolStreamTerminalPending(t, sub, explicit, observationDone)

				release()
				for message := range sub.Messages() {
					t.Errorf("unexpected stream data: %x", message)
				}
				if err := <-sub.Errors(); !errors.Is(err, context.Canceled) || err != sub.Err() {
					t.Fatalf("terminal cancellation: channel=%v Err=%v", err, sub.Err())
				}
				if _, open := <-sub.Errors(); open {
					t.Fatal("terminal error reported more than once")
				}
				if source == "explicit close" {
					<-explicit
				}
				if client.pool.open.Load() != 1 || client.pool.streams.Load() != 0 {
					t.Fatal("terminal notification preceded capacity release")
				}

				replacement, unaryPeer := net.Pipe()
				defer unaryPeer.Close()
				client.pool.Put(replacement)
				if len(client.pool.conns) != 1 {
					t.Fatal("released slot cannot accept another connection")
				}
				unary := replyToPoolRequest(unaryPeer, []byte{statusOK, 7})
				if data, err := client.CallWithMaskContext(t.Context(), "", 2, nil, nil); err != nil || len(data) != 1 || data[0] != 7 {
					t.Fatalf("unary did not acquire the released slot: %x %v", data, err)
				}
				if err := <-unary; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func assertPoolStreamTerminalPending(t *testing.T, sub *Subscription, explicit, observationDone <-chan struct{}) {
	t.Helper()
	select {
	case err := <-sub.Errors():
		t.Fatalf("errors notified before pool slot release: %v", err)
	case <-observationDone:
	}
	// Check again in case the timer and a terminal channel became ready together.
	select {
	case _, open := <-sub.Messages():
		t.Fatalf("messages notified before pool slot release: open=%t", open)
	case err := <-sub.Errors():
		t.Fatalf("errors notified before pool slot release: %v", err)
	case <-explicit:
		t.Fatal("Close returned before pool slot release")
	default:
	}
}

// A goroutine joining sync.Once is not durably blocked for synctest. Arm a
// real watchdog outside the bubble only after the pipe close is paused.
func poolCloseWatchdog(t *testing.T, timeout time.Duration) (arm, fired chan struct{}) {
	arm, fired = make(chan struct{}), make(chan struct{})
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-arm:
		case <-ctx.Done():
			return
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			close(fired)
		case <-ctx.Done():
		}
	}()
	t.Cleanup(func() { <-done })
	return arm, fired
}

func replyToPoolRequest(peer net.Conn, response []byte) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := ReadFrame(peer, nil)
		if err == nil {
			err = WriteFrame(peer, response)
		}
		done <- err
	}()
	return done
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
