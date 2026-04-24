package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/errors"
)

func TestBinaryRequestRoundTrip(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	reg.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	// Encode binary request
	body := EncodeBinaryRequest(1, map[string]any{"id": 42}, reg.paramOrder["getUser"])

	// Decode
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "getUser" {
		t.Fatalf("API = %q, want getUser", req.API)
	}
	id, err := req.ParamInt("id")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestBinaryRequestMultipleParams(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("createTask", 2)
	reg.RegisterParams("createTask", []ParamMeta{
		{Name: "title", Type: "String", FieldID: 1},
		{Name: "projectId", Type: "Int", FieldID: 2},
		{Name: "priority", Type: "Int", FieldID: 3},
	})

	body := EncodeBinaryRequest(2, map[string]any{
		"title":     "Test task",
		"projectId": 1,
		"priority":  3,
	}, reg.paramOrder["createTask"])

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.API != "createTask" {
		t.Fatalf("API = %q", req.API)
	}
	title, _ := req.ParamString("title")
	if title != "Test task" {
		t.Fatalf("title = %q", title)
	}
	pid, _ := req.ParamInt("projectId")
	if pid != 1 {
		t.Fatalf("projectId = %d", pid)
	}
}

func TestBinaryModeHTTP(t *testing.T) {
	rt := NewRouter()

	// Register handler
	rt.Handle("ping", func(ctx context.Context, req *Request) error {
		req.Buf.AppendString(`"pong"`)
		return nil
	})

	// Register in binary registry
	rt.Registry.Register("ping", 99)
	rt.Registry.RegisterParams("ping", nil)

	// Build binary request
	body := EncodeBinaryRequest(99, nil, nil)

	// Send as HTTP with X-Luxo-Mode: binary
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(string(body)))
	r.Header.Set("X-Luxo-Mode", "binary")
	w := httptest.NewRecorder()

	rt.ServeHTTP(w, r)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Luxo-Mode") != "binary" {
		t.Fatal("response should have X-Luxo-Mode: binary")
	}
	if resp.Header.Get("Content-Type") != "application/x-luxo" {
		t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != `"pong"` {
		t.Fatalf("body = %q", string(respBody))
	}
}

func TestBinaryModeUnknownAPI(t *testing.T) {
	reg := NewAPIRegistry()
	// API ID 999 not registered
	body := EncodeBinaryRequest(999, nil, nil)
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on unknown API ID")
	}
}

func TestBinaryRequestEmpty(t *testing.T) {
	reg := NewAPIRegistry()
	_, err := reg.ParseBinaryRequest(nil)
	if err == nil {
		t.Fatal("should error on empty body")
	}
}

func TestTypedParamsInt(t *testing.T) {
	req := &Request{paramNames: []string{"id"}, paramCount: 1, paramSlots: [16]any{int64(42)}}
	v, err := req.ParamInt("id")
	if err != nil || v != 42 {
		t.Fatalf("got %d, err=%v", v, err)
	}
	_, err = req.ParamInt("missing")
	if err == nil {
		t.Fatal("should error on missing")
	}
}

func TestTypedParamsString(t *testing.T) {
	req := &Request{paramNames: []string{"name"}, paramCount: 1, paramSlots: [16]any{"alice"}}
	v, err := req.ParamString("name")
	if err != nil || v != "alice" {
		t.Fatalf("got %q, err=%v", v, err)
	}
}

func TestTypedParamsFloat(t *testing.T) {
	req := &Request{paramNames: []string{"amount"}, paramCount: 1, paramSlots: [16]any{99.5}}
	v, err := req.ParamFloat("amount")
	if err != nil || v != 99.5 {
		t.Fatalf("got %f, err=%v", v, err)
	}
}

func TestTypedParamsBool(t *testing.T) {
	req := &Request{paramNames: []string{"active"}, paramCount: 1, paramSlots: [16]any{true}}
	v, err := req.ParamBool("active")
	if err != nil || !v {
		t.Fatalf("got %v, err=%v", v, err)
	}
}

