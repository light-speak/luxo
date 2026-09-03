package schema

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinaryToJSON_NestedModel(t *testing.T) {
	// Schema: AuthPayload { token: String, member: User }
	// User { id: Int, name: String }
	s := New()
	s.RegisterModel(&Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
		},
	})
	s.RegisterModel(&Model{
		Name: "AuthPayload",
		Fields: []Field{
			{ID: 1, Name: "token", Type: FieldString},
			{ID: 2, Name: "member", Type: FieldModel, TypeName: "User", Relation: true},
		},
	})

	// Encode: AuthPayload { token: "abc", member: User { id: 42, name: "Alice" } }
	// Build binary manually: [arenaHeader] field 1 (string "abc"), field 2 (nested User [arenaHeader] ...)
	var data []byte
	data = codec.AppendVarint(data, 0) // arena header for AuthPayload
	// Field 1: token = "abc"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "abc")
	// Field 2: member = nested User
	data = codec.AppendVarint(data, 2)
	// Nested User: [arenaHeader] fields... [0x00]
	data = codec.AppendVarint(data, 0) // arena header for User
	data = codec.AppendVarint(data, 1) // User.id
	data = codec.AppendSvarint(data, 42)
	data = codec.AppendVarint(data, 2) // User.name
	data = codec.AppendString(data, "Alice")
	data = append(data, 0x00) // end of nested User
	data = append(data, 0x00) // end of AuthPayload

	m := s.Models["AuthPayload"]
	result := BinaryToJSON(nil, data, m, s)
	got := string(result)

	if !strings.Contains(got, `"token":"abc"`) {
		t.Errorf("missing token: %s", got)
	}
	if !strings.Contains(got, `"id":42`) {
		t.Errorf("missing nested id: %s", got)
	}
	if !strings.Contains(got, `"name":"Alice"`) {
		t.Errorf("missing nested name: %s", got)
	}
}

func TestBinaryToJSON_NestedType(t *testing.T) {
	// Test with TypeDecl (non-model), resolved via s.Types -> AsModel()
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "Inner",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldString},
		},
	})
	s.RegisterModel(&Model{
		Name: "Outer",
		Fields: []Field{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "inner", Type: FieldModel, TypeName: "Inner", Relation: true},
		},
	})

	var data []byte
	data = codec.AppendVarint(data, 0) // arena header for Outer
	// Field 1: name = "test"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "test")
	// Field 2: inner = nested Inner
	data = codec.AppendVarint(data, 2)
	data = codec.AppendVarint(data, 0) // arena header for Inner
	data = codec.AppendVarint(data, 1) // Inner.value
	data = codec.AppendString(data, "hello")
	data = append(data, 0x00) // end of Inner
	data = append(data, 0x00) // end of Outer

	m := s.Models["Outer"]
	result := BinaryToJSON(nil, data, m, s)
	got := string(result)

	if !strings.Contains(got, `"name":"test"`) {
		t.Errorf("missing name: %s", got)
	}
	if !strings.Contains(got, `"value":"hello"`) {
		t.Errorf("missing nested value: %s", got)
	}
}

func TestBinaryToJSON_UnknownNestedType(t *testing.T) {
	// Nested type not in schema — should output null
	s := New()
	s.RegisterModel(&Model{
		Name: "Container",
		Fields: []Field{
			{ID: 1, Name: "label", Type: FieldString},
			{ID: 2, Name: "unknown", Type: FieldModel, TypeName: "NonExistent", Relation: true},
		},
	})

	var data []byte
	data = codec.AppendVarint(data, 0) // arena header for Container
	// Field 1: label = "box"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "box")
	// Field 2: unknown = nested (will be drained)
	data = codec.AppendVarint(data, 2)
	data = codec.AppendVarint(data, 0) // arena header for nested (unknown type)
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "data")
	data = append(data, 0x00) // end of nested
	data = append(data, 0x00) // end of Container

	m := s.Models["Container"]
	result := BinaryToJSON(nil, data, m, s)
	got := string(result)

	if !strings.Contains(got, `"label":"box"`) {
		t.Errorf("missing label: %s", got)
	}
	if !strings.Contains(got, `"unknown":null`) {
		t.Errorf("unknown nested should be null: %s", got)
	}
}

