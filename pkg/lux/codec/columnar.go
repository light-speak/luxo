// Package codec — columnar encoding for Luxo binary protocol.
//
// Columnar format (used for both single records and lists):
//
//	[record count varint]
//	[fieldID varint] [value0] [value1] ... [valueN]   ← column 1
//	[fieldID varint] [value0] [value1] ... [valueN]   ← column 2
//	...
//	[0x00]                                             ← end marker
//
// Nullable columns: each value prefixed with 0x00 (null) or 0x01 (present) + value.
//
// Single record = count 1, same format. Zero overhead vs row encoding.
// List of N records: fieldID written once per column (not per row), saves (N-1)*F bytes.
package codec

import (
	"fmt"
	"unsafe"
)

// ColumnarWriter writes records in columnar format.
// Collects all records first, then writes column-by-column.
type ColumnarWriter struct {
	buf            []byte
	count          int
	columns        []column
	totalStringLen int // tracks total string bytes for arena allocation on decode
}

type column struct {
	fieldID int
	data    []byte // pre-encoded values for all records
}

// Reset clears the writer for reuse.
func (w *ColumnarWriter) Reset() {
	w.buf = w.buf[:0]
	w.count = 0
	w.columns = w.columns[:0]
	w.totalStringLen = 0
}

// SetCount sets the number of records. Must be called before writing columns.
func (w *ColumnarWriter) SetCount(n int) {
	w.count = n
}

