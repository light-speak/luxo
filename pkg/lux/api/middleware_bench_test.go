package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var cacheKeySink string

func BenchmarkCacheRequestKey(b *testing.B) {
	for _, sample := range []struct {
		name    string
		request *Request
	}{
		{"jsonSmall", &Request{API: "cached", Params: map[string]json.RawMessage{"id": []byte("42")}}},
		{"json72KiB", &Request{API: "cached", Params: map[string]json.RawMessage{"document": []byte(`"` + strings.Repeat("中文", 12288) + `"`)}}},
		{"binarySmall", &Request{API: "cached", BinaryMode: true, binaryParams: []byte{1, 84, 0}}},
	} {
		for _, previous := range []bool{true, false} {
			b.Run(fmt.Sprintf("%s/previous=%t", sample.name, previous), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if previous {
						cacheKeySink = cacheKeyBeforeConditions(sample.request)
					} else {
						var err error
						cacheKeySink, err = cacheKey(sample.request, "")
						if err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

// A fixed valid request lets both versions produce the same response. This
// does not excuse the old cache's collisions across different request inputs.
func BenchmarkCacheRequestHTTP(b *testing.B) {
	for _, binaryMode := range []bool{false, true} {
		for _, hit := range []bool{false, true} {
			for _, previous := range []bool{true, false} {
				b.Run(fmt.Sprintf("binary=%t/hit=%t/previous=%t", binaryMode, hit, previous), func(b *testing.B) {
					benchmarkCacheRequestHTTP(b, binaryMode, hit, previous, true)
				})
			}
		}
	}
}

func benchmarkCacheRequestHTTP(b *testing.B, binaryMode, hit, previous, scoped bool) {
	var backend cache.Cache = cache.Nop{}
	if hit {
		backend = cache.NewMemory()
	}
	b.Cleanup(func() { _ = backend.Close() })
	router, body := cacheBenchmarkRouter(b, binaryMode, previous, scoped, backend)
	input := bytes.NewReader(body)
	request := httptest.NewRequest("POST", "/luvia", input)
	want := []byte(`{"data":42}`)
	if binaryMode {
		request.Header.Set("X-Luxo-Mode", "binary")
		request.Header.Set("Content-Type", "application/x-luxo")
		want = codec.AppendSvarint(nil, 42)
	} else {
		request.Header.Set("Content-Type", "application/json")
	}
	check := func() {
		input.Reset(body)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), want) {
			b.Fatalf("invalid cached HTTP response: %d %s", response.Code, response.Body.Bytes())
		}
	}
	check()
	b.ReportAllocs()
	for b.Loop() {
		check()
	}
}

func cacheBenchmarkRouter(b *testing.B, binaryMode, previous, scoped bool, backend cache.Cache) (*Router, []byte) {
	b.Helper()
	options := RouterOptions{}
	if scoped {
		options.ResponseCache = testResponseCache(b, backend, "benchmark")
	}
	router := NewRouterWithOptions(options)
	router.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}, Params: []schema.Param{
		{Name: "id", ID: 1, Type: schema.FieldInt},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	handler := func(_ context.Context, req *Request) error {
		id, err := req.ParamInt("id")
		if err != nil {
			return err
		}
		req.Buf.B = codec.AppendSvarint(req.Buf.B, id)
		return nil
	}
	if !scoped {
		router.Handle("cached", withCacheBeforeNamespace(backend, time.Minute, handler))
	} else if previous {
		router.Handle("cached", withCacheBeforeConditions(time.Minute, handler))
	} else {
		router.Handle("cached", withAnonymousTestCache(handler))
	}
	if scoped {
		router.RequireCache("cached")
		if err := router.Prepare(); err != nil {
			b.Fatal(err)
		}
	}
	body := []byte(`{"$api":"cached","id":42}`)
	if binaryMode {
		var err error
		body, err = EncodeBinaryRequest(1, nil, map[string]any{"id": int64(42)}, router.Registry.ParamOrder("cached"))
		if err != nil {
			b.Fatal(err)
		}
	}
	return router, body
}

// These controls share today's Request representation. They isolate HTTP
// binding/key effects, not the Request size change against an old full build.
func BenchmarkResponseCacheScopeHTTP(b *testing.B) {
	for _, binaryMode := range []bool{false, true} {
		for _, hit := range []bool{false, true} {
			for _, scoped := range []bool{false, true} {
				b.Run(fmt.Sprintf("binary=%t/hit=%t/scoped=%t", binaryMode, hit, scoped), func(b *testing.B) {
					benchmarkCacheRequestHTTP(b, binaryMode, hit, false, scoped)
				})
			}
		}
	}
}

func withCacheBeforeNamespace(backend cache.Cache, ttl time.Duration, handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		key, err := cacheKey(req, "")
		if err != nil {
			return err
		}
		if data, err := backend.Get(ctx, key); err == nil && data != nil {
			req.Buf.B = append(req.Buf.B, data...)
			return nil
		}
		start := len(req.Buf.B)
		if err := handler(ctx, req); err != nil {
			return err
		}
		_ = backend.Set(ctx, key, req.Buf.B[start:], ttl)
		return nil
	}
}

// Frozen historical implementation; never linked into a production build.
func cacheKeyBeforeConditions(req *Request) string {
	h := sha256.New()
	h.Write([]byte(req.API))
	for key, value := range req.Params {
		h.Write([]byte(key))
		h.Write(value)
	}
	return req.API + ":" + hex.EncodeToString(h.Sum(nil))
}

func withCacheBeforeConditions(ttl time.Duration, handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		key := cacheKeyBeforeConditions(req)
		if data, _ := req.responseCache.cache.backend.Get(ctx, key); data != nil {
			req.Buf.B = append(req.Buf.B, data...)
			return nil
		}
		start := len(req.Buf.B)
		if err := handler(ctx, req); err != nil {
			return err
		}
		req.responseCache.cache.backend.Set(ctx, key, req.Buf.B[start:], ttl)
		return nil
	}
}
