package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
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

	// Missing param in binary mode — returns nil (nullable semantics)
	err = req.ParamJSON("missing", &v)
	if err != nil {
		t.Fatal("binary mode missing param should not error (nullable)")
	}

	// Non-*any target in binary mode — assignBinaryParam handles typed assignment
	var s string
	err = req.ParamJSON("data", &s)
	if err != nil {
		t.Fatal("should assign string target in binary mode")
	}
	if s != "some-value" {
		t.Fatalf("expected some-value, got %q", s)
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
		// int64 is now the binary-mode raw form (svarint unix seconds) — valid.
		// Use a type the binary path doesn't accept (bool) to provoke the error.
		paramSlots: [16]any{true},
	}
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("should error when value is neither int64 nor string")
	}
}

// --- decodeFieldMask ---

func TestDecodeFieldMask(t *testing.T) {
	model := &schema.Model{Fields: []schema.Field{
		{ID: 1, Name: "id"},
		{ID: 2, Name: "name"},
		{ID: 3, Name: "posts", Relation: true},
	}}
	result := decodeFieldMask([]byte{0b00001100}, model)
	if len(result) != 2 || result[0].Name != "name" || result[1].Name != "posts" {
		t.Fatalf("decoded fields = %#v", result)
	}
	if result[0].Children != nil {
		t.Fatal("scalar field should be a leaf")
	}
	if result[1].Children == nil {
		t.Fatal("relation field should be marked non-leaf for SQL selection")
	}
}

func TestParseBinaryRequestDecodesFieldMaskFromSchema(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	s := schema.New()
	s.RegisterModel(&schema.Model{Name: "User", Fields: []schema.Field{{ID: 1, Name: "id"}, {ID: 2, Name: "name"}}})
	s.RegisterAPI(&schema.API{Name: "getUser", ReturnType: "User"})
	reg.SetSchema(s)

	body := []byte{1, 1, 0b00000100, 0}
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Select) != 1 || req.Select[0].Name != "name" {
		t.Fatalf("request selection = %#v", req.Select)
	}
}

func TestDecodeFieldMaskMissingMetadata(t *testing.T) {
	reg := NewAPIRegistry()
	if fields := reg.decodeFieldMask("missing", []byte{1}); fields != nil {
		t.Fatalf("nil schema fields = %#v, want nil", fields)
	}

	s := schema.New()
	reg.SetSchema(s)
	if fields := reg.decodeFieldMask("missing", []byte{1}); fields != nil {
		t.Fatalf("missing API fields = %#v, want nil", fields)
	}
	s.RegisterAPI(&schema.API{Name: "scalar"})
	if fields := reg.decodeFieldMask("scalar", []byte{1}); fields != nil {
		t.Fatalf("scalar API fields = %#v, want nil", fields)
	}
	s.RegisterAPI(&schema.API{Name: "unknown", ReturnType: "Missing"})
	if fields := reg.decodeFieldMask("unknown", []byte{1}); fields != nil {
		t.Fatalf("missing return type fields = %#v, want nil", fields)
	}
}

func TestDecodeFieldMaskTypeDeclaration(t *testing.T) {
	reg := NewAPIRegistry()
	s := schema.New()
	s.RegisterType(&schema.TypeDecl{Name: "Payload", Fields: []schema.Field{{ID: 1, Name: "id"}}})
	s.RegisterAPI(&schema.API{Name: "payload", ReturnType: "Payload"})
	reg.SetSchema(s)

	fields := reg.decodeFieldMask("payload", []byte{0b00000010})
	if len(fields) != 1 || fields[0].Name != "id" {
		t.Fatalf("type declaration fields = %#v", fields)
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

// --- readBinaryParam: DateTime, Enum, Duration, UUID, Decimal ---

func TestReadBinaryParamDateTime(t *testing.T) {
	// Per protocol: DateTime = svarint(unix seconds);
	// readBinaryParam formats back to RFC3339 string for handler compatibility.
	unixSec := int64(1776427200) // 2026-04-17T12:00:00Z
	var buf []byte
	buf = codec.AppendSvarint(buf, unixSec)

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "date", Type: "DateTime"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != "2026-04-17T12:00:00Z" {
		t.Fatalf("got %v, want 2026-04-17T12:00:00Z", val)
	}
}

func TestReadBinaryParamEnum(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "admin")

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "role", Type: "Enum"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != "admin" {
		t.Fatalf("got %v, want admin", val)
	}
}

func TestReadBinaryParamDuration(t *testing.T) {
	var buf []byte
	buf = codec.AppendSvarint(buf, 3600)

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "dur", Type: "Duration"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != int64(3600) {
		t.Fatalf("got %v, want 3600", val)
	}
}

func TestReadBinaryParamUUID(t *testing.T) {
	// Per protocol: UUID = 16-byte fixed; readBinaryParam formats back to
	// canonical string for ParamString compatibility.
	var buf []byte
	buf = codec.AppendUUID(buf, [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4, 0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00})

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "id", Type: "UUID"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("got %v", val)
	}
}

