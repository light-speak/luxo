package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
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
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })

	called := 0
	handler := func(ctx context.Context, req *Request) error {
		called++
		req.Buf.B = append(req.Buf.B, []byte("result")...)
		return nil
	}

	cached := bindResponseCache(testCacheRoute(t, backend, "test", "cacheTest"), withAnonymousTestCache(handler))
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

func TestWithCache_Error(t *testing.T) {
	handler := func(ctx context.Context, req *Request) error {
		return fmt.Errorf("db error")
	}
	cached := withAnonymousTestCache(handler)
	req := &Request{API: "errorTest", Buf: GetBuf(), Params: map[string]json.RawMessage{}, responseCache: testCacheRoute(t, cache.Nop{}, "test", "errorTest")}
	if err := cached(context.Background(), req); err == nil {
		t.Error("should propagate error")
	}
}

func TestCacheKey(t *testing.T) {
	req1 := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": json.RawMessage(`1`)}}
	req2 := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": json.RawMessage(`2`)}}
	k1 := testCacheKey(t, req1)
	k2 := testCacheKey(t, req2)
	if k1 == k2 {
		t.Error("different params should produce different keys")
	}
}

func TestCacheKeyOnlyAllocatesFinalKey(t *testing.T) {
	req := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": []byte("42")}}
	var key string
	var err error
	allocations := testing.AllocsPerRun(100, func() { key, err = cacheKey(req, "") })
	if err != nil || key == "" {
		t.Fatalf("invalid cache key: %q, %v", key, err)
	}
	if allocations > 1 {
		t.Fatalf("cache key used %.0f allocations, want only its final string", allocations)
	}
}

func TestCachedBinaryHTTPParametersStayIsolated(t *testing.T) {
	memory := cache.NewMemory()
	t.Cleanup(func() { _ = memory.Close() })
	router := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, memory, "test")})
	router.Schema.RegisterAPI(&schema.API{Name: "cached", ID: 1, ReturnType: "Int", CachePolicy: testPrivateCachePolicy(time.Minute), CacheDependencies: &schema.CacheDependencies{}, Params: []schema.Param{
		{Name: "id", ID: 1, Type: schema.FieldInt},
	}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	calls := 0
	router.Handle("cached", withAnonymousTestCache(func(_ context.Context, req *Request) error {
		id, err := req.ParamInt("id")
		if err != nil {
			return err
		}
		calls++
		req.Buf.B = codec.AppendSvarint(req.Buf.B, id)
		return nil
	}))
	router.RequireCache("cached")
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{42, 84, 42, 84} {
		body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": id}, router.Registry.ParamOrder("cached"))
		request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/x-luxo")
		request.Header.Set("X-Luxo-Mode", "binary")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), codec.AppendSvarint(nil, id)) {
			t.Fatalf("id=%d reused another request's response: HTTP %d, body=%x", id, response.Code, response.Body.Bytes())
		}
	}
	if calls != 2 {
		t.Fatalf("cache misses = %d, want 2", calls)
	}
}

func TestCacheKeyIncludesResponseConditions(t *testing.T) {
	base := Request{API: "cached", Params: map[string]json.RawMessage{"id": []byte("42")}}
	for _, change := range []struct {
		name  string
		apply func(*Request)
	}{
		{"fields", func(r *Request) { r.FieldMask = []byte{1} }},
		{"selection", func(r *Request) {
			r.Select = []*selection.Field{{Name: "owner", Children: []*selection.Field{{Name: "id"}}}}
		}},
		{"internal", func(r *Request) { r.Internal = true }},
		{"page", func(r *Request) { r.Page = 2 }},
		{"pageSize", func(r *Request) { r.PageSize = 30 }},
		{"filters", func(r *Request) { r.Filters = []Filter{{Field: "id", Operator: "eq", Value: "84"}} }},
		{"sorters", func(r *Request) { r.Sorters = []Sorter{{Field: "id", Order: "desc"}} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := base
			change.apply(&changed)
			if testCacheKey(t, &changed) == testCacheKey(t, &base) {
				t.Fatal("response-affecting condition missing from cache key")
			}
		})
	}
}

func TestCacheKeyIgnoresMapIterationOrder(t *testing.T) {
	req := &Request{API: "cached", Params: map[string]json.RawMessage{"first": []byte("1"), "second": []byte("2"), "third": []byte("3")}}
	want := testCacheKey(t, req)
	for range 100 {
		if testCacheKey(t, req) != want {
			t.Fatal("same request produced an unstable cache key")
		}
	}
}

func TestCacheKeyFramesParameterNamesAndValues(t *testing.T) {
	first := &Request{API: "cached", Params: map[string]json.RawMessage{"a1": []byte("2")}}
	second := &Request{API: "cached", Params: map[string]json.RawMessage{"a": []byte("12")}}
	if testCacheKey(t, first) == testCacheKey(t, second) {
		t.Fatal("different names and values collide before hashing")
	}
}

