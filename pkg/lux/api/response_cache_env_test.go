package api

import (
	"context"
	"errors"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"testing"
)

func setResponseCacheEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("LUXO_CACHE_PROJECT", "project")
	t.Setenv("LUXO_CACHE_ENVIRONMENT", "test")
	t.Setenv("LUXO_CACHE_REVISION", "revision")
	t.Setenv("LUXO_CACHE_ORIGIN_CONCURRENCY", "16")
	t.Setenv("LUXO_CACHE_ORIGIN_MAX_WAITERS", "128")
	t.Setenv("LUXO_CACHE_ORIGIN_WAIT_TIMEOUT", "1s")
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_PASSWORD", "")
}

func TestResponseCacheExplicitEnvironment(t *testing.T) {
	setResponseCacheEnvironment(t)
	if binding, err := NewResponseCacheFromEnv(t.Context(), "service", nil); err == nil || binding != nil {
		t.Fatal("missing backend accepted")
	}
	binding, err := NewResponseCacheFromEnv(t.Context(), "service", cache.OpenMemory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close() })
	t.Setenv("LUXO_CACHE_PROJECT", "")
	opened := false
	_, err = NewResponseCacheFromEnv(t.Context(), "service", func(ctx context.Context) (cache.Cache, error) { opened = true; return cache.OpenMemory(ctx) })
	if err == nil || opened {
		t.Fatal("invalid namespace reached backend construction")
	}
}

func TestResponseCacheEnvironmentRequiresOriginBudget(t *testing.T) {
	for _, field := range []string{"LUXO_CACHE_ORIGIN_CONCURRENCY", "LUXO_CACHE_ORIGIN_MAX_WAITERS", "LUXO_CACHE_ORIGIN_WAIT_TIMEOUT"} {
		for _, value := range []string{"", "invalid", "-1", "999999999999999999999999999"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				setResponseCacheEnvironment(t)
				t.Setenv("LUXO_CACHE_BACKEND", "memory")
				t.Setenv(field, value)
				if binding, err := NewResponseCacheFromEnv(t.Context(), "service", cache.OpenMemory); err == nil || binding != nil {
					t.Fatal("invalid origin budget silently accepted")
				}
			})
		}
	}
	setResponseCacheEnvironment(t)
	t.Setenv("LUXO_CACHE_BACKEND", "memory")
	t.Setenv("LUXO_CACHE_ORIGIN_MAX_WAITERS", "0")
	t.Setenv("LUXO_CACHE_ORIGIN_WAIT_TIMEOUT", "0s")
	binding, err := NewResponseCacheFromEnv(t.Context(), "service", cache.OpenMemory)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
}

type failedOpenCache struct {
	cache.Nop
	closed bool
}

func (c *failedOpenCache) Close() error { c.closed = true; return nil }

func TestResponseCacheOpenerDoesNotFallback(t *testing.T) {
	setResponseCacheEnvironment(t)
	sentinel := errors.New("backend unavailable")
	backend := &failedOpenCache{}
	binding, err := NewResponseCacheFromEnv(t.Context(), "service", func(context.Context) (cache.Cache, error) { return backend, sentinel })
	if binding != nil || !errors.Is(err, sentinel) || !backend.closed {
		t.Fatalf("failed opener leaked or fell back: %v", err)
	}
	binding, err = NewResponseCacheFromEnv(t.Context(), "service", func(context.Context) (cache.Cache, error) { return nil, nil })
	if binding != nil || err == nil {
		t.Fatal("empty opener result accepted")
	}
}
