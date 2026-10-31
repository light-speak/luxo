package api

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

type cancellationCacheBackend struct {
	cache.Nop
	read func(context.Context) ([]byte, error)
	sets int
}

func (c *cancellationCacheBackend) Get(ctx context.Context, _ string) ([]byte, error) {
	return c.read(ctx)
}

func (c *cancellationCacheBackend) Set(context.Context, string, []byte, time.Duration) error {
	c.sets++
	return nil
}

func TestResponseCacheCanceledMissDoesNotStartOrigin(t *testing.T) {
	for _, result := range []string{"miss", "backend-error", "context-error"} {
		t.Run(result, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := &cancellationCacheBackend{read: func(context.Context) ([]byte, error) {
				cancel()
				switch result {
				case "backend-error":
					return nil, errors.New("backend unavailable")
				case "context-error":
					return nil, ctx.Err()
				default:
					return nil, nil
				}
			}}
			assertCanceledCacheMiss(t, ctx, backend, context.Canceled)
		})
	}
}

func TestResponseCacheExpiredMissDoesNotStartOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		backend := &cancellationCacheBackend{read: func(context.Context) ([]byte, error) {
			time.Sleep(2 * time.Second)
			return nil, ctx.Err()
		}}
		assertCanceledCacheMiss(t, ctx, backend, context.DeadlineExceeded)
	})
}

func assertCanceledCacheMiss(t *testing.T, ctx context.Context, backend *cancellationCacheBackend, want error) {
	t.Helper()
	req := &Request{API: "cached", Buf: &ResponseBuf{B: []byte("prefix")}, responseCache: testCacheRoute(t, backend, "test", "cached")}
	called := false
	err := CacheResponsePolicy(ctx, req, "", func() error {
		called = true
		req.Buf.B = append(req.Buf.B, "origin"...)
		return nil
	})
	if !errors.Is(err, want) || called || backend.sets != 0 || string(req.Buf.B) != "prefix" {
		t.Fatalf("canceled miss reached origin: err=%v called=%t sets=%d body=%q", err, called, backend.sets, req.Buf.B)
	}
}

func TestResponseCacheLiveMissStillRunsOrigin(t *testing.T) {
	// A backend's own timeout is not cancellation of the parent request.
	for _, failure := range []error{errors.New("backend unavailable"), context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			backend := &cancellationCacheBackend{read: func(context.Context) ([]byte, error) {
				return nil, failure
			}}
			req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: testCacheRoute(t, backend, "test", "cached")}
			called := false
			err := CacheResponsePolicy(t.Context(), req, "", func() error { called = true; return nil })
			if err != nil || !called || backend.sets != 1 {
				t.Fatalf("live fallback changed: %v called=%t sets=%d", err, called, backend.sets)
			}
		})
	}
}
