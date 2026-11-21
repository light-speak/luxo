package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func integrityCacheRouter(t testing.TB, backend cache.Cache, result string, origin HandlerFunc) *Router {
	t.Helper()
	return integrityTypedCacheRouter(t, backend, schema.API{ReturnType: result}, origin)
}

func integrityTypedCacheRouter(t testing.TB, backend cache.Cache, definition schema.API, origin HandlerFunc) *Router {
	t.Helper()
	router := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "integrity")})
	definition.ID, definition.Name = 1, "cached"
	definition.CachePolicy, definition.CacheDependencies = testPrivateCachePolicy(time.Minute), &schema.CacheDependencies{}
	if err := router.Schema.RegisterAPI(&definition); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.RequireCache("cached")
	router.Handle("cached", func(ctx context.Context, req *Request) error {
		return CacheResponsePolicy(ctx, req, "", func() error { return origin(ctx, req) })
	})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func integrityCacheHTTP(t *testing.T, router *Router, binary bool) int {
	t.Helper()
	return integrityCacheHTTPResponse(t, router, binary).Code
}

func integrityCacheHTTPResponse(t *testing.T, router *Router, binary bool) *httptest.ResponseRecorder {
	t.Helper()
	body := []byte(`{"$api":"cached"}`)
	contentType := "application/json"
	if binary {
		var err error
		body, err = EncodeBinaryRequest(1, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		contentType = "application/x-luxo"
	}
	request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if binary {
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestResponseCacheCannotPublishRejectedBuffer(t *testing.T) {
	for _, binary := range []bool{false, true} {
		backend := cache.NewMemory()
		t.Cleanup(func() { _ = backend.Close() })
		calls := 0
		router := integrityCacheRouter(t, backend, "Int", func(_ context.Context, req *Request) error {
			calls++
			req.Buf = &ResponseBuf{B: codec.AppendSvarint(nil, 84)}
			return nil
		})
		first, second := integrityCacheHTTP(t, router, binary), integrityCacheHTTP(t, router, binary)
		if first != 500 || second != 500 || calls != 2 || backend.Len() != 0 {
			t.Errorf("rejected response entered cache: binary=%t status=%d/%d calls=%d entries=%d", binary, first, second, calls, backend.Len())
		}
	}
}

func TestResponseCacheEmptyBodyDoesNotBlockRecovery(t *testing.T) {
	for _, binary := range []bool{false, true} {
		backend := cache.NewMemory()
		t.Cleanup(func() { _ = backend.Close() })
		calls := 0
		router := integrityCacheRouter(t, backend, "Int", func(_ context.Context, req *Request) error {
			calls++
			if calls > 1 {
				req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
			}
			return nil
		})
		first, second, third := integrityCacheHTTP(t, router, binary), integrityCacheHTTP(t, router, binary), integrityCacheHTTP(t, router, binary)
		if first != 500 || second != 200 || third != 200 || calls != 2 || backend.Len() != 1 {
			t.Errorf("empty result remained cached: binary=%t status=%d/%d/%d calls=%d entries=%d", binary, first, second, third, calls, backend.Len())
		}
	}
}

func TestResponseCacheRejectsTruncatedPrefix(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	req := &Request{API: "cached", Buf: &ResponseBuf{B: []byte("prefix")},
		responseCache: testCacheRoute(t, backend, "integrity", "cached")}
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("truncated buffer panicked instead of returning an error: %v", recovered)
		}
	}()
	err := CacheResponsePolicy(t.Context(), req, "", func() error {
		req.Buf.B = req.Buf.B[:0]
		return nil
	})
	if err == nil || backend.Len() != 0 {
		t.Fatal("truncated response was published", err, backend.Len())
	}
}

func TestResponseCacheDiscardsEmptyValueForDeclaredResult(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	route := testCacheRoute(t, backend, "integrity", "cached")
	req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: route}
	key, err := cacheKeyInNamespace(req, "", route.plan.Load().policy.prefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Set(t.Context(), key, []byte{}, time.Minute); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for range 2 {
		req.Buf.B = req.Buf.B[:0]
		err := CacheResponsePolicy(t.Context(), req, "", func() error {
			calls++
			req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
			return nil
		})
		if err != nil || !bytes.Equal(req.Buf.B, codec.AppendSvarint(nil, 42)) {
			t.Error("empty backend value was returned as data", req.Buf.B, err)
		}
	}
	if calls != 1 {
		t.Fatal("invalid entry was not replaced by a valid response", calls)
	}
}

func TestResponseCacheRetainsVoidAndBusinessError(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	calls := 0
	router := integrityCacheRouter(t, backend, "", func(context.Context, *Request) error { calls++; return nil })
	for range 2 {
		if status := integrityCacheHTTP(t, router, false); status != 200 {
			t.Fatal("Void response failed", status)
		}
	}
	if calls != 1 {
		t.Fatal("valid empty Void response was not cached", calls)
	}
	failure := errors.New("origin failure")
	req := &Request{API: "cached", Buf: &ResponseBuf{}, responseCache: testCacheRoute(t, cache.Nop{}, "failure", "cached")}
	if got := CacheResponsePolicy(t.Context(), req, "", func() error { req.Buf = nil; return failure }); got != failure {
		t.Fatal("cache replaced the business error", got)
	}
}

func TestResponseCacheRejectsMissingBuffers(t *testing.T) {
	for _, initial := range []bool{false, true} {
		backend := &observedResponseCache{}
		req := &Request{API: "cached", responseCache: testCacheRoute(t, backend, "integrity", "cached")}
		if !initial {
			req.Buf = &ResponseBuf{}
		}
		err := CacheResponsePolicy(t.Context(), req, "", func() error { req.Buf = nil; return nil })
		if err != errHandlerResponseBuffer || backend.sets != 0 {
			t.Error("missing response buffer was accepted", initial, err, backend.sets)
		}
		if initial && backend.gets != 0 {
			t.Error("missing input buffer reached cache backend")
		}
	}
}

func TestResponseCacheRetainsValidZeroPayloads(t *testing.T) {
	for _, sample := range []struct {
		name, json string
		definition schema.API
	}{
		{"null", `{"data":null}`, schema.API{ReturnType: "Int", ReturnNullable: true}},
		{"empty_list", `{"data":[]}`, schema.API{ReturnType: "Int", ReturnList: true}},
		{"empty_string", `{"data":""}`, schema.API{ReturnType: "String"}},
		{"empty_bytes", `{"data":""}`, schema.API{ReturnType: "Bytes"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			backend := cache.NewMemory()
			t.Cleanup(func() { _ = backend.Close() })
			calls := 0
			router := integrityTypedCacheRouter(t, backend, sample.definition, func(_ context.Context, req *Request) error {
				calls++
				req.Buf.B = append(req.Buf.B, 0)
				return nil
			})
			for _, binary := range []bool{false, true} {
				callsAfterFirst := 0
				for attempt := range 2 {
					response := integrityCacheHTTPResponse(t, router, binary)
					want := []byte(sample.json)
					if binary {
						want = []byte{0}
					}
					if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) {
						t.Fatal("valid zero payload changed", binary, response.Code, response.Body.String())
					}
					if attempt == 0 {
						callsAfterFirst = calls
					} else if calls != callsAfterFirst {
						t.Fatal("second identical request missed the cache", binary, calls)
					}
				}
			}
			if calls < 1 || calls > 2 {
				t.Fatal("valid zero payload was not filled once per key", calls)
			}
		})
	}
}