func TestCacheRejectsBinarySlotsWithoutCanonicalBody(t *testing.T) {
	for _, binaryMode := range []bool{false, true} {
		req := &Request{API: "cached", BinaryMode: binaryMode, Buf: &ResponseBuf{}, responseCache: testCacheRoute(t, cache.Nop{}, "test", "cached")}
		if !binaryMode {
			req.SetBinaryParams([]string{"id"}, 1)
			req.SetParamSlot(0, int64(42))
		}
		if key, err := cacheKey(req, ""); err != errUnpreparedCacheRequest || key != "" {
			t.Fatalf("unprepared request key = %q, err=%v", key, err)
		}
		called := false
		handler := withAnonymousTestCache(func(context.Context, *Request) error { called = true; return nil })
		if err := handler(context.Background(), req); err != errUnpreparedCacheRequest || called {
			t.Fatalf("invalid request executed: called=%t, err=%v", called, err)
		}
	}
}

func TestCacheKeyLargeMetadataAndModes(t *testing.T) {
	params := make(map[string]json.RawMessage)
	for index := range 17 {
		params[fmt.Sprintf("value%d", index)] = []byte("42")
	}
	req := &Request{API: "cached", Params: params}
	want := testCacheKey(t, req)
	for range 20 {
		if testCacheKey(t, req) != want {
			t.Fatal("large parameter map changed key")
		}
	}
	jsonRequest := &Request{API: "cached", Params: map[string]json.RawMessage{}}
	binaryRequest := &Request{API: "cached", BinaryMode: true, binaryParams: []byte{0}}
	if testCacheKey(t, jsonRequest) == testCacheKey(t, binaryRequest) {
		t.Fatal("transport modes share an ambiguous cache entry")
	}
}

func testCacheKey(t testing.TB, req *Request) string {
	t.Helper()
	key, err := cacheKey(req, "")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Unit tests without authentication use an explicitly anonymous cache partition.
func withAnonymousTestCache(handler HandlerFunc) HandlerFunc {
	return func(ctx context.Context, req *Request) error {
		return CacheResponsePolicy(ctx, req, "", func() error { return handler(ctx, req) })
	}
}

func TestCacheKeyVerifiedIdentityIsolation(t *testing.T) {
	req := &Request{API: "getUser", Params: map[string]json.RawMessage{"id": []byte("42")}}
	keys := make(map[string]bool)
	for _, identity := range []string{"", "verified-user-a", "verified-user-b", "verified-user-a-tenant-b"} {
		key, err := cacheKey(req, identity)
		if err != nil || keys[key] {
			t.Fatalf("identity cache collision: %q, %v", identity, err)
		}
		if identity != "" && bytes.Contains([]byte(key), []byte(identity)) {
			t.Fatal("verified token exposed in stored key")
		}
		keys[key] = true
	}
}

func TestCacheIdentityKeyOnlyAllocatesFinalString(t *testing.T) {
	req := &Request{API: "cached", Params: map[string]json.RawMessage{}}
	for _, length := range []int{0, 1, 31, 32, 33, 255, 256, 257, 1024, 4096} {
		identity := strings.Repeat("x", length)
		allocations := testing.AllocsPerRun(100, func() {
			var err error
			cacheKeySink, err = cacheKey(req, identity)
			if err != nil {
				t.Fatal(err)
			}
		})
		if allocations > 1 {
			t.Fatalf("%d-byte identity hashing allocated %.0f times, want only the final key", length, allocations)
		}
		// Independent, contiguous framing proves chunk boundaries do not alter keys.
		var material []byte
		for _, part := range []string{"luxo-response-cache-v2", req.API, identity} {
			material = binary.LittleEndian.AppendUint64(material, uint64(len(part)))
			material = append(material, part...)
		}
		material = append(material, make([]byte, 9*8)...)
		if want := fmt.Sprintf("%s:%x", req.API, sha256.Sum256(material)); cacheKeySink != want {
			t.Fatalf("%d-byte identity changed framing: got %s, want %s", length, cacheKeySink, want)
		}
	}
}

type failingResponseCache struct{ cache.Nop }

func (failingResponseCache) Get(context.Context, string) ([]byte, error) {
	return []byte("untrusted-partial-cache-value"), fmt.Errorf("cache unavailable")
}

func (failingResponseCache) Set(context.Context, string, []byte, time.Duration) error {
	return fmt.Errorf("cache unavailable")
}

func TestCacheFailureDoesNotPublishPartialEntry(t *testing.T) {
	req := &Request{API: "cached", Buf: &ResponseBuf{B: []byte("prefix:")}, responseCache: testCacheRoute(t, failingResponseCache{}, "test", "cached")}
	if err := CacheResponsePolicy(context.Background(), req, "verified", func() error {
		req.Buf.B = append(req.Buf.B, "fresh"...)
		return nil
	}); err != nil || string(req.Buf.B) != "prefix:fresh" {
		t.Fatalf("cache failure changed successful response: %q, %v", req.Buf.B, err)
	}
}
