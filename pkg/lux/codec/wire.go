// Package codec implements the Luxo binary wire format.
// Used for event payloads (NATSBus), multi-service RPC, and API transport.
//
// Wire format per field: [fieldID varint] [value]
// Types are known at compile time from schema — no type tags on wire.
//
// Encoding rules:
//   - Int: signed ZigZag varint; Boolean: one byte; Enum: string
//   - Float: fixed 8 bytes (little-endian float64)
//   - String/Bytes: length-prefixed (varint length + raw bytes)
//   - Nullable: 1-byte flag (0x00=null, 0x01=present) + value if present
//   - Message end: 0x00 terminator (fieldID 0 is reserved)
package codec

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// ErrNonFiniteFloat rejects values outside the protocol's finite Float domain.
var ErrNonFiniteFloat = errors.New("luxo: Float must be finite")

// ErrInvalidUTF8 rejects String values outside the protocol's text domain.
var ErrInvalidUTF8 = errors.New("luxo: String must be valid UTF-8")

// ErrInvalidEnum rejects enum values not declared by the service's schema.
var ErrInvalidEnum = errors.New("luxo: undeclared enum value")

// ErrInsufficientElementBytes rejects collections before allocating their
// results when even their minimum encoded size cannot fit the remaining bytes.
var ErrInsufficientElementBytes = errors.New("codec: insufficient element bytes")

// ErrInsufficientArenaBytes rejects an allocation larger than the available payload.
var ErrInsufficientArenaBytes = errors.New("codec: insufficient arena bytes")

// ErrEmptyColumnarProjection rejects nonempty lists with no returned column.
var ErrEmptyColumnarProjection = errors.New("codec: nonempty list requires at least one column")

// ErrInvalidColumnarCount rejects counts outside the supported wire domain.
var ErrInvalidColumnarCount = errors.New("codec: invalid columnar record count")

// ErrColumnarRowCount rejects columns whose values do not match the record count.
var ErrColumnarRowCount = errors.New("codec: column values must match the record count")

// ErrInvalidColumnarField rejects IDs that collide with the end marker or overflow.
var ErrInvalidColumnarField = errors.New("codec: column field ID must be positive")

// ErrInvalidColumnarArena rejects metadata outside the decoder's allocation domain.
var ErrInvalidColumnarArena = errors.New("codec: invalid columnar arena size")

// Safety limits to prevent OOM from malicious/corrupted wire data.
const (
	// MaxSelectionFieldID bounds model/type/extend masks to 8 KiB per level.
	MaxSelectionFieldID   = (1 << 16) - 1
	MaxSelectionMaskBytes = (MaxSelectionFieldID + 7) / 8
	// MaxAPIID and MaxEventFieldID fit the common signed 32-bit metadata domain.
	MaxAPIID        = (1 << 31) - 1
	MaxEventFieldID = (1 << 31) - 1
	// Request control IDs are not available to user-declared parameters.
	FiltersFieldID = (1 << 31) - 2
	SortersFieldID = (1 << 31) - 1
	MaxParameterID = FiltersFieldID - 1
	// MaxArrayElements caps decoded array/list size (1M elements).
	MaxArrayElements = 1_000_000
	// MaxColumnarRecords caps columnar record count (10M rows).
	MaxColumnarRecords = 10_000_000
)

// --- Varint (unsigned LEB128) ---

// AppendVarint appends a varint-encoded uint64 to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadVarint reads a varint from buf at offset. Returns value and bytes consumed.
// Returns (0, 0) on truncation and (0, -1) on overflow.
// Use ReadVarintErr for error details.
func ReadVarint(buf []byte, off int) (uint64, int) {
	var v uint64
	var shift uint
	for i := off; i < len(buf); i++ {
		b := buf[i]
		v |= uint64(b&0x7F) << shift
		if b < 0x80 {
			return v, i - off + 1
		}
		shift += 7
		// Only the tenth byte can overflow uint64. Validate it on the
		// continuation path so short values need no extra terminal check.
		if shift == 63 && i+1 < len(buf) && buf[i+1] > 1 {
			return 0, -1 // overflow (distinguishable from truncation)
		}
	}
	return 0, 0 // incomplete/truncated
}

// --- Signed varint (zigzag encoding) ---

// AppendSvarint appends a zigzag-encoded int64 to dst.
func AppendSvarint(dst []byte, v int64) []byte {
	return AppendVarint(dst, uint64(v<<1)^uint64(v>>63))
}

// ReadSvarint reads a zigzag-encoded int64 from buf at offset.
func ReadSvarint(buf []byte, off int) (int64, int) {
	uv, n := ReadVarint(buf, off)
	if n <= 0 {
		return 0, 0
	}
	return int64(uv>>1) ^ -int64(uv&1), n
}

// --- Fixed64 (float64, little-endian) ---

// AppendFixed64 appends a finite Float as 8 bytes little-endian.
// On failure it returns the unchanged destination and ErrNonFiniteFloat.
func AppendFixed64(dst []byte, v float64) ([]byte, error) {
	if !IsFiniteFloat(v) {
		return dst, ErrNonFiniteFloat
	}
	return appendFixed64(dst, v), nil
}

// appendFixed64 is used only after the caller has checked the finite domain.
func appendFixed64(dst []byte, v float64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
	return append(dst, buf[:]...)
}

// IsFiniteFloat reports whether a value belongs to the protocol Float domain.
// Both signs of zero and all finite subnormal values are preserved.
func IsFiniteFloat(value float64) bool {
	return math.Float64bits(value)&0x7ff0000000000000 != 0x7ff0000000000000
}