func TestTypedParamsHasParam(t *testing.T) {
	req := &Request{paramNames: []string{"id"}, paramCount: 1, paramSlots: [16]any{int64(1)}}
	if !req.HasParam("id") {
		t.Error("should have id")
	}
	if req.HasParam("missing") {
		t.Error("should not have missing")
	}
}

func TestTypedParamsDateTime(t *testing.T) {
	req := &Request{paramNames: []string{"date"}, paramCount: 1, paramSlots: [16]any{"2026-04-17T12:00:00Z"}}
	v, err := req.ParamDateTime("date")
	if err != nil {
		t.Fatal(err)
	}
	if v.Year() != 2026 {
		t.Fatalf("year = %d", v.Year())
	}
}

func TestTypedParamsMissing(t *testing.T) {
	req := &Request{paramNames: []string{}, paramCount: 0}
	_, err := req.ParamFloat("x")
	if err == nil {
		t.Fatal("should error")
	}
	_, err = req.ParamBool("x")
	if err == nil {
		t.Fatal("should error")
	}
	_, err = req.ParamDateTime("x")
	if err == nil {
		t.Fatal("should error")
	}
}

func TestBinaryErrorResponse(t *testing.T) {
	rt := NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *Request) error {
		return errors.NotFound
	})
	rt.Registry.Register("fail", 1)
	rt.Registry.RegisterParams("fail", nil)

	body := EncodeBinaryRequest(1, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(string(body)))
	r.Header.Set("X-Luxo-Mode", "binary")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if w.Header().Get("X-Luxo-Mode") != "binary" {
		t.Fatal("error response should be binary mode")
	}
}

// --- APIRegistry methods ---

func TestRegistryNameByID(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)

	name, ok := reg.NameByID(1)
	if !ok || name != "getUser" {
		t.Fatalf("NameByID(1) = %q, %v", name, ok)
	}
	_, ok = reg.NameByID(999)
	if ok {
		t.Fatal("NameByID(999) should return false")
	}
}

func TestRegistryIDByName(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)

	id, ok := reg.IDByName("getUser")
	if !ok || id != 1 {
		t.Fatalf("IDByName(getUser) = %d, %v", id, ok)
	}
	_, ok = reg.IDByName("missing")
	if ok {
		t.Fatal("IDByName(missing) should return false")
	}
}

func TestRegistryParamOrder(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	meta := []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}}
	reg.RegisterParams("getUser", meta)

	got := reg.ParamOrder("getUser")
	if len(got) != 1 || got[0].Name != "id" {
		t.Fatalf("ParamOrder wrong: %v", got)
	}
	if reg.ParamOrder("missing") != nil {
		t.Fatal("missing API should return nil")
	}
}

func TestRegistryParamNames(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	reg.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
		{Name: "name", Type: "String", FieldID: 2},
	})

	names := reg.ParamNames("getUser")
	if len(names) != 2 || names[0] != "id" || names[1] != "name" {
		t.Fatalf("ParamNames wrong: %v", names)
	}
}

// --- ParseBinaryRequest edge cases ---

func TestParseBinaryRequestInvalidVarint(t *testing.T) {
	reg := NewAPIRegistry()
	// Invalid varint: 0x80 without continuation
	_, err := reg.ParseBinaryRequest([]byte{0x80})
	if err == nil {
		t.Fatal("should error on invalid varint")
	}
}

func TestParseBinaryRequestFieldMaskOverflow(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)

	// API ID = 1, mask length = 255 (way more than body)
	body := []byte{0x01, 0xFF, 0x01} // varint 1, varint 255
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on field mask overflow")
	}
}

func TestParseBinaryRequestTruncatedParam(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)
	reg.RegisterParams("test", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	// API ID = 1, mask len = 0, then param field ID 1 but no value
	body := []byte{0x01, 0x00, 0x01}
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on truncated int param")
	}
}

