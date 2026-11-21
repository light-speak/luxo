package api

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestInternalRouteRegistration(t *testing.T) {
	rt := NewRouter()
	if rt.internalHandlers != nil {
		t.Fatal("public-only router allocated an internal handler table")
	}
	handler := func(_ context.Context, req *Request) error { req.Buf.AppendString(`7`); return nil }
	rt.HandleInternal("privateWithoutPrefix", handler)
	rt.Handle("svc:public", handler)
	for name, status := range map[string]int{"privateWithoutPrefix": 404, "svc:public": 200} {
		response := httptest.NewRecorder()
		rt.ServeHTTP(response, httptest.NewRequest("POST", "/luvia", strings.NewReader(`{"$api":"`+name+`"}`)))
		if response.Code != status {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
	exported := rt.ExportHandlers()
	if len(exported) != 2 || exported["privateWithoutPrefix"] == nil || exported["svc:public"] == nil {
		t.Fatal("RPC snapshot omitted a route")
	}
	delete(exported, "privateWithoutPrefix")
	if rt.ExportHandlers()["privateWithoutPrefix"] == nil {
		t.Fatal("exported map aliases registration table")
	}
	rt.HandleLocal("privateWithoutPrefix", handler)
	if rt.internalHandlers["privateWithoutPrefix"] != nil || !rt.handlers["privateWithoutPrefix"].localJSON {
		t.Fatal("explicit public replacement retained the internal route")
	}
	rt.HandleInternal("privateWithoutPrefix", handler)
	if _, exists := rt.handlers["privateWithoutPrefix"]; exists {
		t.Fatal("internal replacement retained public access")
	}
	if err := rt.Validate(); err != nil {
		t.Fatalf("removed local JSON route left stale validation: %v", err)
	}
}

func TestBinaryRequestDoesNotRetainTrustedDispatchState(t *testing.T) {
	registry := NewAPIRegistry()
	if err := registry.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{{1, 0, 0}, {1}, nil} {
		req := &Request{Internal: true, ClientKey: "trusted-peer", Buf: &ResponseBuf{Identity: "previous-user"}}
		err := registry.ParseBinaryRequestInto(body, req)
		if (len(body) == 3) != (err == nil) {
			t.Fatalf("unexpected parse result for %x: %v", body, err)
		}
		if req.Internal || req.ClientKey != "" || req.Buf != nil {
			t.Fatalf("trusted state survived decoding %x: %+v", body, req)
		}
	}
}

func TestPrepareRejectsNilUnaryHandlerAndAllowsRepair(t *testing.T) {
	for _, kind := range []string{"public", "internal", "local"} {
		for _, cached := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/plain", true: "/cache"}[cached], func(t *testing.T) {
				rt := NewRouter()
				if cached {
					backend := cache.NewMemory()
					t.Cleanup(func() { backend.Close() })
					rt = NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "nil-handler")})
				}
				if err := rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "route", ReturnType: "Int", Internal: kind == "internal"}); err != nil {
					t.Fatal(err)
				}
				if err := rt.Registry.RegisterSchemaAPIs(rt.Schema); err != nil {
					t.Fatal(err)
				}
				register := rt.Handle
				if kind == "internal" {
					register = rt.HandleInternal
				}
				if kind == "local" {
					register = rt.HandleLocal
				}
				register("route", nil)
				if err := rt.Prepare(); err == nil || !strings.Contains(err.Error(), "route") {
					t.Fatalf("nil handler passed startup validation: %v", err)
				}
				if rt.prepared || rt.binaryRoutes != nil {
					t.Fatal("failed preparation froze the router")
				}
				called := false
				register("route", func(_ context.Context, req *Request) error {
					called = true
					req.Buf.AppendByte(14) // Binary Int 7.
					return nil
				})
				if err := rt.Prepare(); err != nil {
					t.Fatal(err)
				}
				req := GetRequest()
				defer PutRequest(req)
				req.Buf = &ResponseBuf{}
				if err := rt.ExportHandlers()["route"](t.Context(), req); err != nil || !called {
					t.Fatalf("repaired handler did not execute: %v", err)
				}
				verifyRepairedRouteHTTP(t, rt, kind == "internal")
			})
		}
	}
}

func verifyRepairedRouteHTTP(t *testing.T, rt *Router, internal bool) {
	t.Helper()
	for _, binary := range []bool{false, true} {
		body, expected := []byte(`{"$api":"route"}`), []byte(`{"data":7}`)
		if binary {
			body, expected = []byte{1, 0, 0}, []byte{14}
		}
		request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
		if binary {
			request.Header.Set("X-Luxo-Mode", "binary")
		}
		response := httptest.NewRecorder()
		rt.ServeHTTP(response, request)
		if internal {
			if response.Code != 404 {
				t.Fatalf("internal route exposed after repair: mode=%v status=%d", binary, response.Code)
			}
		} else if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("repaired HTTP route: binary=%v status=%d body=%q", binary, response.Code, response.Body.Bytes())
		}
	}
}

func TestRouteScopeStartupValidation(t *testing.T) {
	router := NewRouter()
	router.Schema = nil
	if err := router.Validate(); err != nil {
		t.Fatalf("schema-free manual routes unexpectedly require metadata: %v", err)
	}
	for _, kind := range []string{"public", "internal", "matcher", "stream", "metadata-only"} {
		for _, internal := range []bool{false, true} {
			rt := NewRouter()
			rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "route", Internal: internal})
			switch kind {
			case "public":
				rt.Handle("route", func(context.Context, *Request) error { return nil })
			case "internal":
				rt.HandleInternal("route", func(context.Context, *Request) error { return nil })
			case "matcher":
				rt.streamMatchers["route"] = nil
			case "stream":
				rt.streamHandlers["route"] = streamHandlerRoute{handler: func(context.Context, *StreamParams, any, *Stream) error { return nil }}
			}
			wantError := kind != "metadata-only" && (kind == "internal") != internal
			if err := rt.Validate(); (err != nil) != wantError {
				t.Fatalf("kind=%s internal=%v: %v", kind, internal, err)
			}
		}
	}
}

func TestInternalRouteCacheBinding(t *testing.T) {
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	rt := NewRouterWithOptions(RouterOptions{ResponseCache: testResponseCache(t, backend, "internal")})
	rt.HandleInternal("private", func(_ context.Context, req *Request) error {
		if req.responseCache == nil {
			t.Fatal("internal registration bypassed the cache binding")
		}
		return nil
	})
	if err := rt.ExportHandlers()["private"](t.Context(), &Request{}); err != nil {
		t.Fatal(err)
	}
}

func TestResponseIdentityBindingUsesRouterConfiguration(t *testing.T) {
	router := NewRouter()
	router.Handle("route", func(_ context.Context, req *Request) error { return nil })
	call := router.ExportHandlers()["route"]
	req := &Request{Buf: &ResponseBuf{Identity: "existing"}}
	if err := call(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if req.Buf.Identity != "existing" {
		t.Fatal("router without extractor replaced explicit identity")
	}
	router.IdentityExtractor = func(context.Context) any { return "verified" }
	if err := call(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if req.Buf.Identity != "verified" {
		t.Fatal("extractor configured after registration was ignored")
	}
	if err := call(t.Context(), &Request{}); err != nil {
		t.Fatal(err)
	}
	router.IdentityExtractor = func(context.Context) any { return nil }
	if err := call(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if req.Buf.Identity != nil {
		t.Fatal("anonymous request retained previous identity")
	}
}