func TestResponseCacheWebSocketCannotPublishRejectedBuffer(t *testing.T) {
	for _, binary := range []bool{false, true} {
		backend := cache.NewMemory()
		t.Cleanup(func() { _ = backend.Close() })
		var calls atomic.Int32
		router := integrityCacheRouter(t, backend, "Int", func(_ context.Context, req *Request) error {
			calls.Add(1)
			req.Buf = &ResponseBuf{B: codec.AppendSvarint(nil, 84)}
			return nil
		})
		server := httptest.NewServer(router)
		t.Cleanup(server.Close)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		for sequence := uint64(1); sequence <= 2; sequence++ {
			kind, request := originWebSocketRequest(binary, "cached", 1, sequence)
			if err := conn.Write(ctx, kind, request); err != nil {
				t.Fatal(err)
			}
			gotKind, body, err := conn.Read(ctx)
			if err != nil || gotKind != kind {
				t.Fatal("cache error frame missing", gotKind, err)
			}
			requireIntegrityCacheWSError(t, binary, sequence, body)
		}
		if err := conn.CloseNow(); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 || backend.Len() != 0 {
			t.Fatal("WebSocket published rejected response", binary, calls.Load(), backend.Len())
		}
	}
}

func requireIntegrityCacheWSError(t *testing.T, binary bool, sequence uint64, body []byte) {
	t.Helper()
	if binary {
		if len(body) < 2 || body[0] != BinaryFrameCallError {
			t.Fatalf("rejected cache body became success: %x", body)
		}
		id, size := codec.ReadVarint(body, 1)
		if size <= 0 || id != sequence || decodeWireError(t, body[1+size:]).Code != 500 {
			t.Fatalf("wrong cache error identity: %x", body)
		}
		return
	}
	var response struct {
		ID   uint64          `json:"$id"`
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != sequence || response.Code != 500 || len(response.Data) != 0 {
		t.Fatal("rejected cache body became success", string(body))
	}
}
