package codec

import (
	"fmt"
	"unsafe"
)

// Decoder reads fields from a binary message.
// Generated code uses NextField() to iterate and typed Read methods to extract values.
//
// Usage (generated code):
//
//	dec := codec.NewDecoder(data)
//	for dec.NextField() {
//	    switch dec.FieldID() {
//	    case 1: event.TaskId = dec.ReadInt()
//	    case 2: event.Name = dec.ReadString()
//	    }
//	}
type Decoder struct {
	buf     []byte
	off     int
	fieldID int
	err     error
}

// NewDecoder creates a decoder for the given binary data.
func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

// NextField advances to the next field. Returns false at end or on error.
func (d *Decoder) NextField() bool {
	if d.err != nil || d.off >= len(d.buf) {
		return false
	}
	id, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		if n < 0 {
			d.err = fmt.Errorf("codec: varint overflow at offset %d", d.off)
		} else {
			d.err = fmt.Errorf("codec: truncated varint at offset %d", d.off)
		}
		return false
	}
	d.off += n
	d.fieldID = int(id)
	return d.fieldID != 0 // 0 = end marker
}

// FieldID returns the current field ID.
func (d *Decoder) FieldID() int {
	return d.fieldID
}

// Err returns any decoding error.
func (d *Decoder) Err() error {
	return d.err
}

// Offset returns the current read position.
func (d *Decoder) Offset() int {
	return d.off
}

// --- Typed readers ---

// ReadInt reads a signed int64 value.
func (d *Decoder) ReadInt() int64 {
	v, n := ReadSvarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid svarint at offset %d", d.off)
		return 0
	}
	d.off += n
	return v
}

// ReadFloat reads a float64 value.
func (d *Decoder) ReadFloat() float64 {
	v, n := ReadFixed64(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid fixed64 at offset %d", d.off)
		return 0
	}
	d.off += n
	return v
}

// ReadString reads a length-prefixed string.
func (d *Decoder) ReadString() string {
	v, n := ReadString(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid string at offset %d", d.off)
		return ""
	}
	d.off += n
	return v
}

// ReadBool reads a boolean value.
func (d *Decoder) ReadBool() bool {
	v, n := ReadBool(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid bool at offset %d", d.off)
		return false
	}
	d.off += n
	return v
}

// ReadBytes reads raw bytes.
func (d *Decoder) ReadBytes() []byte {
	v, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid bytes at offset %d", d.off)
		return nil
	}
	d.off += n
	return v
}

// --- Nullable readers ---

// ReadIntPtr reads a nullable int64. Returns nil for null.
func (d *Decoder) ReadIntPtr() *int64 {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.ReadInt()
	return &v
}

// ReadFloatPtr reads a nullable float64. Returns nil for null.
func (d *Decoder) ReadFloatPtr() *float64 {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.ReadFloat()
	return &v
}

// ReadStringPtr reads a nullable string. Returns nil for null.
func (d *Decoder) ReadStringPtr() *string {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.ReadString()
	return &v
}

// ReadBoolPtr reads a nullable boolean. Returns nil for null.
func (d *Decoder) ReadBoolPtr() *bool {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.ReadBool()
	return &v
}

// ReadBytesPtr reads nullable bytes. Returns nil for null.
func (d *Decoder) ReadBytesPtr() []byte {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	return d.ReadBytes()
}

// --- Array readers ---

// ReadIntArray reads a count-prefixed int64 array.
func (d *Decoder) ReadIntArray() []int64 {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadInt())
		if d.err != nil {
			return nil
		}
	}
	return result
}

// ReadStringArray reads a count-prefixed string array.
func (d *Decoder) ReadStringArray() []string {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadString())
		if d.err != nil {
			return nil
		}
	}
	return result
}

// ReadFloatArray reads a count-prefixed float64 array.
func (d *Decoder) ReadFloatArray() []float64 {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadFloat())
		if d.err != nil {
			return nil
		}
	}
	return result
}

// ReadBoolArray reads a count-prefixed bool array.
func (d *Decoder) ReadBoolArray() []bool {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([]bool, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadBool())
		if d.err != nil {
			return nil
		}
	}
	return result
}

// ReadBytesArray reads a count-prefixed [][]byte array (inverse of
// Encoder.WriteFieldBytesArray). Each element is a length-prefixed byte blob.
func (d *Decoder) ReadBytesArray() [][]byte {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadBytes())
		if d.err != nil {
			return nil
		}
	}
	return result
}

// ReadUUID reads a fixed 16-byte UUID value.
func (d *Decoder) ReadUUID() [16]byte {
	u, n := ReadUUID(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: truncated uuid at offset %d", d.off)
		return u
	}
	d.off += n
	return u
}

// ReadUUIDPtr reads a nullable UUID. Returns nil if the value is null.
func (d *Decoder) ReadUUIDPtr() *[16]byte {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: truncated nullable uuid at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	u, un := ReadUUID(d.buf, d.off)
	if un == 0 {
		d.err = fmt.Errorf("codec: truncated uuid value at offset %d", d.off)
		return nil
	}
	d.off += un
	return &u
}

