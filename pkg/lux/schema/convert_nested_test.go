package schema

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestNestedJSONRejectsUnavailableSchema(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, nullable := range []bool{false, true} {
			s, parent, child := nestedBoundaryFixture(t, list, nullable)
			for name, schemas := range map[string][]*Schema{
				"omitted": nil, "nil": {nil}, "unknown": {New()},
				"nil-model": {{Models: map[string]*Model{"Child": nil}}},
			} {
				for path, convert := range nestedBoundaryConverters(t, parent, child, true, schemas...) {
					t.Run(fmt.Sprintf("%s/%s/list=%t/nullable=%t", name, path, list, nullable), func(t *testing.T) {
						got, err := convert([]byte("prefix"))
						if err == nil || string(got) != "prefix" || !strings.Contains(err.Error(), "Child") {
							t.Fatalf("unavailable schema became success: %q / %v", got, err)
						}
					})
				}
			}
			for path, convert := range nestedBoundaryConverters(t, parent, child, true, s) {
				if _, err := convert(nil); err != nil {
					t.Fatalf("%s rejected valid child: %v", path, err)
				}
			}
		}
	}
}

func TestNestedJSONRejectsBrokenChild(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, nullable := range []bool{false, true} {
			s, parent, child := nestedBoundaryFixture(t, list, nullable)
			broken := [][]byte{nil, {}, {0}, append(bytes.Clone(child), 42)}
			for end := 1; end < len(child); end++ {
				broken = append(broken, child[:end])
			}
			for i, raw := range broken {
				for path, convert := range nestedBoundaryConverters(t, parent, raw, true, s) {
					t.Run(fmt.Sprintf("%s/list=%t/nullable=%t/case=%d", path, list, nullable, i), func(t *testing.T) {
						got, err := convert([]byte("prefix"))
						if err == nil || string(got) != "prefix" {
							t.Fatalf("broken child became success: %q / %v", got, err)
						}
					})
				}
			}
		}
	}
}

func TestNestedJSONPreservesExplicitEmptyAndNull(t *testing.T) {
	for _, list := range []bool{false, true} {
		s, parent, _ := nestedBoundaryFixture(t, list, true)
		child, emptyValue := []byte{0, 0}, "{}"
		if list {
			child, emptyValue = []byte{0, 0, 0}, "[]"
		}
		for _, present := range []bool{false, true} {
			activeSchema, value := s, emptyValue
			if !present {
				activeSchema, value = nil, "null"
			}
			for path, convert := range nestedBoundaryConverters(t, parent, child, present, activeSchema) {
				got, err := convert(nil)
				if err != nil || !bytes.Contains(got, []byte(`"child":`+value)) {
					t.Fatalf("%s changed explicit child value: %s / %v", path, got, err)
				}
			}
		}
	}
}

func nestedBoundaryFixture(t testing.TB, list, nullable bool) (*Schema, *Model, []byte) {
	s := New()
	s.RegisterModel(&Model{Name: "Child", Fields: []Field{{ID: 1, Name: "value", Type: FieldInt}}})
	parent := &Model{Name: "Parent", Fields: []Field{
		{ID: 1, Name: "id", Type: FieldInt},
		{ID: 2, Name: "child", Type: FieldModel, TypeName: "Child", IsList: list, Nullable: nullable, Relation: true},
	}}
	s.RegisterModel(parent)
	child := []byte{0, 1, 14, 0}
	if list {
		var w codec.ColumnarWriter
		w.SetCount(2)
		w.WriteColumnInt(1, []int64{7, 8})
		child = testutil.Bytes(t)(w.Bytes())
	}
	return s, parent, child
}

