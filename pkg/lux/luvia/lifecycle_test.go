package luvia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
		server := &http.Server{}
		gw := &Gateway{server: server}
		server.RegisterOnShutdown(func() { serveErr <- http.ErrServerClosed })
		done := make(chan error, 1)
		go func() { done <- gw.waitForShutdown(quit, serveErr, time.Second, func() {}) }()
		synctest.Wait()
		assertShutdownPending(t, done)
		quit <- syscall.SIGTERM
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestWaitForShutdownCancelsReadinessBeforeDrain(t *testing.T) {
	readiness, stopReadiness := context.WithCancel(t.Context())
	t.Cleanup(stopReadiness)
	quit := make(chan os.Signal, 1)
	quit <- syscall.SIGTERM
	serveErr := make(chan error, 1)
	drain := observedGatewayDrain(func(ctx context.Context) error {
		if !errors.Is(readiness.Err(), context.Canceled) {
			return errors.New("readiness was active when drain began")
		}
		if ctx.Err() != nil {
			return fmt.Errorf("business drain context was already cancelled: %w", ctx.Err())
		}
		serveErr <- http.ErrServerClosed
		return nil
	})
	gw := &Gateway{server: drain}
	if err := gw.waitForShutdown(quit, serveErr, time.Second, stopReadiness); err != nil {
		t.Fatal(err)
	}
}

func TestStatelessReadinessStopsBeforeDrain(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "*")
	gw := New()
	ready := gw.startReadiness()
	t.Cleanup(ready.stop)
	mux, _, err := gw.buildMux(ready, "test")
	if err != nil {
		t.Fatal(err)
	}
	status := func(method, path string) int {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), method, path, nil))
		return response.Code
	}
	if get, head, post := status(http.MethodGet, "/ready"), status(http.MethodHead, "/ready"), status(http.MethodPost, "/ready"); get != http.StatusOK || head != http.StatusOK || post != http.StatusMethodNotAllowed {
		t.Fatalf("readiness before shutdown: GET=%d HEAD=%d POST=%d", get, head, post)
	}
	quit := make(chan os.Signal, 1)
	quit <- syscall.SIGTERM
	serveErr := make(chan error, 1)
	gw.server = observedGatewayDrain(func(context.Context) error {
		if ready, health := status(http.MethodGet, "/ready"), status(http.MethodGet, "/health"); ready != http.StatusServiceUnavailable || health != http.StatusOK {
			return fmt.Errorf("during drain: readiness=%d health=%d", ready, health)
		}
		serveErr <- http.ErrServerClosed
		return nil
	})
	if err := gw.waitForShutdown(quit, serveErr, time.Second, ready.stop); err != nil {
		t.Fatal(err)
	}
}

type observedGatewayDrain func(context.Context) error

func (d observedGatewayDrain) Shutdown(ctx context.Context) error { return d(ctx) }

func TestWaitForShutdownServerExit(t *testing.T) {
	want := errors.New("listener failed")
	for _, result := range []error{http.ErrServerClosed, want} {
		t.Run(result.Error(), func(t *testing.T) {
			serveErr := make(chan error, 1)
			serveErr <- result
			err := New().waitForShutdown(nil, serveErr, time.Second, func() {})
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
		server := &http.Server{}
		gw := &Gateway{server: server}
		server.RegisterOnShutdown(func() { serveErr <- want })
		if err := gw.waitForShutdown(quit, serveErr, time.Second, func() {}); !errors.Is(err, want) {
			t.Fatalf("waitForShutdown = %v, want %v", err, want)
		}
	})
}

func TestWaitForShutdownDrainsActiveRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gw, serveErr, release, response := blockedRequestGateway(t)
		quit := make(chan os.Signal, 1)
		done := make(chan error, 1)
		go func() { done <- gw.waitForShutdown(quit, serveErr, time.Minute, func() {}) }()
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
		err := gw.waitForShutdown(quit, serveErr, 7*time.Second, func() {})
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
