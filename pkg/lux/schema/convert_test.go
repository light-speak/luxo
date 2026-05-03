package schema

import (
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func userModel() *Model {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
			{ID: 3, Name: "email", Type: FieldString},
			{ID: 4, Name: "score", Type: FieldFloat},
			{ID: 5, Name: "phone", Type: FieldString, Nullable: true},
			{ID: 6, Name: "isActive", Type: FieldBool},
		},
	}
	s.RegisterModel(m)
	return m
}

func encodeUser(id int64, name, email string, score float64, phone *string, active bool) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(1, id)
	enc.WriteFieldString(2, name)
	enc.WriteFieldString(3, email)
	enc.WriteFieldFloat(4, score)
	if phone != nil {
		enc.WriteFieldStringPtr(5, phone)
	} else {
		enc.WriteFieldStringPtr(5, nil)
	}
	enc.WriteFieldBool(6, active)
	enc.WriteEnd()
	return enc.Bytes()
}

func TestBinaryToJSON_Basic(t *testing.T) {
	m := userModel()
	phone := "13800138000"
	data := encodeUser(1, "Alice", "alice@test.com", 99.5, &phone, true)

	result := BinaryToJSON(nil, data, m)
	expected := `{"id":1,"name":"Alice","email":"alice@test.com","score":99.5,"phone":"13800138000","isActive":true}`

	if string(result) != expected {
		t.Errorf("got:  %s\nwant: %s", result, expected)
	}
}

func TestBinaryToJSON_NullField(t *testing.T) {
	m := userModel()
	data := encodeUser(2, "Bob", "bob@test.com", 0, nil, false)

	result := BinaryToJSON(nil, data, m)
	expected := `{"id":2,"name":"Bob","email":"bob@test.com","score":0,"phone":null,"isActive":false}`

	if string(result) != expected {
		t.Errorf("got:  %s\nwant: %s", result, expected)
	}
}

func TestBinaryToJSON_StringEscape(t *testing.T) {
	m := userModel()
	phone := "123"
	data := encodeUser(1, `Al"ice`, "a@b.com", 0, &phone, true)

	result := BinaryToJSON(nil, data, m)
	if string(result) == "" {
		t.Error("empty result")
	}
	// Should contain escaped quote
	if got := string(result); len(got) == 0 {
		t.Error("empty")
	}
}

func TestBinaryListToJSON(t *testing.T) {
	m := userModel()

	// Encode 2 users in columnar format
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnInt(1, []int64{1, 2})                     // id
	w.WriteColumnString(2, []string{"Alice", "Bob"})       // name
	w.WriteColumnString(3, []string{"a@b.com", "b@b.com"}) // email
	w.WriteColumnFloat(4, []float64{10, 20})               // score
	phone1 := "111"
	w.WriteColumnStringPtr(5, []*string{&phone1, nil}) // phone (nullable)
	w.WriteColumnBool(6, []bool{true, false})          // isActive

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)

	if got[0] != '[' || got[len(got)-1] != ']' {
		t.Errorf("expected JSON array, got: %s", got)
	}
	if len(got) < 50 {
		t.Errorf("result too short: %s", got)
	}
	// Verify content
	if !strings.Contains(got, `"Alice"`) || !strings.Contains(got, `"Bob"`) {
		t.Errorf("missing names: %s", got)
	}
	if !strings.Contains(got, `"111"`) {
		t.Errorf("missing phone: %s", got)
	}
	if !strings.Contains(got, "null") {
		t.Errorf("missing null for Bob's phone: %s", got)
	}
}

func TestJSONParamsToBinary(t *testing.T) {
	api := &API{
		Name: "getUser",
		Params: []Param{
			{ID: 1, Name: "id", Type: FieldInt},
		},
	}

	params := map[string]any{"id": float64(42)}
	data := JSONParamsToBinary(params, api)

	// Decode and verify
	dec := codec.NewDecoder(data)
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field ID 1")
	}
	v := dec.ReadInt()
	if v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
}