func TestReadBinaryParamDecimal(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "123.456")

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "price", Type: "Decimal"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != "123.456" {
		t.Fatalf("got %v", val)
	}
}

func TestReadBinaryParamUnknownType(t *testing.T) {
	_, _, err := readBinaryParam([]byte{0x01}, 0, ParamMeta{Name: "x", Type: "UnknownType"})
	if err == nil {
		t.Fatal("should error on unknown type")
	}
}

// --- assignBinaryParam ---

func TestAssignBinaryParamStringPtr(t *testing.T) {
	var s *string
	err := assignBinaryParam("hello", &s)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || *s != "hello" {
		t.Fatalf("got %v", s)
	}
}

func TestAssignBinaryParamInt64Ptr(t *testing.T) {
	var i *int64
	err := assignBinaryParam(int64(42), &i)
	if err != nil {
		t.Fatal(err)
	}
	if i == nil || *i != 42 {
		t.Fatalf("got %v", i)
	}
}

func TestAssignBinaryParamFloat64Ptr(t *testing.T) {
	var f *float64
	err := assignBinaryParam(float64(3.14), &f)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || *f != 3.14 {
		t.Fatalf("got %v", f)
	}
}

func TestAssignBinaryParamBoolPtr(t *testing.T) {
	var b *bool
	err := assignBinaryParam(true, &b)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil || !*b {
		t.Fatalf("got %v", b)
	}
}

func TestAssignBinaryParamStringDirect(t *testing.T) {
	var s string
	err := assignBinaryParam("world", &s)
	if err != nil {
		t.Fatal(err)
	}
	if s != "world" {
		t.Fatalf("got %q", s)
	}
}

func TestAssignBinaryParamInt64Direct(t *testing.T) {
	var i int64
	err := assignBinaryParam(int64(99), &i)
	if err != nil {
		t.Fatal(err)
	}
	if i != 99 {
		t.Fatalf("got %d", i)
	}
}

func TestAssignBinaryParamFloat64Direct(t *testing.T) {
	var f float64
	err := assignBinaryParam(float64(2.71), &f)
	if err != nil {
		t.Fatal(err)
	}
	if f != 2.71 {
		t.Fatalf("got %f", f)
	}
}

func TestAssignBinaryParamBoolDirect(t *testing.T) {
	var b bool
	err := assignBinaryParam(true, &b)
	if err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Fatal("should be true")
	}
}

func TestAssignBinaryParamTypeMismatch(t *testing.T) {
	// Assigning int64 to *string should silently return nil (no error, no assignment)
	var s string
	err := assignBinaryParam(int64(42), &s)
	if err != nil {
		t.Fatal("should not error on type mismatch")
	}
	if s != "" {
		t.Fatalf("should not assign, got %q", s)
	}
}

// --- EncodeBinaryRequest: Duration, DateTime, Enum, UUID, Decimal types ---

func TestEncodeBinaryRequestDuration(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("setTimer", 1)
	meta := []ParamMeta{
		{Name: "dur", Type: "Duration", FieldID: 1},
	}
	reg.RegisterParams("setTimer", meta)

	body := EncodeBinaryRequest(1, map[string]any{"dur": int64(3600)}, meta)
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := req.ParamInt("dur")
	if v != 3600 {
		t.Fatalf("dur = %d, want 3600", v)
	}
}

