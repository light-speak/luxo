package codec

import (
	"cmp"
	"slices"
)

// SelectionMaskChild binds a relation field to its recursively encoded child mask.
type SelectionMaskChild struct {
	FieldID int
	Mask    []byte
}

// AppendSelectionMask appends one canonical selection node. A node contains a
// bitmap followed by length-delimited child nodes ordered by relation field ID.
// Invalid input returns an error without modifying dst or reordering children.
func AppendSelectionMask(dst, fields []byte, children []SelectionMaskChild) ([]byte, error) {
	if !validSelectionBitmap(fields) {
		return nil, ErrInvalidSelectionMask
	}
	sorted, err := checkedSelectionChildren(fields, children)
	if err != nil {
		return nil, err
	}
	dst = AppendVarint(dst, uint64(len(fields)))
	dst = append(dst, fields...)
	for _, child := range sorted {
		dst = AppendVarint(dst, uint64(child.FieldID))
		dst = AppendVarint(dst, uint64(len(child.Mask)))
		dst = append(dst, child.Mask...)
	}
	return dst, nil
}

func validSelectionBitmap(fields []byte) bool {
	return len(fields) > 0 && fields[len(fields)-1] != 0 && validFieldMaskBits(fields)
}

// Validate before touching dst; only clone when sorting is actually needed.
func checkedSelectionChildren(fields []byte, children []SelectionMaskChild) ([]SelectionMaskChild, error) {
	ordered, previous := true, 0
	for _, child := range children {
		if !FieldMaskHas(fields, child.FieldID) || len(child.Mask) == 0 {
			return nil, ErrInvalidSelectionMask
		}
		if _, _, ok := SplitSelectionMask(child.Mask); !ok {
			return nil, ErrInvalidSelectionMask
		}
		if child.FieldID <= previous {
			ordered = false
		}
		previous = child.FieldID
	}
	if ordered {
		return children, nil
	}
	sorted := slices.Clone(children)
	slices.SortFunc(sorted, func(a, b SelectionMaskChild) int { return cmp.Compare(a.FieldID, b.FieldID) })
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].FieldID == sorted[i].FieldID {
			return nil, ErrInvalidSelectionMask
		}
	}
	return sorted, nil
}

// SplitSelectionMask returns the current node's bitmap and encoded child area.
// An empty mask means select all. Malformed nodes return ok=false.
func SplitSelectionMask(mask []byte) (fields, children []byte, ok bool) {
	if len(mask) == 0 {
		return nil, nil, true
	}
	fieldLen, n := ReadVarint(mask, 0)
	if n <= 0 || fieldLen == 0 || fieldLen > MaxSelectionMaskBytes || fieldLen > uint64(len(mask)-n) {
		return nil, nil, false
	}
	end := n + int(fieldLen)
	fields = mask[n:end]
	if !validSelectionBitmap(fields) || !validSelectionChildren(mask[end:]) {
		return nil, nil, false
	}
	return fields, mask[end:], true
}

func validSelectionChildren(children []byte) bool {
	previousID := uint64(0)
	for off := 0; off < len(children); {
		fieldID, n := ReadVarint(children, off)
		if n <= 0 || fieldID == 0 || fieldID > MaxSelectionFieldID || fieldID <= previousID {
			return false
		}
		off += n
		length, n := ReadVarint(children, off)
		if n <= 0 || length == 0 || length > uint64(len(children)-off-n) {
			return false
		}
		off += n
		end := off + int(length)
		if _, _, ok := SplitSelectionMask(children[off:end]); !ok {
			return false
		}
		off = end
		previousID = fieldID
	}
	return true
}

// SelectionMaskFields returns the current node's bitmap. An empty or malformed
// mask returns nil; request parsing rejects malformed masks before writers run.
func SelectionMaskFields(mask []byte) []byte {
	fields, _, ok := SplitSelectionMask(mask)
	if !ok {
		return nil
	}
	return fields
}

// SelectionMaskNested returns the nested mask for a selected relation field.
func SelectionMaskNested(mask []byte, targetID int) ([]byte, bool) {
	_, children, ok := SplitSelectionMask(mask)
	if !ok || targetID <= 0 {
		return nil, false
	}
	for off := 0; off < len(children); {
		fieldID, n := ReadVarint(children, off)
		off += n
		length, n := ReadVarint(children, off)
		off += n
		end := off + int(length)
		if int(fieldID) == targetID {
			return children[off:end], true
		}
		if int(fieldID) > targetID {
			return nil, false
		}
		off = end
	}
	return nil, false
}
