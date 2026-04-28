package codec

import "fmt"

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

// --- Array readers ---

// ReadIntArray reads a count-prefixed int64 array.
func (d *Decoder) ReadIntArray() []int64 {
	count, n := ReadArrayHeader(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
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

// SkipField skips the current field's value. Used for unknown fields (forward compat).
// Requires knowing the wire type, which in Luxo is always known from schema.
// For forward compat with unknown fields, caller should provide expected types.
// For now, unknown fields cause an error — schema mismatch.
func (d *Decoder) SkipField() {
	d.err = fmt.Errorf("codec: cannot skip unknown field %d at offset %d (schema mismatch)", d.fieldID, d.off)
}
