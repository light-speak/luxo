package schema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestJSONToBinaryStructuredValue(t *testing.T) {
	s := newJSONEncodingSchema()
	raw := json.RawMessage(`{
		"count":42,"ratio":1.5,"name":"é","active":true,
		"createdAt":"2024-01-02T03:04:05.123Z","timeout":15,"data":"AQI=",
		"role":"ADMIN","address":{"city":"東京"},
		"key":"550e8400-e29b-41d4-a716-446655440000","price":"12.50",
		"metadata":{"ok":true},"note":null,"ids":[1,-2],
		"addresses":[{"city":"A"},{"city":"BC"}],"roles":["ADMIN","USER"],
		"optionalCount":7
	}`)

	message, err := s.JSONToBinary("Input", raw)
	if err != nil {
		t.Fatal(err)
	}
	dec := codec.NewDecoder(message)
	if got := dec.ReadArenaSize(); got != len("é")+len("ADMIN") {
		t.Fatalf("arena size = %d", got)
	}
	assertJSONEncodedFields(t, dec)
	if err := dec.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONToBinaryTypeDeclaration(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Point", Fields: []Field{
		{ID: 1, Name: "x", Type: FieldInt},
		{ID: 2, Name: "y", Type: FieldInt},
	}})
	got, err := s.JSONToBinary("Point", json.RawMessage(`{"x":3,"y":4}`))
	if err != nil {
		t.Fatal(err)
	}
	want := append(codec.AppendSvarint(codec.AppendVarint(codec.AppendVarint(nil, 0), 1), 3), 2)
	want = codec.AppendSvarint(want, 4)
	want = append(want, 0)
	if !bytes.Equal(got, want) {
		t.Fatalf("message = %x, want %x", got, want)
	}
}

func TestJSONToBinaryRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		typeName  string
		field     Field
		value     string
		wantError string
	}{
		{name: "unknown type", typeName: "Missing", value: `{}`, wantError: "unknown structured type"},
		{name: "null object", typeName: "Input", value: `null`, wantError: "expected object"},
		{name: "array object", typeName: "Input", value: `[]`, wantError: "expected object"},
		{name: "malformed object", typeName: "Input", value: `{`, wantError: "expected object"},
		{name: "arena string", field: Field{Type: FieldString}, value: `1`, wantError: "must be a string"},
		{name: "required null", field: Field{Type: FieldInt}, value: `null`, wantError: "must not be null"},
		{name: "integer", field: Field{Type: FieldInt}, value: `1.5`, wantError: "expected integer"},
		{name: "float", field: Field{Type: FieldFloat}, value: `"x"`, wantError: "expected number"},
		{name: "string", field: Field{Type: FieldString, IsList: true}, value: `[1]`, wantError: "expected string"},
		{name: "boolean", field: Field{Type: FieldBool}, value: `"true"`, wantError: "expected boolean"},
		{name: "datetime type", field: Field{Type: FieldDateTime}, value: `1`, wantError: "expected RFC3339 string"},
		{name: "datetime value", field: Field{Type: FieldDateTime}, value: `"today"`, wantError: "expected RFC3339 string"},
		{name: "bytes", field: Field{Type: FieldBytes}, value: `"***"`, wantError: "expected base64 bytes"},
		{name: "enum type", field: Field{Type: FieldEnum, TypeName: "Role", IsList: true}, value: `[1]`, wantError: "expected enum string"},
		{name: "enum value", field: Field{Type: FieldEnum, TypeName: "Role"}, value: `"ROOT"`, wantError: "unknown Role value"},
		{name: "nested missing", field: Field{Type: FieldModel, TypeName: "Missing"}, value: `{}`, wantError: "unknown structured type"},
		{name: "nested value", field: Field{Type: FieldModel, TypeName: "Address"}, value: `1`, wantError: "expected object"},
		{name: "uuid type", field: Field{Type: FieldUUID}, value: `1`, wantError: "expected UUID string"},
		{name: "uuid value", field: Field{Type: FieldUUID}, value: `"bad"`, wantError: "expected UUID string"},
		{name: "decimal type", field: Field{Type: FieldDecimal}, value: `1`, wantError: "expected decimal string"},
		{name: "decimal value", field: Field{Type: FieldDecimal}, value: `"bad"`, wantError: "expected decimal string"},
		{name: "list type", field: Field{Type: FieldInt, IsList: true}, value: `1`, wantError: "expected array"},
		{name: "list null", field: Field{Type: FieldInt, IsList: true}, value: `[null]`, wantError: "element 0 must not be null"},
		{name: "list item", field: Field{Type: FieldInt, IsList: true}, value: `["x"]`, wantError: "element 0: expected integer"},
		{name: "unsupported", field: Field{Type: FieldType(99)}, value: `1`, wantError: "unsupported field type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newJSONEncodingSchema()
			typeName := tt.typeName
			if typeName == "" {
				typeName = "Single"
				tt.field.ID = 1
				tt.field.Name = "value"
				s.RegisterModel(&Model{Name: typeName, Fields: []Field{tt.field}})
				tt.value = `{"value":` + tt.value + `}`
			}
			if _, err := s.JSONToBinary(typeName, json.RawMessage(tt.value)); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestJSONToBinaryNullableAndOpenEnum(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "Input", Fields: []Field{
		{ID: 1, Name: "value", Type: FieldString, Nullable: true},
		{ID: 2, Name: "status", Type: FieldEnum, TypeName: "ExternalStatus"},
	}})
	message, err := s.JSONToBinary("Input", json.RawMessage(`{"value":"ok","status":"NEW"}`))
	if err != nil {
		t.Fatal(err)
	}
	dec := codec.NewDecoder(message)
	arena := make([]byte, dec.ReadArenaSize())
	off := 0
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("missing nullable string")
	}
	if got := dec.ReadStringArenaPtr(arena, &off); got == nil || *got != "ok" {
		t.Fatalf("nullable string = %v", got)
	}
	if !dec.NextField() || dec.FieldID() != 2 || dec.ReadStringArena(arena, &off) != "NEW" {
		t.Fatal("open enum was not encoded")
	}
	if dec.NextField() || dec.Err() != nil {
		t.Fatalf("decoder error = %v", dec.Err())
	}
}

