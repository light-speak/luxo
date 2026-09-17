package rpc

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
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
