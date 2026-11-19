package rpc

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestServerCacheRejectsInvalidOriginResponses(t *testing.T) {
	for _, scenario := range []string{"replaced", "nil", "empty_then_valid", "zero"} {
		t.Run(scenario, func(t *testing.T) { testServerCacheIntegrity(t, scenario) })
	}
}

func newIntegrityCacheRPC(t *testing.T, scenario string) (*Client, *cache.MemoryCache, *atomic.Int32) {
	t.Helper()
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	binding, err := api.NewResponseCache(backend, api.CacheNamespace{Project: "p", Environment: "test", Service: "s", Revision: "r"}, api.ResponseOriginOptions{MaxConcurrent: 16})
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", ReturnType: "Int",
		CacheDependencies: &schema.CacheDependencies{}, CachePolicy: &schema.CachePolicy{Mode: "ttl", TTL: time.Minute, Scope: "private"}}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.RequireCache("cached")
	var calls atomic.Int32
	router.Handle("cached", func(ctx context.Context, req *api.Request) error {
		return api.CacheResponsePolicy(ctx, req, "", func() error {
			return writeIntegrityCacheRPC(req, scenario, calls.Add(1))
		})
	})
	client := NewClient(startTestRPCServer(t, NewServer(router)))
	t.Cleanup(client.Close)
	return client, backend, &calls
}

func writeIntegrityCacheRPC(req *api.Request, scenario string, attempt int32) error {
	switch scenario {
	case "replaced":
		req.Buf = &api.ResponseBuf{B: codec.AppendSvarint(nil, 84)}
	case "nil":
		req.Buf = nil
	case "empty_then_valid":
		if attempt > 1 {
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		}
	case "zero":
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 0)
	}
	return nil
}

func testServerCacheIntegrity(t *testing.T, scenario string) {
	t.Helper()
	client, backend, calls := newIntegrityCacheRPC(t, scenario)
	for attempt := range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		body, err := client.CallWithMaskContext(ctx, "", 1, nil, []byte{0})
		cancel()
		if scenario == "replaced" || scenario == "nil" || scenario == "empty_then_valid" && attempt == 0 {
			var failure *luxerrors.AppError
			if !errors.As(err, &failure) || failure.Code != 500 || failure.Data != nil || len(body) != 0 {
				t.Fatalf("invalid origin became success: attempt=%d body=%x error=%v", attempt, body, err)
			}
		} else {
			want := int64(42)
			if scenario == "zero" {
				want = 0
			}
			if err != nil || !bytes.Equal(body, codec.AppendSvarint(nil, want)) {
				t.Fatal("valid cache response changed", attempt, body, err)
			}
		}
	}
	wantCalls, wantEntries := int32(3), 0
	if scenario == "empty_then_valid" {
		wantCalls, wantEntries = 2, 1
	} else if scenario == "zero" {
		wantCalls, wantEntries = 1, 1
	}
	if calls.Load() != wantCalls || backend.Len() != wantEntries {
		t.Fatal("cache retained an invalid response or failed to hit", calls.Load(), backend.Len())
	}
}