func TestNestedModelJSONWarmPathAllocs(t *testing.T) {
	s, parent, child := nestedBoundaryFixture(t, false, false)
	dst := make([]byte, 0, 64)
	allocs := testing.AllocsPerRun(1000, func() {
		result, err := appendNestedColumnBlobJSON(dst[:0], child, &parent.Fields[1], s)
		if err != nil || string(result) != `{"value":7}` {
			t.Fatalf("invalid nested result: %s / %v", result, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("nested model validation allocated: %g", allocs)
	}
}

func nestedBoundaryConverters(t testing.TB, parent *Model, child []byte, present bool, schemas ...*Schema) map[string]func([]byte) ([]byte, error) {
	field := &parent.Fields[1]
	row := []byte{0, 1, 84, 2}
	var column codec.ColumnarWriter
	column.SetCount(1)
	column.WriteColumnInt(1, []int64{42})
	if field.Nullable {
		var value *[]byte
		if present {
			value = &child
			row = append(row, 1)
		} else {
			row = append(row, 0)
		}
		column.WriteColumnBytesPtr(2, []*[]byte{value})
	} else {
		column.WriteColumnBytes(2, [][]byte{child})
	}
	if present {
		row = codec.AppendBytes(row, child)
	}
	row = append(row, 0)
	columns := testutil.Bytes(t)(column.Bytes())
	page := append(bytes.Clone(columns), 2, 2, 20)
	return map[string]func([]byte) ([]byte, error){
		"row":    func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, row, parent, schemas...) },
		"column": func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, columns, parent, schemas...) },
		"page":   func(dst []byte) ([]byte, error) { return BinaryPaginatedListToJSON(dst, page, parent, schemas...) },
	}
}

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
	data = codec.AppendBytes(data, []byte("abc"))
	// Field 2: member = nested User
	data = codec.AppendVarint(data, 2)
	// Nested User: [arenaHeader] fields... [0x00]
	nestedStart := len(data)
	data = codec.AppendVarint(data, 0) // arena header for User
	data = codec.AppendVarint(data, 1) // User.id
	data = codec.AppendSvarint(data, 42)
	data = codec.AppendVarint(data, 2) // User.name
	data = codec.AppendBytes(data, []byte("Alice"))
	data = append(data, 0x00) // end of nested User
	data = frameNestedFixture(data, nestedStart)
	data = append(data, 0x00) // end of AuthPayload

	m := s.Models["AuthPayload"]
	result := checkedJSON(t)(BinaryToJSON(nil, data, m, s))
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
	data = codec.AppendBytes(data, []byte("test"))
	// Field 2: inner = nested Inner
	data = codec.AppendVarint(data, 2)
	nestedStart := len(data)
	data = codec.AppendVarint(data, 0) // arena header for Inner
	data = codec.AppendVarint(data, 1) // Inner.value
	data = codec.AppendBytes(data, []byte("hello"))
	data = append(data, 0x00) // end of Inner
	data = frameNestedFixture(data, nestedStart)
	data = append(data, 0x00) // end of Outer

	m := s.Models["Outer"]
	result := checkedJSON(t)(BinaryToJSON(nil, data, m, s))
	got := string(result)

	if !strings.Contains(got, `"name":"test"`) {
		t.Errorf("missing name: %s", got)
	}
	if !strings.Contains(got, `"value":"hello"`) {
		t.Errorf("missing nested value: %s", got)
	}
}

func TestBinaryToJSON_UnknownNestedType(t *testing.T) {
	// An unknown nested type must not become a fabricated null value.
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
	data = codec.AppendBytes(data, []byte("box"))
	// Field 2: unknown = nested (will be drained)
	data = codec.AppendVarint(data, 2)
	nestedStart := len(data)
	data = codec.AppendVarint(data, 0) // arena header for nested (unknown type)
	data = codec.AppendVarint(data, 1)
	data = codec.AppendBytes(data, []byte("data"))
	data = append(data, 0x00) // end of nested
	data = frameNestedFixture(data, nestedStart)
	data = append(data, 0x00) // end of Container

	m := s.Models["Container"]
	result, err := BinaryToJSON(nil, data, m, s)
	if err == nil || len(result) != 0 || !strings.Contains(err.Error(), "NonExistent") {
		t.Fatalf("unknown nested type became success: %s / %v", result, err)
	}
}

