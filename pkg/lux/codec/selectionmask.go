package codec

import "sort"

// SelectionMaskChild binds a relation field to its recursively encoded child mask.
type SelectionMaskChild struct {
	FieldID int
	Mask    []byte
}

// AppendSelectionMask appends one canonical selection node. A node contains a
// bitmap followed by length-delimited child nodes ordered by relation field ID.
func AppendSelectionMask(dst, fields []byte, children []SelectionMaskChild) []byte {
	dst = AppendVarint(dst, uint64(len(fields)))
	dst = append(dst, fields...)
	if len(children) == 0 {
		return dst
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
	return dst
}

// SplitSelectionMask returns the current node's bitmap and encoded child area.
// An empty mask means select all. Malformed nodes return ok=false.
func SplitSelectionMask(mask []byte) (fields, children []byte, ok bool) {
	if len(mask) == 0 {
		return nil, nil, true
	}
	fieldLen, n := ReadVarint(mask, 0)
	if n <= 0 || fieldLen == 0 || fieldLen > uint64(len(mask)-n) {
		return nil, nil, false
	}
	end := n + int(fieldLen)
	fields = mask[n:end]
	if fields[len(fields)-1] == 0 || !validSelectionChildren(mask[end:]) {
		return nil, nil, false
	}
	return fields, mask[end:], true
}

func validSelectionChildren(children []byte) bool {
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
