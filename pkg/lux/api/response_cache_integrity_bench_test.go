package api

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"unsafe"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// A read-only backend control isolates dispatch from backend copies and I/O.
type cacheIntegrityBackend struct {
	cache.Nop
	value []byte
	hit   bool
}

func (backend cacheIntegrityBackend) Get(context.Context, string) ([]byte, error) {
	if backend.hit {
		return backend.value, nil
	}
	return nil, nil
}

func BenchmarkCacheResponseIntegrity(b *testing.B) {
	for _, sample := range []struct {
		name, result string
		value        []byte
	}{
		{"int", "Int", codec.AppendSvarint(nil, 42)},
		{"bytes4KiB", "Bytes", codec.AppendBytes(nil, make([]byte, 4096))},
		{"void", "", make([]byte, 0)},
	} {
		for _, hit := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/hit=%t", sample.name, hit), func(b *testing.B) {
				backend := cacheIntegrityBackend{value: sample.value, hit: hit}
				router := integrityCacheRouter(b, backend, sample.result, func(context.Context, *Request) error { return nil })
				req := &Request{API: "cached", BinaryMode: true, binaryParams: []byte{0}, responseCache: router.cacheBindings["cached"],
					Buf: &ResponseBuf{B: make([]byte, 0, len(sample.value))}}
				execute := func() error { req.Buf.B = append(req.Buf.B, sample.value...); return nil }
				b.ReportAllocs()
				for b.Loop() {
					req.Buf.B = req.Buf.B[:0]
					if err := CacheResponsePolicy(b.Context(), req, "verified", execute); err != nil || !bytes.Equal(req.Buf.B, sample.value) {
						b.Fatal("cache response changed", err)
					}
				}
			})
		}
	}
}

var cacheIntegrityPlanSink *cacheDependencyPlan

func BenchmarkPrepareCacheResponseIntegrity(b *testing.B) {
	router := integrityCacheRouter(b, cache.Nop{}, "Int", func(context.Context, *Request) error { return nil })
	b.Logf("raw prepared plan size: %d bytes", unsafe.Sizeof(cacheDependencyPlan{}))
	b.ReportAllocs()
	for b.Loop() {
		var err error
		cacheIntegrityPlanSink, err = router.prepareCachePlan("cached", nil)
		if err != nil || cacheIntegrityPlanSink.policy == nil {
			b.Fatal("cache preparation changed", err)
		}
	}
}
