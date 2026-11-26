package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type acceptTestListener struct {
	accept func() (net.Conn, error)
	closed chan struct{}
	once   sync.Once
}

func (l *acceptTestListener) Accept() (net.Conn, error) { return l.accept() }
func (l *acceptTestListener) Addr() net.Addr            { return &net.TCPAddr{} }
func (l *acceptTestListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

type temporaryAcceptError struct{ permanent bool }

func TestLRPCReadiness(t *testing.T) {
	server := NewServer(api.NewRouter())
	if err := server.CheckReady(t.Context()); err == nil {
		t.Fatal("unstarted LRPC server reported ready")
	}
	client := NewClient(startTestRPCServer(t, server))
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := client.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	if open := client.pool.open.Load(); open != 0 {
		t.Fatalf("readiness used the business connection pool: open=%d", open)
	}
	stop, stopCancel := context.WithCancel(t.Context())
	stopCancel()
	for _, check := range []func(context.Context) error{client.CheckReady, server.CheckReady} {
		if err := check(stop); !errors.Is(err, context.Canceled) {
			t.Fatalf("checked readiness after cancellation: %v", err)
		}
	}
	server.Close()
	if server.CheckReady(ctx) == nil || client.CheckReady(ctx) == nil {
		t.Fatal("stopped LRPC listener reported ready")
	}
	client.Close()
	if err := client.CheckReady(ctx); !errors.Is(err, errPoolClosed) {
		t.Fatalf("closed client readiness: %v", err)
	}
}

func TestLRPCReadinessClosesHandshakeWhenClientCloses(t *testing.T) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	deadline := time.Now().Add(3 * time.Second)
	if err := listener.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()

	client := NewClient(listener.Addr().String())
	t.Cleanup(client.Close)
	readiness := make(chan error, 1)
	go func() { readiness <- client.CheckReady(ctx) }()

	peer, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	var preface [12]byte
	if _, err := io.ReadFull(peer, preface[:]); err != nil {
		t.Fatal(err)
	}
	if err := validatePreface(preface); err != nil {
		t.Fatal(err)
	}
	client.Close()
	reply := protocolPreface()
	if err := writePreface(peer, &reply); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readiness:
		if !errors.Is(err, errPoolClosed) {
			t.Fatalf("closed client readiness = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("readiness completed after its deadline: %v", ctx.Err())
	}
	if _, err := peer.Read(preface[:]); !errors.Is(err, io.EOF) {
		t.Fatalf("ready handshake connection stayed open: %v", err)
	}
}

type startupValidationListener struct {
	net.Listener
	accepts int
}

func (l *startupValidationListener) Accept() (net.Conn, error) {
	l.accepts++
	return nil, errors.New("startup validation reached Accept")
}

func TestServerRejectsNilRoutesAndReleasesOwnedListener(t *testing.T) {
	for _, internal := range []bool{false, true} {
		router := api.NewRouter()
		if internal {
			router.HandleInternal("missing", nil)
		} else {
			router.Handle("missing", nil)
		}
		server := NewServer(router)
		t.Cleanup(server.Close)
		if err := server.ListenAndServe("invalid-address"); err == nil || !strings.Contains(err.Error(), `"missing"`) {
			t.Fatalf("missing route not checked before listen: %v", err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		owned := &startupValidationListener{Listener: listener}
		if err := server.Serve(owned); err == nil || !strings.Contains(err.Error(), `"missing"`) {
			t.Fatalf("missing route not checked before accept: %v", err)
		}
		if owned.accepts != 0 {
			t.Fatal("invalid route started accepting connections")
		}
		rebound, err := net.Listen("tcp", listener.Addr().String())
		if err != nil {
			t.Fatalf("failed startup retained listener: %v", err)
		}
		rebound.Close()
	}
}

func (temporaryAcceptError) Error() string     { return "temporary accept failure" }
func (e temporaryAcceptError) Temporary() bool { return !e.permanent }

type failedReplyConn struct {
	net.Conn
	writes atomic.Int32
	closes atomic.Int32
}

func (c *failedReplyConn) Write(data []byte) (int, error) {
	if c.writes.Add(1) > 1 {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(data)
}

func (c *failedReplyConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

func TestServerClosesConnectionWhenMalformedRequestReplyFails(t *testing.T) {
	socket, peer := net.Pipe()
	defer peer.Close()
	conn := &failedReplyConn{Conn: socket}
	server := NewServer(api.NewRouter())
	defer server.Close()
	if !server.registerConnection(conn) {
		t.Fatal("registration failed")
	}
	done := make(chan struct{})
	go func() { server.handleConn(conn); close(done) }()
	if err := initiateHandshake(t.Context(), peer, time.Second); err != nil {
		t.Fatal(err)
	}
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	if err := WriteFrame(peer, []byte{255}); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("failed reply did not close connection", err)
	}
	<-done
	if conn.closes.Load() != 1 || conn.writes.Load() != 2 || len(server.connections) != 0 {
		t.Fatalf("failed response lifecycle: closes=%d writes=%d registered=%d", conn.closes.Load(), conn.writes.Load(), len(server.connections))
	}
}

func TestServerValidatesRouterBeforeListeningOrAccepting(t *testing.T) {
	router := api.NewRouter()
	router.RequireCache("cached")
	server := NewServer(router)
	defer server.Close()
	if err := server.ListenAndServe("invalid-address"); err == nil || !strings.Contains(err.Error(), "cached") {
		t.Fatalf("listened before validating router: %v", err)
	}
	calls := 0
	listener := &acceptTestListener{closed: make(chan struct{}), accept: func() (net.Conn, error) { calls++; return nil, net.ErrClosed }}
	if err := server.Serve(listener); err == nil || !strings.Contains(err.Error(), "cached") {
		t.Fatalf("accepted before validating router: %v", err)
	}
	if calls != 0 {
		t.Fatal("invalid router accepted connections")
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("failed startup did not close owned listener")
	}
}

func TestServerRejectsMissingCachePolicyBeforeAccepting(t *testing.T) {
	binding, err := api.NewResponseCache(cache.Nop{}, api.CacheNamespace{Project: "p", Environment: "test", Service: "s", Revision: "r"}, api.ResponseOriginOptions{MaxConcurrent: 16})
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	if err := router.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CacheDependencies: &schema.CacheDependencies{}}); err != nil {
		t.Fatal(err)
	}
	router.RequireCache("cached")
	router.Handle("cached", func(context.Context, *api.Request) error { t.Fatal("invalid route executed"); return nil })
	server := NewServer(router)
	defer server.Close()
	listener := &acceptTestListener{closed: make(chan struct{}), accept: func() (net.Conn, error) {
		t.Error("missing policy accepted an RPC connection")
		return nil, net.ErrClosed
	}}
	if err := server.Serve(listener); err == nil || !strings.Contains(err.Error(), "explicit policy") {
		t.Fatalf("missing policy did not reject startup: %v", err)
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("failed startup retained listener")
	}
}

func TestServerPublishesDeclaredCacheRouteBeforeRPC(t *testing.T) {
	for _, declared := range []bool{false, true} {
		backend := cache.NewMemory()
		t.Cleanup(func() { _ = backend.Close() })
		binding, err := api.NewResponseCache(backend, api.CacheNamespace{Project: "p", Environment: "test", Service: "s", Revision: "r"}, api.ResponseOriginOptions{MaxConcurrent: 16})
		if err != nil {
			t.Fatal(err)
		}
		router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
		router.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CacheDependencies: &schema.CacheDependencies{}, CachePolicy: &schema.CachePolicy{Mode: "ttl", TTL: time.Minute, Scope: "private"}})
		router.Registry.RegisterSchemaAPIs(router.Schema)
		if declared {
			router.RequireCache("cached")
		}
		var calls atomic.Int32
		router.Handle("cached", func(ctx context.Context, req *api.Request) error {
			return api.CacheResponsePolicy(ctx, req, "", func() error {
				calls.Add(1)
				req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
				return nil
			})
		})
		client := NewClient(startTestRPCServer(t, NewServer(router)))
		t.Cleanup(client.Close)
		for range 2 {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			response, err := client.CallWithMaskContext(ctx, "", 1, nil, []byte{0})
			cancel()
			if declared {
				if err != nil || !bytes.Equal(response, codec.AppendSvarint(nil, 42)) {
					t.Fatalf("declared cache RPC: response=%x, err=%v", response, err)
				}
			} else if err == nil {
				t.Fatal("undeclared RPC cache accepted")
			}
		}
		wantCalls := int32(0)
		if declared {
			wantCalls = 1
		}
		if calls.Load() != wantCalls {
			t.Fatalf("declared=%t calls=%d, want %d", declared, calls.Load(), wantCalls)
		}
	}
}