func TestBinaryToJSON_NestedTypeListRowWise(t *testing.T) {
	// Single type response containing a nested type LIST (e.g. ServiceSchema
	// { serviceName, types: [SchemaType] }). Row-wise wire format:
	// [fieldID][byte length][columnar items]
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
	data = codec.AppendBytes(data, []byte("svc"))
	data = codec.AppendVarint(data, 2)
	var items codec.ColumnarWriter
	items.SetCount(2)
	items.WriteColumnInt(1, []int64{7, 8})
	data = codec.AppendBytes(data, testutil.Bytes(t)(items.Bytes()))
	data = codec.AppendVarint(data, 3)
	data = codec.AppendBytes(data, []byte("after"))
	data = append(data, 0x00) // end of Outer

	outer := s.Types["Outer"].AsModel()
	got := string(checkedJSON(t)(BinaryToJSON(nil, data, outer, s)))

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
	nestedStart := len(data)
	data = codec.AppendVarint(data, 0) // nested arena header
	data = codec.AppendVarint(data, 1)
	data = codec.AppendBytes(data, []byte("hi"))
	data = append(data, 0x00)
	data = frameNestedFixture(data, nestedStart)
	data = codec.AppendVarint(data, 2)
	data = codec.AppendBytes(data, []byte("after"))
	data = append(data, 0x00)

	outer := s.Types["Outer"].AsModel()
	got := string(checkedJSON(t)(BinaryToJSON(nil, data, outer, s)))
	if !strings.Contains(got, `"inner":{"value":"hi"}`) || !strings.Contains(got, `"tail":"after"`) {
		t.Errorf("present nullable nested should decode its child frame: %s", got)
	}

	// Null case
	var data2 []byte
	data2 = codec.AppendVarint(data2, 0)
	data2 = codec.AppendVarint(data2, 1)
	data2 = codec.AppendNull(data2)
	data2 = codec.AppendVarint(data2, 2)
	data2 = codec.AppendBytes(data2, []byte("after"))
	data2 = append(data2, 0x00)

	got2 := string(checkedJSON(t)(BinaryToJSON(nil, data2, outer, s)))
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
	cell0 := testutil.Bytes(t)(innerW.Bytes())

	// An empty list still has its own count, arena header and end marker.
	var empty codec.ColumnarWriter
	cell1 := testutil.Bytes(t)(empty.Bytes())

	w := &codec.ColumnarWriter{}
	w.SetCount(2)
	w.WriteColumnString(1, []string{"a", "b"})
	w.WriteColumnBytes(2, [][]byte{cell0, cell1})

	outer := s.Types["Outer"].AsModel()
	got := string(checkedJSON(t)(BinaryListToJSON(nil, testutil.Bytes(t)(w.Bytes()), outer, s)))

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
	cell = codec.AppendBytes(cell, []byte("hello"))
	cell = append(cell, 0x00)

	w := &codec.ColumnarWriter{}
	w.SetCount(1)
	w.WriteColumnString(1, []string{"a"})
	w.WriteColumnBytes(2, [][]byte{cell})

	outer := s.Types["Outer"].AsModel()
	got := string(checkedJSON(t)(BinaryListToJSON(nil, testutil.Bytes(t)(w.Bytes()), outer, s)))

	if !strings.Contains(got, `"inner":{"value":"hello"}`) {
		t.Errorf("nested single type should decode via Types lookup: %s", got)
	}
}

func TestBinaryToJSON_NestedNoSchema(t *testing.T) {
	// Present relations require metadata, even when their frame can be skipped.
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
	data = codec.AppendBytes(data, []byte("ignored"))
	data = append(data, 0x00)

	m := s.Models["M"]
	result, err := BinaryToJSON(nil, data, m)
	if err == nil || len(result) != 0 || !strings.Contains(err.Error(), "Other") {
		t.Fatalf("missing nested schema became success: %s / %v", result, err)
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

	got := string(checkedJSON(t)(BinaryListToJSON(nil, testutil.Bytes(t)(writer.Bytes()), outer, s)))
	if got != `[{"child":null},{"child":{"name":"Ada"}}]` {
		t.Fatalf("nullable nested column = %s", got)
	}
}

func frameNestedFixture(data []byte, start int) []byte {
	return codec.AppendBytes(append([]byte(nil), data[:start]...), data[start:])
}