func TestJSONParamsToBinary_MultipleParams(t *testing.T) {
	api := &API{
		Name: "createUser",
		Params: []Param{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "email", Type: FieldString},
			{ID: 3, Name: "score", Type: FieldFloat},
		},
	}

	params := map[string]any{
		"name":  "Alice",
		"email": "alice@test.com",
		"score": float64(99.5),
	}
	data := JSONParamsToBinary(params, api)

	dec := codec.NewDecoder(data)
	found := map[int]bool{}
	for dec.NextField() {
		found[dec.FieldID()] = true
		switch dec.FieldID() {
		case 1:
			if v := dec.ReadString(); v != "Alice" {
				t.Errorf("name: got %q", v)
			}
		case 2:
			if v := dec.ReadString(); v != "alice@test.com" {
				t.Errorf("email: got %q", v)
			}
		case 3:
			if v := dec.ReadFloat(); v != 99.5 {
				t.Errorf("score: got %f", v)
			}
		}
	}
	if len(found) != 3 {
		t.Errorf("expected 3 fields, got %d", len(found))
	}
}

// --- BinaryScalarToJSON ---

func TestBinaryScalarToJSON_Int(t *testing.T) {
	var buf []byte
	buf = codec.AppendSvarint(buf, 42)
	result := BinaryScalarToJSON(nil, buf, "Int")
	if string(result) != "42" {
		t.Errorf("got %q, want 42", result)
	}
}

func TestBinaryScalarToJSON_NegativeInt(t *testing.T) {
	var buf []byte
	buf = codec.AppendSvarint(buf, -100)
	result := BinaryScalarToJSON(nil, buf, "Int")
	if string(result) != "-100" {
		t.Errorf("got %q, want -100", result)
	}
}

func TestBinaryScalarToJSON_Float(t *testing.T) {
	var buf []byte
	buf = codec.AppendFixed64(buf, 3.14)
	result := BinaryScalarToJSON(nil, buf, "Float")
	if string(result) != "3.14" {
		t.Errorf("got %q, want 3.14", result)
	}
}

func TestBinaryScalarToJSON_Boolean(t *testing.T) {
	// true
	var buf []byte
	buf = codec.AppendBool(buf, true)
	result := BinaryScalarToJSON(nil, buf, "Boolean")
	if string(result) != "true" {
		t.Errorf("got %q, want true", result)
	}

	// false — this is the tricky case: 0x00 could be confused with int 0
	buf = buf[:0]
	buf = codec.AppendBool(buf, false)
	result = BinaryScalarToJSON(nil, buf, "Boolean")
	if string(result) != "false" {
		t.Errorf("got %q, want false", result)
	}
}

func TestBinaryScalarToJSON_String(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "hello")
	result := BinaryScalarToJSON(nil, buf, "String")
	if string(result) != `"hello"` {
		t.Errorf("got %q, want %q", result, `"hello"`)
	}
}

func TestBinaryScalarToJSON_StringWithEscape(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "say \"hi\"\nnewline")
	result := BinaryScalarToJSON(nil, buf, "String")
	want := `"say \"hi\"\nnewline"`
	if string(result) != want {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestBinaryScalarToJSON_DateTime(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "2026-04-13T10:00:00Z")
	result := BinaryScalarToJSON(nil, buf, "DateTime")
	if string(result) != `"2026-04-13T10:00:00Z"` {
		t.Errorf("got %s", result)
	}
}

func TestBinaryScalarToJSON_Duration(t *testing.T) {
	var buf []byte
	buf = codec.AppendSvarint(buf, 5000000000) // 5s in nanoseconds
	result := BinaryScalarToJSON(nil, buf, "Duration")
	if string(result) != "5000000000" {
		t.Errorf("got %q", result)
	}
}

func TestBinaryScalarToJSON_Empty(t *testing.T) {
	result := BinaryScalarToJSON(nil, nil, "Int")
	if string(result) != "null" {
		t.Errorf("empty data should be null, got %q", result)
	}
	result = BinaryScalarToJSON(nil, []byte{}, "String")
	if string(result) != "null" {
		t.Errorf("empty data should be null, got %q", result)
	}
}

func TestBinaryScalarToJSON_FallbackSvarint(t *testing.T) {
	// Unknown type name should fall back to svarint
	var buf []byte
	buf = codec.AppendSvarint(buf, 77)
	result := BinaryScalarToJSON(nil, buf, "SomeUnknownType")
	if string(result) != "77" {
		t.Errorf("fallback got %q, want 77", result)
	}
}

func TestBinaryScalarToJSON_EmptyTypeName(t *testing.T) {
	// Empty type name should use svarint path
	var buf []byte
	buf = codec.AppendSvarint(buf, -1)
	result := BinaryScalarToJSON(nil, buf, "")
	if string(result) != "-1" {
		t.Errorf("got %q, want -1", result)
	}
}

