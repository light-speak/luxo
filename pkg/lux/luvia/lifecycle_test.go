package luvia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitForShutdownWaitsForSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		quit := make(chan os.Signal, 1)
		serveErr := make(chan error, 1)
		gw := &Gateway{server: &http.Server{}}
		gw.server.RegisterOnShutdown(func() { serveErr <- http.ErrServerClosed })
		done := make(chan error, 1)
		go func() { done <- gw.waitForShutdown(quit, serveErr, time.Second) }()
		synctest.Wait()
		assertShutdownPending(t, done)
		quit <- syscall.SIGTERM
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWaitForShutdownServerExit(t *testing.T) {
	want := errors.New("listener failed")
	for _, result := range []error{http.ErrServerClosed, want} {
		t.Run(result.Error(), func(t *testing.T) {
			serveErr := make(chan error, 1)
			serveErr <- result
			err := New().waitForShutdown(nil, serveErr, time.Second)
			if result == http.ErrServerClosed {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, want) {
				t.Fatalf("waitForShutdown = %v, want %v", err, want)
			}
		})
	}
}

func TestWaitForShutdownServerFailureAfterSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		want := errors.New("listener failed during shutdown")
		quit := make(chan os.Signal, 1)
		quit <- syscall.SIGINT
		serveErr := make(chan error, 1)
		gw := &Gateway{server: &http.Server{}}
		gw.server.RegisterOnShutdown(func() { serveErr <- want })
		if err := gw.waitForShutdown(quit, serveErr, time.Second); !errors.Is(err, want) {
			t.Fatalf("waitForShutdown = %v, want %v", err, want)
		}
	})
}

func TestWaitForShutdownDrainsActiveRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gw, serveErr, release, response := blockedRequestGateway(t)
		quit := make(chan os.Signal, 1)
		done := make(chan error, 1)
		go func() { done <- gw.waitForShutdown(quit, serveErr, time.Minute) }()
		quit <- syscall.SIGTERM
		synctest.Wait()
		assertShutdownPending(t, done)
		release()
		if err := <-response; err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWaitForShutdownHonorsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gw, serveErr, release, response := blockedRequestGateway(t)
		quit := make(chan os.Signal, 1)
		quit <- syscall.SIGINT
		start := time.Now()
		err := gw.waitForShutdown(quit, serveErr, 7*time.Second)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 7*time.Second {
			t.Fatalf("shutdown error = %v, elapsed = %s", err, time.Since(start))
		}
		release()
		if err := <-response; err != nil {
			t.Fatal(err)
		}
	})
}

func assertShutdownPending(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("shutdown completed before expected: %v", err)
	default:
	}
}

func blockedRequestGateway(t *testing.T) (*Gateway, <-chan error, func(), <-chan error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close(); clientConn.Close() })
	listener := &gatewayPipeListener{conn: serverConn, closed: make(chan struct{})}
	entered, blocked := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(blocked) })
	t.Cleanup(release)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-blocked
		fmt.Fprint(w, "drained")
	})}
	t.Cleanup(func() { server.Close() })
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	response := make(chan error, 1)
	go func() {
		resp, err := (&http.Client{Transport: transport}).Get("http://gateway.test/")
		if err == nil {
			var body []byte
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil && string(body) != "drained" {
				err = fmt.Errorf("response body = %q", body)
			}
		}
		response <- err
	}()
	<-entered
	return &Gateway{server: server}, serveErr, release, response
}

// A channel-backed listener keeps lifecycle tests inside the synctest bubble.
type gatewayPipeListener struct {
	conn     net.Conn
	accepted bool
	closed   chan struct{}
	once     sync.Once
}

func (l *gatewayPipeListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *gatewayPipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *gatewayPipeListener) Addr() net.Addr { return l.conn.LocalAddr() }