func TestParseBinaryRequestUnknownParamFieldID(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)
	reg.RegisterParams("test", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	// API ID = 1, mask len = 0, then unknown field ID 99
	body := []byte{0x01, 0x00, 99}
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on unknown param field ID")
	}
}

// --- SetBinaryParams / SetParamSlot ---

func TestSetBinaryParamsAndSlot(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"id", "name"}, 2)
	req.SetParamSlot(0, int64(42))
	req.SetParamSlot(1, "alice")

	id, err := req.ParamInt("id")
	if err != nil || id != 42 {
		t.Fatalf("id = %d, err = %v", id, err)
	}
	name, err := req.ParamString("name")
	if err != nil || name != "alice" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
}

func TestSetParamSlotOutOfRange(t *testing.T) {
	req := &Request{}
	req.SetBinaryParams([]string{"id"}, 1)
	// Should not panic with out-of-range index
	req.SetParamSlot(16, "overflow")
	req.SetParamSlot(-1, "negative")
}

// --- ParamIntArray/StringArray with paramSlots ---

func TestParamIntArrayBinary(t *testing.T) {
	req := &Request{
		paramNames: []string{"ids"},
		paramCount: 1,
		paramSlots: [16]any{[]int64{1, 2, 3}},
	}
	ids, err := req.ParamIntArray("ids")
	if err != nil || len(ids) != 3 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
	// Missing param
	_, err = req.ParamIntArray("missing")
	if err == nil {
		t.Fatal("should error on missing")
	}
	// Wrong type
	req2 := &Request{
		paramNames: []string{"ids"},
		paramCount: 1,
		paramSlots: [16]any{"not-array"},
	}
	_, err = req2.ParamIntArray("ids")
	if err == nil {
		t.Fatal("should error on wrong type")
	}
}

func TestParamStringArrayBinary(t *testing.T) {
	req := &Request{
		paramNames: []string{"tags"},
		paramCount: 1,
		paramSlots: [16]any{[]string{"a", "b"}},
	}
	tags, err := req.ParamStringArray("tags")
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags = %v, err = %v", tags, err)
	}
	// Missing param
	_, err = req.ParamStringArray("missing")
	if err == nil {
		t.Fatal("should error on missing")
	}
}

// --- ParamJSON with paramSlots ---

func TestParamJSONBinary(t *testing.T) {
	req := &Request{
		paramNames: []string{"data"},
		paramCount: 1,
		paramSlots: [16]any{"some-value"},
	}
	var v any
	err := req.ParamJSON("data", &v)
	if err != nil {
		t.Fatal(err)
	}
	if v != "some-value" {
		t.Fatalf("got %v", v)
	}

	// Missing param
	err = req.ParamJSON("missing", &v)
	if err == nil {
		t.Fatal("should error on missing")
	}

	// Non-*any target in binary mode
	var s string
	err = req.ParamJSON("data", &s)
	if err == nil {
		t.Fatal("should error on non-*any target in binary mode")
	}
}

// --- ParamDateTime edge cases ---

func TestParamDateTimeBinaryInvalid(t *testing.T) {
	req := &Request{
		paramNames: []string{"date"},
		paramCount: 1,
		paramSlots: [16]any{"not-a-date"},
	}
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("should error on invalid date format")
	}
}

func TestParamDateTimeBinaryNotString(t *testing.T) {
	req := &Request{
		paramNames: []string{"date"},
		paramCount: 1,
		paramSlots: [16]any{int64(12345)},
	}
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("should error when value is not a string")
	}
}

// --- decodeFieldMask ---

func TestDecodeFieldMask(t *testing.T) {
	// decodeFieldMask currently returns nil (stub)
	result := decodeFieldMask([]byte{0xFF}, nil)
	if result != nil {
		t.Fatal("decodeFieldMask should return nil (stub)")
	}
}

// --- ExportHandlers ---

func TestExportHandlers(t *testing.T) {
	rt := NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *Request) error {
		return nil
	})
	handlers := rt.ExportHandlers()
	if _, ok := handlers["ping"]; !ok {
		t.Fatal("ExportHandlers should include registered handlers")
	}
}