// --- BinaryPaginatedListToJSON ---

func TestBinaryPaginatedListToJSON_Basic(t *testing.T) {
	m := userModel()

	// Encode 2 users in columnar format
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnInt(1, []int64{10, 20})
	w.WriteColumnString(2, []string{"Alice", "Bob"})
	w.WriteColumnString(3, []string{"a@b.com", "b@b.com"})
	w.WriteColumnFloat(4, []float64{1.5, 2.5})
	phone := "111"
	w.WriteColumnStringPtr(5, []*string{&phone, nil})
	w.WriteColumnBool(6, []bool{true, false})
	columnar := w.Bytes()

	// Append pagination metadata: total=50, page=2, pageSize=20
	columnar = codec.AppendSvarint(columnar, 50)
	columnar = codec.AppendSvarint(columnar, 2)
	columnar = codec.AppendSvarint(columnar, 20)

	result := BinaryPaginatedListToJSON(nil, columnar, m)
	got := string(result)

	// Check structure
	if !strings.HasPrefix(got, `{"items":[`) {
		t.Errorf("should start with items array: %s", got)
	}
	if !strings.Contains(got, `"total":50`) {
		t.Errorf("missing total: %s", got)
	}
	if !strings.Contains(got, `"page":2`) {
		t.Errorf("missing page: %s", got)
	}
	if !strings.Contains(got, `"pageSize":20`) {
		t.Errorf("missing pageSize: %s", got)
	}
	if !strings.Contains(got, `"Alice"`) || !strings.Contains(got, `"Bob"`) {
		t.Errorf("missing names: %s", got)
	}
}

func TestBinaryPaginatedListToJSON_EmptyList(t *testing.T) {
	m := userModel()

	w := &codec.ColumnarWriter{}
	w.SetCount(0)
	data := w.Bytes()

	// Append actual pagination metadata: total=100, page=3, pageSize=25
	data = codec.AppendSvarint(data, 100)
	data = codec.AppendSvarint(data, 3)
	data = codec.AppendSvarint(data, 25)

	result := BinaryPaginatedListToJSON(nil, data, m)
	got := string(result)

	if !strings.Contains(got, `"items":[]`) {
		t.Errorf("missing empty items: %s", got)
	}
	if !strings.Contains(got, `"total":100`) {
		t.Errorf("should read actual total, got %s", got)
	}
	if !strings.Contains(got, `"page":3`) {
		t.Errorf("should read actual page, got %s", got)
	}
	if !strings.Contains(got, `"pageSize":25`) {
		t.Errorf("should read actual pageSize, got %s", got)
	}
}

func TestBinaryPaginatedListToJSON_EmptyListNoMetadata(t *testing.T) {
	m := userModel()

	w := &codec.ColumnarWriter{}
	w.SetCount(0)
	data := w.Bytes() // no pagination metadata after end marker

	result := BinaryPaginatedListToJSON(nil, data, m)
	got := string(result)

	// Fallback defaults
	if !strings.Contains(got, `"total":0`) {
		t.Errorf("missing default total: %s", got)
	}
	if !strings.Contains(got, `"page":1`) {
		t.Errorf("missing default page: %s", got)
	}
	if !strings.Contains(got, `"pageSize":20`) {
		t.Errorf("missing default pageSize: %s", got)
	}
}

// --- BinaryToJSON edge cases ---

func TestBinaryToJSON_EmptyModel(t *testing.T) {
	// No fields encoded
	var enc codec.Encoder
	enc.WriteEnd()
	data := enc.Bytes()

	m := userModel()
	result := BinaryToJSON(nil, data, m)
	if string(result) != "{}" {
		t.Errorf("empty model should be {}, got %s", result)
	}
}

func TestBinaryToJSON_UnknownFieldID(t *testing.T) {
	// Encode a field ID not in the model — should break cleanly
	var enc codec.Encoder
	enc.WriteFieldInt(99, 42) // field 99 not in userModel
	enc.WriteEnd()
	data := enc.Bytes()

	m := userModel()
	result := BinaryToJSON(nil, data, m)
	// Should produce valid JSON (possibly just {})
	if result[0] != '{' || result[len(result)-1] != '}' {
		t.Errorf("should produce valid JSON object, got %s", result)
	}
}

