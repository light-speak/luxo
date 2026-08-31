package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestRouterBasic(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		req.Buf.AppendString(`{"id":1,"name":"lin","email":"lin@test.com"}`)
		return nil
	})

	body := `{"$api":"getUser"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["data"]; !ok {
		t.Fatal("response should have data field")
	}
}

func TestRouterWithSelect(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		// Simulate WriteJSON with field selection
		req.Buf.AppendByte('{')
		first := true
		for _, f := range req.Select {
			if f.Name == "name" {
				if !first {
					req.Buf.AppendByte(',')
				}
				req.Buf.AppendString(`"name":"lin"`)
				first = false
			}
		}
		req.Buf.AppendByte('}')
		return nil
	})

	body := `{"$api":"getUser","$select":"name"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &resp)
	var data map[string]any
	json.Unmarshal(resp["data"], &data)
	if _, ok := data["email"]; ok {
		t.Error("email should be filtered out")
	}
	if data["name"] != "lin" {
		t.Error("name should be present")
	}
}

func TestRouterUnknownAPI(t *testing.T) {
	rt := NewRouter()
	body := `{"$api":"notExist"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestRouterWrongMethod(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodGet, "/luvia", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestRouterBadJSON(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRouterHandlerError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("something went wrong")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestRouterEmptyBody(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(""))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRouterAppError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.MapData{"resource": "User"})
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "NotFound" {
		t.Errorf("error = %v", resp["error"])
	}
	if resp["code"].(float64) != 404 {
		t.Errorf("code = %v", resp["code"])
	}
	data := resp["data"].(map[string]any)
	if data["resource"] != "User" {
		t.Errorf("data.resource = %v", data["resource"])
	}
}

func TestRouterAppErrorWithI18n(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "zh.yaml"), []byte(`
error:
  not_found: "资源 ${resource} 不存在"
`), 0644)

	tr := i18n.New("en")
	tr.LoadDir(dir)

	rt := NewRouter()
	rt.SetTranslator(tr)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.MapData{"resource": "User"})
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	r.Header.Set("Accept-Language", "zh-CN")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "资源 User 不存在" {
		t.Errorf("message = %v", resp["message"])
	}
}

func TestRouterPlainErrorWrapped(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("db connection failed")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "Internal" {
		t.Errorf("error = %v", resp["error"])
	}
	if resp["data"] != nil {
		t.Error("internal error should not expose data")
	}
}

func TestRouterDevModeShowsCause(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.InternalError.WithCause(fmt.Errorf("disk full"))
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["cause"] != "disk full" {
		t.Errorf("cause = %v", resp["cause"])
	}
}

func TestRouterDevModeNoCauseWhenNil(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["cause"]; ok {
		t.Error("should not include cause when nil")
	}
}

func TestRouterUnknownAPIUsesAppError(t *testing.T) {
	rt := NewRouter()
	body := `{"$api":"nonExistent"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "NotFound" {
		t.Errorf("error = %v", resp["error"])
	}
	data := resp["data"].(map[string]any)
	if data["resource"] != "nonExistent" {
		t.Errorf("data.resource = %v", data["resource"])
	}
}

func TestRouterTraceIdInError(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.BadRequest
	})

	handler := TraceMiddleware(rt)

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	traceID, ok := resp["traceId"].(string)
	if !ok || traceID == "" {
		t.Error("error response should include traceId")
	}
}

func TestRouterNoTraceIdWithoutMiddleware(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.BadRequest
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["traceId"]; ok {
		t.Error("should not include traceId without middleware")
	}
}

func TestRouterInternalErrorNoMessage(t *testing.T) {
	rt := NewRouter()
	tr := i18n.New("en")
	rt.SetTranslator(tr)
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("sensitive db error")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "error.internal" {
		t.Errorf("message = %v, want raw key for internal error", resp["message"])
	}
}

