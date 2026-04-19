package codec

// Field mask bitmap encoding for Luxo binary protocol.
// Each bit represents a field ID. Bit N = field ID N+1 is selected.
// Byte 0 covers field IDs 1-8, byte 1 covers 9-16, etc.
// Empty mask (length 0) = SELECT * (all fields).

// FieldMaskHas checks if a field ID is set in the mask.
// Returns true if mask is nil/empty (SELECT *).
func FieldMaskHas(mask []byte, fieldID int) bool {
	if len(mask) == 0 {
		return true // nil mask = all fields
	}
	if fieldID <= 0 {
		return false
	}
	idx := (fieldID - 1) / 8
	bit := uint(fieldID-1) % 8
	if idx >= len(mask) {
		return false
	}
	return mask[idx]&(1<<bit) != 0
}

// FieldMaskSet sets a field ID bit in the mask, growing if needed.
func FieldMaskSet(mask []byte, fieldID int) []byte {
	if fieldID <= 0 {
		return mask
	}
	idx := (fieldID - 1) / 8
	bit := uint(fieldID-1) % 8
	for len(mask) <= idx {
		mask = append(mask, 0)
	}
	mask[idx] |= 1 << bit
	return mask
}

// FieldMaskAll creates a mask with all field IDs up to maxID set.
func FieldMaskAll(maxID int) []byte {
	if maxID <= 0 {
		return nil
	}
	size := (maxID + 7) / 8
	mask := make([]byte, size)
	for i := 1; i <= maxID; i++ {
		idx := (i - 1) / 8
		bit := uint(i-1) % 8
		mask[idx] |= 1 << bit
	}
	return mask
}