// ReadFixed64 reads a finite float64 from 8 bytes at offset.
// A non-finite or truncated value consumes no bytes.
func ReadFixed64(buf []byte, off int) (float64, int) {
	if off+8 > len(buf) {
		return 0, 0
	}
	bits := binary.LittleEndian.Uint64(buf[off:])
	if bits&0x7ff0000000000000 == 0x7ff0000000000000 {
		return 0, 0
	}
	return math.Float64frombits(bits), 8
}

// --- Length-prefixed bytes (String, Bytes) ---

// AppendBytes appends length-prefixed bytes to dst.
func AppendBytes(dst []byte, v []byte) []byte {
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// AppendString appends a UTF-8 string, leaving dst unchanged on failure.
func AppendString(dst []byte, v string) ([]byte, error) {
	if !utf8.ValidString(v) {
		return dst, ErrInvalidUTF8
	}
	return appendString(dst, v), nil
}

// appendString is used only after the caller has validated the complete value.
func appendString(dst []byte, v string) []byte {
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// ReadBytes reads length-prefixed bytes from buf at offset.
// Returns the bytes slice (pointing into buf) and total bytes consumed.
func ReadBytes(buf []byte, off int) ([]byte, int) {
	if off < 0 {
		return nil, 0
	}
	length, n := ReadVarint(buf, off)
	if n <= 0 {
		return nil, 0
	}
	start := off + n
	if length > uint64(len(buf)-start) {
		return nil, 0
	}
	return buf[start : start+int(length)], n + int(length)
}

// ReadString reads strict UTF-8, preserving a leading U+FEFF as field data.
// Malformed text consumes no bytes; opaque binary values use ReadBytes.
func ReadString(buf []byte, off int) (string, int) {
	b, n := readStringBytes(buf, off)
	if n == 0 {
		return "", 0
	}
	return string(b), n
}

func readStringBytes(buf []byte, off int) ([]byte, int) {
	b, n := ReadBytes(buf, off)
	if n == 0 || !utf8.Valid(b) {
		return nil, 0
	}
	return b, n
}

// --- Boolean ---

// AppendBool appends a boolean as varint (0 or 1).
func AppendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

// ReadBool reads a boolean from buf at offset.
func ReadBool(buf []byte, off int) (bool, int) {
	if off < 0 || off >= len(buf) {
		return false, 0
	}
	switch buf[off] {
	case 0:
		return false, 1
	case 1:
		return true, 1
	default:
		return false, 0
	}
}

// --- Array (count + items) ---

// AppendArrayHeader appends the array count as a varint.
func AppendArrayHeader(dst []byte, count int) []byte {
	return AppendVarint(dst, uint64(count))
}

// ReadArrayHeader reads a count within MaxArrayElements before narrowing to int.
// Invalid or oversized headers consume no bytes.
func ReadArrayHeader(buf []byte, off int) (count int, n int) {
	v, n := ReadVarint(buf, off)
	if n <= 0 || v > MaxArrayElements {
		return 0, 0
	}
	return int(v), n
}

// HasElementBytes checks a collection's minimum payload before allocation.
// Division avoids overflowing count*minSize. Variable-width values still need
// their ordinary per-element validation after this lower-bound check.
func HasElementBytes(buf []byte, off, count, minSize int) bool {
	return uint(off) <= uint(len(buf)) && count >= 0 && minSize > 0 && count <= (len(buf)-off)/minSize
}

// --- Nullable flag ---

// AppendNull appends the null marker (0x00).
func AppendNull(dst []byte) []byte {
	return append(dst, 0x00)
}

// AppendPresent appends the present marker (0x01).
func AppendPresent(dst []byte) []byte {
	return append(dst, 0x01)
}

// ReadNullable reads the nullable flag. Returns true if value is present.
func ReadNullable(buf []byte, off int) (present bool, n int) {
	if off >= len(buf) {
		return false, 0
	}
	switch buf[off] {
	case 0:
		return false, 1
	case 1:
		return true, 1
	default:
		return false, 0
	}
}

// UnwrapNullableRoot validates the root presence envelope without copying.
// The caller must still validate the complete non-null value against its schema.
func UnwrapNullableRoot(data []byte) (value []byte, null bool, err error) {
	present, size := ReadNullable(data, 0)
	if size == 0 || (!present && len(data) != 1) || (present && len(data) == 1) {
		return nil, false, ErrInvalidNullableRoot
	}
	return data[1:], !present, nil
}

// ErrInvalidNullableRoot rejects missing flags, invalid flags and malformed nulls.
var ErrInvalidNullableRoot = errors.New("codec: invalid nullable root envelope")

// --- Fixed 16-byte (UUID) ---

// AppendUUID appends a fixed 16-byte UUID without a length prefix.
func AppendUUID(dst []byte, v [16]byte) []byte {
	return append(dst, v[:]...)
}

// ReadUUID reads a fixed 16-byte UUID from buf at offset.
// Returns the value and 16 (bytes consumed), or the zero value and 0 on truncation.
// Bounds check is overflow-safe: a hostile off near MaxInt would wrap `off+16`
// to a negative value and bypass a naive guard, so we compare via `len(buf)-off`.
func ReadUUID(buf []byte, off int) ([16]byte, int) {
	var u [16]byte
	if off < 0 || off > len(buf) || len(buf)-off < 16 {
		return u, 0
	}
	copy(u[:], buf[off:off+16])
	return u, 16
}
