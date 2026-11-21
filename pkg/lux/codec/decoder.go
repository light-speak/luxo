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
	if d.err != nil {
		return false
	}
	if d.off >= len(d.buf) {
		d.err = fmt.Errorf("codec: missing end marker at offset %d", d.off)
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
	if id > uint64(^uint(0)>>1) {
		d.err = fmt.Errorf("codec: field ID %d exceeds host index range at offset %d", id, d.off)
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

// Fail records a semantic decoding error and stops further reads.
// The first error is preserved so callers see the original failure.
func (d *Decoder) Fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// Offset returns the current read position.
func (d *Decoder) Offset() int {
	return d.off
}

// Finish validates an independent message or value boundary. Inline fields must
// not call it: their following bytes still belong to the enclosing message.
func (d *Decoder) Finish() error {
	if d.err == nil && d.off != len(d.buf) {
		d.err = fmt.Errorf("codec: trailing data at offset %d", d.off)
	}
	return d.err
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
	if d.err != nil {
		return nil
	}
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

// ReadBytesValuePtr reads nullable bytes while preserving the distinction
// between null and an empty byte slice for generated pointer fields.
func (d *Decoder) ReadBytesValuePtr() *[]byte {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	value := d.ReadBytes()
	if d.err != nil {
		return nil
	}
	return &value
}

// --- Array readers ---

// ReadArrayLength reads and validates the canonical unsigned array header.
func (d *Decoder) ReadArrayLength() int {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header (limit %d) at offset %d", MaxArrayElements, d.off)
		return 0
	}
	d.off += n
	return count
}

// CheckElementBytes validates the minimum payload before allocating results.
// Generated inline readers use it after ReadArrayLength; it preserves errors
// and does not consume data or replace per-element validation.
func (d *Decoder) CheckElementBytes(count, minSize int) bool {
	if d.err == nil && !HasElementBytes(d.buf, d.off, count, minSize) {
		d.err = ErrInsufficientElementBytes
	}
	return d.err == nil
}

// ReadIntArray reads a count-prefixed int64 array.
func (d *Decoder) ReadIntArray() []int64 {
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 1) {
		return nil
	}
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
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 1) {
		return nil
	}
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
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 8) {
		return nil
	}
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
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 1) {
		return nil
	}
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
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 1) {
		return nil
	}
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
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 16) {
		return nil
	}
	result := make([][16]byte, 0, count)
	for i := 0; i < count; i++ {
		var u [16]byte
		copy(u[:], d.buf[d.off:d.off+16])
		d.off += 16
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
// Returns 0 with an error if the size exceeds MaxArenaSize or the remaining payload.
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
	if v > uint64(len(d.buf)-d.off) {
		d.err = ErrInsufficientArenaBytes
		return 0
	}
	return int(v)
}

// SkipArenaHeader skips the totalStringLen varint prefix.
// Used by code paths that don't need arena allocation (e.g. Binary→JSON conversion).
func (d *Decoder) SkipArenaHeader() {
	d.ReadArenaSize()
}

// ReadStringArena reads a length-prefixed string, copies the bytes into arena
// at *arenaOff, and returns an unsafe.String pointing into the arena.
// Advances *arenaOff by the string length.
// Falls back to a regular string allocation if the arena is too small
// (defensive: should not happen with correct totalStringLen).
func (d *Decoder) ReadStringArena(arena []byte, arenaOff *int) string {
	b, n := readStringBytes(d.buf, d.off)
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
	if d.err != nil {
		return nil
	}
	return &v
}

// FieldSkipType identifies how to skip a field value in the binary stream.
// Used by merger/planner to advance past fields without full decoding.
type FieldSkipType int

const (
	SkipVarint       FieldSkipType = iota // Int, Duration, Bool
	SkipFixed64                           // Float (8 bytes)
	SkipBytes                             // Opaque length-prefixed bytes
	SkipNullVarint                        // Nullable varint (1 byte flag + optional varint)
	SkipNullFixed64                       // Nullable float (1 byte flag + optional 8 bytes)
	SkipNullBytes                         // Nullable opaque bytes (1 byte flag + optional length-prefixed)
	SkipFixed16                           // UUID (16 bytes)
	SkipNullFixed16                       // Nullable UUID (1 byte flag + optional 16 bytes)
	SkipString                            // Strict UTF-8 length-prefixed text
	SkipNullString                        // Nullable strict UTF-8 text
	SkipDateTime                          // Signed seconds + unsigned nanoseconds
	SkipNullDateTime                      // Nullable seconds/nanoseconds pair
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
	if _, n := ReadFixed64(d.buf, d.off); n == 0 {
		d.err = fmt.Errorf("codec: invalid fixed64 at offset %d", d.off)
		return
	}
	d.off += 8
}

// SkipFixed16 advances past a 16-byte fixed value.
func (d *Decoder) SkipFixed16() {
	if d.off < 0 || d.off > len(d.buf) || len(d.buf)-d.off < 16 {
		d.err = fmt.Errorf("codec: truncated fixed16 at offset %d", d.off)
		return
	}
	d.off += 16
}

// SkipLenPrefixed advances past opaque bytes without allocating. Text uses SkipString.
func (d *Decoder) SkipLenPrefixed() {
	_, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid length-prefixed at offset %d", d.off)
		return
	}
	d.off += n
}

// SkipString validates UTF-8 without materializing a Go string.
func (d *Decoder) SkipString() {
	_, n := readStringBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid string at offset %d", d.off)
		return
	}
	d.off += n
}

// SkipNullableString preserves null and validates only present text.
func (d *Decoder) SkipNullableString() {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return
	}
	d.off += n
	if present {
		d.SkipString()
	}
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

// SkipNullableFixed16 advances past a nullable 16-byte fixed value.
func (d *Decoder) SkipNullableFixed16() {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return
	}
	d.off += n
	if present {
		d.SkipFixed16()
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

// SkipValue advances past one value of the supplied wire type.
func (d *Decoder) SkipValue(fieldType FieldSkipType) {
	switch fieldType {
	case SkipVarint:
		d.SkipVarint()
	case SkipFixed64:
		d.SkipFixed64()
	case SkipBytes:
		d.SkipLenPrefixed()
	case SkipNullVarint:
		d.SkipNullableVarint()
	case SkipNullFixed64:
		d.SkipNullableFixed64()
	case SkipNullBytes:
		d.SkipNullableLenPrefixed()
	case SkipFixed16:
		d.SkipFixed16()
	case SkipNullFixed16:
		d.SkipNullableFixed16()
	case SkipString:
		d.SkipString()
	case SkipNullString:
		d.SkipNullableString()
	case SkipDateTime:
		d.SkipDateTime()
	case SkipNullDateTime:
		d.SkipNullableDateTime()
	default:
		d.err = fmt.Errorf("codec: unknown skip type %d", fieldType)
	}
}

// SkipArray advances past a count-prefixed sequence without allocating.
func (d *Decoder) SkipArray(elementType FieldSkipType) {
	count := d.ReadArrayLength()
	for range count {
		d.SkipValue(elementType)
		if d.err != nil {
			return
		}
	}
}

// SkipField skips the current field's value. Used for unknown fields (forward compat).
// Requires knowing the wire type, which in Luxo is always known from schema.
// For forward compat with unknown fields, caller should provide expected types.
// For now, unknown fields cause an error — schema mismatch.
func (d *Decoder) SkipField() {
	d.err = fmt.Errorf("codec: cannot skip unknown field %d at offset %d (schema mismatch)", d.fieldID, d.off)
}
