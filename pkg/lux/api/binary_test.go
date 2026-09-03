package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/shopspring/decimal"
)

func mustEncodeBinaryRequest(t *testing.T, apiID int, fieldMask []byte, params map[string]any, meta []ParamMeta) []byte {
	t.Helper()
	body, err := EncodeBinaryRequest(apiID, fieldMask, params, meta)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func testSelectionMask(fieldMask []byte, children ...codec.SelectionMaskChild) []byte {
	return codec.AppendSelectionMask(nil, fieldMask, children)
}

func TestBinaryRequestRoundTrip(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	reg.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	// Encode binary request
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": 42}, reg.paramOrder["getUser"])

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

	body := mustEncodeBinaryRequest(t, 2, nil, map[string]any{
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

func TestPrepareJSONRequestParamsProducesCanonicalBinary(t *testing.T) {
	rt := NewRouter()
	rt.Registry.Register("watch", 41)
	rt.Registry.RegisterParams("watch", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
		{Name: "payload", Type: "JSON", FieldID: 2, Nullable: true},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 41, Name: "watch", Stream: true,
		Params: []schema.Param{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "payload", Type: schema.FieldJSON, Nullable: true},
		},
	})
	req, err := parseRawRequest(map[string]json.RawMessage{
		"$api":    json.RawMessage(`"watch"`),
		"id":      json.RawMessage(`42`),
		"payload": json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.prepareRequest(req, false); err != nil {
		t.Fatal(err)
	}
	canonical, err := rt.Registry.ParseBinaryRequest(req.BinaryRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := canonical.ParamInt("id"); err != nil || got != 42 {
		t.Fatalf("canonical id = %d, err = %v", got, err)
	}
	var payload map[string]bool
	if err := canonical.ParamJSON("payload", &payload); err != nil || !payload["ok"] {
		t.Fatalf("canonical payload = %v, err = %v", payload, err)
	}
}

func TestPrepareJSONRequestParamsRejectsInvalidInput(t *testing.T) {
	rt := NewRouter()
	rt.Registry.Register("watch", 42)
	rt.Registry.RegisterParams("watch", []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 42, Name: "watch", Stream: true,
		Params: []schema.Param{{ID: 1, Name: "id", Type: schema.FieldInt}},
	})

	tests := []map[string]json.RawMessage{
		{"$api": json.RawMessage(`"watch"`)},
		{"$api": json.RawMessage(`"watch"`), "id": json.RawMessage(`1.5`)},
		{"$api": json.RawMessage(`"watch"`), "id": json.RawMessage(`1`), "extra": json.RawMessage(`true`)},
	}
	for _, raw := range tests {
		req, err := parseRawRequest(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.prepareRequest(req, false); err == nil {
			t.Fatalf("expected invalid params to fail: %v", raw)
		}
	}
}

func TestJSONParamValueCanonicalDurationAndBytes(t *testing.T) {
	tests := []struct {
		name string
		meta ParamMeta
		raw  json.RawMessage
		want any
	}{
		{name: "duration", meta: ParamMeta{Name: "ttl", Type: "Duration"}, raw: json.RawMessage(`10`), want: int64(10)},
		{name: "duration list", meta: ParamMeta{Name: "ttls", Type: "Duration", IsList: true}, raw: json.RawMessage(`[10,20]`), want: []int64{10, 20}},
		{name: "bytes", meta: ParamMeta{Name: "blob", Type: "Bytes"}, raw: json.RawMessage(`"AQI="`), want: []byte{1, 2}},
		{name: "bytes list", meta: ParamMeta{Name: "blobs", Type: "Bytes", IsList: true}, raw: json.RawMessage(`["AQI=","Aw=="]`), want: [][]byte{{1, 2}, {3}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{Params: map[string]json.RawMessage{tt.meta.Name: tt.raw}}
			got, present, err := jsonParamValue(req, tt.meta)
			if err != nil {
				t.Fatal(err)
			}
			if !present || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("value = %#v, present = %v, want %#v", got, present, tt.want)
			}
		})
	}
}

func TestJSONParamValueRejectsInvalidDurationAndBytes(t *testing.T) {
	tests := []struct {
		name string
		meta ParamMeta
		raw  json.RawMessage
	}{
		{name: "duration type", meta: ParamMeta{Name: "ttl", Type: "Duration"}, raw: json.RawMessage(`"bad"`)},
		{name: "duration list type", meta: ParamMeta{Name: "ttls", Type: "Duration", IsList: true}, raw: json.RawMessage(`10`)},
		{name: "duration list item", meta: ParamMeta{Name: "ttls", Type: "Duration", IsList: true}, raw: json.RawMessage(`[10,"bad"]`)},
		{name: "bytes", meta: ParamMeta{Name: "blob", Type: "Bytes"}, raw: json.RawMessage(`"***"`)},
		{name: "bytes list", meta: ParamMeta{Name: "blobs", Type: "Bytes", IsList: true}, raw: json.RawMessage(`["***"]`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{Params: map[string]json.RawMessage{tt.meta.Name: tt.raw}}
			if _, _, err := jsonParamValue(req, tt.meta); err == nil {
				t.Fatal("expected invalid parameter to fail")
			}
		})
	}
}

func TestBinaryRequestRetainsCanonicalWireBytes(t *testing.T) {
	rt := NewRouter()
	rt.Registry.Register("get", 43)
	meta := []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}}
	rt.Registry.RegisterParams("get", meta)
	body, err := EncodeBinaryRequest(43, nil, map[string]any{"id": int64(9)}, meta)
	if err != nil {
		t.Fatal(err)
	}
	req, err := rt.Registry.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(req.BinaryRequest(), body) {
		t.Fatalf("binary request changed: got %x want %x", req.BinaryRequest(), body)
	}
}

func TestEncodeBinaryRequestPreservesMaskBytesAndBinaryTypes(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("upload", 7)
	meta := []ParamMeta{
		{Name: "chunks", Type: "Bytes", FieldID: 1, IsList: true},
		{Name: "metadata", Type: "JSON", FieldID: 2},
	}
	reg.RegisterParams("upload", meta)
	selectionMask := testSelectionMask([]byte{0x12, 0x80})
	body := mustEncodeBinaryRequest(t, 7, selectionMask, map[string]any{
		"chunks":   [][]byte{{0xde, 0xad}, {}},
		"metadata": map[string]any{"ok": true},
	}, meta)

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(req.FieldMask, selectionMask) {
		t.Fatalf("FieldMask = %x", req.FieldMask)
	}
	chunks, ok := req.paramSlots[0].([][]byte)
	if !ok || len(chunks) != 2 || !bytes.Equal(chunks[0], []byte{0xde, 0xad}) {
		t.Fatalf("chunks = %#v", req.paramSlots[0])
	}
	if got := req.paramSlots[1].(json.RawMessage); string(got) != `{"ok":true}` {
		t.Fatalf("metadata = %s", got)
	}
}

func TestEncodeBinaryRequestRejectsWrongParamType(t *testing.T) {
	_, err := EncodeBinaryRequest(1, nil, map[string]any{"active": "yes"}, []ParamMeta{{Name: "active", Type: "Boolean", FieldID: 1}})
	if err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("error = %v", err)
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
	body := mustEncodeBinaryRequest(t, 99, nil, nil, nil)

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
	body := mustEncodeBinaryRequest(t, 999, nil, nil, nil)
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
	req := &Request{paramNames: []string{"date"}, paramCount: 1, paramSlots: [16]any{time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)}}
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

	body := mustEncodeBinaryRequest(t, 1, nil, nil, nil)
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

	// Missing required params fail; callers must opt into optional semantics.
	err = req.ParamJSON("missing", &v)
	if err == nil {
		t.Fatal("binary mode missing required param should error")
	}
	if err = req.ParamJSONOptional("missing", &v); err != nil {
		t.Fatal("binary mode missing optional param should not error")
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

func TestParamDateTimeBinaryRejectsStringRepresentation(t *testing.T) {
	req := &Request{
		paramNames: []string{"date"},
		paramCount: 1,
		paramSlots: [16]any{"not-a-date"},
	}
	_, err := req.ParamDateTime("date")
	if err == nil {
		t.Fatal("should reject non-native DateTime representation")
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
		t.Fatal("should error when value is not time.Time")
	}
}

// --- decodeFieldMask ---

func TestDecodeFieldMask(t *testing.T) {
	s := schema.New()
	model := &schema.Model{Name: "User", Fields: []schema.Field{
		{ID: 1, Name: "id"},
		{ID: 2, Name: "name"},
		{ID: 3, Name: "posts", Relation: true},
	}}
	s.RegisterModel(model)
	result, err := decodeFieldMask(testSelectionMask([]byte{0b00000110}), model, s, 0)
	if err != nil {
		t.Fatal(err)
	}
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

	mask := testSelectionMask([]byte{0b00000010})
	body := append([]byte{1, byte(len(mask))}, mask...)
	body = append(body, 0)
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
	if fields, err := reg.decodeFieldMask("missing", testSelectionMask([]byte{1})); err != nil || fields != nil {
		t.Fatalf("nil schema fields = %#v, want nil", fields)
	}

	s := schema.New()
	reg.SetSchema(s)
	if fields, err := reg.decodeFieldMask("missing", testSelectionMask([]byte{1})); err != nil || fields != nil {
		t.Fatalf("missing API fields = %#v, want nil", fields)
	}
	s.RegisterAPI(&schema.API{Name: "scalar"})
	if fields, err := reg.decodeFieldMask("scalar", testSelectionMask([]byte{1})); err != nil || fields != nil {
		t.Fatalf("scalar API fields = %#v, want nil", fields)
	}
	s.RegisterAPI(&schema.API{Name: "unknown", ReturnType: "Missing"})
	if fields, err := reg.decodeFieldMask("unknown", testSelectionMask([]byte{1})); err != nil || fields != nil {
		t.Fatalf("missing return type fields = %#v, want nil", fields)
	}
}

func TestDecodeFieldMaskTypeDeclaration(t *testing.T) {
	reg := NewAPIRegistry()
	s := schema.New()
	s.RegisterType(&schema.TypeDecl{Name: "Payload", Fields: []schema.Field{{ID: 1, Name: "id"}}})
	s.RegisterAPI(&schema.API{Name: "payload", ReturnType: "Payload"})
	reg.SetSchema(s)

	fields, err := reg.decodeFieldMask("payload", testSelectionMask([]byte{0b00000001}))
	if err != nil {
		t.Fatal(err)
	}
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

func TestParseBinaryRequestRejectsNonCanonicalBool(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("toggle", 1)
	reg.RegisterParams("toggle", []ParamMeta{{Name: "active", Type: "Boolean", FieldID: 1}})
	if _, err := reg.ParseBinaryRequest([]byte{1, 0, 1, 2, 0}); err == nil {
		t.Fatal("non-canonical bool must fail")
	}
}

func TestParseBinaryRequestRejectsNonCanonicalBoolArray(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("toggle", 1)
	reg.RegisterParams("toggle", []ParamMeta{{Name: "active", Type: "Boolean", FieldID: 1, IsList: true}})
	if _, err := reg.ParseBinaryRequest([]byte{1, 0, 1, 1, 2, 0}); err == nil {
		t.Fatal("non-canonical bool array item must fail")
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
	// Build: API ID=1, recursive selection mask, params
	mask := testSelectionMask([]byte{0xFF})
	var body []byte
	body = append(body, 0x01) // API ID=1
	body = append(body, byte(len(mask)))
	body = append(body, mask...)
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
	if !bytes.Equal(req.FieldMask, mask) {
		t.Fatalf("field mask = %v, want %v", req.FieldMask, mask)
	}
}

func TestParseBinaryRequestDecodesNestedFieldMask(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	s := schema.New()
	s.RegisterModel(&schema.Model{Name: "Post", Fields: []schema.Field{{ID: 1, Name: "id"}, {ID: 2, Name: "title"}}})
	s.RegisterModel(&schema.Model{Name: "User", Fields: []schema.Field{
		{ID: 1, Name: "id"},
		{ID: 3, Name: "posts", Type: schema.FieldModel, TypeName: "Post", IsList: true, Relation: true},
	}})
	s.RegisterAPI(&schema.API{Name: "getUser", ReturnType: "User"})
	reg.SetSchema(s)
	child := testSelectionMask(codec.FieldMaskSet(nil, 2))
	rootFields := codec.FieldMaskSet(nil, 1)
	rootFields = codec.FieldMaskSet(rootFields, 3)
	mask := testSelectionMask(rootFields, codec.SelectionMaskChild{FieldID: 3, Mask: child})

	req, err := reg.ParseBinaryRequest(mustEncodeBinaryRequest(t, 1, mask, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Select) != 2 || req.Select[1].Name != "posts" || len(req.Select[1].Children) != 1 || req.Select[1].Children[0].Name != "title" {
		t.Fatalf("nested selection = %#v", req.Select)
	}
}

func TestParseBinaryRequestRejectsInvalidSelectionTree(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("getUser", 1)
	s := schema.New()
	s.RegisterModel(&schema.Model{Name: "User", Fields: []schema.Field{{ID: 1, Name: "id"}}})
	s.RegisterAPI(&schema.API{Name: "getUser", ReturnType: "User"})
	reg.SetSchema(s)

	unknown := testSelectionMask(codec.FieldMaskSet(nil, 2))
	body := append([]byte{1, byte(len(unknown))}, unknown...)
	body = append(body, 0)
	if _, err := reg.ParseBinaryRequest(body); err == nil {
		t.Fatal("unknown selected field ID must fail")
	}
	malformed := []byte{1, 1, 0}
	body = append([]byte{1, byte(len(malformed))}, malformed...)
	body = append(body, 0)
	if _, err := reg.ParseBinaryRequest(body); err == nil {
		t.Fatal("malformed recursive mask must fail")
	}
}

func TestParseBinaryRequestAppliesPaginationParams(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("listUsers", 1)
	meta := []ParamMeta{{Name: "page", Type: "Int", FieldID: 1}, {Name: "pageSize", Type: "Int", FieldID: 2}}
	reg.RegisterParams("listUsers", meta)
	req, err := reg.ParseBinaryRequest(mustEncodeBinaryRequest(t, 1, nil, map[string]any{"page": 3, "pageSize": 40}, meta))
	if err != nil {
		t.Fatal(err)
	}
	if req.Page != 3 || req.PageSize != 40 {
		t.Fatalf("pagination = %d/%d, want 3/40", req.Page, req.PageSize)
	}
}

func TestBinaryRequestRoundTripsFiltersAndSorters(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("listUsers", 1)
	params := map[string]any{
		"$filters": []Filter{
			{Field: "age", Operator: "gte", Value: "18"},
			{Field: "name", Operator: "contains", Value: "lin"},
		},
		"$sorters": []Sorter{{Field: "createdAt", Order: "desc"}},
	}
	req, err := reg.ParseBinaryRequest(mustEncodeBinaryRequest(t, 1, nil, params, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req.Filters, params["$filters"]) {
		t.Fatalf("filters = %#v", req.Filters)
	}
	if !reflect.DeepEqual(req.Sorters, params["$sorters"]) {
		t.Fatalf("sorters = %#v", req.Sorters)
	}
}

func TestBinaryRequestRejectsInvalidListControls(t *testing.T) {
	invalid := []map[string]any{
		{"$filters": []Filter{{Field: "name", Operator: "unknown", Value: "x"}}},
		{"$filters": []Filter{{Field: "", Operator: "eq", Value: "x"}}},
		{"$sorters": []Sorter{{Field: "name", Order: "sideways"}}},
		{"$sorters": []Sorter{{Field: "", Order: "asc"}}},
	}
	for _, params := range invalid {
		if _, err := EncodeBinaryRequest(1, nil, params, nil); err == nil {
			t.Fatalf("invalid controls unexpectedly encoded: %#v", params)
		}
	}
	if _, err := EncodeBinaryRequest(1, nil, map[string]any{"$filters": []any{}}, nil); err == nil {
		t.Fatal("untyped filters must be rejected")
	}
}

func TestParseBinaryRequestRejectsMalformedListControls(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("listUsers", 1)
	var body []byte
	body = codec.AppendVarint(body, 1)
	body = codec.AppendVarint(body, 0)
	body = codec.AppendVarint(body, BinarySortersFieldID)
	body = codec.AppendVarint(body, 1)
	body = codec.AppendString(body, "name")
	body = codec.AppendVarint(body, 2)
	body = append(body, 0)
	if _, err := reg.ParseBinaryRequest(body); err == nil {
		t.Fatal("non-canonical sorter order must fail")
	}
}

func TestParseBinaryRequestRejectsAllMalformedListControlShapes(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("listUsers", 1)
	request := func(fieldID int, payload []byte) []byte {
		body := []byte{1, 0}
		body = codec.AppendVarint(body, uint64(fieldID))
		body = append(body, payload...)
		return append(body, 0)
	}
	filter := func(field string, operator uint64, value string) []byte {
		payload := codec.AppendVarint(nil, 1)
		payload = codec.AppendString(payload, field)
		payload = codec.AppendVarint(payload, operator)
		return codec.AppendString(payload, value)
	}
	sorter := func(field string, descending byte) []byte {
		payload := codec.AppendVarint(nil, 1)
		payload = codec.AppendString(payload, field)
		return append(payload, descending)
	}
	tests := [][]byte{
		request(BinaryFiltersFieldID, codec.AppendVarint(nil, 1001)),
		request(BinaryFiltersFieldID, filter("", 1, "x")),
		request(BinaryFiltersFieldID, filter("name", 11, "x")),
		request(BinaryFiltersFieldID, []byte{1, 4, 'n'}),
		request(BinarySortersFieldID, codec.AppendVarint(nil, 101)),
		request(BinarySortersFieldID, sorter("", 0)),
		request(BinarySortersFieldID, sorter("name", 2)),
	}
	for _, body := range tests {
		if _, err := reg.ParseBinaryRequest(body); err == nil {
			t.Fatalf("malformed controls accepted: %v", body)
		}
	}

	duplicateFilters := []byte{1, 0}
	duplicateFilters = codec.AppendVarint(duplicateFilters, BinaryFiltersFieldID)
	duplicateFilters = codec.AppendVarint(duplicateFilters, 0)
	duplicateFilters = codec.AppendVarint(duplicateFilters, BinaryFiltersFieldID)
	duplicateFilters = codec.AppendVarint(duplicateFilters, 0)
	duplicateFilters = append(duplicateFilters, 0)
	if _, err := reg.ParseBinaryRequest(duplicateFilters); err == nil {
		t.Fatal("duplicate filters accepted")
	}

	duplicateSorters := []byte{1, 0}
	duplicateSorters = codec.AppendVarint(duplicateSorters, BinarySortersFieldID)
	duplicateSorters = codec.AppendVarint(duplicateSorters, 0)
	duplicateSorters = codec.AppendVarint(duplicateSorters, BinarySortersFieldID)
	duplicateSorters = codec.AppendVarint(duplicateSorters, 0)
	duplicateSorters = append(duplicateSorters, 0)
	if _, err := reg.ParseBinaryRequest(duplicateSorters); err == nil {
		t.Fatal("duplicate sorters accepted")
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
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": 42}, meta)
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
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": int64(1)}, meta)
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

	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{
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
	if val != time.Unix(unixSec, 0).UTC() {
		t.Fatalf("got %v, want native time.Time", val)
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
	if val != time.Duration(3600) {
		t.Fatalf("got %v, want 3600", val)
	}
}

func TestReadBinaryParamUUID(t *testing.T) {
	var buf []byte
	buf = codec.AppendUUID(buf, [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4, 0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00})

	val, n, err := readBinaryParam(buf, 0, ParamMeta{Name: "id", Type: "UUID"})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("should consume bytes")
	}
	if val != uuid.MustParse("550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("got %v", val)
	}
}

func TestReadBinaryParamJSONPreservesRawJSON(t *testing.T) {
	var buf []byte
	buf = codec.AppendBytes(buf, []byte(`{"name":"luxo"}`))
	val, _, err := readBinaryParam(buf, 0, ParamMeta{Name: "input", Type: "JSON"})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := val.(json.RawMessage); !ok || string(got) != `{"name":"luxo"}` {
		t.Fatalf("JSON param = %#v", val)
	}
}

func TestStructuredJSONParamsRoundTripIntoTypedTargets(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	meta := []ParamMeta{
		{Name: "input", Type: "JSON", FieldID: 1},
		{Name: "inputs", Type: "JSON", FieldID: 2, IsList: true},
	}
	body := mustEncodeBinaryRequest(t, 9, nil, map[string]any{
		"input":  input{Name: "one"},
		"inputs": []any{input{Name: "two"}, input{Name: "three"}},
	}, meta)
	registry := NewAPIRegistry()
	registry.Register("create", 9)
	registry.RegisterParams("create", meta)
	req, err := registry.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var one input
	var many []input
	if err := req.ParamJSON("input", &one); err != nil {
		t.Fatal(err)
	}
	if err := req.ParamJSON("inputs", &many); err != nil {
		t.Fatal(err)
	}
	if one.Name != "one" || !reflect.DeepEqual(many, []input{{Name: "two"}, {Name: "three"}}) {
		t.Fatalf("round trip = %#v / %#v", one, many)
	}
}

func TestNullableBinaryParamsDistinguishNullPresentAndAbsent(t *testing.T) {
	meta := []ParamMeta{
		{Name: "nickname", Type: "String", FieldID: 1, Nullable: true},
		{Name: "age", Type: "Int", FieldID: 2, Nullable: true},
		{Name: "bio", Type: "String", FieldID: 3, Nullable: true},
	}
	body := mustEncodeBinaryRequest(t, 10, nil, map[string]any{
		"nickname": nil,
		"age":      int64(42),
	}, meta)
	registry := NewAPIRegistry()
	registry.Register("update", 10)
	registry.RegisterParams("update", meta)
	req, err := registry.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !req.HasParam("nickname") || !req.ParamIsNull("nickname") {
		t.Fatal("explicit null was not preserved")
	}
	if !req.HasParam("age") || req.ParamIsNull("age") {
		t.Fatal("present value marker was not preserved")
	}
	if req.HasParam("bio") || req.ParamIsNull("bio") {
		t.Fatal("absent param must remain absent")
	}
	var nickname *string
	if err := req.ParamJSONOptionalNullable("nickname", &nickname); err != nil || nickname != nil {
		t.Fatalf("nickname = %v, err = %v", nickname, err)
	}
	var age *int64
	if err := req.ParamJSONOptionalNullable("age", &age); err != nil || age == nil || *age != 42 {
		t.Fatalf("age = %v, err = %v", age, err)
	}
}

func TestNullableBinaryParamRequiresMarker(t *testing.T) {
	registry := NewAPIRegistry()
	registry.Register("update", 10)
	registry.RegisterParams("update", []ParamMeta{{Name: "name", Type: "String", FieldID: 1, Nullable: true}})
	if _, err := registry.ParseBinaryRequest([]byte{10, 0, 1}); err == nil {
		t.Fatal("missing nullable marker should fail")
	}
}

func TestParseBinaryRequestRejectsMalformedParamEnvelope(t *testing.T) {
	registry := NewAPIRegistry()
	registry.Register("update", 10)
	registry.RegisterParams("update", []ParamMeta{{Name: "name", Type: "String", FieldID: 1, Nullable: true}})

	tests := []struct {
		name string
		body []byte
	}{
		{name: "missing terminator", body: []byte{10, 0}},
		{name: "truncated field id", body: []byte{10, 0, 0x80}},
		{name: "trailing bytes", body: []byte{10, 0, 0, 0}},
		{name: "invalid nullable marker", body: []byte{10, 0, 1, 2, 0}},
		{name: "duplicate field", body: []byte{10, 0, 1, 0, 1, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := registry.ParseBinaryRequest(tt.body); err == nil {
				t.Fatal("malformed request should fail")
			}
		})
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
	if !val.(decimal.Decimal).Equal(decimal.RequireFromString("123.456")) {
		t.Fatalf("got %v", val)
	}
}

func TestReadBinaryParamUnknownType(t *testing.T) {
	_, _, err := readBinaryParam([]byte{0x01}, 0, ParamMeta{Name: "x", Type: "UnknownType"})
	if err == nil {
		t.Fatal("should error on unknown type")
	}
}

func TestParseBinaryRequestListParams(t *testing.T) {
	reg := NewAPIRegistry()
	reg.Register("allLists", 7)
	reg.RegisterParams("allLists", []ParamMeta{
		{Name: "ints", Type: "Int", FieldID: 1, IsList: true},
		{Name: "floats", Type: "Float", FieldID: 2, IsList: true},
		{Name: "strings", Type: "String", FieldID: 3, IsList: true},
		{Name: "bools", Type: "Boolean", FieldID: 4, IsList: true},
		{Name: "dates", Type: "DateTime", FieldID: 5, IsList: true},
		{Name: "durations", Type: "Duration", FieldID: 6, IsList: true},
		{Name: "uuids", Type: "UUID", FieldID: 7, IsList: true},
		{Name: "decimals", Type: "Decimal", FieldID: 8, IsList: true},
		{Name: "bytes", Type: "Bytes", FieldID: 9, IsList: true},
	})

	var enc codec.Encoder
	enc.WriteFieldIntArray(1, []int64{1, -2})
	enc.WriteFieldFloatArray(2, []float64{1.5, -2.25})
	enc.WriteFieldStringArray(3, []string{"a", "bb"})
	enc.WriteFieldBoolArray(4, []bool{true, false})
	enc.WriteFieldIntArray(5, []int64{0, 1_700_000_000})
	enc.WriteFieldIntArray(6, []int64{10, 20})
	uuidA := [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4, 0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00}
	enc.WriteFieldUUIDArray(7, [][16]byte{uuidA})
	enc.WriteFieldStringArray(8, []string{"1.25", "2.50"})
	enc.WriteFieldBytesArray(9, [][]byte{{1, 2}, {3}})
	enc.WriteEnd()
	body := []byte{7, 0}
	body = append(body, enc.Bytes()...)

	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]any{
		"ints":      []int64{1, -2},
		"floats":    []float64{1.5, -2.25},
		"strings":   []string{"a", "bb"},
		"bools":     []bool{true, false},
		"durations": []time.Duration{10, 20},
		"decimals":  []decimal.Decimal{decimal.RequireFromString("1.25"), decimal.RequireFromString("2.50")},
		"bytes":     [][]byte{{1, 2}, {3}},
	}
	for name, want := range wants {
		got, ok := req.findParam(name)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	if got, _ := req.findParam("dates"); !reflect.DeepEqual(got, []time.Time{time.Unix(0, 0).UTC(), time.Unix(1_700_000_000, 0).UTC()}) {
		t.Errorf("dates = %#v", got)
	}
	if got, _ := req.findParam("uuids"); !reflect.DeepEqual(got, []uuid.UUID{uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")}) {
		t.Errorf("uuids = %#v", got)
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

	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"dur": int64(3600)}, meta)
	req, err := reg.ParseBinaryRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var v time.Duration
	if err := req.ParamJSON("dur", &v); err != nil {
		t.Fatal(err)
	}
	if v != 3600 {
		t.Fatalf("dur = %d, want 3600", v)
	}
}

func TestEncodeBinaryRequestDateTime(t *testing.T) {
	meta := []ParamMeta{
		{Name: "date", Type: "DateTime", FieldID: 1},
	}
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"date": "2026-01-01T00:00:00Z"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode DateTime")
	}
}

func TestEncodeBinaryRequestEnum(t *testing.T) {
	meta := []ParamMeta{
		{Name: "role", Type: "Enum", FieldID: 1},
	}
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"role": "ADMIN"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Enum")
	}
}

func TestEncodeBinaryRequestUUID(t *testing.T) {
	meta := []ParamMeta{
		{Name: "id", Type: "UUID", FieldID: 1},
	}
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000"}, meta)
	if len(body) == 0 {
		t.Fatal("should encode UUID")
	}
}

func TestEncodeBinaryRequestDecimal(t *testing.T) {
	meta := []ParamMeta{
		{Name: "price", Type: "Decimal", FieldID: 1},
	}
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"price": "99.99"}, meta)
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

	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"id": float64(42)}, meta)
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
	body := mustEncodeBinaryRequest(t, 1, nil, map[string]any{"dur": 100}, meta)
	if len(body) == 0 {
		t.Fatal("should encode Duration with plain int")
	}
}

func TestEncodeBinaryRequestRejectsDurationFloat64(t *testing.T) {
	meta := []ParamMeta{
		{Name: "dur", Type: "Duration", FieldID: 1},
	}
	if _, err := EncodeBinaryRequest(1, nil, map[string]any{"dur": float64(500)}, meta); err == nil {
		t.Fatal("should reject lossy Duration representation")
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

func TestEncodeBinaryRequestAllScalarTypesRoundTrip(t *testing.T) {
	instant := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	meta := []ParamMeta{
		{Name: "integer", Type: "Int", FieldID: 1},
		{Name: "float", Type: "Float", FieldID: 2},
		{Name: "text", Type: "String", FieldID: 3},
		{Name: "active", Type: "Boolean", FieldID: 4},
		{Name: "createdAt", Type: "DateTime", FieldID: 5},
		{Name: "ttl", Type: "Duration", FieldID: 6},
		{Name: "id", Type: "UUID", FieldID: 7},
		{Name: "price", Type: "Decimal", FieldID: 8},
		{Name: "blob", Type: "Bytes", FieldID: 9},
		{Name: "role", Type: "Enum", FieldID: 10},
		{Name: "metadata", Type: "JSON", FieldID: 11},
	}
	params := map[string]any{
		"integer": int(42), "float": float32(1.5), "text": "luxo", "active": true,
		"createdAt": instant, "ttl": 3 * time.Second, "id": id,
		"price": decimal.RequireFromString("12.50"), "blob": []byte{0, 255},
		"role": "ADMIN", "metadata": map[string]any{"ok": true},
	}
	reg := NewAPIRegistry()
	reg.Register("allScalars", 1)
	reg.RegisterParams("allScalars", meta)
	req, err := reg.ParseBinaryRequest(mustEncodeBinaryRequest(t, 1, nil, params, meta))
	if err != nil {
		t.Fatal(err)
	}
	wants := []any{
		int64(42), 1.5, "luxo", true, instant, 3 * time.Second, id,
		decimal.RequireFromString("12.5"), []byte{0, 255}, "ADMIN", json.RawMessage(`{"ok":true}`),
	}
	for i, want := range wants {
		if !reflect.DeepEqual(req.paramSlots[i], want) {
			t.Errorf("param %s = %#v, want %#v", meta[i].Name, req.paramSlots[i], want)
		}
	}
}

func TestEncodeBinaryRequestAllListTypesRoundTrip(t *testing.T) {
	instant := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	meta := []ParamMeta{
		{Name: "integers", Type: "Int", FieldID: 1, IsList: true},
		{Name: "floats", Type: "Float", FieldID: 2, IsList: true},
		{Name: "texts", Type: "String", FieldID: 3, IsList: true},
		{Name: "flags", Type: "Boolean", FieldID: 4, IsList: true},
		{Name: "dates", Type: "DateTime", FieldID: 5, IsList: true},
		{Name: "durations", Type: "Duration", FieldID: 6, IsList: true},
		{Name: "ids", Type: "UUID", FieldID: 7, IsList: true},
		{Name: "prices", Type: "Decimal", FieldID: 8, IsList: true},
		{Name: "blobs", Type: "Bytes", FieldID: 9, IsList: true},
		{Name: "roles", Type: "Enum", FieldID: 10, IsList: true},
		{Name: "metadata", Type: "JSON", FieldID: 11, IsList: true},
	}
	params := map[string]any{
		"integers": []int{1, -2}, "floats": []float64{1.5}, "texts": []string{"a"},
		"flags": []bool{true, false}, "dates": []time.Time{instant},
		"durations": []time.Duration{time.Second}, "ids": []uuid.UUID{id},
		"prices": []decimal.Decimal{decimal.RequireFromString("1.25")},
		"blobs":  [][]byte{{1, 2}}, "roles": []string{"ADMIN"},
		"metadata": []any{map[string]any{"ok": true}},
	}
	reg := NewAPIRegistry()
	reg.Register("allLists", 1)
	reg.RegisterParams("allLists", meta)
	req, err := reg.ParseBinaryRequest(mustEncodeBinaryRequest(t, 1, nil, params, meta))
	if err != nil {
		t.Fatal(err)
	}
	wants := []any{
		[]int64{1, -2}, []float64{1.5}, []string{"a"}, []bool{true, false},
		[]time.Time{instant}, []time.Duration{time.Second}, []uuid.UUID{id},
		[]decimal.Decimal{decimal.RequireFromString("1.25")}, [][]byte{{1, 2}},
		[]string{"ADMIN"}, []json.RawMessage{json.RawMessage(`{"ok":true}`)},
	}
	for i, want := range wants {
		if !reflect.DeepEqual(req.paramSlots[i], want) {
			t.Errorf("param %s = %#v, want %#v", meta[i].Name, req.paramSlots[i], want)
		}
	}
}

func TestEncodeBinaryRequestRejectsInvalidListElements(t *testing.T) {
	tests := []struct {
		typeName string
		value    any
	}{
		{typeName: "Int", value: []any{"x"}},
		{typeName: "Duration", value: []any{"x"}},
		{typeName: "Float", value: []any{"x"}},
		{typeName: "String", value: []any{1}},
		{typeName: "Decimal", value: []any{"invalid"}},
		{typeName: "Boolean", value: []any{1}},
		{typeName: "DateTime", value: []any{"invalid"}},
		{typeName: "UUID", value: []any{"invalid"}},
		{typeName: "Bytes", value: []any{"invalid"}},
		{typeName: "Unknown", value: []any{"value"}},
	}
	for _, tt := range tests {
		meta := []ParamMeta{{Name: "values", Type: tt.typeName, FieldID: 1, IsList: true}}
		if _, err := EncodeBinaryRequest(1, nil, map[string]any{"values": tt.value}, meta); err == nil {
			t.Errorf("invalid %s list encoded", tt.typeName)
		}
	}
	if _, err := EncodeBinaryRequest(1, nil, map[string]any{"values": 1}, []ParamMeta{{Name: "values", Type: "Int", FieldID: 1, IsList: true}}); err == nil {
		t.Fatal("non-list value encoded")
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
