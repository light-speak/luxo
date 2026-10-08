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
	"unicode/utf8"
	"unsafe"
)

// ColumnarWriter writes records in columnar format.
// Collects all records first, then writes column-by-column.
type ColumnarWriter struct {
	buf            []byte
	count          int
	columns        []column
	totalStringLen int // tracks total string bytes for arena allocation on decode
	columnRows     int // shared cardinality, or a negative invalid-column state
}

const (
	invalidColumnField     = -1
	inconsistentColumnRows = -2
)

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
	w.columnRows = 0
}

// SetCount sets the number of records. Must be called before writing columns.
func (w *ColumnarWriter) SetCount(n int) {
	w.count = n
}

// Record cardinality while values are available; finalization never decodes columns.
func (w *ColumnarWriter) appendColumn(fieldID, rows int, data []byte) {
	if fieldID <= 0 {
		w.columnRows = invalidColumnField
	} else if len(w.columns) == 0 {
		w.columnRows = rows
	} else if w.columnRows >= 0 && rows != w.columnRows {
		w.columnRows = inconsistentColumnRows
	}
	w.columns = append(w.columns, column{fieldID: fieldID, data: data})
}

// WriteColumnInt writes a full int64 column for all records.
func (w *ColumnarWriter) WriteColumnInt(fieldID int, values []int64) {
	// One byte per value is the lower bound; avoid tiny growth for wide varints.
	var data []byte
	if len(values) > 0 {
		data = make([]byte, 0, (len(values)+7)&^7)
	}
	for _, v := range values {
		data = AppendSvarint(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnFloat writes a full float64 column.
func (w *ColumnarWriter) WriteColumnFloat(fieldID int, values []float64) error {
	if len(values) == 0 {
		w.appendColumn(fieldID, 0, nil)
		return nil
	}
	for _, v := range values {
		if !IsFiniteFloat(v) {
			return ErrNonFiniteFloat
		}
	}
	// Validate before reserving the payload, including invalid values near the end.
	data := make([]byte, 0, len(values)*8)
	for _, v := range values {
		data = appendFixed64(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
	return nil
}

// WriteColumnString writes a full string column.
func (w *ColumnarWriter) WriteColumnString(fieldID int, values []string) error {
	var data []byte
	totalStringLen := 0
	for _, v := range values {
		if !utf8.ValidString(v) {
			return ErrInvalidUTF8
		}
		totalStringLen += len(v)
		data = appendString(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
	w.totalStringLen += totalStringLen
	return nil
}

// WriteColumnBool writes a full boolean column.
func (w *ColumnarWriter) WriteColumnBool(fieldID int, values []bool) {
	var data []byte
	if len(values) > 0 {
		data = make([]byte, 0, len(values))
	}
	for _, v := range values {
		data = AppendBool(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
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
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnFloatPtr writes a nullable float64 column.
func (w *ColumnarWriter) WriteColumnFloatPtr(fieldID int, values []*float64) error {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			if !IsFiniteFloat(*v) {
				return ErrNonFiniteFloat
			}
			data = AppendPresent(data)
			data = appendFixed64(data, *v)
		}
	}
	w.appendColumn(fieldID, len(values), data)
	return nil
}

// WriteColumnStringPtr writes a nullable string column.
func (w *ColumnarWriter) WriteColumnStringPtr(fieldID int, values []*string) error {
	var data []byte
	totalStringLen := 0
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			if !utf8.ValidString(*v) {
				return ErrInvalidUTF8
			}
			totalStringLen += len(*v)
			data = AppendPresent(data)
			data = appendString(data, *v)
		}
	}
	w.appendColumn(fieldID, len(values), data)
	w.totalStringLen += totalStringLen
	return nil
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
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnBytes writes a full []byte column (length-prefixed blobs).
// Also serves scalar array-field columns: each cell is a length-prefixed inline
// array encoding ([count][items...]).
func (w *ColumnarWriter) WriteColumnBytes(fieldID int, values [][]byte) {
	var data []byte
	for _, v := range values {
		data = AppendBytes(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnBytesPtr writes a nullable []byte column.
func (w *ColumnarWriter) WriteColumnBytesPtr(fieldID int, values []*[]byte) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendBytes(data, *v)
		}
	}
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnBytesNullable writes nil slices as null and non-nil slices as
// present blobs, including empty blobs. Array cells always contain a count byte,
// so their nil slice can represent a null array without a second pointer slice.
// WriteColumnBytesPtr remains available when pointer presence is independent of
// the slice value (a pointer to a nil slice represents a present empty blob).
func (w *ColumnarWriter) WriteColumnBytesNullable(fieldID int, values [][]byte) {
	var data []byte
	for _, value := range values {
		if value == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendBytes(data, value)
		}
	}
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnUUID writes a full UUID column (fixed 16-byte values).
func (w *ColumnarWriter) WriteColumnUUID(fieldID int, values [][16]byte) {
	var data []byte
	if len(values) > 0 {
		data = make([]byte, 0, len(values)*16)
	}
	for _, v := range values {
		data = AppendUUID(data, v)
	}
	w.appendColumn(fieldID, len(values), data)
}

// WriteColumnUUIDPtr writes a nullable UUID column.
func (w *ColumnarWriter) WriteColumnUUIDPtr(fieldID int, values []*[16]byte) {
	var data []byte
	for _, v := range values {
		if v == nil {
			data = AppendNull(data)
		} else {
			data = AppendPresent(data)
			data = AppendUUID(data, *v)
		}
	}
	w.appendColumn(fieldID, len(values), data)
}

// Bytes returns the complete columnar-encoded message, or nil and an error.
// Invalid counts, columns or arena metadata never publish a partial message.
// Reset is required after writing inconsistent columns or invalid field IDs.
// Format: [count varint][totalStringLen varint][col1...][col2...][0x00]
func (w *ColumnarWriter) Bytes() ([]byte, error) {
	if err := w.validate(); err != nil {
		return nil, err
	}
	w.buf = w.buf[:0]
	// Record count
	w.buf = AppendVarint(w.buf, uint64(w.count))
	// Arena size: total string bytes for arena allocation on decode
	w.buf = AppendVarint(w.buf, uint64(w.totalStringLen))
	// An empty result has no column values, so field IDs would be ambiguous with
	// metadata appended after the end marker. Emit the canonical empty payload.
	if w.count > 0 {
		// Columns: [fieldID varint] [column data]
		for _, col := range w.columns {
			w.buf = AppendVarint(w.buf, uint64(col.fieldID))
			w.buf = append(w.buf, col.data...)
		}
	}
	// End marker
	w.buf = append(w.buf, 0x00)
	return w.buf, nil
}

func (w *ColumnarWriter) validate() error {
	if w.count < 0 || w.count > MaxColumnarRecords {
		return ErrInvalidColumnarCount
	}
	if w.columnRows == invalidColumnField {
		return ErrInvalidColumnarField
	}
	if w.count > 0 && len(w.columns) == 0 {
		return ErrEmptyColumnarProjection
	}
	if len(w.columns) > 0 && w.columnRows != w.count {
		return ErrColumnarRowCount
	}
	if w.totalStringLen < 0 || w.totalStringLen > MaxArenaSize {
		return ErrInvalidColumnarArena
	}
	return nil
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
	r.readHeader()
	return r
}

// Keep construction small enough to inline so local readers stay on the stack.
func (r *ColumnarReader) readHeader() {
	// Read record count
	count, n := ReadVarint(r.buf, 0)
	if n <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar record count")
		return
	}
	if count > MaxColumnarRecords {
		r.err = fmt.Errorf("codec: columnar count %d exceeds limit %d", count, MaxColumnarRecords)
		return
	}
	r.count = int(count)
	r.off = n

	// Read totalStringLen (arena size)
	arenaSize, an := ReadVarint(r.buf, r.off)
	if an <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar arena size")
		return
	}
	r.off += an
	if arenaSize > uint64(MaxArenaSize) {
		r.err = fmt.Errorf("codec: columnar arena size %d exceeds limit %d", arenaSize, MaxArenaSize)
		return
	}
	if arenaSize > uint64(len(r.buf)-r.off) {
		r.err = ErrInsufficientArenaBytes
		return
	}
	r.arenaSize = int(arenaSize)
	if r.count > 0 {
		r.checkProjection()
	}
}

// Check before generated readers allocate count-sized model slices. The first
// field ID and end marker are not row values; every row needs at least one byte.
func (r *ColumnarReader) checkProjection() {
	if r.count > len(r.buf)-r.off-2 {
		r.err = ErrInsufficientElementBytes
		return
	}
	id, size := uint64(r.buf[r.off]), 1
	if id >= 0x80 {
		id, size = ReadVarint(r.buf, r.off)
		if size <= 0 || r.count > len(r.buf)-r.off-size-1 {
			r.err = ErrInsufficientElementBytes
			return
		}
	}
	if id == 0 {
		r.err = ErrEmptyColumnarProjection
	}
}

// Count returns the number of records.
func (r *ColumnarReader) Count() int {
	return r.count
}

// ArenaSize returns the total string byte count (arena allocation size).
func (r *ColumnarReader) ArenaSize() int {
	return r.arenaSize
}

// NextColumn advances to the next column. EOF without an end marker is an error.
func (r *ColumnarReader) NextColumn() bool {
	if r.err != nil {
		return false
	}
	if r.off >= len(r.buf) {
		r.err = fmt.Errorf("codec: missing column end marker at offset %d", r.off)
		return false
	}
	id, n := ReadVarint(r.buf, r.off)
	if n <= 0 || id > uint64(^uint(0)>>1) {
		r.err = fmt.Errorf("codec: invalid column field ID at offset %d", r.off)
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

func (r *ColumnarReader) checkElements(minSize int) bool {
	if r.err == nil && !HasElementBytes(r.buf, r.off, r.count, minSize) {
		r.err = ErrInsufficientElementBytes
	}
	return r.err == nil
}

// ReadColumnInt reads count int64 values from the current column.
func (r *ColumnarReader) ReadColumnInt() []int64 {
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(8) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	if !r.checkElements(1) {
		return nil
	}
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
	buf, off := r.buf, r.off
	for i := 0; i < r.count; i++ {
		// A single terminal byte needs no value decoding. Longer values still
		// use the shared reader's truncation and uint64 overflow checks.
		if off < len(buf) && buf[off] < 0x80 {
			off++
			continue
		}
		_, n := ReadVarint(buf, off)
		if n <= 0 {
			r.off = off
			r.err = fmt.Errorf("codec: truncated int column at record %d", i)
			return
		}
		off += n
	}
	r.off = off
}

// SkipColumnFloat skips count float64 values without allocating.
func (r *ColumnarReader) SkipColumnFloat() {
	for i := 0; i < r.count; i++ {
		if _, n := ReadFixed64(r.buf, r.off); n == 0 {
			r.err = fmt.Errorf("codec: invalid float column at record %d", i)
			return
		}
		r.off += 8
	}
}

// SkipColumnString skips count length-prefixed strings without allocating.
func (r *ColumnarReader) SkipColumnString() {
	for i := 0; i < r.count; i++ {
		_, n := readStringBytes(r.buf, r.off)
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
			if _, n := ReadFixed64(r.buf, r.off); n == 0 {
				r.err = fmt.Errorf("codec: invalid float value at record %d", i)
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
			_, sn := readStringBytes(r.buf, r.off)
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
	if !r.checkElements(1) {
		return nil
	}
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

// ReadColumnBytesPtr reads count nullable []byte blobs.
func (r *ColumnarReader) ReadColumnBytesPtr() []*[]byte {
	if !r.checkElements(1) {
		return nil
	}
	result := make([]*[]byte, 0, r.count)
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
		v, bn := ReadBytes(r.buf, r.off)
		if bn == 0 {
			r.err = fmt.Errorf("codec: truncated bytes value at record %d", i)
			return nil
		}
		r.off += bn
		b := v
		result = append(result, &b)
	}
	return result
}

// ReadColumnUUID reads count fixed 16-byte UUID values.
func (r *ColumnarReader) ReadColumnUUID() [][16]byte {
	if !r.checkElements(16) {
		return nil
	}
	result := make([][16]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		var u [16]byte
		copy(u[:], r.buf[r.off:r.off+16])
		r.off += 16
		result = append(result, u)
	}
	return result
}

// ReadColumnUUIDPtr reads count nullable UUID values.
func (r *ColumnarReader) ReadColumnUUIDPtr() []*[16]byte {
	if !r.checkElements(1) {
		return nil
	}
	result := make([]*[16]byte, 0, r.count)
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
		u, un := ReadUUID(r.buf, r.off)
		if un == 0 {
			r.err = fmt.Errorf("codec: truncated uuid value at record %d", i)
			return nil
		}
		r.off += un
		result = append(result, &u)
	}
	return result
}

// SkipColumnBytesPtr skips count nullable byte blobs without allocating.
func (r *ColumnarReader) SkipColumnBytesPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			_, bn := ReadBytes(r.buf, r.off)
			if bn == 0 {
				r.err = fmt.Errorf("codec: truncated bytes value at record %d", i)
				return
			}
			r.off += bn
		}
	}
}

// SkipColumnUUID skips count fixed 16-byte UUID values without allocating.
func (r *ColumnarReader) SkipColumnUUID() {
	for i := 0; i < r.count; i++ {
		if r.off < 0 || r.off > len(r.buf) || len(r.buf)-r.off < 16 {
			r.err = fmt.Errorf("codec: truncated uuid column at record %d", i)
			return
		}
		r.off += 16
	}
}

// SkipColumnUUIDPtr skips count nullable UUID values without allocating.
func (r *ColumnarReader) SkipColumnUUIDPtr() {
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return
		}
		r.off += n
		if present {
			if r.off < 0 || r.off > len(r.buf) || len(r.buf)-r.off < 16 {
				r.err = fmt.Errorf("codec: truncated uuid value at record %d", i)
				return
			}
			r.off += 16
		}
	}
}

// --- Arena-based column readers ---
// These methods reduce allocations by copying all string data into a single
// pre-allocated []byte (the arena). Strings are backed by unsafe.String.

// ReadColumnStringArena reads count string values into the shared arena.
// Falls back to regular allocation if arena space is insufficient.
func (r *ColumnarReader) ReadColumnStringArena(arena []byte, arenaOff *int) []string {
	if !r.checkElements(1) {
		return nil
	}
	result := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		b, n := readStringBytes(r.buf, r.off)
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
	if !r.checkElements(1) {
		return nil
	}
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
		b, bn := readStringBytes(r.buf, r.off)
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
