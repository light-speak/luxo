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
	// Build binary manually: field 1 (string "abc"), field 2 (nested User)
	var data []byte
	// Field 1: token = "abc"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "abc")
	// Field 2: member = nested User
	data = codec.AppendVarint(data, 2)
	// Nested User fields inline
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
	// Field 1: name = "test"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "test")
	// Field 2: inner = nested Inner
	data = codec.AppendVarint(data, 2)
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
	// Field 1: label = "box"
	data = codec.AppendVarint(data, 1)
	data = codec.AppendString(data, "box")
	// Field 2: unknown = nested (will be drained)
	data = codec.AppendVarint(data, 2)
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
