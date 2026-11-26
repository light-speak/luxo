package luvia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestGatewayReadinessSharesOneProbeAndRefreshesItsResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	readiness := &gatewayReadiness{lifetime: t.Context(), checks: []func(context.Context) error{
		func(ctx context.Context) error {
			if calls.Add(1) != 1 {
				return errors.New("dependency offline")
			}
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan bool, 1)
	go func() { first <- readiness.check(ctx) }()
	<-started
	cancel()
	if readiness.check(ctx) {
		t.Fatal("cancelled caller observed readiness")
	}
	second := make(chan bool, 1)
	go func() { second <- readiness.check(t.Context()) }()
	close(release)
	if <-first || !<-second || !readiness.check(t.Context()) || calls.Load() != 1 {
		t.Fatalf("healthy probe did not survive caller cancellation or share its result, calls=%d", calls.Load())
	}
	readiness.mu.Lock()
	readiness.expires = time.Time{}
	readiness.mu.Unlock()
	if readiness.check(t.Context()) || readiness.check(t.Context()) || calls.Load() != 2 {
		t.Fatalf("expired success did not refresh to a cached failure, calls=%d", calls.Load())
	}
}

func TestGatewayReadinessBoundsChecksAndKeepsErrorsPrivate(t *testing.T) {
	gw := New()
	gw.AddReadinessCheck(nil)
	gw.AddReadinessCheck(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	gw.AddReadinessCheck(func(context.Context) error { return errors.New("private-db.internal") })
	resp := httptest.NewRecorder()
	testReadinessHandler(t, gw).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if resp.Code != http.StatusServiceUnavailable || resp.Body.String() != `{"status":"unavailable"}` {
		t.Fatalf("got %d %q", resp.Code, resp.Body.String())
	}
	if resp.Header().Get("Cache-Control") != "no-store" || resp.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected readiness headers %v", resp.Header())
	}
	resp = httptest.NewRecorder()
	testReadinessHandler(t, New()).ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("stateless gateway returned %d", resp.Code)
	}
}

func TestGatewayReadinessTimesOutFirstAndSharedHTTPWaiters(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseProbe)
	var calls atomic.Int32

	gw := New()
	gw.AddReadinessCheck(func(context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			// The dependency library's synchronous I/O may return after ctx expires.
			<-release
		}
		return nil
	})
	handler := testReadinessHandler(t, gw)
	first := beginReadinessHTTP(t.Context(), handler)
	<-started
	second := beginReadinessHTTP(t.Context(), handler)
	requireReadinessHTTP(t, first, http.StatusServiceUnavailable)
	requireReadinessHTTP(t, second, http.StatusServiceUnavailable)
	for range 3 {
		requireReadinessHTTP(t, beginReadinessHTTP(t.Context(), handler), http.StatusServiceUnavailable)
	}
	if calls.Load() != 1 {
		t.Fatalf("stalled readiness started %d checks", calls.Load())
	}

	releaseProbe()
	deadline := time.Now().Add(2 * readinessCachePeriod)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))
		if response.Code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness did not recover after the dependency returned")
		}
		<-ticker.C
	}
	if calls.Load() != 2 {
		t.Fatalf("recovery ran %d checks", calls.Load())
	}
}

func TestGatewayReadinessStopsWaitingDuringShutdown(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	defer close(release)

	gw := New()
	gw.AddReadinessCheck(func(ctx context.Context) error {
		started <- ctx
		<-release
		return nil
	})
	handler := gw.startReadiness()
	defer handler.stop()
	request := beginReadinessHTTP(t.Context(), handler)
	probeContext := <-started
	handler.stop()
	requireReadinessHTTP(t, request, http.StatusServiceUnavailable)
	if !errors.Is(probeContext.Err(), context.Canceled) {
		t.Fatalf("dependency did not inherit gateway shutdown: %v", probeContext.Err())
	}
	requireReadinessHTTP(t, beginReadinessHTTP(t.Context(), handler), http.StatusServiceUnavailable)
}

func TestGatewayReadinessKeepsWorkerPanicPrivate(t *testing.T) {
	for _, extraCheck := range []bool{false, true} {
		gw := New()
		gw.AddReadinessCheck(func(context.Context) error { panic("private dependency diagnostic") })
		if extraCheck {
			gw.AddReadinessCheck(func(context.Context) error { return nil })
		}
		response := httptest.NewRecorder()
		testReadinessHandler(t, gw).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))
		if response.Code != http.StatusServiceUnavailable || response.Body.String() != `{"status":"unavailable"}` {
			t.Fatalf("readiness disclosed a dependency failure: %d %q", response.Code, response.Body.String())
		}
	}
}

