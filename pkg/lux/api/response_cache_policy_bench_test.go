package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type policyBenchmarkBackend struct {
	cache.Nop
	hit bool
}

var policyBenchmarkValue = []byte{84}

func (backend policyBenchmarkBackend) Get(context.Context, string) ([]byte, error) {
	if backend.hit {
		return policyBenchmarkValue, nil
	}
	return nil, nil
}

// Compare the pre-policy body, the shared legacy entry and the compiled entry.
// The fixed backend isolates dispatch/key costs: no Redis, expiry or copying by
// the backend is measured. All cases validate the same emitted response.
func BenchmarkCompiledCachePolicy(b *testing.B) {
	for _, hit := range []bool{false, true} {
		for _, variant := range []string{"before", "legacy", "named"} {
			b.Run(fmt.Sprintf("hit=%t/%s", hit, variant), func(b *testing.B) {
				benchmarkCompiledCachePolicy(b, hit, variant)
			})
		}
	}
}

func benchmarkCompiledCachePolicy(b *testing.B, hit bool, variant string) {
	policy := &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Minute, Scope: cachepolicy.Private}
	router := namedPolicyRouter(b, policyBenchmarkBackend{hit: hit}, policy)
	if err := router.Prepare(); err != nil {
		b.Fatal(err)
	}
	// Reconstruct the historical plan only for test-local baseline execution.
	if variant != "named" {
		router.cacheBindings["cached"].plan.Load().policy = nil
	}
	req := &Request{API: "cached", BinaryMode: true, binaryParams: []byte{1, 84, 0},
		Buf: &ResponseBuf{B: make([]byte, 0, 16)}, responseCache: router.cacheBindings["cached"]}
	execute := func() error { req.Buf.B = append(req.Buf.B, 84); return nil }
	ctx := b.Context()
	run := func() error { return cacheResponseLegacyForBenchmark(ctx, req, "verified", time.Minute, execute) }
	if variant == "before" {
		run = func() error { return cacheResponseBeforePolicy(ctx, req, "verified", time.Minute, execute) }
	} else if variant == "named" {
		run = func() error { return CacheResponsePolicy(ctx, req, "verified", execute) }
	}
	b.ReportAllocs()
	for b.Loop() {
		req.Buf.B = req.Buf.B[:0]
		if err := run(); err != nil || len(req.Buf.B) != 1 || req.Buf.B[0] != 84 {
			b.Fatalf("invalid cache response: %x %v", req.Buf.B, err)
		}
	}
}

// The removed caller-TTL entry remains a benchmark control, not a public API.
func cacheResponseLegacyForBenchmark(ctx context.Context, req *Request, identity string, ttl time.Duration, execute func() error) error {
	route, plan, err := preparedCacheRoute(req)
	if err != nil {
		return err
	}
	if plan.policy != nil {
		return errors.New("api: use the compiled response cache policy instead of a caller-supplied TTL")
	}
	if ttl <= 0 {
		return errors.New("api: response cache TTL must be positive")
	}
	return cacheResponse(ctx, req, identity, ttl, route.cache, route.cache.prefix, execute)
}

// Keep the previous production body intact as a test-only local control.
func cacheResponseBeforePolicy(ctx context.Context, req *Request, identity string, ttl time.Duration, execute func() error) error {
	route := req.responseCache
	if route == nil {
		return errors.New("api: response cache is not bound to this request")
	}
	if route.plan.Load() == nil {
		return errors.New("api: response cache route has not been declared and prepared")
	}
	if route.api != req.API {
		return errors.New("api: response cache route does not match request API")
	}
	if ttl <= 0 {
		return errors.New("api: response cache TTL must be positive")
	}
	binding := route.cache
	key, err := cacheKeyInNamespace(req, identity, binding.prefix)
	if err != nil {
		return err
	}
	if data, err := binding.backend.Get(ctx, key); err == nil && data != nil {
		req.Buf.B = append(req.Buf.B, data...)
		return nil
	}
	startLen := len(req.Buf.B)
	if err := execute(); err != nil {
		return err
	}
	binding.backend.Set(ctx, key, req.Buf.B[startLen:], ttl)
	return nil
}