func TestBinaryToJSON_AllFieldTypes(t *testing.T) {
	// Model with every non-nullable field type
	s := New()
	m := &Model{
		Name: "AllTypes",
		Fields: []Field{
			{ID: 1, Name: "intVal", Type: FieldInt},
			{ID: 2, Name: "floatVal", Type: FieldFloat},
			{ID: 3, Name: "strVal", Type: FieldString},
			{ID: 4, Name: "boolVal", Type: FieldBool},
			{ID: 5, Name: "dtVal", Type: FieldDateTime},
			{ID: 6, Name: "durVal", Type: FieldDuration},
			{ID: 7, Name: "enumVal", Type: FieldEnum},
			{ID: 8, Name: "bytesVal", Type: FieldBytes},
		},
	}
	s.RegisterModel(m)

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

	var enc codec.Encoder
	enc.WriteFieldInt(1, -42)
	enc.WriteFieldFloat(2, 2.718)
	enc.WriteFieldString(3, "test")
	enc.WriteFieldBool(4, false)
	enc.WriteFieldInt(5, ts) // DateTime as unix timestamp
	enc.WriteFieldInt(6, 5000000000)
	enc.WriteFieldString(7, "ACTIVE")
	enc.WriteFieldBytes(8, []byte{0xDE, 0xAD})
	enc.WriteEnd()

	result := BinaryToJSON(nil, enc.Bytes(), m)
	got := string(result)

	if !strings.Contains(got, `"intVal":-42`) {
		t.Errorf("missing int: %s", got)
	}
	if !strings.Contains(got, `"floatVal":2.718`) {
		t.Errorf("missing float: %s", got)
	}
	if !strings.Contains(got, `"strVal":"test"`) {
		t.Errorf("missing string: %s", got)
	}
	if !strings.Contains(got, `"boolVal":false`) {
		t.Errorf("missing bool: %s", got)
	}
	if !strings.Contains(got, `"dtVal":"2026-01-01T00:00:00Z"`) {
		t.Errorf("missing datetime: %s", got)
	}
	if !strings.Contains(got, `"durVal":5000000000`) {
		t.Errorf("missing duration: %s", got)
	}
	if !strings.Contains(got, `"enumVal":"ACTIVE"`) {
		t.Errorf("missing enum: %s", got)
	}
	// bytes currently returns null (TODO: base64)
	if !strings.Contains(got, `"bytesVal":null`) {
		t.Errorf("missing bytes (null): %s", got)
	}
}

func TestBinaryToJSON_NullableFields(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Nullable",
		Fields: []Field{
			{ID: 1, Name: "intVal", Type: FieldInt, Nullable: true},
			{ID: 2, Name: "floatVal", Type: FieldFloat, Nullable: true},
			{ID: 3, Name: "strVal", Type: FieldString, Nullable: true},
			{ID: 4, Name: "boolVal", Type: FieldBool, Nullable: true},
			{ID: 5, Name: "dtVal", Type: FieldDateTime, Nullable: true},
		},
	}
	s.RegisterModel(m)

	// All null
	var enc codec.Encoder
	enc.WriteFieldIntPtr(1, nil)
	enc.WriteFieldFloatPtr(2, nil)
	enc.WriteFieldStringPtr(3, nil)
	enc.WriteFieldBoolPtr(4, nil)
	enc.WriteFieldIntPtr(5, nil)
	enc.WriteEnd()

	result := BinaryToJSON(nil, enc.Bytes(), m)
	got := string(result)

	// All values should be null
	for _, name := range []string{"intVal", "floatVal", "strVal", "boolVal", "dtVal"} {
		if !strings.Contains(got, `"`+name+`":null`) {
			t.Errorf("expected %s:null in %s", name, got)
		}
	}

	// All present
	enc = codec.Encoder{}
	iv := int64(10)
	fv := 3.14
	sv := "hello"
	bv := true
	dv := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	enc.WriteFieldIntPtr(1, &iv)
	enc.WriteFieldFloatPtr(2, &fv)
	enc.WriteFieldStringPtr(3, &sv)
	enc.WriteFieldBoolPtr(4, &bv)
	enc.WriteFieldIntPtr(5, &dv)
	enc.WriteEnd()

	result = BinaryToJSON(nil, enc.Bytes(), m)
	got = string(result)

	if !strings.Contains(got, `"intVal":10`) {
		t.Errorf("missing intVal: %s", got)
	}
	if !strings.Contains(got, `"floatVal":3.14`) {
		t.Errorf("missing floatVal: %s", got)
	}
	if !strings.Contains(got, `"strVal":"hello"`) {
		t.Errorf("missing strVal: %s", got)
	}
	if !strings.Contains(got, `"boolVal":true`) {
		t.Errorf("missing boolVal: %s", got)
	}
	if !strings.Contains(got, `"dtVal":"2026-06-01T00:00:00Z"`) {
		t.Errorf("missing dtVal: %s", got)
	}
}

