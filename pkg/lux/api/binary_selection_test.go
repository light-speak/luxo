package api

import (
	"github.com/light-speak/luxo/internal/testutil"

	"fmt"
	"reflect"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func selectionFixture(t testing.TB, width, selected, branches int) (*schema.Schema, *schema.Model, []byte) {
	s := schema.New()
	model := &schema.Model{Name: "Record"}
	var bitmap []byte
	for i := range width {
		model.Fields = append(model.Fields, schema.Field{ID: i + 1, Name: fmt.Sprintf("field%d", i+1)})
		if i < selected {
			bitmap = testutil.Bytes(t)(codec.FieldMaskSet(bitmap, i+1))
		}
	}
	s.RegisterModel(model)
	mask := testSelectionMask(bitmap)
	if branches == 0 {
		return s, model, mask
	}
	parent := &schema.Model{Name: "Parent"}
	var parentBitmap []byte
	var children []codec.SelectionMaskChild
	for i := range branches {
		id := 2*i + 1
		parent.Fields = append(parent.Fields, schema.Field{ID: id, Name: fmt.Sprintf("child%d", id), Relation: true, TypeName: model.Name})
		parentBitmap = testutil.Bytes(t)(codec.FieldMaskSet(parentBitmap, id))
		children = append(children, codec.SelectionMaskChild{FieldID: id, Mask: mask})
	}
	s.RegisterModel(parent)
	return s, parent, testSelectionMask(parentBitmap, children...)
}

func TestDecodeFieldMaskCompactAllocation(t *testing.T) {
	for _, count := range []int{1, 16, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, model, mask := selectionFixture(t, 128, count, 0)
			var fields []*selection.Field
			allocs := testing.AllocsPerRun(100, func() {
				var err error
				fields, err = decodeFieldMask(mask, model, s, 0)
				if err != nil {
					t.Fatal(err)
				}
			})
			if len(fields) != count || allocs > 2 {
				t.Fatalf("selection has %d fields, %.0f allocations; want %d fields and at most two allocations", len(fields), allocs, count)
			}
		})
	}
}

func TestDecodeFieldMaskIndependentTrees(t *testing.T) {
	s, model, mask := selectionFixture(t, 16, 16, 3)
	first, err := decodeFieldMask(mask, model, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := decodeFieldMask(mask, model, s, 0)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("independent decode mismatch: %v", err)
	}
	first[0].Name = "changed"
	first[0].Children[0].Name = "changed child"
	first[1].Children = nil
	if second[0].Name != "child1" || second[0].Children[0].Name != "field1" || len(second[1].Children) != 16 {
		t.Fatal("selection trees share writable storage")
	}
	if first[2].Children[0].Name != "field1" || s.Models["Record"].Fields[0].Name != "field1" {
		t.Fatal("sibling or registered metadata mutated")
	}
}

func TestDecodeFieldMaskSparseSchemaOrder(t *testing.T) {
	s := schema.New()
	model := &schema.Model{Name: "Record", Fields: []schema.Field{
		{ID: 1001, Name: "child", Relation: true, TypeName: "Record"},
		{ID: 9, Name: "name"},
		{ID: 1, Name: "id"},
	}}
	s.RegisterModel(model)
	bitmap := testutil.Bytes(t)(codec.FieldMaskSet(testutil.Bytes(t)(codec.FieldMaskSet(nil, 1001)), 9))
	mask := testSelectionMask(bitmap, codec.SelectionMaskChild{FieldID: 1001, Mask: testSelectionMask(testutil.Bytes(t)(codec.FieldMaskSet(nil, 1)))})
	fields, err := decodeFieldMask(mask, model, s, 0)
	want := []*selection.Field{{Name: "child", Children: []*selection.Field{{Name: "id"}}}, {Name: "name"}}
	if err != nil || !reflect.DeepEqual(fields, want) {
		t.Fatalf("sparse unordered fields = %#v, error = %v", fields, err)
	}
}

func TestDecodeFieldMaskRejectsInvalidChildren(t *testing.T) {
	s := schema.New()
	model := &schema.Model{Name: "Record", Fields: []schema.Field{
		{ID: 1, Name: "id"},
		{ID: 3, Name: "child", Relation: true, TypeName: "Record"},
		{ID: 5, Name: "missing", Relation: true, TypeName: "Missing"},
	}}
	s.RegisterModel(model)
	valid := testSelectionMask(testutil.Bytes(t)(codec.FieldMaskSet(nil, 1)))
	for _, item := range []struct {
		name            string
		selected, child int
		mask            []byte
	}{
		{"scalar child", 1, 1, valid},
		{"unselected relation", 1, 3, valid},
		{"unknown child", 1, 2, valid},
		{"missing nested type", 5, 5, valid},
		{"unknown nested field", 3, 3, testSelectionMask(testutil.Bytes(t)(codec.FieldMaskSet(nil, 2)))},
		{"malformed nested field", 3, 3, []byte{1}},
	} {
		t.Run(item.name, func(t *testing.T) {
			mask := testSelectionMask(testutil.Bytes(t)(codec.FieldMaskSet(nil, item.selected)), codec.SelectionMaskChild{FieldID: item.child, Mask: item.mask})
			if fields, err := decodeFieldMask(mask, model, s, 0); err == nil || fields != nil {
				t.Fatalf("invalid mask accepted: fields=%#v, error=%v", fields, err)
			}
		})
	}
	for range 32 {
		valid = testSelectionMask(testutil.Bytes(t)(codec.FieldMaskSet(nil, 3)), codec.SelectionMaskChild{FieldID: 3, Mask: valid})
	}
	if _, err := decodeFieldMask(valid, model, s, 0); err == nil {
		t.Fatal("accepted selection deeper than 32 levels")
	}
}

func TestDecodeFieldMaskRejectsUnknownBits(t *testing.T) {
	s, model, _ := selectionFixture(t, 2, 2, 0)
	for _, bitmap := range [][]byte{{0xff}, {0x04}, {0x81}} {
		if fields, err := decodeFieldMask(testSelectionMask(bitmap), model, s, 0); err == nil || fields != nil {
			t.Fatalf("unknown bits accepted: %x, %#v, %v", bitmap, fields, err)
		}
	}
	if fields, err := decodeFieldMask(nil, model, s, 0); err == nil || fields != nil {
		t.Fatalf("empty mask implicitly selected fields: %#v, %v", fields, err)
	}
}

func TestDecodeFieldMaskInvalidSchemaDoesNotPanic(t *testing.T) {
	s := schema.New()
	model := &schema.Model{Name: "Duplicate", Fields: []schema.Field{{ID: 1, Name: "a"}, {ID: 1, Name: "b"}}}
	s.RegisterModel(model)
	if fields, err := decodeFieldMask(testSelectionMask([]byte{1}), model, s, 0); err == nil || fields != nil {
		t.Fatalf("duplicate schema field IDs accepted: %#v, %v", fields, err)
	}
	fields, err := decodeFieldMask(nil, &schema.Model{Name: "Empty"}, s, 0)
	if err != nil || fields == nil || len(fields) != 0 {
		t.Fatalf("empty schema changed meaning: %#v, %v", fields, err)
	}
}
