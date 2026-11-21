package api

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Keep the former body as a local control with exactly the same binding, key,
// callback and backend. Neither variant includes network or origin I/O.
func cacheResponseBeforeCancellation(ctx context.Context, req *Request, identity string, ttl time.Duration, binding *ResponseCache, prefix string, execute func() error) error {
	key, err := cacheKeyInNamespace(req, identity, prefix)
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

// Freeze the cancellation-only body so later origin-budget work cannot silently
// change what the independent cancellation benchmark measures.
func cacheResponseBeforeOriginLimit(ctx context.Context, req *Request, identity string, ttl time.Duration, binding *ResponseCache, prefix string, execute func() error) error {
	key, err := cacheKeyInNamespace(req, identity, prefix)
	if err != nil {
		return err
	}
	if data, err := binding.backend.Get(ctx, key); err == nil && data != nil {
		req.Buf.B = append(req.Buf.B, data...)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	startLen := len(req.Buf.B)
	if err := execute(); err != nil {
		return err
	}
	binding.backend.Set(ctx, key, req.Buf.B[startLen:], ttl)
	return nil
}

type cacheResponseBenchmarkCall func(context.Context, *Request, string, time.Duration, *ResponseCache, string, func() error) error

func BenchmarkCacheOriginCancellation(b *testing.B) {
	benchmarkCacheResponseVariants(b, cacheResponseBeforeCancellation, cacheResponseBeforeOriginLimit)
}

func BenchmarkCacheOriginBudget(b *testing.B) {
	benchmarkCacheResponseVariants(b, cacheResponseBeforeOriginLimit, cacheResponse)
}

func benchmarkCacheResponseVariants(b *testing.B, before, after cacheResponseBenchmarkCall) {
	for _, kind := range []string{"background", "cancelable", "deadline"} {
		for _, hit := range []bool{false, true} {
			for _, variant := range []string{"before", "after"} {
				b.Run(fmt.Sprintf("%s/hit=%t/%s", kind, hit, variant), func(b *testing.B) {
					run := before
					if variant == "after" {
						run = after
					}
					benchmarkCacheResponseCall(b, kind, hit, run)
				})
			}
		}
	}
}

func benchmarkCacheResponseCall(b *testing.B, kind string, hit bool, run cacheResponseBenchmarkCall) {
	ctx := context.Background()
	if kind == "cancelable" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	} else if kind == "deadline" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Hour)
		defer cancel()
	}
	binding := testResponseCache(b, policyBenchmarkBackend{hit: hit}, "test")
	req := &Request{API: "cached", BinaryMode: true, binaryParams: []byte{1, 84, 0}, Buf: &ResponseBuf{B: make([]byte, 0, 16)}}
	execute := func() error { req.Buf.B = append(req.Buf.B, 84); return nil }
	b.ReportAllocs()
	for b.Loop() {
		req.Buf.B = req.Buf.B[:0]
		if err := run(ctx, req, "verified", time.Minute, binding, binding.prefix, execute); err != nil || len(req.Buf.B) != 1 || req.Buf.B[0] != 84 {
			b.Fatalf("invalid response: %x %v", req.Buf.B, err)
		}
	}
}