// WriteColumnInt writes a full int64 column for all records.
func (w *ColumnarWriter) WriteColumnInt(fieldID int, values []int64) {
	var data []byte
	for _, v := range values {
		data = AppendSvarint(data, v)
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnFloat writes a full float64 column.
func (w *ColumnarWriter) WriteColumnFloat(fieldID int, values []float64) {
	var data []byte
	for _, v := range values {
		data = AppendFixed64(data, v)
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnString writes a full string column.
func (w *ColumnarWriter) WriteColumnString(fieldID int, values []string) {
	var data []byte
	for _, v := range values {
		w.totalStringLen += len(v)
		data = AppendString(data, v)
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnBool writes a full boolean column.
func (w *ColumnarWriter) WriteColumnBool(fieldID int, values []bool) {
	var data []byte
	for _, v := range values {
		data = AppendBool(data, v)
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnIntPtr writes a nullable int64 column.
func (w *ColumnarWriter) WriteColumnIntPtr(fieldID int, values []*int64) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendSvarint(data, *v)
		}
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnFloatPtr writes a nullable float64 column.
func (w *ColumnarWriter) WriteColumnFloatPtr(fieldID int, values []*float64) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendFixed64(data, *v)
		}
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnStringPtr writes a nullable string column.
func (w *ColumnarWriter) WriteColumnStringPtr(fieldID int, values []*string) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			w.totalStringLen += len(*v)
			data = AppendPresent(data)
			data = AppendString(data, *v)
		}
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnBoolPtr writes a nullable boolean column.
func (w *ColumnarWriter) WriteColumnBoolPtr(fieldID int, values []*bool) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendBool(data, *v)
		}
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// Bytes returns the complete columnar-encoded message.
// Format: [count varint][totalStringLen varint][col1...][col2...][0x00]
func (w *ColumnarWriter) Bytes() []byte {
	w.buf = w.buf[:0]
	// Record count
	w.buf = AppendVarint(w.buf, uint64(w.count))
	// Arena size: total string bytes for arena allocation on decode
	w.buf = AppendVarint(w.buf, uint64(w.totalStringLen))
	// Columns: [fieldID varint] [column data]
	for _, col := range w.columns {
		w.buf = AppendVarint(w.buf, uint64(col.fieldID))
		w.buf = append(w.buf, col.data...)
	}
	// End marker
	w.buf = append(w.buf, 0x00)
	return w.buf
}

// TotalStringLen returns the tracked total string byte length.
func (w *ColumnarWriter) TotalStringLen() int {
	return w.totalStringLen
}

// ColumnarReader reads records from columnar format.
type ColumnarReader struct {
	buf       []byte
	off       int
	count     int
	fieldID   int
	arenaSize int // total string bytes for arena allocation
	err       error
}

// NewColumnarReader creates a reader for columnar-encoded data.
// Reads both the record count and totalStringLen prefix.
func NewColumnarReader(buf []byte) *ColumnarReader {
	r := &ColumnarReader{buf: buf}
	// Read record count
	count, n := ReadVarint(buf, 0)
	if n <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar record count")
		return r
	}
	if count > MaxColumnarRecords {
		r.err = fmt.Errorf("codec: columnar count %d exceeds limit %d", count, MaxColumnarRecords)
		return r
	}
	r.count = int(count)
	r.off = n

	// Read totalStringLen (arena size)
	arenaSize, an := ReadVarint(buf, r.off)
	if an <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar arena size")
		return r
	}
	r.off += an
	if arenaSize > uint64(MaxArenaSize) {
		r.err = fmt.Errorf("codec: columnar arena size %d exceeds limit %d", arenaSize, MaxArenaSize)
		return r
	}
	r.arenaSize = int(arenaSize)
	return r
}

// Count returns the number of records.
func (r *ColumnarReader) Count() int {
	return r.count
}

// ArenaSize returns the total string byte count (arena allocation size).
func (r *ColumnarReader) ArenaSize() int {
	return r.arenaSize
}

// NextColumn advances to the next column. Returns false at end.
func (r *ColumnarReader) NextColumn() bool {
	if r.err != nil || r.off >= len(r.buf) {
		return false
	}
	id, n := ReadVarint(r.buf, r.off)
	if n <= 0 {
		return false
	}
	if id == 0 {
		r.off += n // advance past 0x00 end marker
		return false
	}
	r.off += n
	r.fieldID = int(id)
	return true
}

// FieldID returns the current column's field ID.
func (r *ColumnarReader) FieldID() int {
	return r.fieldID
}

// Err returns any error.
func (r *ColumnarReader) Err() error {
	return r.err
}

// Offset returns the current read position (after all columns + end marker).
func (r *ColumnarReader) Offset() int {
	return r.off
}

// ReadColumnInt reads count int64 values from the current column.
func (r *ColumnarReader) ReadColumnInt() []int64 {
	result := make([]int64, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

// ReadColumnFloat reads count float64 values.
func (r *ColumnarReader) ReadColumnFloat() []float64 {
	result := make([]float64, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadFixed64(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated float column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

// ReadColumnString reads count string values.
func (r *ColumnarReader) ReadColumnString() []string {
	result := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadString(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

// ReadColumnBool reads count boolean values.
func (r *ColumnarReader) ReadColumnBool() []bool {
	result := make([]bool, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadBool(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bool column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

// ReadColumnIntPtr reads count nullable int64 values.
func (r *ColumnarReader) ReadColumnIntPtr() []*int64 {
	result := make([]*int64, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

// ReadColumnStringPtr reads count nullable string values.
func (r *ColumnarReader) ReadColumnStringPtr() []*string {
	result := make([]*string, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadString(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

// ReadColumnFloatPtr reads count nullable float64 values.
func (r *ColumnarReader) ReadColumnFloatPtr() []*float64 {
	result := make([]*float64, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadFixed64(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated float value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

// ReadColumnBoolPtr reads count nullable bool values.
func (r *ColumnarReader) ReadColumnBoolPtr() []*bool {
	result := make([]*bool, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadBool(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bool value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

// SkipColumnInt skips count int64 values without allocating.
func (r *ColumnarReader) SkipColumnInt() {
	for i := 0; i < r.count; i++ {
		_, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int column at record %d", i)
			return
		}
		r.off += n
	}
}

// SkipColumnFloat skips count float64 values without allocating.
func (r *ColumnarReader) SkipColumnFloat() {
	for i := 0; i < r.count; i++ {
		if r.off+8 > len(r.buf) {
			r.err = fmt.Errorf("codec: truncated float column at record %d", i)
			return
		}
		r.off += 8
	}
}

// SkipColumnString skips count length-prefixed strings without allocating.
func (r *ColumnarReader) SkipColumnString() {
	for i := 0; i < r.count; i++ {
		_, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return
		}
		r.off += n
	}
}

// SkipColumnBool skips count boolean values without allocating.
func (r *ColumnarReader) SkipColumnBool() {
	for i := 0; i < r.count; i++ {
		_, n := ReadBool(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bool column at record %d", i)
			return
		}
		r.off += n
	}
}

// SkipColumnIntPtr skips count nullable int64 values without allocating.
func (r *ColumnarReader) SkipColumnIntPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			_, sn := ReadSvarint(r.buf, r.off)
			if sn <= 0 {
				r.err = fmt.Errorf("codec: truncated int value at record %d", i)
				return
			}
			r.off += sn
		}
	}
}

// SkipColumnFloatPtr skips count nullable float64 values without allocating.
func (r *ColumnarReader) SkipColumnFloatPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			if r.off+8 > len(r.buf) {
				r.err = fmt.Errorf("codec: truncated float value at record %d", i)
				return
			}
			r.off += 8
		}
	}
}

// SkipColumnStringPtr skips count nullable string values without allocating.
func (r *ColumnarReader) SkipColumnStringPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			_, sn := ReadBytes(r.buf, r.off)
			if sn == 0 {
				r.err = fmt.Errorf("codec: truncated string value at record %d", i)
				return
			}
			r.off += sn
		}
	}
}

// SkipColumnBoolPtr skips count nullable bool values without allocating.
func (r *ColumnarReader) SkipColumnBoolPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			_, bn := ReadBool(r.buf, r.off)
			if bn == 0 {
				r.err = fmt.Errorf("codec: truncated bool value at record %d", i)
				return
			}
			r.off += bn
		}
	}
}

// SkipColumnBytes skips count length-prefixed byte blobs without allocating.
func (r *ColumnarReader) SkipColumnBytes() {
	for i := 0; i < r.count; i++ {
		_, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bytes column at record %d", i)
			return
		}
		r.off += n
	}
}

// ReadColumnBytes reads count length-prefixed byte blobs from the current column.
// Used for federated extend fields: each blob contains nested model/list binary data.
func (r *ColumnarReader) ReadColumnBytes() [][]byte {
	result := make([][]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bytes column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

// --- Arena-based column readers ---
// These methods reduce allocations by copying all string data into a single
// pre-allocated []byte (the arena). Strings are backed by unsafe.String.

// ReadColumnStringArena reads count string values into the shared arena.
// Falls back to regular allocation if arena space is insufficient.
func (r *ColumnarReader) ReadColumnStringArena(arena []byte, arenaOff *int) []string {
	result := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		b, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return nil
		}
		r.off += n
		if len(b) == 0 {
			result = append(result, "")
			continue
		}
		start := *arenaOff
		end := start + len(b)
		if end > len(arena) {
			// Arena too small — fall back to regular allocation (no panic)
			result = append(result, string(b))
			continue
		}
		copy(arena[start:end], b)
		*arenaOff = end
		result = append(result, unsafe.String(&arena[start], len(b)))
	}
	return result
}

// ReadColumnStringPtrArena reads count nullable string values into the shared arena.
// Returns nil for null values. Non-null strings are arena-backed.
func (r *ColumnarReader) ReadColumnStringPtrArena(arena []byte, arenaOff *int) []*string {
	result := make([]*string, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		b, bn := ReadBytes(r.buf, r.off)
		if bn == 0 {
			r.err = fmt.Errorf("codec: truncated string value at record %d", i)
			return nil
		}
		r.off += bn
		if len(b) == 0 {
			s := ""
			result = append(result, &s)
			continue
		}
		start := *arenaOff
		end := start + len(b)
		if end > len(arena) {
			s := string(b)
			result = append(result, &s)
			continue
		}
		copy(arena[start:end], b)
		*arenaOff = end
		s := unsafe.String(&arena[start], len(b))
		result = append(result, &s)
	}
	return result
}
