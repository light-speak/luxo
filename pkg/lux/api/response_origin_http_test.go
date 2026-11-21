package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func originBudgetRouter(t *testing.T, binding *ResponseCache) *Router {
	t.Helper()
	router := NewRouterWithOptions(RouterOptions{ResponseCache: binding})
	for i, name := range []string{"first", "second", "plain"} {
		definition := &schema.API{ID: i + 1, Name: name, ReturnType: "Int"}
		if name != "plain" {
			definition.CachePolicy = testPrivateCachePolicy(time.Minute)
			definition.CacheDependencies = &schema.CacheDependencies{}
			router.RequireCache(name)
		}
		if err := router.Schema.RegisterAPI(definition); err != nil {
			t.Fatal(err)
		}
		router.Handle(name, func(ctx context.Context, req *Request) error {
			execute := func() error { req.Buf.B = codec.AppendSvarint(req.Buf.B, 42); return nil }
			if name == "plain" {
				return execute()
			}
			return CacheResponsePolicy(ctx, req, "", execute)
		})
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func requestOriginBudget(t *testing.T, router *Router, name string, binary bool, want int) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"$api":%q}`, name))
	if binary {
		body = mustEncodeBinaryRequest(t, router.Schema.APIs[name].ID, nil, nil, nil)
	}
	request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if binary {
		request.Header.Set("Content-Type", "application/x-luxo")
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s binary=%t: HTTP %d want %d: %s", name, binary, response.Code, want, response.Body.String())
	}
	if want == 503 && !bytes.Contains(response.Body.Bytes(), []byte("CacheOriginBusy")) {
		t.Fatalf("missing explicit overload error: %s", response.Body.String())
	}
}

func TestResponseOriginHTTPHitBypassesSharedBudget(t *testing.T) {
	for _, binary := range []bool{false, true} {
		backend := cache.NewMemory()
		t.Cleanup(func() { _ = backend.Close() })
		binding, err := NewResponseCache(backend, CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}, ResponseOriginOptions{MaxConcurrent: 1})
		if err != nil {
			t.Fatal(err)
		}
		first, second := originBudgetRouter(t, binding), originBudgetRouter(t, binding)
		requestOriginBudget(t, first, "first", binary, 200)
		if err := binding.origin.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		requestOriginBudget(t, second, "first", binary, 200)
		requestOriginBudget(t, second, "second", binary, 503)
		requestOriginBudget(t, second, "plain", binary, 200)
		if len(binding.origin.slots) != 1 {
			t.Fatal("hit or uncached request changed origin admission")
		}
		binding.origin.release()
		requestOriginBudget(t, second, "second", binary, 200)
	}
}

func TestResponseOriginHTTPWaitTimeoutIsUnavailable(t *testing.T) {
	for _, binary := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			binding, err := NewResponseCache(cache.Nop{}, CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}, ResponseOriginOptions{MaxConcurrent: 1, MaxWaiting: 1, WaitTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			router := originBudgetRouter(t, binding)
			if err := binding.origin.acquire(t.Context()); err != nil {
				t.Fatal(err)
			}
			requestOriginBudget(t, router, "first", binary, 503)
			binding.origin.release()
			requestOriginBudget(t, router, "first", binary, 200)
		})
	}
}

type originSetObserver struct {
	cache.Nop
	origin *responseOrigin
	t      *testing.T
}

func (backend originSetObserver) Set(context.Context, string, []byte, time.Duration) error {
	if len(backend.origin.slots) != 0 {
		backend.t.Error("cache publication still holds database admission")
	}
	return nil
}

func TestResponseOriginReleasesBeforeCachePublication(t *testing.T) {
	backend := &originSetObserver{t: t}
	binding := testResponseCache(t, backend, "s")
	backend.origin = binding.origin
	requestOriginBudget(t, originBudgetRouter(t, binding), "first", false, 200)
}
