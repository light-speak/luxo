package codec

import "testing"

func TestSelectionMaskRoundTrip(t *testing.T) {
	childFields := FieldMaskSet(nil, 2)
	child := AppendSelectionMask(nil, childFields, nil)
	rootFields := FieldMaskSet(nil, 1)
	rootFields = FieldMaskSet(rootFields, 3)
	root := AppendSelectionMask(nil, rootFields, []SelectionMaskChild{{FieldID: 3, Mask: child}})

	fields, children, ok := SplitSelectionMask(root)
	if !ok || !FieldMaskHas(fields, 1) || !FieldMaskHas(fields, 3) || FieldMaskHas(fields, 2) {
		t.Fatalf("unexpected root fields: %v, ok=%v", fields, ok)
	}
	if len(children) == 0 {
		t.Fatal("missing encoded child entries")
	}
	if got := SelectionMaskFields(root); !FieldMaskHas(got, 3) {
		t.Fatalf("selection fields = %v", got)
	}
	got, found := SelectionMaskNested(root, 3)
	if !found {
		t.Fatal("missing child mask")
	}
	gotFields, _, ok := SplitSelectionMask(got)
	if !ok || !FieldMaskHas(gotFields, 2) || FieldMaskHas(gotFields, 1) {
		t.Fatalf("unexpected child fields: %v, ok=%v", gotFields, ok)
	}
	if _, found := SelectionMaskNested(root, 1); found {
		t.Fatal("scalar field must not have a child mask")
	}
}

func TestSelectionMaskEmptyMeansAll(t *testing.T) {
	fields, children, ok := SplitSelectionMask(nil)
	if !ok || fields != nil || children != nil {
		t.Fatalf("empty selection = (%v, %v, %v), want all", fields, children, ok)
	}
	if child, found := SelectionMaskNested(nil, 1); found || child != nil {
		t.Fatalf("empty selection child = (%v, %v)", child, found)
	}
	if fields := SelectionMaskFields([]byte{1}); fields != nil {
		t.Fatalf("malformed selection fields = %v", fields)
	}
	if child, found := SelectionMaskNested([]byte{1, 1}, 0); found || child != nil {
		t.Fatalf("invalid target child = (%v, %v)", child, found)
	}
}

func TestSelectionMaskRejectsMalformedNodes(t *testing.T) {
	tests := [][]byte{
		{2, 1},                   // truncated bitmap
		{1, 1, 0},                // child field ID 0
		{1, 1, 2},                // missing child length
		{1, 1, 2, 2, 1},          // truncated child
		{1, 1, 2, 1, 0},          // invalid empty-bitmap child node
		{1, 1, 2, 2, 1, 0},       // child contains trailing malformed data
		{1, 1, 0x80, 0x80, 0x80}, // truncated child field ID varint
	}
	for _, data := range tests {
		if _, _, ok := SplitSelectionMask(data); ok {
			t.Errorf("SplitSelectionMask(%v) unexpectedly succeeded", data)
		}
	}
}

func TestAppendSelectionMaskSortsChildren(t *testing.T) {
	child := AppendSelectionMask(nil, []byte{1}, nil)
	root := AppendSelectionMask(nil, []byte{7}, []SelectionMaskChild{
		{FieldID: 3, Mask: child},
		{FieldID: 1, Mask: child},
	})
	_, children, ok := SplitSelectionMask(root)
	if !ok {
		t.Fatal("invalid generated mask")
	}
	first, n := ReadVarint(children, 0)
	if n <= 0 || first != 1 {
		t.Fatalf("first child field ID = %d, want 1", first)
	}
}

func TestAppendSelectionMaskSkipsInvalidChildren(t *testing.T) {
	child := AppendSelectionMask(nil, []byte{1}, nil)
	root := AppendSelectionMask(nil, []byte{1}, []SelectionMaskChild{
		{FieldID: 0, Mask: child},
		{FieldID: 1},
	})
	if _, children, ok := SplitSelectionMask(root); !ok || len(children) != 0 {
		t.Fatalf("selection = %v, children = %v, ok = %v", root, children, ok)
	}
}
