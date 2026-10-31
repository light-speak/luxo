package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type policyObservedCache struct {
	cache.Cache
	ttls []time.Duration
}

func (c *policyObservedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	c.ttls = append(c.ttls, ttl)
	return c.Cache.Set(ctx, key, value, ttl)
}

func namedPolicyRouter(t testing.TB, backend cache.Cache, policy *schema.CachePolicy) *Router {
	t.Helper()
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "s")})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "cached", ReturnType: "Int", CacheDependencies: &schema.CacheDependencies{}, CachePolicy: policy})
	rt.RequireCache("cached")
	rt.Handle("cached", func(context.Context, *Request) error { return nil })
	return rt
}

func TestResponseCacheRequiresExplicitPolicyAtStartup(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		rt := namedPolicyRouter(t, cache.Nop{}, nil)
		var err error
		if prepare {
			err = rt.Prepare()
		} else {
			err = rt.Validate()
		}
		if err == nil || !strings.Contains(err.Error(), "explicit") {
			t.Fatalf("cache without explicit policy accepted: prepare=%t err=%v", prepare, err)
		}
		if rt.cacheBindings["cached"].plan.Load() != nil {
			t.Fatal("missing policy published an executable plan")
		}
		rt.Schema.APIs["cached"].CachePolicy = testPrivateCachePolicy(time.Minute)
		if err := rt.Prepare(); err != nil {
			t.Fatalf("startup policy repair failed: %v", err)
		}
		if rt.cacheBindings["cached"].plan.Load().policy == nil {
			t.Fatal("repaired policy was not published")
		}
	}
}

func TestNamedPolicyRejectsUnsupportedCapabilities(t *testing.T) {
	for _, sample := range []struct {
		policy schema.CachePolicy
		want   string
	}{
		{schema.CachePolicy{}, "mode"},
		{schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Second}, "scope"},
		{schema.CachePolicy{Mode: cachepolicy.TTL, Scope: cachepolicy.Private}, "ttl"},
		{schema.CachePolicy{Mode: cachepolicy.Mode("invalidation"), TTL: time.Second, Scope: cachepolicy.Private}, "invalidation"},
		{schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Second, Scope: cachepolicy.Public}, "public"},
	} {
		rt := namedPolicyRouter(t, cache.Nop{}, &sample.policy)
		if err := rt.Prepare(); err == nil || !strings.Contains(err.Error(), sample.want) {
			t.Fatalf("unsupported policy accepted: %v, want %s", err, sample.want)
		}
		if rt.cacheBindings["cached"].plan.Load() != nil {
			t.Fatal("failed preparation published a plan")
		}
	}
}

func TestNamedPolicyOwnsTTLAndPreservesPrivateIsolation(t *testing.T) {
	backend := &policyObservedCache{Cache: cache.NewMemory()}
	t.Cleanup(func() { _ = backend.Close() })
	policy := &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Minute, Scope: cachepolicy.Private}
	rt := namedPolicyRouter(t, backend, policy)
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	policy.TTL = time.Hour
	calls := 0
	for _, identity := range []string{"verified-a", "verified-a", "verified-b"} {
		req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: rt.cacheBindings["cached"]}
		err := CacheResponsePolicy(t.Context(), req, identity, func() error {
			calls++
			req.Buf.B = append(req.Buf.B, byte(calls))
			return nil
		})
		if err != nil || len(req.Buf.B) != 1 {
			t.Fatalf("policy request: %x, %v", req.Buf.B, err)
		}
	}
	if calls != 2 || len(backend.ttls) != 2 || backend.ttls[0] != time.Minute || backend.ttls[1] != time.Minute {
		t.Fatalf("lost immutable policy or identity partition: calls=%d ttls=%v", calls, backend.ttls)
	}
}

func TestNamedPolicyMissingBindingNeverExecutes(t *testing.T) {
	rt := namedPolicyRouter(t, cache.Nop{}, testPrivateCachePolicy(time.Minute))
	unprepared := namedPolicyRouter(t, cache.Nop{}, &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Second, Scope: cachepolicy.Private})
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	// Exercise the defensive request guard without permitting missing policy at startup.
	rt.cacheBindings["cached"].plan.Load().policy = nil
	for _, req := range []*Request{
		{API: "cached", Buf: &ResponseBuf{}},
		{API: "cached", Buf: &ResponseBuf{}, responseCache: unprepared.cacheBindings["cached"]},
		{API: "wrong", Buf: &ResponseBuf{}, responseCache: rt.cacheBindings["cached"]},
		{API: "cached", Buf: &ResponseBuf{}, responseCache: rt.cacheBindings["cached"]},
	} {
		if err := CacheResponsePolicy(t.Context(), req, "", func() error { t.Fatal("unbound policy executed"); return nil }); err == nil {
			t.Fatal("missing compiled policy accepted")
		}
	}
}

func TestNamedPolicyChangeSeparatesStoredResults(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	for _, ttl := range []time.Duration{time.Minute, time.Second} {
		rt := namedPolicyRouter(t, backend, &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: ttl, Scope: cachepolicy.Private})
		if err := rt.Prepare(); err != nil {
			t.Fatal(err)
		}
		if got := len(rt.cacheBindings["cached"].plan.Load().policy.prefix); got != len(rt.responseCache.prefix) {
			t.Fatalf("compiled policy enlarged request cache keys: %d instead of %d", got, len(rt.responseCache.prefix))
		}
		called := false
		req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: rt.cacheBindings["cached"]}
		if err := CacheResponsePolicy(t.Context(), req, "", func() error { called = true; req.Buf.B = append(req.Buf.B, 1); return nil }); err != nil || !called {
			t.Fatalf("changed contract reused earlier cache: %v, executed=%v", err, called)
		}
	}
}

func TestTTLExternalDependencyNeedsNoInvalidationInfrastructure(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	rt := namedPolicyRouter(t, backend, testPrivateCachePolicy(time.Minute))
	rt.Schema.APIs["cached"].CacheDependencies.External = []string{"pricing/rates"}
	if err := rt.Prepare(); err != nil {
		t.Fatalf("TTL-only external dependency required invalidation infrastructure: %v", err)
	}
	calls := 0
	for range 2 {
		req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: rt.cacheBindings["cached"]}
		err := CacheResponsePolicy(t.Context(), req, "verified-user", func() error {
			calls++
			req.Buf.B = append(req.Buf.B, 42)
			return nil
		})
		if err != nil || len(req.Buf.B) != 1 || req.Buf.B[0] != 42 {
			t.Fatalf("TTL response: %x, %v", req.Buf.B, err)
		}
	}
	if calls != 1 {
		t.Fatalf("TTL response did not hit cache: executed %d times", calls)
	}
}
