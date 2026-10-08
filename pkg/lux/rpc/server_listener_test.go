package rpc

import (
	"bytes"
	"context"
	"errors"
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

func (temporaryAcceptError) Error() string     { return "temporary accept failure" }
func (e temporaryAcceptError) Temporary() bool { return !e.permanent }

func TestServerClosesConnectionWhenMalformedRequestReplyFails(t *testing.T) {
	preface := protocolPreface()
	var input bytes.Buffer
	input.Write(preface[:])
	if err := WriteFrame(&input, []byte{255}); err != nil {
		t.Fatal(err)
	}
	owned := &poolLifecycleConn{}
	writer := &errWriterAfterN{n: 1}
	conn := &prefaceTestConn{Conn: owned, reader: &input, writer: writer}
	server := NewServer(api.NewRouter())
	defer server.Close()
	if !server.registerConnection(conn) {
		t.Fatal("test connection registration failed")
	}
	server.handleConn(conn)
	if owned.closes.Load() != 1 || writer.calls != 2 || len(server.connections) != 0 {
		t.Fatalf("failed error response leaked connection: closes=%d, writes=%d, registered=%d", owned.closes.Load(), writer.calls, len(server.connections))
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
