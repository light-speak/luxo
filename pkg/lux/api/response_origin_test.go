package api

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func testResponseOrigin(t testing.TB, concurrent, waiting int, timeout time.Duration) *responseOrigin {
	t.Helper()
	origin, err := newResponseOrigin(ResponseOriginOptions{MaxConcurrent: concurrent, MaxWaiting: waiting, WaitTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return origin
}

func requireOriginBusy(t *testing.T, err error) {
	t.Helper()
	var app *luxerrors.AppError
	if !errors.As(err, &app) || app.Code != 503 || app.Name != "CacheOriginBusy" {
		t.Fatalf("want explicit 503 CacheOriginBusy, got %v", err)
	}
}

func TestResponseOriginRejectsInvalidOptions(t *testing.T) {
	for _, options := range []ResponseOriginOptions{
		{}, {MaxConcurrent: -1}, {MaxConcurrent: 1, MaxWaiting: -1},
		{MaxConcurrent: 1, MaxWaiting: 1}, {MaxConcurrent: 1, MaxWaiting: 1, WaitTimeout: -1},
		{MaxConcurrent: 1, WaitTimeout: time.Second},
	} {
		if got, err := newResponseOrigin(options); err == nil || got != nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
}

func TestResponseCacheRejectsMissingOriginBudget(t *testing.T) {
	namespace := CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}
	if binding, err := NewResponseCache(cache.Nop{}, namespace, ResponseOriginOptions{}); err != errResponseOriginConfig || binding != nil {
		t.Fatalf("missing origin budget accepted: %v %v", binding, err)
	}
}

func TestResponseOriginRejectsOverflowAndCanceledAdmission(t *testing.T) {
	origin := testResponseOrigin(t, 1, 0, 0)
	if err := origin.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireOriginBusy(t, origin.acquire(t.Context()))
	origin.release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := origin.acquire(ctx); !errors.Is(err, context.Canceled) || len(origin.slots) != 0 {
		t.Fatalf("canceled admission: %v slots=%d", err, len(origin.slots))
	}
	var unconfigured *responseOrigin
	if err := unconfigured.run(t.Context(), func() error { t.Error("unconfigured origin ran"); return nil }); err == nil {
		t.Fatal("unconfigured origin accepted")
	}
}

func TestResponseOriginBoundsWaitingAndTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := testResponseOrigin(t, 1, 1, time.Second)
		if err := origin.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- origin.acquire(t.Context()) }()
		synctest.Wait()
		if len(origin.waiters) != 1 {
			t.Fatal("missing waiting admission")
		}
		requireOriginBusy(t, origin.acquire(t.Context()))
		time.Sleep(2 * time.Second)
		requireOriginBusy(t, <-result)
		if len(origin.waiters) != 0 || len(origin.slots) != 1 {
			t.Fatal("timeout leaked a waiter or removed another request's slot")
		}
		origin.release()
	})
}

func TestResponseOriginWaitingReleaseAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			origin := testResponseOrigin(t, 1, 1, time.Second)
			if err := origin.acquire(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- origin.acquire(ctx) }()
			synctest.Wait()
			if canceled {
				cancel()
			} else {
				origin.release()
			}
			err := <-result
			if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
				t.Fatalf("waiting result: canceled=%t err=%v", canceled, err)
			}
			if len(origin.waiters) != 0 || len(origin.slots) != 1 {
				t.Fatal("waiting outcome lost accounting")
			}
			origin.release()
		})
	}
}

func TestResponseOriginRunAlwaysReleases(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			origin := testResponseOrigin(t, 1, 0, 0)
			failure := errors.New("business failure")
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() {
					if value := recover(); mode == "panic" && value != failure || mode != "panic" && value != nil {
						t.Errorf("changed business panic: %v", value)
					}
				}()
				err := origin.run(t.Context(), func() error {
					switch mode {
					case "error":
						return failure
					case "panic":
						panic(failure)
					case "goexit":
						runtime.Goexit()
					}
					return nil
				})
				if mode == "error" && err != failure || mode == "success" && err != nil {
					t.Errorf("changed business error: %v", err)
				}
			}()
			<-done
			if len(origin.slots) != 0 {
				t.Fatal("business exit leaked admission")
			}
		})
	}
}

func TestResponseOriginConcurrentLimit(t *testing.T) {
	origin := testResponseOrigin(t, 4, 64, time.Minute)
	var workers sync.WaitGroup
	var active, completed atomic.Int32
	for range 64 {
		workers.Go(func() {
			for range 20 {
				err := origin.run(t.Context(), func() error {
					if active.Add(1) > 4 {
						t.Error("origin concurrency limit exceeded")
					}
					runtime.Gosched()
					active.Add(-1)
					completed.Add(1)
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	if completed.Load() != 1280 || len(origin.slots) != 0 || len(origin.waiters) != 0 {
		t.Fatal("concurrent requests leaked admission")
	}
}

// Trigger real cancellation at the post-admission check, after select has
// chosen a slot. This makes the ready-slot/cancellation race deterministic.
type cancelAtOriginAdmission struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (ctx *cancelAtOriginAdmission) Err() error {
	ctx.checks++
	if ctx.checks == 2 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestResponseOriginRechecksQueuedAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := testResponseOrigin(t, 1, 1, time.Second)
		origin.slots <- struct{}{}
		parent, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx := &cancelAtOriginAdmission{Context: parent, cancel: cancel}
		result := make(chan error, 1)
		go func() { result <- origin.acquire(ctx) }()
		synctest.Wait()
		origin.release()
		if err := <-result; !errors.Is(err, context.Canceled) || len(origin.slots) != 0 || len(origin.waiters) != 0 {
			t.Fatalf("late cancellation retained admission: %v", err)
		}
		if err := originWaitingResult(t.Context(), time.Now()); err != errResponseOriginBusy {
			t.Fatalf("expired waiting budget accepted: %v", err)
		}
	})
}