// --- ParseBinaryRequest: truncated Float param ---

func TestParseBinaryRequestTruncatedFloat(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("calc", 1)
	reg.RegisterParams("calc", []ParamMeta{
		{Name: "amount", Type: "Float", FieldID: 1},
	})
	// API ID=1, mask=0, param field ID=1, then only 2 bytes (need 8 for float)
	body := []byte{0x01, 0x00, 0x01, 0xAA, 0xBB}
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on truncated float param")
	}
}

// --- ParseBinaryRequest: truncated String param ---

func TestParseBinaryRequestTruncatedString(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("search", 1)
	reg.RegisterParams("search", []ParamMeta{
		{Name: "query", Type: "String", FieldID: 1},
	})
	// API ID=1, mask=0, param field ID=1, string len=100 but no data
	var body []byte
	body = append(body, 0x01, 0x00, 0x01) // api, mask, param field
	body = append(body, 0x64)             // string length=100
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on truncated string param")
	}
}

// --- ParseBinaryRequest: truncated Boolean param ---

func TestParseBinaryRequestTruncatedBool(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("toggle", 1)
	reg.RegisterParams("toggle", []ParamMeta{
		{Name: "active", Type: "Boolean", FieldID: 1},
	})
	// API ID=1, mask=0, param field ID=1, no bool byte
	body := []byte{0x01, 0x00, 0x01}
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on truncated bool param")
	}
}

// --- ParseBinaryRequest: field mask exceeds body ---

func TestParseBinaryRequestFieldMaskExceedsBody(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)
	// API ID=1, mask length=5 but only 2 bytes remaining
	body := []byte{0x01, 0x05, 0xAA, 0xBB}
	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error when field mask exceeds body")
	}
}

// --- ParseBinaryRequest: with actual field mask ---

func TestParseBinaryRequestWithFieldMask(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	reg.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})
	// Build: API ID=1, mask length=1, mask=[0xFF], params
	var body []byte
	body = append(body, 0x01) // API ID=1
	body = append(body, 0x01) // mask length=1
	body = append(body, 0xFF) // mask byte
	body = append(body, 0x01) // param field ID=1
	body = append(body, 0x54) // svarint 42 (zigzag: 84 = 0x54)
	body = append(body, 0x00) // terminator

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !req.BinaryMode {
		t.Fatal("should be binary mode")
	}
	if len(req.FieldMask) != 1 {
		t.Fatalf("field mask should be 1 byte, got %d", len(req.FieldMask))
	}
}

// --- EncodeBinaryRequest: int type coercion ---

func TestEncodeBinaryRequestIntCoercion(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)
	meta := []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	}
	reg.RegisterParams("test", meta)

	// Test with plain int (not int64)
	body := EncodeBinaryRequest(1, map[string]any{"id": 42}, meta)
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := req.ParamInt("id")
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

// --- EncodeBinaryRequest: missing param skipped ---

func TestEncodeBinaryRequestMissingParam(t *testing.T) {
	meta := []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
		{Name: "name", Type: "String", FieldID: 2},
	}
	// Only provide "id", not "name"
	body := EncodeBinaryRequest(1, map[string]any{"id": int64(1)}, meta)
	if len(body) == 0 {
		t.Fatal("should produce encoded body")
	}
}

func TestEncodeBinaryRequestFloat(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("calc", 1)
	meta := []ParamMeta{
		{Name: "amount", Type: "Float", FieldID: 1},
		{Name: "active", Type: "Boolean", FieldID: 2},
	}
	reg.RegisterParams("calc", meta)

	body := EncodeBinaryRequest(1, map[string]any{
		"amount": 99.5,
		"active": true,
	}, meta)

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	amount, _ := req.ParamFloat("amount")
	if amount != 99.5 {
		t.Fatalf("amount = %f", amount)
	}
	active, _ := req.ParamBool("active")
	if !active {
		t.Fatal("active should be true")
	}
}