func TestJSONToBinaryOmitsAbsentFields(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "Input", Fields: []Field{{ID: 1, Name: "value", Type: FieldInt}}})
	message, err := s.JSONToBinary("Input", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(message, []byte{0, 0}) {
		t.Fatalf("message = %x", message)
	}
}

func TestJSONToBinaryRejectsUnknownFields(t *testing.T) {
	s := newJSONEncodingSchema()
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "root", value: `{"count":1,"typo":2}`, wantError: `Input: unknown field "typo"`},
		{name: "nested", value: `{"address":{"city":"Tokyo","zip":"100-0001"}}`, wantError: `Input.address: Address: unknown field "zip"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.JSONToBinary("Input", json.RawMessage(tt.value)); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestJSONToBinarySupportsDirectSchemaMetadata(t *testing.T) {
	s := &Schema{Models: map[string]*Model{
		"Input": {Name: "Input", Fields: []Field{{ID: 1, Name: "value", Type: FieldInt}}},
	}}
	message, err := s.JSONToBinary("Input", json.RawMessage(`{"value":7}`))
	if err != nil {
		t.Fatal(err)
	}
	dec := codec.NewDecoder(message)
	dec.SkipArenaHeader()
	if !dec.NextField() || dec.FieldID() != 1 || dec.ReadInt() != 7 || dec.NextField() || dec.Err() != nil {
		t.Fatalf("unexpected message %x: %v", message, dec.Err())
	}
}

func newJSONEncodingSchema() *Schema {
	s := New()
	s.RegisterEnum(&Enum{Name: "Role", Values: []string{"ADMIN", "USER"}})
	s.RegisterType(&TypeDecl{Name: "Address", Fields: []Field{{ID: 1, Name: "city", Type: FieldString}}})
	s.RegisterModel(&Model{Name: "Input", Fields: []Field{
		{ID: 1, Name: "count", Type: FieldInt},
		{ID: 2, Name: "ratio", Type: FieldFloat},
		{ID: 3, Name: "name", Type: FieldString},
		{ID: 4, Name: "active", Type: FieldBool},
		{ID: 5, Name: "createdAt", Type: FieldDateTime},
		{ID: 6, Name: "timeout", Type: FieldDuration},
		{ID: 7, Name: "data", Type: FieldBytes},
		{ID: 8, Name: "role", Type: FieldEnum, TypeName: "Role"},
		{ID: 9, Name: "address", Type: FieldModel, TypeName: "Address"},
		{ID: 10, Name: "key", Type: FieldUUID},
		{ID: 11, Name: "price", Type: FieldDecimal},
		{ID: 12, Name: "metadata", Type: FieldJSON},
		{ID: 13, Name: "note", Type: FieldString, Nullable: true},
		{ID: 14, Name: "ids", Type: FieldInt, IsList: true},
		{ID: 15, Name: "addresses", Type: FieldModel, TypeName: "Address", IsList: true},
		{ID: 16, Name: "roles", Type: FieldEnum, TypeName: "Role", IsList: true},
		{ID: 17, Name: "optionalCount", Type: FieldInt, Nullable: true},
	}})
	return s
}

func assertJSONEncodedFields(t *testing.T, dec *codec.Decoder) {
	t.Helper()
	arena := make([]byte, len("é")+len("ADMIN"))
	arenaOff := 0
	assertNextField(t, dec, 1)
	if got := dec.ReadInt(); got != 42 {
		t.Fatalf("count = %d", got)
	}
	assertNextField(t, dec, 2)
	if got := dec.ReadFloat(); got != 1.5 {
		t.Fatalf("ratio = %g", got)
	}
	assertNextField(t, dec, 3)
	if got := dec.ReadStringArena(arena, &arenaOff); got != "é" {
		t.Fatalf("name = %q", got)
	}
	assertNextField(t, dec, 4)
	if !dec.ReadBool() {
		t.Fatal("active = false")
	}
	assertNextField(t, dec, 5)
	wantTime, _ := time.Parse(time.RFC3339Nano, "2024-01-02T03:04:05.123Z")
	if got := dec.ReadInt(); got != wantTime.Unix() {
		t.Fatalf("createdAt = %d", got)
	}
	assertNextField(t, dec, 6)
	if got := dec.ReadInt(); got != 15 {
		t.Fatalf("timeout = %d", got)
	}
	assertNextField(t, dec, 7)
	if got := dec.ReadBytes(); !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("data = %x", got)
	}
	assertNextField(t, dec, 8)
	if got := dec.ReadStringArena(arena, &arenaOff); got != "ADMIN" {
		t.Fatalf("role = %q", got)
	}
	assertNestedAddress(t, dec, 9, "東京")
	assertNextField(t, dec, 10)
	if got := uuid.UUID(dec.ReadUUID()).String(); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("key = %s", got)
	}
	assertNextField(t, dec, 11)
	if got := dec.ReadString(); got != "12.50" {
		t.Fatalf("price = %q", got)
	}
	assertNextField(t, dec, 12)
	if got := dec.ReadBytes(); string(got) != `{"ok":true}` {
		t.Fatalf("metadata = %s", got)
	}
	assertNextField(t, dec, 13)
	if got := dec.ReadStringArenaPtr(arena, &arenaOff); got != nil {
		t.Fatalf("note = %q", *got)
	}
	assertNextField(t, dec, 14)
	if got := dec.ReadIntArray(); len(got) != 2 || got[0] != 1 || got[1] != -2 {
		t.Fatalf("ids = %v", got)
	}
	assertNextField(t, dec, 15)
	if count := dec.ReadArrayLength(); count != 2 {
		t.Fatalf("address count = %d", count)
	}
	assertNestedMessage(t, dec, "A")
	assertNestedMessage(t, dec, "BC")
	assertNextField(t, dec, 16)
	if got := dec.ReadStringArray(); len(got) != 2 || got[0] != "ADMIN" || got[1] != "USER" {
		t.Fatalf("roles = %v", got)
	}
	assertNextField(t, dec, 17)
	if got := dec.ReadIntPtr(); got == nil || *got != 7 {
		t.Fatalf("optionalCount = %v", got)
	}
	if dec.NextField() {
		t.Fatal("unexpected field")
	}
}

func assertNestedAddress(t *testing.T, dec *codec.Decoder, fieldID int, city string) {
	t.Helper()
	assertNextField(t, dec, fieldID)
	assertNestedMessage(t, dec, city)
}

func assertNestedMessage(t *testing.T, dec *codec.Decoder, city string) {
	t.Helper()
	arena := make([]byte, dec.ReadArenaSize())
	off := 0
	assertNextField(t, dec, 1)
	if got := dec.ReadStringArena(arena, &off); got != city {
		t.Fatalf("city = %q", got)
	}
	if dec.NextField() {
		t.Fatal("unexpected nested field")
	}
}

func assertNextField(t *testing.T, dec *codec.Decoder, id int) {
	t.Helper()
	if !dec.NextField() || dec.FieldID() != id {
		t.Fatalf("field = %d, want %d, error = %v", dec.FieldID(), id, dec.Err())
	}
}
