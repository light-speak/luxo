package codec

import "errors"

// ErrInvalidSelectionField indicates an ID outside the selection bitmap domain.
var ErrInvalidSelectionField = errors.New("codec: selection field ID must be from 1 to 65535")

// ErrInvalidSelectionMask indicates a malformed bitmap or selection node.
var ErrInvalidSelectionMask = errors.New("codec: invalid selection mask")

func validFieldMaskBits(mask []byte) bool {
	return len(mask) < MaxSelectionMaskBytes ||
		(len(mask) == MaxSelectionMaskBytes && mask[len(mask)-1]&0x80 == 0)
}

// Field mask bitmap encoding for Luxo binary protocol.
// Each bit represents a field ID. Bit N = field ID N+1 is selected.
// Byte 0 covers field IDs 1-8, byte 1 covers 9-16, etc.
// Empty mask (length 0) = SELECT * (all fields).

// FieldMaskHas checks if a field ID is set in the mask.
// Returns true for valid IDs if mask is nil/empty (the internal all-fields sentinel).
func FieldMaskHas(mask []byte, fieldID int) bool {
	if fieldID <= 0 || fieldID > MaxSelectionFieldID {
		return false
	}
	if len(mask) == 0 {
		return true // nil mask = all fields
	}
	idx := (fieldID - 1) / 8
	bit := uint(fieldID-1) % 8
	if idx >= len(mask) {
		return false
	}
	return mask[idx]&(1<<bit) != 0
}

// FieldMaskSet sets a field ID bit in the mask, growing if needed.
// Invalid IDs or oversized bitmaps return an error without modifying mask.
func FieldMaskSet(mask []byte, fieldID int) ([]byte, error) {
	if fieldID <= 0 || fieldID > MaxSelectionFieldID {
		return nil, ErrInvalidSelectionField
	}
	if !validFieldMaskBits(mask) {
		return nil, ErrInvalidSelectionMask
	}
	idx := (fieldID - 1) / 8
	bit := uint(fieldID-1) % 8
	if len(mask) <= idx {
		mask = append(mask, make([]byte, idx+1-len(mask))...)
	}
	mask[idx] |= 1 << bit
	return mask, nil
}

// FieldMaskAll creates a mask with all field IDs up to maxID set.
// maxID must be in [1, MaxSelectionFieldID]; zero is not an all-fields request.
func FieldMaskAll(maxID int) ([]byte, error) {
	if maxID <= 0 || maxID > MaxSelectionFieldID {
		return nil, ErrInvalidSelectionField
	}
	size := (maxID + 7) / 8
	mask := make([]byte, size)
	for i := range mask {
		mask[i] = 0xff
	}
	mask[size-1] >>= uint(size*8 - maxID)
	return mask, nil
}