func TestEncodeBinaryRequestDateTime(t *testing.T) {
	meta := []ParamMeta{
		{Name: "date", Type: "DateTime", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"date": "2026-01-01T00:00:00Z"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode DateTime")
	}
}

func TestEncodeBinaryRequestEnum(t *testing.T) {
	meta := []ParamMeta{
		{Name: "role", Type: "Enum", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"role": "ADMIN"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Enum")
	}
}

func TestEncodeBinaryRequestUUID(t *testing.T) {
	meta := []ParamMeta{
		{Name: "id", Type: "UUID", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode UUID")
	}
}

func TestEncodeBinaryRequestDecimal(t *testing.T) {
	meta := []ParamMeta{
		{Name: "price", Type: "Decimal", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"price": "99.99"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Decimal")
	}
}

func TestEncodeBinaryRequestIntFloat64(t *testing.T) {
	// float64 coercion for Int type (common from JSON)
	reg := NewAPIRegistry()
	reg.Register("test", 1)
	meta := []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	}
	reg.RegisterParams("test", meta)

	body := EncodeBinaryRequest(1, map[string]any{"id": float64(42)}, meta)
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := req.ParamInt("id")
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
}

func TestEncodeBinaryRequestDurationInt(t *testing.T) {
	// Duration with plain int
	meta := []ParamMeta{
		{Name: "dur", Type: "Duration", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"dur": 100}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Duration with plain int")
	}
}

func TestEncodeBinaryRequestDurationFloat64(t *testing.T) {
	// Duration with float64
	meta := []ParamMeta{
		{Name: "dur", Type: "Duration", FieldID: 1},
	}
	body := EncodeBinaryRequest(1, map[string]any{"dur": float64(500)}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Duration with float64")
	}
}

// --- ParseBinaryRequest: field mask size exceeds limit ---

func TestParseBinaryRequestFieldMaskSizeLimit(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)

	// Build body: API ID=1, mask length = maxFieldMaskSize+1
	var body []byte
	body = codec.AppendVarint(body, 1)     // API ID
	body = codec.AppendVarint(body, 10241) // mask length > 10*1024
	// Append enough bytes to make it valid
	body = append(body, make([]byte, 10241)...)

	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on field mask size exceeding limit")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ParseBinaryRequest: field mask length > body length ---

func TestParseBinaryRequestFieldMaskLenOverflow(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)

	// mask length > remaining body
	var body []byte
	body = codec.AppendVarint(body, 1)    // API ID
	body = codec.AppendVarint(body, 5000) // mask length = 5000 but body is small

	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error on mask length overflow")
	}
}

// --- ParseBinaryRequest: too many params ---

func TestParseBinaryRequestTooManyParams(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("test", 1)

	// Register 17 params (> 16 limit)
	var params []ParamMeta
	for i := 0; i < 17; i++ {
		params = append(params, ParamMeta{
			Name:    fmt.Sprintf("p%d", i),
			Type:    "Int",
			FieldID: i + 1,
		})
	}
	reg.RegisterParams("test", params)

	// Build request with param at index 16 (exceeds limit)
	var body []byte
	body = codec.AppendVarint(body, 1) // API ID
	body = codec.AppendVarint(body, 0) // mask=0
	// Write param with field ID 17 (index 16)
	body = codec.AppendVarint(body, 17)        // field ID 17
	body = codec.AppendSvarint(body, int64(1)) // value
	body = append(body, 0x00)                  // end

	_, err := reg.ParseBinaryRequest(body)
	if err == nil {
		t.Fatal("should error when param index >= 16")
	}
	if !strings.Contains(err.Error(), "too many params") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAssignBinaryParamUnknownTarget(t *testing.T) {
	// Unknown target type — should return nil
	var x struct{ Name string }
	err := assignBinaryParam("val", &x)
	if err != nil {
		t.Fatal("should not error for unknown target type")
	}
}
