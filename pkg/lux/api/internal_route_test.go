package api

import (
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