func TestRouterJSONWriterFastPath(t *testing.T) {
	rt := NewRouter()
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		w := &testJSONWriter{id: 1, name: "lin"}
		w.WriteJSON(req.Buf, req.Select)
		return nil
	})

	body := `{"$api":"getUser","$select":"id,name"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"data":`) {
		t.Errorf("response should have data wrapper: %s", got)
	}
	if !strings.Contains(got, `"id":1`) {
		t.Errorf("should contain id: %s", got)
	}
}

// testJSONWriter is a mock model implementing WriteJSON with ResponseBuf.
type testJSONWriter struct {
	id   int64
	name string
}

func (tw *testJSONWriter) WriteJSON(buf *ResponseBuf, fields []*selection.Field) {
	buf.AppendByte('{')
	first := true
	if fields == nil {
		buf.AppendString(`"id":`)
		buf.AppendInt(tw.id)
		buf.AppendString(`,"name":`)
		buf.AppendJSONString(tw.name)
	} else {
		for _, f := range fields {
			switch f.Name {
			case "id":
				if !first {
					buf.AppendByte(',')
				}
				buf.AppendString(`"id":`)
				buf.AppendInt(tw.id)
				first = false
			case "name":
				if !first {
					buf.AppendByte(',')
				}
				buf.AppendString(`"name":`)
				buf.AppendJSONString(tw.name)
				first = false
			}
		}
	}
	buf.AppendByte('}')
}

func TestRouterDeleteHandler(t *testing.T) {
	rt := NewRouter()
	rt.Handle("deleteUser", func(ctx context.Context, req *Request) error {
		// Delete writes directly — no map, no any
		req.Buf.AppendString(`{"deleted":1}`)
		return nil
	})

	body := `{"$api":"deleteUser"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Errorf("got %s", w.Body.String())
	}
}

func TestRouterHandlerPanicRecovery(t *testing.T) {
	rt := NewRouter()
	rt.Handle("crashTest", func(ctx context.Context, req *Request) error {
		panic("boom")
	})

	body := `{"$api":"crashTest"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("expected error response, got %s", w.Body.String())
	}
}

// --- Introspection Tests ---

func TestIntrospectionDisabledByDefault(t *testing.T) {
	rt := NewRouter()
	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestIntrospectionWrongKey(t *testing.T) {
	rt := NewRouter()
	rt.IntrospectionKey = "correct-key"

	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema&key=wrong", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestIntrospectionSuccess(t *testing.T) {
	rt := NewRouter()
	rt.IntrospectionKey = "test-key"
	rt.Version = "v1.2.3"
	rt.Schema.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "getUser", Module: "user",
		ReturnType: "User",
	})

	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema&key=test-key", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := w.Header().Get("X-Luxo-Version"); got != "v1.2.3" {
		t.Errorf("X-Luxo-Version = %q, want v1.2.3", got)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	models, ok := result["models"].(map[string]any)
	if !ok {
		t.Fatal("missing models in schema")
	}
	if _, ok := models["User"]; !ok {
		t.Error("missing User model")
	}

	apis, ok := result["apis"].(map[string]any)
	if !ok {
		t.Fatal("missing apis in schema")
	}
	if _, ok := apis["getUser"]; !ok {
		t.Error("missing getUser API")
	}
}

func TestIntrospectionFieldTypes(t *testing.T) {
	rt := NewRouter()
	rt.IntrospectionKey = "key"
	rt.Schema.RegisterModel(&schema.Model{
		Name: "Post",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "title", Type: schema.FieldString},
			{ID: 3, Name: "score", Type: schema.FieldFloat},
			{ID: 4, Name: "active", Type: schema.FieldBool},
			{ID: 5, Name: "tags", Type: schema.FieldEnum},
			{ID: 6, Name: "bio", Type: schema.FieldString, Nullable: true},
		},
	})

	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema&key=key", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	body := w.Body.String()
	// Field types should be strings, not numbers
	if !strings.Contains(body, `"Int"`) {
		t.Error("FieldInt should serialize as 'Int'")
	}
	if !strings.Contains(body, `"String"`) {
		t.Error("FieldString should serialize as 'String'")
	}
	if !strings.Contains(body, `"Float"`) {
		t.Error("FieldFloat should serialize as 'Float'")
	}
	if !strings.Contains(body, `"Boolean"`) {
		t.Error("FieldBool should serialize as 'Boolean'")
	}
	if !strings.Contains(body, `"Enum"`) {
		t.Error("FieldEnum should serialize as 'Enum'")
	}
	if !strings.Contains(body, `"nullable":true`) {
		t.Error("nullable field should have nullable:true")
	}
}