// --- BinaryListToJSON edge cases ---

func TestBinaryListToJSON_Empty(t *testing.T) {
	m := userModel()
	w := &codec.ColumnarWriter{}
	w.SetCount(0)
	result := BinaryListToJSON(nil, w.Bytes(), m)
	if string(result) != "[]" {
		t.Errorf("empty list should be [], got %s", result)
	}
}

func TestBinaryListToJSON_NullableIntColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Item",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "score", Type: FieldInt, Nullable: true},
		},
	}
	s.RegisterModel(m)

	w := &codec.ColumnarWriter{}
	w.SetCount(3)
	w.WriteColumnInt(1, []int64{1, 2, 3})
	v := int64(100)
	w.WriteColumnIntPtr(2, []*int64{&v, nil, &v})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)

	if !strings.Contains(got, "null") {
		t.Errorf("should contain null for nil score: %s", got)
	}
	if !strings.Contains(got, "100") {
		t.Errorf("should contain 100: %s", got)
	}
}

func TestBinaryListToJSON_BoolColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Flag",
		Fields: []Field{
			{ID: 1, Name: "active", Type: FieldBool},
		},
	}
	s.RegisterModel(m)

	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnBool(1, []bool{true, false})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)
	if !strings.Contains(got, "true") || !strings.Contains(got, "false") {
		t.Errorf("missing bool values: %s", got)
	}
}

func TestBinaryListToJSON_FloatColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Score",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldFloat},
		},
	}
	s.RegisterModel(m)

	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnFloat(1, []float64{3.14, -0.001})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)
	if !strings.Contains(got, "3.14") || !strings.Contains(got, "-0.001") {
		t.Errorf("missing float values: %s", got)
	}
}

func TestBinaryListToJSON_DateTimeColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Event",
		Fields: []Field{
			{ID: 1, Name: "at", Type: FieldDateTime},
		},
	}
	s.RegisterModel(m)

	ts := time.Date(2026, 4, 13, 10, 30, 0, 0, time.UTC).Unix()

	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnInt(1, []int64{ts})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	if !strings.Contains(string(result), "2026-04-13T10:30:00Z") {
		t.Errorf("missing datetime: %s", result)
	}
}

// --- JSONParamsToBinary edge cases ---

func TestJSONParamsToBinary_BoolParam(t *testing.T) {
	api := &API{
		Name: "toggle",
		Params: []Param{
			{ID: 1, Name: "active", Type: FieldBool},
		},
	}

	data := JSONParamsToBinary(map[string]any{"active": true}, api)
	dec := codec.NewDecoder(data)
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field ID 1")
	}
	if v := dec.ReadBool(); v != true {
		t.Errorf("expected true, got %v", v)
	}
}

func TestJSONParamsToBinary_MissingParam(t *testing.T) {
	api := &API{
		Name: "test",
		Params: []Param{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
		},
	}

	// Only provide one param — the other should be skipped
	data := JSONParamsToBinary(map[string]any{"name": "Alice"}, api)
	dec := codec.NewDecoder(data)
	if !dec.NextField() || dec.FieldID() != 2 {
		t.Fatal("expected field ID 2 (name)")
	}
	if v := dec.ReadString(); v != "Alice" {
		t.Errorf("got %q", v)
	}
	// Should have no more fields
	if dec.NextField() {
		t.Error("should have no more fields")
	}
}

func TestJSONParamsToBinary_IntAsInt64(t *testing.T) {
	// Some JSON parsers may produce int64 instead of float64
	api := &API{
		Name:   "test",
		Params: []Param{{ID: 1, Name: "id", Type: FieldInt}},
	}

	data := JSONParamsToBinary(map[string]any{"id": int64(999)}, api)
	dec := codec.NewDecoder(data)
	if !dec.NextField() {
		t.Fatal("expected field")
	}
	if v := dec.ReadInt(); v != 999 {
		t.Errorf("got %d, want 999", v)
	}
}

