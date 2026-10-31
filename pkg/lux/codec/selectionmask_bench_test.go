package codec

import (
	"bytes"
	"fmt"
	"sort"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
)

// Frozen pre-error constructors, retained only for equivalent legal-input comparisons.
func legacyFieldMaskSet(mask []byte, fieldID int) ([]byte, error) {
	if fieldID <= 0 {
		return mask, nil
	}
	idx, bit := (fieldID-1)/8, uint(fieldID-1)%8
	for len(mask) <= idx {
		mask = append(mask, 0)
	}
	mask[idx] |= 1 << bit
	return mask, nil
}

func legacyFieldMaskAll(maxID int) ([]byte, error) {
	if maxID <= 0 {
		return nil, nil
	}
	mask := make([]byte, (maxID+7)/8)
	for i := 1; i <= maxID; i++ {
		mask[(i-1)/8] |= 1 << (uint(i-1) % 8)
	}
	return mask, nil
}

func legacyAppendSelectionMask(dst, fields []byte, children []SelectionMaskChild) ([]byte, error) {
	dst = AppendVarint(dst, uint64(len(fields)))
	dst = append(dst, fields...)
	if len(children) == 0 {
		return dst, nil
	}
	sorted := append([]SelectionMaskChild(nil), children...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].FieldID < sorted[j].FieldID })
	for _, child := range sorted {
		if child.FieldID <= 0 || len(child.Mask) == 0 {
			continue
		}
		dst = AppendVarint(dst, uint64(child.FieldID))
		dst = AppendVarint(dst, uint64(len(child.Mask)))
		dst = append(dst, child.Mask...)
	}
	return dst, nil
}

func BenchmarkFieldMaskConstructors(b *testing.B) {
	for _, id := range []int{1, 128, MaxSelectionFieldID} {
		for _, reuse := range []bool{false, true} {
			for _, impl := range []struct {
				name string
				set  func([]byte, int) ([]byte, error)
			}{{"before", legacyFieldMaskSet}, {"after", FieldMaskSet}} {
				b.Run(fmt.Sprintf("set/%d/reuse=%t/%s", id, reuse, impl.name), func(b *testing.B) {
					var mask []byte
					if reuse {
						mask = make([]byte, (id+7)/8)
					}
					b.ReportAllocs()
					for b.Loop() {
						out, err := impl.set(mask, id)
						if err != nil || out[(id-1)/8] == 0 {
							b.Fatal("invalid mask", err)
						}
					}
				})
			}
		}
		for _, impl := range []struct {
			name string
			all  func(int) ([]byte, error)
		}{{"before", legacyFieldMaskAll}, {"after", FieldMaskAll}} {
			b.Run(fmt.Sprintf("all/%d/%s", id, impl.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					out, err := impl.all(id)
					if err != nil || len(out) != (id+7)/8 {
						b.Fatal("invalid mask", err)
					}
				}
			})
		}
	}
}

func BenchmarkSelectionMaskConstruction(b *testing.B) {
	leaf := testutil.Bytes(b)(AppendSelectionMask(nil, []byte{1}, nil))
	ordered := make([]SelectionMaskChild, 64)
	reversed := make([]SelectionMaskChild, 64)
	for i := range ordered {
		ordered[i] = SelectionMaskChild{FieldID: i + 1, Mask: leaf}
		reversed[63-i] = ordered[i]
	}
	fields := testutil.Bytes(b)(FieldMaskAll(64))
	nested := leaf
	for range 30 {
		nested = testutil.Bytes(b)(AppendSelectionMask(nil, []byte{1}, []SelectionMaskChild{{FieldID: 1, Mask: nested}}))
	}
	for _, sample := range []struct {
		name     string
		fields   []byte
		children []SelectionMaskChild
	}{
		{"leaf", []byte{1}, nil},
		{"ordered-64", fields, ordered},
		{"reversed-64", fields, reversed},
		{"depth-32", []byte{1}, []SelectionMaskChild{{FieldID: 1, Mask: nested}}},
	} {
		before := testutil.Bytes(b)(legacyAppendSelectionMask(nil, sample.fields, sample.children))
		after := testutil.Bytes(b)(AppendSelectionMask(nil, sample.fields, sample.children))
		if !bytes.Equal(before, after) {
			b.Fatal("legal wire changed")
		}
		for _, impl := range []struct {
			name   string
			append func([]byte, []byte, []SelectionMaskChild) ([]byte, error)
		}{{"before", legacyAppendSelectionMask}, {"after", AppendSelectionMask}} {
			b.Run(sample.name+"/"+impl.name, func(b *testing.B) {
				dst := make([]byte, 0, len(after))
				b.ReportAllocs()
				for b.Loop() {
					out, err := impl.append(dst[:0], sample.fields, sample.children)
					if err != nil || len(out) != len(after) {
						b.Fatal("invalid node", err)
					}
				}
			})
		}
	}
}

// Frozen pre-limit parser for local comparison, never used by production.
func legacySplitSelectionMask(mask []byte) (fields, children []byte, ok bool) {
	if len(mask) == 0 {
		return nil, nil, true
	}
	fieldLen, n := ReadVarint(mask, 0)
	if n <= 0 || fieldLen == 0 || fieldLen > uint64(len(mask)-n) {
		return nil, nil, false
	}
	end := n + int(fieldLen)
	fields = mask[n:end]
	if fields[len(fields)-1] == 0 || !legacyValidSelectionChildren(mask[end:]) {
		return nil, nil, false
	}
	return fields, mask[end:], true
}

func legacyValidSelectionChildren(children []byte) bool {
	previousID := uint64(0)
	for off := 0; off < len(children); {
		fieldID, n := ReadVarint(children, off)
		if n <= 0 || fieldID == 0 || fieldID <= previousID {
			return false
		}
		off += n
		length, n := ReadVarint(children, off)
		if n <= 0 || length == 0 || length > uint64(len(children)-off-n) {
			return false
		}
		off += n
		end := off + int(length)
		if _, _, ok := legacySplitSelectionMask(children[off:end]); !ok {
			return false
		}
		off = end
		previousID = fieldID
	}
	return true
}

func BenchmarkSelectionMaskLimits(b *testing.B) {
	child := testutil.Bytes(b)(AppendSelectionMask(nil, []byte{1}, nil))
	children := make([]SelectionMaskChild, 64)
	for i := range children {
		children[i] = SelectionMaskChild{FieldID: i + 1, Mask: child}
	}
	samples := []struct {
		name string
		wire []byte
	}{
		{"empty", nil},
		{"small", child},
		{"maximum-bitmap", testutil.Bytes(b)(AppendSelectionMask(nil, testutil.Bytes(b)(FieldMaskSet(nil, MaxSelectionFieldID)), nil))},
		{"64-children", testutil.Bytes(b)(AppendSelectionMask(nil, testutil.Bytes(b)(FieldMaskAll(64)), children))},
	}
	for _, sample := range samples {
		for _, parser := range []struct {
			name string
			read func([]byte) ([]byte, []byte, bool)
		}{{"before", legacySplitSelectionMask}, {"after", SplitSelectionMask}} {
			b.Run(sample.name+"/"+parser.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, ok := parser.read(sample.wire); !ok {
						b.Fatal("valid selection rejected")
					}
				}
			})
		}
	}
}
