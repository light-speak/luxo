package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestWithRateLimit(t *testing.T) {
	called := 0
	handler := func(context.Context, *Request) error {
		called++
		return nil
	}
	limited := WithRateLimit(1, time.Minute, handler)

	if err := limited(context.Background(), &Request{ClientKey: "client-a"}); err != nil {
		t.Fatal(err)
	}
	if err := limited(context.Background(), &Request{ClientKey: "client-a"}); err != luxerrors.RateLimited {
		t.Fatalf("second request error = %v, want RateLimited", err)
	}
	if err := limited(context.Background(), &Request{ClientKey: "client-b"}); err != nil {
		t.Fatalf("different client should have an independent bucket: %v", err)
	}
	if called != 2 {
		t.Fatalf("handler called %d times, want 2", called)
	}
}

func TestWithRateLimitUsesFallbackForEmptyClientKey(t *testing.T) {
	limited := WithRateLimit(1, time.Minute, func(context.Context, *Request) error { return nil })
	if err := limited(context.Background(), &Request{}); err != nil {
		t.Fatal(err)
	}
	if err := limited(context.Background(), &Request{}); err != luxerrors.RateLimited {
		t.Fatalf("second anonymous request error = %v, want RateLimited", err)
	}
}

func TestWithRateLimitSkipsTrustedInternalDispatch(t *testing.T) {
	called := 0
	limited := WithRateLimit(1, time.Minute, func(context.Context, *Request) error {
		called++
		return nil
	})
	req := &Request{Internal: true}

	if err := limited(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := limited(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if called != 2 {
		t.Fatalf("internal handler called %d times, want 2", called)
	}
}

func TestWithCache(t *testing.T) {
	previous := DefaultCache
	SetCache(cache.NewMemory())
	t.Cleanup(func() { SetCache(previous) })

	called := 0
	handler := func(ctx context.Context, req *Request) error {
		called++
		req.Buf.B = append(req.Buf.B, []byte("result")...)
		return nil
	}

	cached := WithCache(time.Minute, handler)
	req := &Request{API: "cacheTest", Buf: GetBuf(), Params: map[string]json.RawMessage{}}

	if err := cached(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("first call should invoke handler, called=%d", called)
	}

	req2 := &Request{API: "cacheTest", Buf: GetBuf(), Params: map[string]json.RawMessage{}}
	if err := cached(context.Background(), req2); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("second call should hit cache, called=%d", called)
	}
}

func TestInvalidateCache(t *testing.T) {
	ctx := context.Background()
	DefaultCache.Set(ctx, "User:test123", []byte("data"), time.Minute)
	InvalidateCache("User")
	if got, _ := DefaultCache.Get(ctx, "User:test123"); got != nil {
		t.Error("should be invalidated")
	}
}

func TestWithCache_Error(t *testing.T) {
	handler := func(ctx context.Context, req *Request) error {
		return fmt.Errorf("db error")
	}
	cached := WithCache(time.Minute, handler)
	req := &Request{API: "errorTest", Buf: GetBuf(), Params: map[string]json.RawMessage{}}
	if err := cached(context.Background(), req); err == nil {
		t.Error("should propagate error")
	}
}

func TestCacheKey(t *testing.T) {
	req1 := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": json.RawMessage(`1`)}}
	req2 := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": json.RawMessage(`2`)}}
	k1 := cacheKey(req1)
	k2 := cacheKey(req2)
	if k1 == k2 {
		t.Error("different params should produce different keys")
	}
}