func TestBinaryToJSON_NestedTypeListRowWise(t *testing.T) {
	// Single type response containing a nested type LIST (e.g. ServiceSchema
	// { serviceName, types: [SchemaType] }). Row-wise wire format:
	// [fieldID][varint count][item1 WriteLuxo][item2 WriteLuxo]
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "Inner",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldInt},
		},
	})
	s.RegisterType(&TypeDecl{
		Name: "Outer",
		Fields: []Field{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "items", Type: FieldModel, TypeName: "Inner", IsList: true, Relation: true},
			{ID: 3, Name: "tail", Type: FieldString},
		},
	})

	var data []byte
	data = codec.AppendVarint(data, 0) // arena header for Outer
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "svc")
	data = codec.AppendVarint(data, 2)
	data = codec.AppendArrayHeader(data, 2) // 2 nested items
	for _, v := range []int64{7, 8} {
		data = codec.AppendVarint(data, 0) // arena header for Inner
		data = codec.AppendVarint(data, 1)
		data = codec.AppendSvarint(data, v)
		data = append(data, 0x00) // end of Inner
	}
	data = codec.AppendVarint(data, 3)
	data = codec.AppendString(data, "after")
	data = append(data, 0x00) // end of Outer

	outer := s.Types["Outer"].AsModel()
	got := string(BinaryToJSON(nil, data, outer, s))

	if !strings.Contains(got, `"items":[{"value":7},{"value":8}]`) {
		t.Errorf("nested list should decode as JSON array: %s", got)
	}
	if !strings.Contains(got, `"tail":"after"`) {
		t.Errorf("fields after nested list must stay aligned: %s", got)
	}
}

func TestBinaryToJSON_NestedNullableRowWise(t *testing.T) {
	// Nullable nested single — wire format prefixes a present/null flag byte.
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "Inner",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldString},
		},
	})
	s.RegisterType(&TypeDecl{
		Name: "Outer",
		Fields: []Field{
			{ID: 1, Name: "inner", Type: FieldModel, TypeName: "Inner", Nullable: true, Relation: true},
			{ID: 2, Name: "tail", Type: FieldString},
		},
	})

	// Present case
	var data []byte
	data = codec.AppendVarint(data, 0) // arena header
	data = codec.AppendVarint(data, 1)
	data = codec.AppendPresent(data)
	data = codec.AppendVarint(data, 0) // nested arena header
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "hi")
	data = append(data, 0x00)
	data = codec.AppendVarint(data, 2)
	data = codec.AppendString(data, "after")
	data = append(data, 0x00)

	outer := s.Types["Outer"].AsModel()
	got := string(BinaryToJSON(nil, data, outer, s))
	if !strings.Contains(got, `"inner":{"value":"hi"}`) || !strings.Contains(got, `"tail":"after"`) {
		t.Errorf("present nullable nested should decode inline: %s", got)
	}

	// Null case
	var data2 []byte
	data2 = codec.AppendVarint(data2, 0)
	data2 = codec.AppendVarint(data2, 1)
	data2 = codec.AppendNull(data2)
	data2 = codec.AppendVarint(data2, 2)
	data2 = codec.AppendString(data2, "after")
	data2 = append(data2, 0x00)

	got2 := string(BinaryToJSON(nil, data2, outer, s))
	if !strings.Contains(got2, `"inner":null`) || !strings.Contains(got2, `"tail":"after"`) {
		t.Errorf("null nullable nested should render null and stay aligned: %s", got2)
	}
}

