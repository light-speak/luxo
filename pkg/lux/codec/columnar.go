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

import "fmt"

// ColumnarWriter writes records in columnar format.
// Collects all records first, then writes column-by-column.
type ColumnarWriter struct {
	buf     []byte
	count   int
	columns []column
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
func (w *ColumnarWriter) Bytes() []byte {
	w.buf = w.buf[:0]
	// Record count
	w.buf = AppendVarint(w.buf, uint64(w.count))
	// Columns: [fieldID varint] [column data]
	for _, col := range w.columns {
		w.buf = AppendVarint(w.buf, uint64(col.fieldID))
		w.buf = append(w.buf, col.data...)
	}
	// End marker
	w.buf = append(w.buf, 0x00)
	return w.buf
}

// ColumnarReader reads records from columnar format.
type ColumnarReader struct {
	buf     []byte
	off     int
	count   int
	fieldID int
	err     error
}

// NewColumnarReader creates a reader for columnar-encoded data.
func NewColumnarReader(buf []byte) *ColumnarReader {
	r := &ColumnarReader{buf: buf}
	// Read record count
	count, n := ReadVarint(buf, 0)
	if n <= 0 {
		r.err = fmt.Errorf("codec: invalid columnar record count")
		return r
	}
	r.count = int(count)
	r.off = n
	return r
}

// Count returns the number of records.
func (r *ColumnarReader) Count() int {
	return r.count
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
