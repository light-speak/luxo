package codec

import (
	"bytes"
	"math"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
)

func TestFieldMaskConstructionBoundaries(t *testing.T) {
	for _, id := range []int{1, 7, 8, 9, 128, MaxSelectionFieldID} {
		mask := testutil.Bytes(t)(FieldMaskSet(nil, id))
		if len(mask) != (id+7)/8 || !FieldMaskHas(mask, id) {
			t.Fatalf("incorrect bitmap size or bit for %d", id)
		}
		all := testutil.Bytes(t)(FieldMaskAll(id))
		for field := 1; field <= id; field++ {
			if !FieldMaskHas(all, field) {
				t.Fatalf("missing field %d from full mask %d", field, id)
			}
		}
		if FieldMaskHas(all, id+1) || !validSelectionBitmap(all) {
			t.Fatalf("unexpected extra bit in full mask %d", id)
		}
	}
	backing := bytes.Repeat([]byte{0xff}, 16)
	mask := testutil.Bytes(t)(FieldMaskSet(backing[:1], 128))
	if mask[0] != 0xff || mask[15] != 0x80 || !bytes.Equal(mask[1:15], make([]byte, 14)) {
		t.Fatal("growth retained stale backing-array bits")
	}
}

func TestFieldMaskErrorsPreserveInput(t *testing.T) {
	for _, id := range []int{0, -1, MaxSelectionFieldID + 1, math.MaxInt, math.MinInt} {
		mask := []byte{1}
		if out, err := FieldMaskSet(mask, id); err == nil || out != nil || !bytes.Equal(mask, []byte{1}) {
			t.Fatalf("invalid ID %d changed the input or succeeded: %x, %v", id, out, err)
		}
		if out, err := FieldMaskAll(id); err == nil || out != nil {
			t.Fatalf("invalid maximum %d accepted: %v", id, err)
		}
		if FieldMaskHas(nil, id) {
			t.Fatalf("invalid ID %d matched an empty mask", id)
		}
	}
	for _, mask := range [][]byte{make([]byte, MaxSelectionMaskBytes+1), append(make([]byte, MaxSelectionMaskBytes-1), 128)} {
		before := append([]byte(nil), mask...)
		if out, err := FieldMaskSet(mask, 1); err == nil || out != nil || !bytes.Equal(mask, before) {
			t.Fatal("invalid bitmap accepted or modified")
		}
	}
}

func TestAppendSelectionMaskFailureIsAtomic(t *testing.T) {
	for _, fields := range [][]byte{nil, {0}, {1, 0}, make([]byte, MaxSelectionMaskBytes+1)} {
		backing := bytes.Repeat([]byte{0x77}, 64)
		if out, err := AppendSelectionMask(backing[:1], fields, nil); err == nil || out != nil || !bytes.Equal(backing, bytes.Repeat([]byte{0x77}, 64)) {
			t.Fatal("invalid bitmap appended partial output")
		}
	}
	valid := []byte{1, 1}
	for _, children := range [][]SelectionMaskChild{
		{{FieldID: 0, Mask: valid}}, {{FieldID: MaxSelectionFieldID + 1, Mask: valid}},
		{{FieldID: 1}}, {{FieldID: 1, Mask: []byte{0}}},
		{{FieldID: 1, Mask: valid}, {FieldID: 1, Mask: valid}},
		{{FieldID: 2, Mask: valid}, {FieldID: 1, Mask: valid}, {FieldID: 2, Mask: valid}},
		{{FieldID: 3, Mask: valid}},
	} {
		backing := bytes.Repeat([]byte{0x77}, 64)
		if out, err := AppendSelectionMask(backing[:1], []byte{3}, children); err == nil || out != nil || !bytes.Equal(backing, bytes.Repeat([]byte{0x77}, 64)) {
			t.Fatal("invalid children appended partial output")
		}
	}
}