func TestGatewayReadinessMuxRequiresEveryDependencyToReturn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		exit     bool
		waitPeer bool
		want     int
	}{
		{"all-return", false, false, http.StatusOK},
		{"worker-goexit", true, false, http.StatusServiceUnavailable},
		{"worker-goexit-cancels-peer", true, true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORS_ORIGIN", "*")
			synctest.Test(t, func(t *testing.T) {
				gw := New()
				peerRan := make(chan struct{})
				gw.AddReadinessCheck(func(context.Context) error {
					<-peerRan
					if tc.exit {
						runtime.Goexit()
					}
					return nil
				})
				gw.AddReadinessCheck(func(ctx context.Context) error {
					close(peerRan)
					if tc.waitPeer {
						<-ctx.Done()
						return ctx.Err()
					}
					return nil
				})
				mux := buildTestReadinessMux(t, gw)
				started := time.Now()
				for range 2 {
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ready", nil))
					if response.Code != tc.want || response.Header().Get("Cache-Control") != "no-store" {
						t.Fatalf("readiness = %d %q, want %d", response.Code, response.Body.String(), tc.want)
					}
					if tc.exit && response.Body.String() != `{"status":"unavailable"}` {
						t.Fatalf("abnormal readiness disclosed an unexpected body: %q", response.Body.String())
					}
				}
				if tc.waitPeer && time.Since(started) >= readinessTimeout {
					t.Fatal("abnormal probe exit waited for the shared deadline")
				}
				liveness := httptest.NewRecorder()
				mux.ServeHTTP(liveness, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil))
				if liveness.Code != http.StatusOK {
					t.Fatalf("liveness after readiness = %d, want 200", liveness.Code)
				}
			})
		})
	}
}

func TestGatewayReadinessMuxKeepsConcurrencyAndDeadlineBounded(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "*")
	synctest.Test(t, func(t *testing.T) {
		gw := New()
		release := make(chan struct{})
		releaseChecks := sync.OnceFunc(func() { close(release) })
		defer releaseChecks()

		var started atomic.Int32
		for range maxReadinessChecks + 2 {
			gw.AddReadinessCheck(func(context.Context) error {
				started.Add(1)
				// Hold the dependency workers past their shared HTTP deadline.
				<-release
				return nil
			})
		}
		mux := buildTestReadinessMux(t, gw)
		first := beginReadinessHTTP(t.Context(), mux)
		second := beginReadinessHTTP(t.Context(), mux)
		synctest.Wait()
		if got := started.Load(); got != maxReadinessChecks {
			t.Fatalf("running readiness checks = %d, want %d", got, maxReadinessChecks)
		}
		requireReadinessHTTP(t, first, http.StatusServiceUnavailable)
		requireReadinessHTTP(t, second, http.StatusServiceUnavailable)
		releaseChecks()
		synctest.Wait()
		if got := started.Load(); got != maxReadinessChecks {
			t.Fatalf("readiness started a new dependency after timeout: %d", got)
		}
	})
}

func TestGatewayReadinessSkipsWorkerCancelledBeforeStart(t *testing.T) {
	for _, count := range []int{1, 2} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var called atomic.Bool
		readiness := gatewayReadiness{lifetime: ctx}
		for range count {
			readiness.checks = append(readiness.checks, func(context.Context) error {
				called.Store(true)
				return nil
			})
		}

		readiness.mu.Lock()
		pending := readiness.startProbeLocked()
		readiness.mu.Unlock()
		<-pending.done
		if pending.ready || called.Load() {
			t.Fatalf("%d cancelled checks: ready=%v, called=%v", count, pending.ready, called.Load())
		}
	}
}

func TestGatewayReadinessLifetimesRemainIndependent(t *testing.T) {
	for _, test := range []struct {
		name  string
		check func(context.Context) error
	}{
		{name: "stateless"},
		{name: "checked", check: func(context.Context) error { return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			gw := New()
			gw.AddReadinessCheck(test.check)
			first := gw.startReadiness()
			t.Cleanup(first.stop)
			second := gw.startReadiness()
			t.Cleanup(second.stop)
			requireReadinessHTTP(t, beginReadinessHTTP(t.Context(), first), http.StatusOK)
			first.stop()
			requireReadinessHTTP(t, beginReadinessHTTP(t.Context(), first), http.StatusServiceUnavailable)
			requireReadinessHTTP(t, beginReadinessHTTP(t.Context(), second), http.StatusOK)
		})
	}
}

func testReadinessHandler(t testing.TB, gw *Gateway) http.Handler {
	t.Helper()
	readiness := gw.startReadiness()
	t.Cleanup(readiness.stop)
	return readiness
}

func buildTestReadinessMux(t *testing.T, gw *Gateway) *http.ServeMux {
	t.Helper()
	mux, _, err := gw.buildMux(testReadinessHandler(t, gw), "test")
	if err != nil {
		t.Fatal(err)
	}
	return mux
}

func beginReadinessHTTP(ctx context.Context, handler http.Handler) <-chan int {
	result := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/ready", nil))
		result <- response.Code
	}()
	return result
}

func requireReadinessHTTP(t *testing.T, result <-chan int, expected int) {
	t.Helper()
	select {
	case status := <-result:
		if status != expected {
			t.Fatalf("readiness = %d, expected %d", status, expected)
		}
	case <-time.After(2 * readinessTimeout):
		t.Fatal("HTTP readiness waiter outlived the shared probe deadline")
	}
}
