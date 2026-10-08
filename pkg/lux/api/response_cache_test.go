package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func testPrivateCachePolicy(ttl time.Duration) *schema.CachePolicy {
	return &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: ttl, Scope: cachepolicy.Private}
}

func testResponseCache(t testing.TB, backend cache.Cache, service string) *ResponseCache {
	t.Helper()
	c, err := NewResponseCache(backend, CacheNamespace{Project: "project", Environment: "test", Service: service, Revision: "revision"}, testResponseOriginOptions())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testResponseOriginOptions() ResponseOriginOptions {
	return ResponseOriginOptions{MaxConcurrent: 16, MaxWaiting: 128, WaitTimeout: time.Second}
}

func testCacheRoute(t testing.TB, backend cache.Cache, service, name string) *cacheRouteBinding {
	t.Helper()
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, service)})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: name, ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
	rt.RequireCache(name)
	rt.Handle(name, func(context.Context, *Request) error { return nil })
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	return rt.cacheBindings[name]
}

type observedResponseCache struct {
	cache.Nop
	gets, sets int
}

func (c *observedResponseCache) Get(context.Context, string) ([]byte, error) {
	c.gets++
	return nil, nil
}

func (c *observedResponseCache) Set(context.Context, string, []byte, time.Duration) error {
	c.sets++
	return nil
}

func TestResponseCacheRequiresPreparedRoute(t *testing.T) {
	for _, state := range []string{"undeclared", "unprepared", "validate-only", "failed-prepare", "wrong-api", "prepared"} {
		t.Run(state, func(t *testing.T) {
			backend := &observedResponseCache{}
			rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "test")})
			rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
			calls := 0
			rt.Handle("cached", func(ctx context.Context, req *Request) error {
				return CacheResponsePolicy(ctx, req, "", func() error { calls++; return nil })
			})
			if state != "undeclared" {
				rt.RequireCache("cached")
			}
			if state == "failed-prepare" {
				rt.RequireStream("missing")
				if err := rt.Prepare(); err == nil {
					t.Fatal("missing stream accepted")
				}
			} else if state == "validate-only" {
				if err := rt.Validate(); err != nil {
					t.Fatal(err)
				}
			} else if state != "unprepared" {
				if err := rt.Prepare(); err != nil {
					t.Fatal(err)
				}
			}
			req := &Request{API: "cached", Buf: &ResponseBuf{}}
			if state == "wrong-api" {
				req.API = "another"
			}
			err := rt.ExportHandlers()["cached"](t.Context(), req)
			if state == "prepared" {
				if err != nil || calls != 1 || backend.gets != 1 || backend.sets != 1 {
					t.Fatalf("prepared route: %v, calls=%d, backend=%+v", err, calls, backend)
				}
			} else if err == nil || calls != 0 || backend.gets != 0 || backend.sets != 0 {
				t.Fatalf("unprepared/mismatched route reached cache: %v, calls=%d, backend=%+v", err, calls, backend)
			}
			if req.responseCache != nil {
				t.Fatal("request retained binding")
			}
		})
	}
}

func TestResponseCachePreparationRetryAndReplacedHandler(t *testing.T) {
	backend := &observedResponseCache{}
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "test")})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
	rt.RequireCache("cached")
	handler := func(ctx context.Context, req *Request) error {
		return CacheResponsePolicy(ctx, req, "", func() error { return nil })
	}
	rt.Handle("cached", handler)
	old := rt.ExportHandlers()["cached"]
	rt.Handle("cached", handler)
	current := rt.ExportHandlers()["cached"]
	rt.RequireStream("pending")
	if err := rt.Prepare(); err == nil {
		t.Fatal("missing stream accepted")
	}
	rt.HandleStream("pending", nil)
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := old(t.Context(), &Request{API: "cached", Buf: &ResponseBuf{}}); err == nil {
		t.Fatal("replaced handler inherited another registration's plan")
	}
	if backend.gets != 0 || backend.sets != 0 {
		t.Fatal("old handler accessed backend")
	}
	if err := current(t.Context(), &Request{API: "cached", Buf: &ResponseBuf{}}); err != nil {
		t.Fatal(err)
	}
	if backend.gets != 1 || backend.sets != 1 {
		t.Fatal("retry failed to publish current plan")
	}
}

func TestResponseCacheConcurrentPreparationPublication(t *testing.T) {
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, cache.Nop{}, "test")})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
	rt.RequireCache("cached")
	rt.Handle("cached", func(ctx context.Context, req *Request) error {
		return CacheResponsePolicy(ctx, req, "", func() error { return nil })
	})
	handler := rt.ExportHandlers()["cached"]
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				req := &Request{API: "cached", Buf: &ResponseBuf{}}
				// An early request may be rejected, but it must not race publication.
				if err := handler(t.Context(), req); err != nil && !strings.Contains(err.Error(), "prepared") {
					t.Error(err)
				}
				if req.responseCache != nil {
					t.Error("binding leaked")
				}
			}
		})
	}
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if err := handler(t.Context(), &Request{API: "cached", Buf: &ResponseBuf{}}); err != nil {
		t.Fatal(err)
	}
}

