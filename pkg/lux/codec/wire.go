// Package codec implements the Luxo binary wire format.
// Used for event payloads (NATSBus), multi-service RPC, and API transport.
//
// Wire format per field: [fieldID varint] [value]
// Types are known at compile time from schema — no type tags on wire.
//
// Encoding rules:
//   - Int/Boolean/Enum: varint (LEB128)
//   - Float: fixed 8 bytes (little-endian float64)
//   - String/Bytes: length-prefixed (varint length + raw bytes)
//   - Nullable: 1-byte flag (0x00=null, 0x01=present) + value if present
//   - Message end: 0x00 terminator (fieldID 0 is reserved)
package codec

import (
	"encoding/binary"
	"math"
)

// Safety limits to prevent OOM from malicious/corrupted wire data.
const (
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
// Returns (0, 0) if buf is too short or varint overflows 64 bits.
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
		if shift >= 64 {
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

// AppendFixed64 appends a float64 as 8 bytes little-endian.
func AppendFixed64(dst []byte, v float64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
	return append(dst, buf[:]...)
}

// ReadFixed64 reads a float64 from 8 bytes at offset.
func ReadFixed64(buf []byte, off int) (float64, int) {
	if off+8 > len(buf) {
		return 0, 0
	}
	bits := binary.LittleEndian.Uint64(buf[off:])
	return math.Float64frombits(bits), 8
}

// --- Length-prefixed bytes (String, Bytes) ---

// AppendBytes appends length-prefixed bytes to dst.
func AppendBytes(dst []byte, v []byte) []byte {
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// AppendString appends a length-prefixed string to dst.
func AppendString(dst []byte, v string) []byte {
	dst = AppendVarint(dst, uint64(len(v)))
	return append(dst, v...)
}

// ReadBytes reads length-prefixed bytes from buf at offset.
// Returns the bytes slice (pointing into buf) and total bytes consumed.
func ReadBytes(buf []byte, off int) ([]byte, int) {
	length, n := ReadVarint(buf, off)
	if n <= 0 {
		return nil, 0
	}
	if length > uint64(len(buf)) {
		return nil, 0 // length exceeds buffer, prevent overflow
	}
	start := off + n
	end := start + int(length)
	if end > len(buf) {
		return nil, 0
	}
	return buf[start:end], n + int(length)
}

// ReadString reads a length-prefixed string from buf at offset.
func ReadString(buf []byte, off int) (string, int) {
	b, n := ReadBytes(buf, off)
	if n == 0 {
		return "", 0
	}
	return string(b), n
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
	v, n := ReadVarint(buf, off)
	if n <= 0 {
		return false, 0
	}
	return v != 0, n
}

// --- Array (count + items) ---

// AppendArrayHeader appends the array count as a varint.
func AppendArrayHeader(dst []byte, count int) []byte {
	return AppendVarint(dst, uint64(count))
}

// ReadArrayHeader reads the array count from buf at offset.
func ReadArrayHeader(buf []byte, off int) (count int, n int) {
	v, n := ReadVarint(buf, off)
	if n <= 0 {
		return 0, 0
	}
	return int(v), n
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
	return buf[off] != 0, 1
}

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