type transientTestListener struct {
	net.Listener
	retried bool
}

func (l *transientTestListener) Accept() (net.Conn, error) {
	if !l.retried {
		l.retried = true
		return nil, temporaryAcceptError{}
	}
	return l.Listener.Accept()
}

func TestServerServeRejectsNilAndClosesUnusedListener(t *testing.T) {
	server := NewServer(api.NewRouter())
	if err := server.Serve(nil); err == nil {
		t.Fatal("nil listener was accepted")
	}
	server.Close()
	listener := &acceptTestListener{closed: make(chan struct{}), accept: func() (net.Conn, error) {
		t.Fatal("closed server must not accept connections")
		return nil, net.ErrClosed
	}}
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("unused listener was not closed")
	}
}

func TestServerServeReturnsPermanentAcceptError(t *testing.T) {
	for _, want := range []error{errors.New("permanent accept failure"), net.ErrClosed, temporaryAcceptError{permanent: true}} {
		t.Run(want.Error(), func(t *testing.T) {
			calls := 0
			listener := &acceptTestListener{closed: make(chan struct{})}
			listener.accept = func() (net.Conn, error) {
				calls++
				if calls > 1 {
					t.Fatal("permanent accept error was retried")
				}
				return nil, want
			}
			server := NewServer(api.NewRouter())
			defer server.Close()
			if err := server.Serve(listener); !errors.Is(err, want) {
				t.Fatalf("Serve error = %v, want %v", err, want)
			}
			select {
			case <-listener.closed:
			default:
				t.Fatal("failed listener was not closed")
			}
		})
	}
}

func TestServerServeResetsBackoffAfterAcceptedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer, conn := net.Pipe()
		peer.Close()
		defer conn.Close()
		calls, previous := 0, time.Now()
		listener := &acceptTestListener{closed: make(chan struct{})}
		listener.accept = func() (net.Conn, error) {
			calls++
			elapsed := time.Since(previous)
			previous = time.Now()
			switch calls {
			case 1, 3:
				return nil, temporaryAcceptError{}
			case 2, 4:
				if elapsed != 5*time.Millisecond {
					t.Fatalf("retry %d delay = %v, want 5ms", calls, elapsed)
				}
				if calls == 2 {
					return conn, nil
				}
			}
			return nil, net.ErrClosed
		}
		server := NewServer(api.NewRouter())
		defer server.Close()
		if err := server.Serve(listener); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestServerServeClosesLateConnectionDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		peer, conn := net.Pipe()
		defer peer.Close()
		defer conn.Close()
		server := NewServer(api.NewRouter())
		listener := &acceptTestListener{closed: make(chan struct{})}
		calls := 0
		listener.accept = func() (net.Conn, error) {
			calls++
			if calls == 1 {
				server.Close()
				return conn, nil
			}
			return nil, net.ErrClosed
		}
		if err := server.Serve(listener); err != nil {
			t.Fatal(err)
		}
		if len(server.sem) != 0 || len(server.connections) != 0 {
			t.Fatal("late connection was retained")
		}
		if _, err := peer.Read(make([]byte, 1)); err == nil {
			t.Fatal("late connection was not closed")
		}
	})
}

func TestServerServeRetriesWithCappedBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		want := errors.New("stop retries")
		delays := []time.Duration{5, 10, 20, 40, 80, 160, 320, 640, 1000, 1000}
		previous, calls := time.Now(), 0
		listener := &acceptTestListener{closed: make(chan struct{})}
		listener.accept = func() (net.Conn, error) {
			if calls > 0 {
				if elapsed := time.Since(previous); elapsed != delays[calls-1]*time.Millisecond {
					t.Fatalf("retry %d delay = %v", calls, elapsed)
				}
			}
			previous = time.Now()
			calls++
			if calls > len(delays) {
				return nil, want
			}
			return nil, temporaryAcceptError{}
		}
		server := NewServer(api.NewRouter())
		defer server.Close()
		if err := server.Serve(listener); !errors.Is(err, want) {
			t.Fatalf("Serve error = %v", err)
		}
		if calls != len(delays)+1 {
			t.Fatalf("Accept calls = %d", calls)
		}
	})
}

func TestServerCloseInterruptsAcceptBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		server := NewServer(api.NewRouter())
		listener := &acceptTestListener{closed: make(chan struct{}), accept: func() (net.Conn, error) {
			return nil, temporaryAcceptError{}
		}}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		synctest.Wait()
		server.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != 0 {
			t.Fatal("Close waited for the retry timer")
		}
	})
}

func TestServerServeRejectsConcurrentListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := &acceptTestListener{closed: make(chan struct{})}
		listener.accept = func() (net.Conn, error) {
			<-listener.closed
			return nil, net.ErrClosed
		}
		server := NewServer(api.NewRouter())
		defer server.Close()
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		synctest.Wait()
		other := &acceptTestListener{closed: make(chan struct{}), accept: func() (net.Conn, error) {
			t.Fatal("concurrent listener must not accept")
			return nil, net.ErrClosed
		}}
		if err := server.Serve(other); err == nil || !strings.Contains(err.Error(), "already serving") {
			t.Fatalf("concurrent Serve error = %v", err)
		}
		select {
		case <-other.closed:
		default:
			t.Fatal("rejected listener was not closed")
		}
		server.Close()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

// Allocate the listening socket before starting Serve, without a port race or sleep.
func startTestRPCServer(t testing.TB, server *Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return startTestRPCListener(t, server, listener)
}

func startTestRPCListener(t testing.TB, server *Server, listener net.Listener) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("RPC Serve did not stop")
		}
	})
	return listener.Addr().String()
}

func dialTestRPCConn(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func assertRPCConnectionRejected(t *testing.T, addr string) {
	t.Helper()
	conn := dialTestRPCConn(t, addr)
	_, err := conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("connection at capacity was not rejected")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("connection at capacity was left open")
	}
}

func TestServerCloseCancelsRunningUnaryHandler(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("wait", 1); err != nil {
		t.Fatal(err)
	}
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)
	router.Handle("wait", func(ctx context.Context, _ *api.Request) error {
		started <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return context.Canceled
		}
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	callDone := make(chan error, 1)
	go func() { _, err := client.CallWithMaskContext(ctx, "", 1, nil, nil); callDone <- err }()
	var business context.Context
	select {
	case business = <-started:
	case <-ctx.Done():
		t.Fatal("handler did not start")
	}
	closeDone := make(chan struct{})
	go func() { server.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-ctx.Done():
		t.Fatal("server close did not cancel running handler")
	}
	if business.Err() != context.Canceled {
		t.Fatal("handler context not canceled", business.Err())
	}
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("closed request returned success")
		}
	case <-ctx.Done():
		t.Fatal("client did not finish")
	}
}