// ReadUUIDArray reads a count-prefixed UUID array (fixed 16-byte items).
func (d *Decoder) ReadUUIDArray() [][16]byte {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return nil
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return nil
	}
	d.off += n
	result := make([][16]byte, 0, count)
	for i := 0; i < count; i++ {
		u, un := ReadUUID(d.buf, d.off)
		if un == 0 {
			d.err = fmt.Errorf("codec: truncated uuid at array index %d", i)
			return nil
		}
		d.off += un
		result = append(result, u)
	}
	return result
}

// --- Arena readers ---
// Arena methods reduce allocations by copying all string data into a single
// pre-allocated []byte (the arena). Strings returned by these methods are
// backed by unsafe.String pointing into the arena. The arena is immutable
// after decoding — generated code guarantees no writes after construction.

// MaxArenaSize caps arena allocation to prevent OOM from malicious wire data.
// 64MB — larger than any practical single model's total string content.
const MaxArenaSize = 64 * 1024 * 1024

// ReadArenaSize reads the totalStringLen varint that prefixes a model's binary
// encoding. The caller allocates make([]byte, size) and passes it to
// ReadStringArena / ReadStringArenaPtr.
// Returns 0 (with error) if the size exceeds MaxArenaSize.
func (d *Decoder) ReadArenaSize() int {
	v, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid arena size varint at offset %d", d.off)
		return 0
	}
	d.off += n
	if v > uint64(MaxArenaSize) {
		d.err = fmt.Errorf("codec: arena size %d exceeds limit %d at offset %d", v, MaxArenaSize, d.off-n)
		return 0
	}
	return int(v)
}

// SkipArenaHeader skips the totalStringLen varint prefix.
// Used by code paths that don't need arena allocation (e.g. Binary→JSON conversion).
func (d *Decoder) SkipArenaHeader() {
	_, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid arena header varint at offset %d", d.off)
		return
	}
	d.off += n
}

// ReadStringArena reads a length-prefixed string, copies the bytes into arena
// at *arenaOff, and returns an unsafe.String pointing into the arena.
// Advances *arenaOff by the string length.
// Falls back to a regular string allocation if the arena is too small
// (defensive: should not happen with correct totalStringLen).
func (d *Decoder) ReadStringArena(arena []byte, arenaOff *int) string {
	b, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid string at offset %d", d.off)
		return ""
	}
	d.off += n
	if len(b) == 0 {
		return ""
	}
	start := *arenaOff
	end := start + len(b)
	if end > len(arena) {
		// Arena too small — fall back to regular allocation (no panic)
		return string(b)
	}
	copy(arena[start:end], b)
	*arenaOff = end
	return unsafe.String(&arena[start], len(b))
}

// ReadStringArenaPtr reads a nullable string using arena allocation.
// Returns nil for null values. Non-null strings are arena-backed.
func (d *Decoder) ReadStringArenaPtr(arena []byte, arenaOff *int) *string {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.ReadStringArena(arena, arenaOff)
	return &v
}

// FieldSkipType identifies how to skip a field value in the binary stream.
// Used by merger/planner to advance past fields without full decoding.
type FieldSkipType int

const (
	SkipVarint      FieldSkipType = iota // Int, DateTime, Duration, Enum, Bool (varint)
	SkipFixed64                          // Float (8 bytes)
	SkipBytes                            // String, Bytes, JSON, UUID, Decimal (length-prefixed)
	SkipNullVarint                       // Nullable varint (1 byte flag + optional varint)
	SkipNullFixed64                      // Nullable float (1 byte flag + optional 8 bytes)
	SkipNullBytes                        // Nullable string/bytes (1 byte flag + optional length-prefixed)
)

// --- Zero-allocation skip methods ---
// Used by federation's ExtractID to advance past fields without allocating.

// SkipVarint advances past a varint value without allocating.
func (d *Decoder) SkipVarint() {
	_, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid varint at offset %d", d.off)
		return
	}
	d.off += n
}

// SkipFixed64 advances past an 8-byte fixed64 value.
func (d *Decoder) SkipFixed64() {
	if d.off+8 > len(d.buf) {
		d.err = fmt.Errorf("codec: truncated fixed64 at offset %d", d.off)
		return
	}
	d.off += 8
}

// SkipLenPrefixed advances past a length-prefixed value (string/bytes) without allocating.
func (d *Decoder) SkipLenPrefixed() {
	_, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid length-prefixed at offset %d", d.off)
		return
	}
	d.off += n
}

// SkipNullableVarint advances past a nullable varint (flag + optional value).
func (d *Decoder) SkipNullableVarint() {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return
	}
	d.off += n
	if present {
		d.SkipVarint()
	}
}

// SkipNullableFixed64 advances past a nullable fixed64.
func (d *Decoder) SkipNullableFixed64() {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return
	}
	d.off += n
	if present {
		d.SkipFixed64()
	}
}

// SkipNullableLenPrefixed advances past a nullable length-prefixed value.
func (d *Decoder) SkipNullableLenPrefixed() {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return
	}
	d.off += n
	if present {
		d.SkipLenPrefixed()
	}
}

// SkipField skips the current field's value. Used for unknown fields (forward compat).
// Requires knowing the wire type, which in Luxo is always known from schema.
// For forward compat with unknown fields, caller should provide expected types.
// For now, unknown fields cause an error — schema mismatch.
func (d *Decoder) SkipField() {
	d.err = fmt.Errorf("codec: cannot skip unknown field %d at offset %d (schema mismatch)", d.fieldID, d.off)
}
