package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
)

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