// --- Schema Binary→JSON Conversion Tests ---

func TestRouterSchemaConversion(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "getUser", Module: "user",
		ReturnType: "User",
	})

	// Handler writes binary (WriteLuxo style, with arena header)
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		req.Buf.B = codec.AppendVarint(req.Buf.B, 0) // arena header
		var enc codec.Encoder
		enc.WriteFieldInt(1, 42)
		enc.WriteFieldString(2, "Alice")
		enc.WriteEnd()
		req.Buf.B = append(req.Buf.B, enc.Bytes()...)
		return nil
	})

	// JSON request → handler writes binary → Router converts to JSON via schema
	body := `{"$api":"getUser","id":1}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Response should be JSON converted from binary via schema
	resp := w.Body.String()
	if !strings.Contains(resp, `"id":42`) {
		t.Errorf("missing id in response: %s", resp)
	}
	if !strings.Contains(resp, `"Alice"`) {
		t.Errorf("missing name in response: %s", resp)
	}
}

func TestIntrospectionViaHeader(t *testing.T) {
	rt := NewRouter()
	rt.IntrospectionKey = "secret-key"
	rt.Schema.RegisterModel(&schema.Model{
		Name:   "User",
		Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}},
	})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "getUser", Module: "user", ReturnType: "User"})

	// Header takes priority — query param wrong but header correct should succeed
	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema&key=wrong", nil)
	r.Header.Set("X-Introspection-Key", "secret-key")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("header auth should succeed, got %d", w.Code)
	}

	// Neither header nor query — should fail
	r2 := httptest.NewRequest(http.MethodGet, "/luvia?$schema", nil)
	w2 := httptest.NewRecorder()
	rt.ServeHTTP(w2, r2)

	if w2.Code != http.StatusForbidden {
		t.Errorf("empty key should be forbidden, got %d", w2.Code)
	}
}

func TestConvertBinaryToJSON_EmptyData(t *testing.T) {
	rt := NewRouter()
	// Empty data should return null regardless of schema
	result := rt.convertBinaryToJSON("getUser", nil)
	if string(result) != "null" {
		t.Errorf("expected null for empty data, got %q", result)
	}
	result = rt.convertBinaryToJSON("getUser", []byte{})
	if string(result) != "null" {
		t.Errorf("expected null for zero-length data, got %q", result)
	}
}

func TestConvertBinaryToJSON_NoSchema(t *testing.T) {
	rt := NewRouter()
	rt.Schema = nil
	// Without schema, should pass data through (handler wrote JSON directly)
	input := []byte(`{"id":1,"name":"test"}`)
	result := rt.convertBinaryToJSON("getUser", input)
	if string(result) != string(input) {
		t.Errorf("expected passthrough, got %q", result)
	}
}

func TestConvertBinaryToJSON_UnknownAPI(t *testing.T) {
	rt := NewRouter()
	// Schema exists but API not registered — pass through
	input := []byte(`{"id":1}`)
	result := rt.convertBinaryToJSON("nonExistent", input)
	if string(result) != string(input) {
		t.Errorf("unknown API should passthrough, got %q", result)
	}
}

func TestWriteErrorEscapesJSON(t *testing.T) {
	w := httptest.NewRecorder()
	// Message with quotes and newlines should be properly escaped
	writeError(w, http.StatusBadRequest, "invalid \"field\"\nnewline")

	body := w.Body.String()
	// Should be valid JSON
	var parsed map[string]string
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Errorf("writeError produced invalid JSON: %s (body: %s)", err, body)
	}
	if parsed["error"] != "invalid \"field\"\nnewline" {
		t.Errorf("error message not preserved: %q", parsed["error"])
	}
}

// --- SetMetricsCollector / HandleStream ---

type mockMetrics struct {
	calls []struct {
		api      string
		duration time.Duration
		isError  bool
	}
}

type mockTraceMetrics struct {
	mockMetrics
	traces []TraceRecord
}

func (m *mockTraceMetrics) RecordTrace(record TraceRecord) {
	m.traces = append(m.traces, record)
}

func (m *mockMetrics) Record(api string, duration time.Duration, isError bool) {
	m.calls = append(m.calls, struct {
		api      string
		duration time.Duration
		isError  bool
	}{api, duration, isError})
}

func TestSetMetricsCollector(t *testing.T) {
	rt := NewRouter()
	mc := &mockMetrics{}
	rt.SetMetricsCollector(mc)

	rt.Handle("ping", func(ctx context.Context, req *Request) error {
		req.Buf.AppendString(`"pong"`)
		return nil
	})

	body := `{"$api":"ping"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if len(mc.calls) != 1 {
		t.Fatalf("metrics should be called once, got %d", len(mc.calls))
	}
	if mc.calls[0].api != "ping" {
		t.Errorf("api = %q, want ping", mc.calls[0].api)
	}
	if mc.calls[0].isError {
		t.Error("should not be error")
	}
}

