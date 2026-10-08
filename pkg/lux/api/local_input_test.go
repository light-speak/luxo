package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestLocalJSONInputBoundary(t *testing.T) {
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", Params: []schema.Param{{ID: 1, Name: "value", Type: schema.FieldInt}}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	calls := 0
	handler := func(_ context.Context, req *Request) error {
		if len(req.BinaryParams()) != 0 || len(req.BinaryRequest()) != 0 {
			t.Fatal("local JSON input still creates intermediate binary")
		}
		if _, err := req.ParamInt("value"); err != nil {
			return err
		}
		calls++
		return nil
	}
	router.HandleLocal("read", handler)
	for _, input := range []struct {
		body string
		code int
	}{
		{`{"$api":"read","value":42}`, 200},
		{`{"$api":"read"}`, 400},
		{`{"$api":"read","value":null}`, 400},
		{`{"$api":"read","value":"42"}`, 400},
		{`{"$api":"read","value":42,"unknown":1}`, 400},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/luvia", strings.NewReader(input.body)))
		if response.Code != input.code {
			t.Fatalf("%s: status=%d body=%s", input.body, response.Code, response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("invalid input reached business: %d", calls)
	}
}

func TestParamInputPresence(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for _, nullable := range []bool{false, true} {
			for _, body := range []string{`{"$api":"read"}`, `{"$api":"read","value":null}`, `{"$api":"read","value":{}}`} {
				req, err := ParseRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)))
				if err != nil {
					t.Fatal(err)
				}
				raw, present, err := req.ParamInput("value", optional, nullable)
				wantPresent := strings.Contains(body, "value")
				wantNull := strings.Contains(body, "null")
				wantError := !wantPresent && !optional || wantNull && !nullable
				if present != wantPresent || (err != nil) != wantError {
					t.Fatalf("%s optional=%v nullable=%v: %s %v %v", body, optional, nullable, raw, present, err)
				}
				if wantNull && raw != nil {
					t.Fatal("null is not represented as nil input")
				}
			}
		}
	}
	if _, _, err := (&Request{BinaryMode: true}).ParamInput("value", false, false); err == nil {
		t.Fatal("binary request accepted as raw JSON input")
	}
}

func TestLocalInputRegistrationAndReset(t *testing.T) {
	router := NewRouter()
	router.HandleLocal("read", func(context.Context, *Request) error { return nil })
	if err := router.Validate(); err == nil {
		t.Fatal("local route without schema passed startup validation")
	}
	request := &Request{API: "read", Params: map[string]json.RawMessage{}, binaryParams: []byte{1}, binaryRequest: []byte{2}}
	if err := router.Registry.prepareLocalJSONRequest(request); err == nil || request.BinaryParams() != nil || request.BinaryRequest() != nil {
		t.Fatal("missing schema accepted or previous wire data retained")
	}
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", Paginated: true, DefaultPageSize: 25})
	if err := router.Registry.prepareLocalJSONRequest(request); err == nil {
		t.Fatal("unregistered API accepted")
	}
	router.Registry.RegisterSchemaAPIs(router.Schema)
	// Bypass registration only to exercise the request boundary's defense
	// against corrupted metadata. Public registration rejects this layout.
	router.Registry.paramOrder["read"] = make([]ParamMeta, 17)
	if err := router.Registry.prepareLocalJSONRequest(request); err == nil {
		t.Fatal("oversized metadata accepted")
	}
	router.Registry.paramOrder["read"] = nil
	if err := router.Registry.prepareLocalJSONRequest(request); err != nil || request.PageSize != 25 {
		t.Fatalf("valid local preparation: %v %+v", err, request)
	}
	router.HandleLocal("read", func(context.Context, *Request) error { return nil })
	if err := router.Validate(); err != nil {
		t.Fatal(err)
	}
	snapshot := router.ExportHandlers()
	delete(snapshot, "read")
	if router.handlers["read"].call == nil {
		t.Fatal("RPC snapshot changed public route")
	}
	router.Handle("read", func(context.Context, *Request) error { return nil })
	if router.handlers["read"].localJSON {
		t.Fatal("canonical registration retained old local policy")
	}
}

func TestWSLocalJSONInput(t *testing.T) {
	router := NewRouter()
	router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", Params: []schema.Param{{ID: 1, Name: "value", Type: schema.FieldInt}}})
	router.Registry.RegisterSchemaAPIs(router.Schema)
	router.HandleLocal("read", func(_ context.Context, req *Request) error {
		if len(req.BinaryParams()) != 0 {
			t.Error("WS local JSON encoded intermediate binary")
		}
		_, err := req.ParamInt("value")
		return err
	})
	ctx, conn := newInMemoryWebSocket(t, router)
	writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$api":"read","value":42}`))
	requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"data":null}`))
	writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":2,"$api":"read","value":false}`))
	kind, frame, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || !strings.Contains(string(frame), `"code":400`) {
		t.Fatalf("invalid input: %s %v", frame, err)
	}
}