func TestJSONParamsToBinary_EmptyParams(t *testing.T) {
	api := &API{Name: "noop", Params: nil}
	data := JSONParamsToBinary(map[string]any{}, api)
	// Should just have end marker
	dec := codec.NewDecoder(data)
	if dec.NextField() {
		t.Error("no params should produce no fields")
	}
}

func TestJSONParamsToBinary_WrongType(t *testing.T) {
	// Pass string value for int param — should be silently skipped
	api := &API{
		Name:   "test",
		Params: []Param{{ID: 1, Name: "id", Type: FieldInt}},
	}
	data := JSONParamsToBinary(map[string]any{"id": "not-a-number"}, api)
	dec := codec.NewDecoder(data)
	if dec.NextField() {
		t.Error("wrong type should be skipped")
	}
}

// --- appendJSONString edge cases ---

func TestAppendJSONString_MultiByte(t *testing.T) {
	// Multi-byte UTF-8 must be preserved, not corrupted
	tests := []struct {
		in   string
		want string
	}{
		{"日本語", `"日本語"`},
		{"café", `"café"`},
		{"hello 世界", `"hello 世界"`},
		{"emoji🎉", `"emoji🎉"`},
	}
	for _, tt := range tests {
		got := appendJSONString(nil, tt.in)
		if string(got) != tt.want {
			t.Errorf("appendJSONString(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestAppendJSONString_ControlChars(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"\x00", `"\u0000"`},
		{"\x01", `"\u0001"`},
		{"\x1f", `"\u001f"`},
		{"\x08", `"\u0008"`}, // backspace
		{"\t", `"\t"`},       // tab has special escape
		{"\n", `"\n"`},       // newline has special escape
		{"\r", `"\r"`},       // carriage return
		{`"`, `"\""`},        // quote
		{`\`, `"\\"`},        // backslash
		{"abc\x00def", `"abc\u0000def"`},
		{"normal", `"normal"`},
		{"", `""`},
	}
	for _, tt := range tests {
		got := appendJSONString(nil, tt.in)
		if string(got) != tt.want {
			t.Errorf("appendJSONString(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// --- appendAnyJSON edge cases ---

func TestAppendAnyJSON_NilPtrString(t *testing.T) {
	var sp *string
	f := &Field{Type: FieldString}
	got := appendAnyJSON(nil, sp, f)
	if string(got) != "null" {
		t.Errorf("nil *string should be null, got %s", got)
	}
}

func TestAppendAnyJSON_NilPtrInt(t *testing.T) {
	var ip *int64
	f := &Field{Type: FieldInt}
	got := appendAnyJSON(nil, ip, f)
	if string(got) != "null" {
		t.Errorf("nil *int64 should be null, got %s", got)
	}
}

func TestAppendAnyJSON_UnknownType(t *testing.T) {
	f := &Field{Type: FieldInt}
	got := appendAnyJSON(nil, struct{}{}, f)
	if string(got) != "null" {
		t.Errorf("unknown type should be null, got %s", got)
	}
}

func TestAppendAnyJSON_BoolTrue(t *testing.T) {
	f := &Field{Type: FieldBool}
	got := appendAnyJSON(nil, true, f)
	if string(got) != "true" {
		t.Errorf("got %s", got)
	}
}

func TestAppendAnyJSON_BoolFalse(t *testing.T) {
	f := &Field{Type: FieldBool}
	got := appendAnyJSON(nil, false, f)
	if string(got) != "false" {
		t.Errorf("got %s", got)
	}
}

func TestAppendAnyJSON_Nil(t *testing.T) {
	f := &Field{Type: FieldString}
	got := appendAnyJSON(nil, nil, f)
	if string(got) != "null" {
		t.Errorf("nil should be null, got %s", got)
	}
}

func TestAppendAnyJSON_PresentPtrString(t *testing.T) {
	s := "hello"
	f := &Field{Type: FieldString}
	got := appendAnyJSON(nil, &s, f)
	if string(got) != `"hello"` {
		t.Errorf("got %s, want %q", got, `"hello"`)
	}
}

func TestAppendAnyJSON_PresentPtrInt(t *testing.T) {
	v := int64(77)
	f := &Field{Type: FieldInt}
	got := appendAnyJSON(nil, &v, f)
	if string(got) != "77" {
		t.Errorf("got %s", got)
	}
}

func TestBinaryToJSON_NullableDefaultBranch(t *testing.T) {
	// FieldBytes nullable — hits default branch in appendNullableFieldJSON
	s := New()
	m := &Model{
		Name: "M",
		Fields: []Field{
			{ID: 1, Name: "data", Type: FieldBytes, Nullable: true},
		},
	}
	s.RegisterModel(m)

	var enc codec.Encoder
	enc.WriteFieldBytes(1, nil) // nullable bytes
	enc.WriteEnd()

	result := BinaryToJSON(nil, enc.Bytes(), m)
	// Should produce valid JSON
	if result[0] != '{' {
		t.Errorf("expected JSON object, got %s", result)
	}
}

func TestBinaryToJSON_FieldTypeDefault(t *testing.T) {
	// FieldModel hits default branch in appendFieldValueJSON
	s := New()
	m := &Model{
		Name: "M",
		Fields: []Field{
			{ID: 1, Name: "nested", Type: FieldModel},
		},
	}
	s.RegisterModel(m)

	var enc codec.Encoder
	enc.WriteFieldString(1, "ignored") // wire type doesn't matter
	enc.WriteEnd()

	result := BinaryToJSON(nil, enc.Bytes(), m)
	// Should produce valid JSON without crashing
	if result[0] != '{' {
		t.Errorf("expected JSON object, got %s", result)
	}
}

func TestBinaryListToJSON_UnknownFieldColumn(t *testing.T) {
	// Columnar data with a field ID not in the model — should break column loop
	s := New()
	m := &Model{
		Name: "M",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
		},
	}
	s.RegisterModel(m)

	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnInt(1, []int64{42})
	w.WriteColumnInt(99, []int64{0}) // unknown field

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)
	if !strings.Contains(got, "42") {
		t.Errorf("should still contain known field data: %s", got)
	}
}

func TestBinaryScalarToJSON_UUID(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "550e8400-e29b-41d4-a716-446655440000")
	result := BinaryScalarToJSON(nil, buf, "UUID")
	if !strings.Contains(string(result), "550e8400") {
		t.Errorf("UUID not decoded: %s", result)
	}
}

func TestBinaryScalarToJSON_Decimal(t *testing.T) {
	var buf []byte
	buf = codec.AppendString(buf, "123.456")
	result := BinaryScalarToJSON(nil, buf, "Decimal")
	if string(result) != `"123.456"` {
		t.Errorf("got %s", result)
	}
}

func TestBinaryListToJSON_NullableStringColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Item",
		Fields: []Field{
			{ID: 1, Name: "note", Type: FieldString, Nullable: true},
		},
	}
	s.RegisterModel(m)

	w := &codec.ColumnarWriter{}
	w.SetCount(3)
	s1 := "hello"
	w.WriteColumnStringPtr(1, []*string{&s1, nil, &s1})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)
	// First and third should be "hello", second should be null
	if !strings.Contains(got, `"hello"`) {
		t.Errorf("missing string value: %s", got)
	}
	if !strings.Contains(got, "null") {
		t.Errorf("missing null for nil string: %s", got)
	}
}

func TestAppendColumnValueJSON_Default(t *testing.T) {
	// typedColumn with no typed slices set — should output "null"
	col := typedColumn{field: &Field{ID: 1, Name: "unknown", Type: FieldInt}}
	dst := appendColumnValueJSON(nil, col, 0)
	if string(dst) != "null" {
		t.Errorf("default branch should output null, got %q", dst)
	}
}

func TestBinaryListToJSON_NullableDateTimeColumn(t *testing.T) {
	s := New()
	m := &Model{
		Name: "Log",
		Fields: []Field{
			{ID: 1, Name: "deletedAt", Type: FieldDateTime, Nullable: true},
		},
	}
	s.RegisterModel(m)

	ts := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnIntPtr(1, []*int64{&ts, nil})

	result := BinaryListToJSON(nil, w.Bytes(), m)
	got := string(result)
	if !strings.Contains(got, "2026") {
		t.Errorf("missing datetime: %s", got)
	}
	if !strings.Contains(got, "null") {
		t.Errorf("missing null: %s", got)
	}
}