func TestResponseCacheHTTPRejectsUnpreparedRoute(t *testing.T) {
	backend := &observedResponseCache{}
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "test")})
	rt.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
	rt.Registry.RegisterSchemaAPIs(rt.Schema)
	rt.RequireCache("cached")
	rt.Handle("cached", func(ctx context.Context, req *Request) error {
		return CacheResponsePolicy(ctx, req, "", func() error { t.Error("unprepared operation executed"); return nil })
	})
	for _, binaryMode := range []bool{false, true} {
		body := []byte(`{"$api":"cached"}`)
		if binaryMode {
			body = []byte{1, 0, 0}
		}
		req := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if binaryMode {
			req.Header.Set("X-Luxo-Mode", "binary")
		}
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, req)
		if w.Code != 500 {
			t.Fatalf("unprepared route returned HTTP %d: %s", w.Code, w.Body.Bytes())
		}
	}
	if backend.gets != 0 || backend.sets != 0 {
		t.Fatal("unprepared HTTP route accessed backend")
	}
}

func TestResponseCacheNamespaceIsolation(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	base := CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}
	variations := []CacheNamespace{base, {Project: "p2", Environment: "e", Service: "s", Revision: "r"}, {Project: "p", Environment: "e2", Service: "s", Revision: "r"}, {Project: "p", Environment: "e", Service: "s2", Revision: "r"}, {Project: "p", Environment: "e", Service: "s", Revision: "r2"}}
	for i, scope := range variations {
		c, err := NewResponseCache(backend, scope, testResponseOriginOptions())
		if err != nil {
			t.Fatal(err)
		}
		rt := NewRouterWithOptions(RouterOptions{ResponseCache: c})
		rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "same", ReturnType: "String", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
		rt.RequireCache("same")
		calls := 0
		want := string(rune('a' + i))
		rt.Handle("same", func(ctx context.Context, req *Request) error {
			return CacheResponsePolicy(ctx, req, "identity", func() error { calls++; req.Buf.B = append(req.Buf.B, want...); return nil })
		})
		if err := rt.Prepare(); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			req := &Request{API: "same", Buf: &ResponseBuf{}}
			if err := rt.ExportHandlers()["same"](t.Context(), req); err != nil || string(req.Buf.B) != want {
				t.Fatalf("scope %d: got %q/%v", i, req.Buf.B, err)
			}
			if req.responseCache != nil {
				t.Fatal("handler retained request binding")
			}
		}
		if calls != 1 {
			t.Fatalf("scope %d executed %d times", i, calls)
		}
	}
}

func TestResponseCacheRequiresConfiguration(t *testing.T) {
	rt := NewRouterWithOptions(RouterOptions{})
	rt.RequireCache("cached")
	if err := rt.Validate(); err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("missing cache accepted: %v", err)
	}
	called := false
	err := CacheResponsePolicy(t.Context(), &Request{Buf: &ResponseBuf{}}, "", func() error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("unbound cache executed: %v", err)
	}
}

func TestResponseCacheInvalidNamespace(t *testing.T) {
	for _, scope := range []CacheNamespace{{}, {Project: "p"}, {Project: "p", Environment: "e"}, {Project: "p", Environment: "e", Service: "s"}, {Project: "p", Environment: "e", Service: "s", Revision: " "}, {Project: "\xff", Environment: "e", Service: "s", Revision: "r"}} {
		if _, err := NewResponseCache(cache.Nop{}, scope, testResponseOriginOptions()); err == nil {
			t.Fatalf("invalid scope accepted: %+v", scope)
		}
	}
	if _, err := NewResponseCache(nil, CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}, testResponseOriginOptions()); err == nil {
		t.Fatal("nil backend accepted")
	}
}

func TestResponseCacheHTTPApplicationIsolation(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	for index, service := range []string{"first", "second"} {
		router := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, service)})
		router.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}})
		router.RequireCache("cached")
		router.Registry.RegisterSchemaAPIs(router.Schema)
		value := int64(42 + index)
		router.Handle("cached", func(ctx context.Context, req *Request) error {
			return CacheResponsePolicy(ctx, req, "same-verified-user", func() error { req.Buf.B = codec.AppendSvarint(req.Buf.B, value); return nil })
		})
		if err := router.Prepare(); err != nil {
			t.Fatal(err)
		}
		for _, binaryMode := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/binary=%t", service, binaryMode), func(t *testing.T) {
				t.Parallel()
				for range 10 {
					body, want := []byte(`{"$api":"cached"}`), []byte(fmt.Sprintf(`{"data":%d}`, value))
					if binaryMode {
						body = []byte{1, 0, 0}
						want = codec.AppendSvarint(nil, value)
					}
					r := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					if binaryMode {
						r.Header.Set("X-Luxo-Mode", "binary")
					}
					w := httptest.NewRecorder()
					router.ServeHTTP(w, r)
					if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), want) {
						t.Fatalf("cross-application response: %d %q", w.Code, w.Body.Bytes())
					}
				}
			})
		}
	}
}

func TestResponseCacheRejectsInvalidRuntimeTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		rt := namedPolicyRouter(t, cache.Nop{}, testPrivateCachePolicy(ttl))
		if err := rt.Prepare(); err == nil {
			t.Fatal("invalid TTL accepted")
		}
		if rt.cacheBindings["cached"].plan.Load() != nil {
			t.Fatal("invalid TTL published a plan")
		}
	}
}

func TestResponseCacheRestoresBindingOnPanic(t *testing.T) {
	outer := testCacheRoute(t, cache.Nop{}, "outer", "outer")
	inner := testCacheRoute(t, cache.Nop{}, "inner", "inner")
	req := &Request{responseCache: outer}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected handler panic")
			}
		}()
		_ = bindResponseCache(inner, func(context.Context, *Request) error { panic("handler failure") })(t.Context(), req)
	}()
	if req.responseCache != outer {
		t.Fatal("nested handler leaked binding")
	}
}