func TestColumnarToJSON_NestedTypeList(t *testing.T) {
	// Native API returning [Outer] where Outer is a TYPE with a nested type
	// list field (e.g. MetricTimeSeries.points: [MetricPoint]).
	// Blob cell for a relation LIST holds columnar-encoded nested items.
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "Inner",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldInt},
		},
	})
	s.RegisterType(&TypeDecl{
		Name: "Outer",
		Fields: []Field{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "items", Type: FieldModel, TypeName: "Inner", IsList: true, Relation: true},
		},
	})

	// Build nested cell: columnar [Inner{value:7}, Inner{value:8}]
	innerW := &codec.ColumnarWriter{}
	innerW.SetCount(2)
	innerW.WriteColumnInt(1, []int64{7, 8})
	cell0 := innerW.Bytes()

	// Second record has an empty nested list
	var cell1 []byte

	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnString(1, []string{"a", "b"})
	w.WriteColumnBytes(2, [][]byte{cell0, cell1})

	outer := s.Types["Outer"].AsModel()
	got := string(BinaryListToJSON(nil, w.Bytes(), outer, s))

	if !strings.Contains(got, `"name":"a"`) || !strings.Contains(got, `"name":"b"`) {
		t.Errorf("missing outer fields: %s", got)
	}
	if !strings.Contains(got, `"value":7`) || !strings.Contains(got, `"value":8`) {
		t.Errorf("nested type list should decode via Types lookup: %s", got)
	}
	if !strings.Contains(got, `"items":[]`) {
		t.Errorf("empty nested list should render []: %s", got)
	}
}

func TestColumnarToJSON_NestedTypeSingle(t *testing.T) {
	// Blob cell for a SINGLE nested type holds row-wise WriteLuxo bytes.
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "Inner",
		Fields: []Field{
			{ID: 1, Name: "value", Type: FieldString},
		},
	})
	s.RegisterType(&TypeDecl{
		Name: "Outer",
		Fields: []Field{
			{ID: 1, Name: "name", Type: FieldString},
			{ID: 2, Name: "inner", Type: FieldModel, TypeName: "Inner", Relation: true},
		},
	})

	// Nested cell: row-wise Inner{value:"hello"} ([arena][field1][string][0x00])
	var cell []byte
	cell = codec.AppendVarint(cell, uint64(len("hello"))) // arena header
	cell = codec.AppendVarint(cell, 1)
	cell = codec.AppendString(cell, "hello")
	cell = append(cell, 0x00)

	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnString(1, []string{"a"})
	w.WriteColumnBytes(2, [][]byte{cell})

	outer := s.Types["Outer"].AsModel()
	got := string(BinaryListToJSON(nil, w.Bytes(), outer, s))

	if !strings.Contains(got, `"inner":{"value":"hello"}`) {
		t.Errorf("nested single type should decode via Types lookup: %s", got)
	}
}

func TestBinaryToJSON_NestedNoSchema(t *testing.T) {
	// Relation field but no schema passed — uses appendFieldValueJSON (default for FieldModel -> null)
	s := New()
	s.RegisterModel(&Model{
		Name: "M",
		Fields: []Field{
			{ID: 1, Name: "ref", Type: FieldModel, TypeName: "Other", Relation: true},
		},
	})

	var data []byte
	data = codec.AppendVarint(data, 0) // arena header
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "ignored")
	data = append(data, 0x00)

	m := s.Models["M"]
	// No schema passed — relation field uses appendFieldValueJSON (FieldModel -> default -> null)
	result := BinaryToJSON(nil, data, m)
	got := string(result)
	if got[0] != '{' {
		t.Errorf("expected JSON object, got %s", got)
	}
}

func TestBinaryListToJSON_NullableNestedColumn(t *testing.T) {
	child := &Model{Name: "Child", Fields: []Field{{ID: 1, Name: "name", Type: FieldString}}}
	outer := &Model{Name: "Outer", Fields: []Field{{
		ID: 1, Name: "child", Type: FieldModel, TypeName: "Child", Nullable: true, Relation: true,
	}}}
	s := New()
	s.RegisterModel(child)
	s.RegisterModel(outer)

	var childEncoder codec.Encoder
	childData := codec.AppendVarint(nil, uint64(len("Ada")))
	childEncoder.WriteFieldString(1, "Ada")
	childEncoder.WriteEnd()
	childData = append(childData, childEncoder.Bytes()...)

	values := make([]*[]byte, 2)
	values[1] = &childData
	var writer codec.ColumnarWriter
	writer.SetCount(2)
	writer.WriteColumnBytesPtr(1, values)

	got := string(BinaryListToJSON(nil, writer.Bytes(), outer, s))
	if got != `[{"child":null},{"child":{"name":"Ada"}}]` {
		t.Fatalf("nullable nested column = %s", got)
	}
}