func TestMetricsCollectorOnError(t *testing.T) {
	rt := NewRouter()
	mc := &mockMetrics{}
	rt.SetMetricsCollector(mc)

	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("oops")
	})

	body := `{"$api":"fail"}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if len(mc.calls) != 1 || !mc.calls[0].isError {
		t.Error("metrics should record error")
	}
}

func TestTraceRecorderReceivesRequestMetadata(t *testing.T) {
	rt := NewRouter()
	mc := &mockTraceMetrics{}
	rt.SetMetricsCollector(mc)
	rt.Handle("fail", func(context.Context, *Request) error {
		return errors.Forbidden
	})

	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"fail"}`))
	r.Header.Set("X-Request-Id", "trace-123")
	r.Header.Set("X-Luxo-Client", "typescript")
	r.Header.Set("X-Luxo-Client-Version", "1.2.3")
	w := httptest.NewRecorder()
	TraceMiddleware(rt).ServeHTTP(w, r)

	if len(mc.traces) != 1 {
		t.Fatalf("trace calls = %d, want 1", len(mc.traces))
	}
	got := mc.traces[0]
	if got.TraceID != "trace-123" || got.APIName != "fail" || got.StatusCode != http.StatusForbidden {
		t.Fatalf("trace = %+v", got)
	}
	if got.ClientName != "typescript" || got.ClientVersion != "1.2.3" {
		t.Fatalf("client metadata = %q/%q", got.ClientName, got.ClientVersion)
	}
}

func TestHandleStream(t *testing.T) {
	rt := NewRouter()
	matcher := func(data []byte, params *StreamParams, identity any) bool {
		return true
	}
	rt.HandleStream("watchTest", matcher)
	if rt.streamMatchers["watchTest"] == nil {
		t.Error("matcher should be registered")
	}
}

// --- convertBinaryToJSON: scalar, type declaration, list, paginated ---

func TestConvertBinaryToJSON_ScalarNoReturnType(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "deleteUser", Module: "user",
		ReturnType: "", // no return type = scalar
	})

	// Encode an int scalar
	var data []byte
	data = codec.AppendSvarint(data, 42)

	result := rt.convertBinaryToJSON("deleteUser", data)
	if string(result) != "42" {
		t.Errorf("scalar should be 42, got %q", result)
	}
}

