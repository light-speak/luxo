package codec

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
)

func TestSelectionMaskDepthBoundary(t *testing.T) {
	for _, depth := range []int{1, 2, 31, 32, 33, 1024} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			mask := rawSelectionMaskDepth(depth)
			_, _, ok := SplitSelectionMask(mask)
			if ok != (depth <= 32) {
				t.Fatalf("depth %d accepted=%v, want %v", depth, ok, depth <= 32)
			}
			if depth > 32 {
				if fields := SelectionMaskFields(mask); fields != nil {
					t.Fatal("over-depth mask exposed fields")
				}
				if child, found := SelectionMaskNested(mask, 1); found || child != nil {
					t.Fatal("over-depth mask exposed a child")
				}
			}
		})
	}
}

func TestAppendSelectionMaskCountsParentDepthBeforeWriting(t *testing.T) {
	for _, childDepth := range []int{1, 30, 31, 32, 1024} {
		t.Run(fmt.Sprint(childDepth), func(t *testing.T) {
			child := rawSelectionMaskDepth(childDepth)
			backing := bytes.Repeat([]byte{0x77}, 64)
			before := bytes.Clone(backing)
			out, err := AppendSelectionMask(backing[:1], []byte{1}, []SelectionMaskChild{{FieldID: 1, Mask: child}})
			if childDepth >= 32 {
				if err == nil || out != nil || !bytes.Equal(backing, before) {
					t.Fatal("over-depth child accepted or destination modified")
				}
			} else if err != nil || !bytes.Equal(out[1:], rawSelectionMaskDepth(childDepth+1)) {
				t.Fatalf("valid child depth %d changed wire: %v", childDepth, err)
			}
		})
	}
}

// Construct receiver fixtures independently of the checked production encoder.
func rawSelectionMaskDepth(depth int) []byte {
	mask := []byte{1, 1}
	for level := 1; level < depth; level++ {
		mask = rawSelectionMask([]byte{1}, SelectionMaskChild{FieldID: 1, Mask: mask})
	}
	return mask
}

func TestSelectionMaskRoundTrip(t *testing.T) {
	childFields := testutil.Bytes(t)(FieldMaskSet(nil, 2))
	child := testutil.Bytes(t)(AppendSelectionMask(nil, childFields, nil))
	rootFields := testutil.Bytes(t)(FieldMaskSet(nil, 1))
	rootFields = testutil.Bytes(t)(FieldMaskSet(rootFields, 3))
	root := testutil.Bytes(t)(AppendSelectionMask(nil, rootFields, []SelectionMaskChild{{FieldID: 3, Mask: child}}))

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
	if _, found := SelectionMaskNested(root, 4); found {
		t.Fatal("unselected field must not have a child mask")
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

func TestSelectionMaskClassifiedFieldLimit(t *testing.T) {
	maxFields := testutil.Bytes(t)(FieldMaskSet(nil, MaxSelectionFieldID))
	valid := testutil.Bytes(t)(AppendSelectionMask(nil, maxFields, nil))
	if fields, _, ok := SplitSelectionMask(valid); !ok || !FieldMaskHas(fields, MaxSelectionFieldID) {
		t.Fatal("maximum selection field was rejected")
	}
	child := testutil.Bytes(t)(AppendSelectionMask(nil, []byte{1}, nil))
	maxChild := testutil.Bytes(t)(AppendSelectionMask(nil, maxFields, []SelectionMaskChild{{FieldID: MaxSelectionFieldID, Mask: child}}))
	if _, found := SelectionMaskNested(maxChild, MaxSelectionFieldID); !found {
		t.Fatal("maximum child field was rejected")
	}
	unusedBit := append([]byte(nil), maxFields...)
	unusedBit[len(unusedBit)-1] |= 0x80
	oversized := append(append([]byte(nil), maxFields...), 1)
	cases := map[string][]byte{
		"unused final bit":       rawSelectionMask(unusedBit),
		"oversized bitmap":       rawSelectionMask(oversized),
		"child ID overflow":      rawSelectionMask([]byte{1}, SelectionMaskChild{FieldID: MaxSelectionFieldID + 1, Mask: child}),
		"nested bitmap overflow": rawSelectionMask([]byte{1}, SelectionMaskChild{FieldID: 1, Mask: rawSelectionMask(oversized)}),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := SplitSelectionMask(wire); ok {
				t.Fatal("out-of-range selection accepted")
			}
		})
	}
}

func TestAppendSelectionMaskSortsChildren(t *testing.T) {
	child := testutil.Bytes(t)(AppendSelectionMask(nil, []byte{1}, nil))
	input := []SelectionMaskChild{
		{FieldID: 3, Mask: child},
		{FieldID: 1, Mask: child},
	}
	root := testutil.Bytes(t)(AppendSelectionMask([]byte{99}, []byte{7}, input))
	if input[0].FieldID != 3 || input[1].FieldID != 1 || root[0] != 99 {
		t.Fatal("constructor mutated children or destination prefix")
	}
	root = root[1:]
	_, children, ok := SplitSelectionMask(root)
	if !ok {
		t.Fatal("invalid generated mask")
	}
	first, n := ReadVarint(children, 0)
	if n <= 0 || first != 1 {
		t.Fatalf("first child field ID = %d, want 1", first)
	}
}

func TestAppendSelectionMaskRejectsInvalidChildren(t *testing.T) {
	child := testutil.Bytes(t)(AppendSelectionMask(nil, []byte{1}, nil))
	root, err := AppendSelectionMask(nil, []byte{1}, []SelectionMaskChild{
		{FieldID: 0, Mask: child},
		{FieldID: 1},
	})
	if err == nil || root != nil {
		t.Fatalf("invalid children silently ignored: %v", err)
	}
}

// Build malformed receiver fixtures without calling the checked constructor.
func rawSelectionMask(fields []byte, children ...SelectionMaskChild) []byte {
	mask := AppendVarint(nil, uint64(len(fields)))
	mask = append(mask, fields...)
	for _, child := range children {
		mask = AppendVarint(mask, uint64(child.FieldID))
		mask = AppendVarint(mask, uint64(len(child.Mask)))
		mask = append(mask, child.Mask...)
	}
	return mask
}