func TestConvertBinaryToJSON_TypeDeclaration(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterType(&schema.TypeDecl{
		Name: "AuthPayload",
		Fields: []schema.Field{
			{ID: 1, Name: "token", Type: schema.FieldString},
			{ID: 2, Name: "userId", Type: schema.FieldInt},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "login", Module: "auth",
		ReturnType: "AuthPayload",
	})

	// Encode binary: arena header + fields
	var data []byte
	data = codec.AppendVarint(data, 0) // arena header
	var enc codec.Encoder
	enc.WriteFieldString(1, "abc123")
	enc.WriteFieldInt(2, 99)
	enc.WriteEnd()
	data = append(data, enc.Bytes()...)

	result := rt.convertBinaryToJSON("login", data)
	got := string(result)
	if !strings.Contains(got, `"token":"abc123"`) {
		t.Errorf("should contain token: %s", got)
	}
	if !strings.Contains(got, `"userId":99`) {
		t.Errorf("should contain userId: %s", got)
	}
}

func TestConvertBinaryToJSON_ScalarReturnType(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "count", Module: "stats",
		ReturnType: "String", // scalar type, not a model
	})

	var data []byte
	data = codec.AppendString(data, "hello")

	result := rt.convertBinaryToJSON("count", data)
	if string(result) != `"hello"` {
		t.Errorf("scalar string should be quoted, got %q", result)
	}
}

func TestConvertBinaryToJSON_ListPath(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterModel(&schema.Model{
		Name: "Item",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "listItems", Module: "item",
		ReturnType: "Item", ReturnList: true,
	})

	// Empty list — just test that the list path is reached
	var data []byte
	data = codec.AppendVarint(data, 0) // arena header (totalStringLen)
	data = codec.AppendVarint(data, 0) // count=0

	result := rt.convertBinaryToJSON("listItems", data)
	got := string(result)
	if got != "[]" {
		t.Errorf("empty list should be [], got %q", got)
	}
}

func TestConvertBinaryToJSON_PaginatedListPath(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterModel(&schema.Model{
		Name: "Item",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "listItems", Module: "item",
		ReturnType: "Item", ReturnList: true, Paginated: true,
	})

	// Empty paginated list
	var data []byte
	data = codec.AppendVarint(data, 0)  // arena header
	data = codec.AppendVarint(data, 0)  // total=0
	data = codec.AppendVarint(data, 1)  // page=1
	data = codec.AppendVarint(data, 20) // pageSize=20
	data = codec.AppendVarint(data, 0)  // count=0

	result := rt.convertBinaryToJSON("listItems", data)
	got := string(result)
	if !strings.Contains(got, "total") {
		t.Errorf("paginated should have total: %s", got)
	}
}

// --- Introspection: no schema registered ---

func TestIntrospectionNoSchema(t *testing.T) {
	rt := NewRouter()
	rt.IntrospectionKey = "key"
	rt.Schema = nil

	r := httptest.NewRequest(http.MethodGet, "/luvia?$schema&key=key", nil)
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for nil schema, got %d", w.Code)
	}
}

// --- Binary mode error with binary writeAppError ---

func TestRouterBinaryModeParseError(t *testing.T) {
	rt := NewRouter()
	// Send invalid binary body
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(""))
	r.Header.Set("X-Luxo-Mode", "binary")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- ServeHTTP: JSON request with schema conversion + FieldMask ---

func TestRouterJSONWithFieldMaskConversion(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 3, Name: "email", Type: schema.FieldString},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 1, Name: "getUser", Module: "user",
		ReturnType: "User",
	})

	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		// Handler writes binary with field mask
		req.Buf.B = codec.AppendVarint(req.Buf.B, 0)
		var enc codec.Encoder
		enc.WriteFieldInt(1, 1)
		enc.WriteFieldString(2, "Lin")
		enc.WriteEnd()
		req.Buf.B = append(req.Buf.B, enc.Bytes()...)
		return nil
	})

	body := `{"$api":"getUser","$select":"id,name","id":1}`
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(body))
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// Suppress unused imports
var _ = bytes.NewBuffer
var _ = json.RawMessage{}
var _ = fmt.Sprint
