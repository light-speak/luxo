package schema

import (
	"encoding/base64"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// BinaryToJSON converts a Luxo binary-encoded model to JSON using schema metadata.
// No model struct needed — pure schema-driven conversion.
// Returns the JSON bytes appended to dst.
// schema is optional — needed for nested model/type resolution.
func BinaryToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) []byte {
	dec := codec.NewDecoder(data)
	return binaryToJSONFromDecoder(dst, dec, model, schemas...)
}

// binaryToJSONFromDecoder reads fields from a decoder and produces JSON.
// Used by both top-level and nested model decoding (sharing the same decoder stream).
// Skips the arena header (totalStringLen varint) that WriteLuxo prepends.
func binaryToJSONFromDecoder(dst []byte, dec *codec.Decoder, model *Model, schemas ...*Schema) []byte {
	// Skip arena header — Binary→JSON doesn't use arena allocation
	dec.SkipArenaHeader()
	dst = append(dst, '{')
	first := true
	for dec.NextField() {
		f := model.FieldByID(dec.FieldID())
		if f == nil {
			break
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = append(dst, f.JSONPrefix...)

		// Nested model/type — recurse using same decoder
		if f.Relation && len(schemas) > 0 && schemas[0] != nil {
			dst = appendNestedModelJSON(dst, dec, f, schemas[0])
		} else {
			dst = appendFieldValueJSON(dst, dec, f)
		}
	}
	dst = append(dst, '}')
	return dst
}

// appendNestedModelJSON decodes an inline nested model/type from the same decoder stream.
func appendNestedModelJSON(dst []byte, dec *codec.Decoder, f *Field, s *Schema) []byte {
	nested := s.Models[f.TypeName]
	if nested == nil {
		if td := s.Types[f.TypeName]; td != nil {
			nested = td.AsModel()
		}
	}
	if nested == nil {
		// Schema incomplete: we cannot skip nested fields without type info.
		// This should never happen in practice since codegen ensures schema completeness.
		// Drain the nested sub-message (terminated by 0x00) by consuming field IDs
		// without reading values — this will exhaust the decoder.
		for dec.NextField() {
			// Cannot read field values without type info; decoder is now misaligned.
			break
		}
		return append(dst, "null"...)
	}
	// The nested model's fields are inline in the same byte stream,
	// terminated by 0x00 (which NextField handles).
	return binaryToJSONFromDecoder(dst, dec, nested, s)
}

// BinaryListToJSON converts a columnar-encoded list of models to JSON.
// Columnar format: [count][fieldID][val0..valN][fieldID][val0..valN]...[0x00]
// Optional schema enables federation blob column expansion.
func BinaryListToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) []byte {
	return columnarToJSON(dst, data, model, schemas...)
}

// typedColumn holds a decoded column's values in typed slices (no map[string]any).
type typedColumn struct {
	field     *Field
	ints      []int64
	intPtrs   []*int64
	floats    []float64
	floatPtrs []*float64
	strings   []string
	strPtrs   []*string
	bools     []bool
	boolPtrs  []*bool
	blobs     [][]byte    // nested model/list binary data (federation extend fields)
	uuids     [][16]byte  // 16-byte UUID column
	uuidPtrs  []*[16]byte // nullable UUID column
}

// columnarToJSON decodes columnar binary to JSON array.
// Uses typed column slices instead of map[string]any per record — zero map allocation.
func columnarToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) []byte {
	r := codec.NewColumnarReader(data)
	count := r.Count()
	if count == 0 {
		return append(dst, '[', ']')
	}
	columns := make([]typedColumn, 0, len(model.Fields))

	for r.NextColumn() {
		f := model.FieldByID(r.FieldID())
		if f == nil {
			break
		}
		col := typedColumn{field: f}
		readColumn(r, f, &col)
		columns = append(columns, col)
	}

	// Write JSON array — iterate records, for each record iterate columns
	dst = append(dst, '[')
	for i := 0; i < count; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		first := true
		for ci := range columns {
			col := &columns[ci]
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = append(dst, col.field.JSONPrefix...)
			dst = appendColumnValueJSON(dst, col, i, schemas...)
		}
		dst = append(dst, '}')
	}
	dst = append(dst, ']')
	return dst
}

// readColumn reads a column's values into a typedColumn using the field's type info.
func readColumn(r *codec.ColumnarReader, f *Field, col *typedColumn) {
	// List columns (scalar arrays and relation lists) are Bytes columns: each
	// cell holds an inline [count][items...] array or a nested-model blob.
	if f.IsList {
		col.blobs = r.ReadColumnBytes()
		return
	}
	switch f.Type {
	case FieldInt, FieldDateTime, FieldDuration:
		if f.Nullable {
			col.intPtrs = r.ReadColumnIntPtr()
		} else {
			col.ints = r.ReadColumnInt()
		}
	case FieldFloat:
		if f.Nullable {
			col.floatPtrs = r.ReadColumnFloatPtr()
		} else {
			col.floats = r.ReadColumnFloat()
		}
	case FieldString, FieldEnum:
		if f.Nullable {
			col.strPtrs = r.ReadColumnStringPtr()
		} else {
			col.strings = r.ReadColumnString()
		}
	case FieldBool:
		if f.Nullable {
			col.boolPtrs = r.ReadColumnBoolPtr()
		} else {
			col.bools = r.ReadColumnBool()
		}
	case FieldUUID:
		if f.Nullable {
			col.uuidPtrs = r.ReadColumnUUIDPtr()
		} else {
			col.uuids = r.ReadColumnUUID()
		}
	case FieldModel:
		col.blobs = r.ReadColumnBytes()
	}
}

// appendColumnValueJSON appends the JSON value for record i from a typed column.
func appendColumnValueJSON(dst []byte, col *typedColumn, i int, schemas ...*Schema) []byte {
	f := col.field
	switch {
	case col.ints != nil:
		if f.Type == FieldDateTime {
			dst = append(dst, '"')
			dst = time.Unix(col.ints[i], 0).UTC().AppendFormat(dst, time.RFC3339Nano)
			dst = append(dst, '"')
			return dst
		}
		return strconv.AppendInt(dst, col.ints[i], 10)
	case col.intPtrs != nil:
		if col.intPtrs[i] == nil {
			return append(dst, "null"...)
		}
		if f.Type == FieldDateTime {
			dst = append(dst, '"')
			dst = time.Unix(*col.intPtrs[i], 0).UTC().AppendFormat(dst, time.RFC3339Nano)
			dst = append(dst, '"')
			return dst
		}
		return strconv.AppendInt(dst, *col.intPtrs[i], 10)
	case col.floats != nil:
		return strconv.AppendFloat(dst, col.floats[i], 'f', -1, 64)
	case col.floatPtrs != nil:
		if col.floatPtrs[i] == nil {
			return append(dst, "null"...)
		}
		return strconv.AppendFloat(dst, *col.floatPtrs[i], 'f', -1, 64)
	case col.strings != nil:
		return appendJSONString(dst, col.strings[i])
	case col.strPtrs != nil:
		if col.strPtrs[i] == nil {
			return append(dst, "null"...)
		}
		return appendJSONString(dst, *col.strPtrs[i])
	case col.bools != nil:
		if col.bools[i] {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case col.boolPtrs != nil:
		if col.boolPtrs[i] == nil {
			return append(dst, "null"...)
		}
		if *col.boolPtrs[i] {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case col.uuids != nil:
		return appendUUIDString(dst, col.uuids[i])
	case col.uuidPtrs != nil:
		if col.uuidPtrs[i] == nil {
			return append(dst, "null"...)
		}
		return appendUUIDString(dst, *col.uuidPtrs[i])
	case col.blobs != nil:
		return appendColumnBlobJSON(dst, col, i, schemas...)
	default:
		return append(dst, "null"...)
	}
}

// appendColumnBlobJSON decodes a Bytes-column cell: either a scalar array field
// ([count][items...]) or a federation extend blob (nested model / list binary).
func appendColumnBlobJSON(dst []byte, col *typedColumn, i int, schemas ...*Schema) []byte {
	f := col.field
	blob := col.blobs[i]
	// Scalar array field: each cell is an inline [count][items...] array.
	if f.Type != FieldModel {
		return appendArrayFieldJSON(dst, codec.NewDecoder(blob), f)
	}
	// Federation extend field — blob contains nested model/list binary data.
	if len(blob) == 0 {
		if f.IsList {
			return append(dst, "[]"...)
		}
		return append(dst, "null"...)
	}
	if len(schemas) > 0 && schemas[0] != nil {
		nested := schemas[0].Models[f.TypeName]
		if nested != nil {
			if f.IsList {
				return BinaryListToJSON(dst, blob, nested, schemas[0])
			}
			return BinaryToJSON(dst, blob, nested, schemas[0])
		}
	}
	if f.IsList {
		return append(dst, "[]"...)
	}
	return append(dst, "null"...)
}

func appendAnyJSON(dst []byte, v any, f *Field) []byte {
	if v == nil {
		return append(dst, "null"...)
	}
	switch val := v.(type) {
	case int64:
		if f.Type == FieldDateTime {
			dst = append(dst, '"')
			dst = time.Unix(val, 0).UTC().AppendFormat(dst, time.RFC3339Nano)
			dst = append(dst, '"')
			return dst
		}
		return strconv.AppendInt(dst, val, 10)
	case *int64:
		if val == nil {
			return append(dst, "null"...)
		}
		return strconv.AppendInt(dst, *val, 10)
	case float64:
		return strconv.AppendFloat(dst, val, 'f', -1, 64)
	case string:
		return appendJSONString(dst, val)
	case *string:
		if val == nil {
			return append(dst, "null"...)
		}
		return appendJSONString(dst, *val)
	case bool:
		if val {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	default:
		return append(dst, "null"...)
	}
}

// BinaryPaginatedListToJSON converts a paginated columnar list response to JSON.
// Binary format: [columnar data: count+columns+0x00][total svarint][page svarint][pageSize svarint]
// JSON format: {"items":[...],"total":N,"page":N,"pageSize":N}
func BinaryPaginatedListToJSON(dst []byte, data []byte, model *Model) []byte {
	r := codec.NewColumnarReader(data)
	count := r.Count()
	if count == 0 {
		// Still read pagination metadata from after the end marker.
		// When count=0, NextColumn is never called, so skip the 0x00 end marker manually.
		off := r.Offset()
		if off < len(data) && data[off] == 0x00 {
			off++
		}
		dst = append(dst, `{"items":[]`...)
		remaining := data[off:]
		roff := 0
		if total, tn := codec.ReadSvarint(remaining, roff); tn > 0 {
			roff += tn
			dst = append(dst, `,"total":`...)
			dst = strconv.AppendInt(dst, total, 10)
		} else {
			dst = append(dst, `,"total":0`...)
		}
		if page, pn := codec.ReadSvarint(remaining, roff); pn > 0 {
			roff += pn
			dst = append(dst, `,"page":`...)
			dst = strconv.AppendInt(dst, page, 10)
		} else {
			dst = append(dst, `,"page":1`...)
		}
		if pageSize, psn := codec.ReadSvarint(remaining, roff); psn > 0 {
			dst = append(dst, `,"pageSize":`...)
			dst = strconv.AppendInt(dst, pageSize, 10)
		} else {
			dst = append(dst, `,"pageSize":20`...)
		}
		return append(dst, '}')
	}

	// Read columns into typed slices (zero map allocation)
	columns := make([]typedColumn, 0, len(model.Fields))
	for r.NextColumn() {
		f := model.FieldByID(r.FieldID())
		if f == nil {
			break
		}
		col := typedColumn{field: f}
		readColumn(r, f, &col)
		columns = append(columns, col)
	}

	// Write JSON items
	dst = append(dst, `{"items":[`...)
	for i := 0; i < count; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		first := true
		for ci := range columns {
			col := &columns[ci]
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = append(dst, col.field.JSONPrefix...)
			dst = appendColumnValueJSON(dst, col, i)
		}
		dst = append(dst, '}')
	}
	dst = append(dst, ']')

	// Read pagination metadata after columnar data
	// ColumnarReader.Offset() points past the 0x00 end marker
	remaining := data[r.Offset():]
	roff := 0

	total, tn := codec.ReadSvarint(remaining, roff)
	if tn > 0 {
		roff += tn
		dst = append(dst, `,"total":`...)
		dst = strconv.AppendInt(dst, total, 10)
	}

	page, pn := codec.ReadSvarint(remaining, roff)
	if pn > 0 {
		roff += pn
		dst = append(dst, `,"page":`...)
		dst = strconv.AppendInt(dst, page, 10)
	}

	pageSize, psn := codec.ReadSvarint(remaining, roff)
	if psn > 0 {
		dst = append(dst, `,"pageSize":`...)
		dst = strconv.AppendInt(dst, pageSize, 10)
	}
	_ = psn

	dst = append(dst, '}')
	return dst
}

// appendArrayFieldJSON decodes a scalar array field ([count][items...]) and
// appends it as a JSON array. Shared by row (inline stream) and columnar
// (per-cell) decoding — the caller positions dec at the array header.
func appendArrayFieldJSON(dst []byte, dec *codec.Decoder, f *Field) []byte {
	switch f.Type {
	case FieldInt, FieldDuration:
		return appendIntArrayJSON(dst, dec.ReadIntArray(), false)
	case FieldDateTime:
		return appendIntArrayJSON(dst, dec.ReadIntArray(), true)
	case FieldFloat:
		return appendFloatArrayJSON(dst, dec.ReadFloatArray())
	case FieldString, FieldEnum:
		return appendStringArrayJSON(dst, dec.ReadStringArray())
	case FieldBool:
		return appendBoolArrayJSON(dst, dec.ReadBoolArray())
	case FieldUUID:
		return appendUUIDArrayJSON(dst, dec.ReadUUIDArray())
	case FieldBytes:
		return appendBytesArrayJSON(dst, dec.ReadBytesArray())
	}
	return append(dst, '[', ']')
}

func appendIntArrayJSON(dst []byte, vs []int64, asTime bool) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		if asTime {
			dst = append(dst, '"')
			dst = time.Unix(v, 0).UTC().AppendFormat(dst, time.RFC3339Nano)
			dst = append(dst, '"')
		} else {
			dst = strconv.AppendInt(dst, v, 10)
		}
	}
	return append(dst, ']')
}

func appendFloatArrayJSON(dst []byte, vs []float64) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendFloat(dst, v, 'f', -1, 64)
	}
	return append(dst, ']')
}

func appendStringArrayJSON(dst []byte, vs []string) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, v)
	}
	return append(dst, ']')
}

func appendBoolArrayJSON(dst []byte, vs []bool) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		if v {
			dst = append(dst, "true"...)
		} else {
			dst = append(dst, "false"...)
		}
	}
	return append(dst, ']')
}

func appendUUIDArrayJSON(dst []byte, vs [][16]byte) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendUUIDString(dst, v)
	}
	return append(dst, ']')
}

func appendBytesArrayJSON(dst []byte, vs [][]byte) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '"')
		dst = append(dst, base64.StdEncoding.EncodeToString(v)...)
		dst = append(dst, '"')
	}
	return append(dst, ']')
}

// appendFieldValueJSON reads the next value from decoder and appends as JSON.
func appendFieldValueJSON(dst []byte, dec *codec.Decoder, f *Field) []byte {
	if f.IsList {
		return appendArrayFieldJSON(dst, dec, f)
	}
	if f.Nullable {
		return appendNullableFieldJSON(dst, dec, f)
	}
	switch f.Type {
	case FieldInt:
		v := dec.ReadInt()
		return strconv.AppendInt(dst, v, 10)
	case FieldFloat:
		v := dec.ReadFloat()
		return strconv.AppendFloat(dst, v, 'f', -1, 64)
	case FieldString, FieldEnum:
		v := dec.ReadString()
		return appendJSONString(dst, v)
	case FieldBool:
		v := dec.ReadBool()
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case FieldDateTime:
		v := dec.ReadInt()
		t := time.Unix(v, 0).UTC()
		dst = append(dst, '"')
		dst = t.AppendFormat(dst, time.RFC3339Nano)
		dst = append(dst, '"')
		return dst
	case FieldDuration:
		v := dec.ReadInt()
		return strconv.AppendInt(dst, v, 10)
	case FieldUUID:
		return appendUUIDString(dst, dec.ReadUUID())
	case FieldBytes:
		raw := dec.ReadBytes()
		if raw == nil {
			return append(dst, "null"...)
		}
		dst = append(dst, '"')
		dst = append(dst, base64.StdEncoding.EncodeToString(raw)...)
		dst = append(dst, '"')
		return dst
	default:
		return append(dst, "null"...)
	}
}

// appendNullableFieldJSON handles nullable field reading.
func appendNullableFieldJSON(dst []byte, dec *codec.Decoder, f *Field) []byte {
	switch f.Type {
	case FieldInt:
		v := dec.ReadIntPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		return strconv.AppendInt(dst, *v, 10)
	case FieldFloat:
		v := dec.ReadFloatPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		return strconv.AppendFloat(dst, *v, 'f', -1, 64)
	case FieldString, FieldEnum:
		v := dec.ReadStringPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		return appendJSONString(dst, *v)
	case FieldBool:
		v := dec.ReadBoolPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		if *v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case FieldDateTime:
		v := dec.ReadIntPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		t := time.Unix(*v, 0).UTC()
		dst = append(dst, '"')
		dst = t.AppendFormat(dst, time.RFC3339Nano)
		dst = append(dst, '"')
		return dst
	case FieldDuration:
		v := dec.ReadIntPtr()
		if v == nil {
			return append(dst, "null"...)
		}
		return strconv.AppendInt(dst, *v, 10)
	case FieldUUID:
		u := dec.ReadUUIDPtr()
		if u == nil {
			return append(dst, "null"...)
		}
		return appendUUIDString(dst, *u)
	case FieldBytes:
		raw := dec.ReadBytesPtr()
		if raw == nil {
			return append(dst, "null"...)
		}
		dst = append(dst, '"')
		dst = append(dst, base64.StdEncoding.EncodeToString(raw)...)
		dst = append(dst, '"')
		return dst
	default:
		return append(dst, "null"...)
	}
}

// BinaryScalarToJSON converts a single scalar binary value to JSON.
// Uses the declared return type for precise decoding — no guessing.
func BinaryScalarToJSON(dst []byte, data []byte, typeName string) []byte {
	if len(data) == 0 {
		return append(dst, "null"...)
	}
	switch typeName {
	case "Int", "Duration", "":
		v, n := codec.ReadSvarint(data, 0)
		if n > 0 {
			return strconv.AppendInt(dst, v, 10)
		}
	case "Float":
		v, n := codec.ReadFixed64(data, 0)
		if n > 0 {
			return strconv.AppendFloat(dst, v, 'f', -1, 64)
		}
	case "Boolean":
		v, n := codec.ReadBool(data, 0)
		if n > 0 {
			if v {
				return append(dst, "true"...)
			}
			return append(dst, "false"...)
		}
	case "UUID":
		if len(data) >= 16 {
			var u [16]byte
			copy(u[:], data[:16])
			return appendUUIDString(dst, u)
		}
	case "String", "DateTime", "Decimal":
		v, n := codec.ReadString(data, 0)
		if n > 0 {
			return appendJSONString(dst, v)
		}
	}
	// Fallback: try svarint
	v, n := codec.ReadSvarint(data, 0)
	if n > 0 {
		return strconv.AppendInt(dst, v, 10)
	}
	return append(dst, "null"...)
}

// appendUUIDString formats a 16-byte UUID as the canonical 36-char JSON string.
func appendUUIDString(dst []byte, b [16]byte) []byte {
	const hexd = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			dst = append(dst, '-')
		}
		dst = append(dst, hexd[b[i]>>4], hexd[b[i]&0x0f])
	}
	return append(dst, '"')
}

// parseUUID parses a canonical 36-char UUID string into 16 bytes.
func parseUUID(s string) ([16]byte, bool) {
	var u [16]byte
	if len(s) != 36 {
		return u, false
	}
	j, i := 0, 0
	for i < len(s) {
		if s[i] == '-' {
			i++
			continue
		}
		if i+1 >= len(s) || j >= 16 {
			return u, false
		}
		hi, ok1 := hexNibble(s[i])
		lo, ok2 := hexNibble(s[i+1])
		if !ok1 || !ok2 {
			return u, false
		}
		u[j] = hi<<4 | lo
		j++
		i += 2
	}
	return u, j == 16
}

// hexNibble decodes a single hex digit.
func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// appendJSONString appends a JSON-escaped string.
// Uses a byte-level fast path for ASCII; falls through to rune handling for bytes >= 0x80.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	i := 0
	for i < len(s) {
		// Fast path: scan for longest run of safe ASCII bytes.
		start := i
		for i < len(s) {
			c := s[i]
			if c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
				break
			}
			i++
		}
		if start < i {
			dst = append(dst, s[start:i]...)
		}
		if i >= len(s) {
			break
		}
		c := s[i]
		if c >= 0x80 {
			// Multi-byte UTF-8: decode rune and append.
			ru, size := utf8.DecodeRuneInString(s[i:])
			dst = utf8.AppendRune(dst, ru)
			i += size
			continue
		}
		// Special ASCII byte requiring escape.
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', "0123456789abcdef"[c>>4], "0123456789abcdef"[c&0xf])
		}
		i++
	}
	dst = append(dst, '"')
	return dst
}

// JSONParamsToBinary converts JSON params to Luxo binary format using API schema.
// Input: {"id": 1, "name": "Alice"} → binary encoded params.
func JSONParamsToBinary(jsonParams map[string]any, api *API) []byte {
	var enc codec.Encoder
	for _, p := range api.Params {
		v, ok := jsonParams[p.Name]
		if !ok {
			continue
		}
		if p.IsList {
			writeJSONArrayParam(&enc, p, v)
		} else {
			writeJSONScalarParam(&enc, p, v)
		}
	}
	enc.WriteEnd()
	return enc.Bytes()
}

// writeJSONScalarParam encodes a single scalar JSON param value to binary.
func writeJSONScalarParam(enc *codec.Encoder, p Param, v any) {
	switch p.Type {
	case FieldInt:
		switch iv := v.(type) {
		case float64:
			enc.WriteFieldInt(p.ID, int64(iv))
		case int64:
			enc.WriteFieldInt(p.ID, iv)
		}
	case FieldFloat:
		if fv, ok := v.(float64); ok {
			enc.WriteFieldFloat(p.ID, fv)
		}
	case FieldString, FieldEnum:
		if sv, ok := v.(string); ok {
			enc.WriteFieldString(p.ID, sv)
		}
	case FieldBool:
		if bv, ok := v.(bool); ok {
			enc.WriteFieldBool(p.ID, bv)
		}
	case FieldDuration:
		if fv, ok := v.(float64); ok {
			enc.WriteFieldInt(p.ID, int64(fv))
		}
	case FieldDateTime:
		// JSON callers send DateTime as RFC3339 string; parse to unix seconds.
		// Tolerate raw unix-seconds number too.
		switch dv := v.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, dv); err == nil {
				enc.WriteFieldInt(p.ID, t.Unix())
			}
		case float64:
			enc.WriteFieldInt(p.ID, int64(dv))
		}
	case FieldUUID:
		if sv, ok := v.(string); ok {
			if u, valid := parseUUID(sv); valid {
				enc.WriteFieldUUID(p.ID, u)
			}
		}
	case FieldBytes:
		if sv, ok := v.(string); ok {
			if raw, err := base64.StdEncoding.DecodeString(sv); err == nil {
				enc.WriteFieldBytes(p.ID, raw)
			}
		}
	}
}

// writeJSONArrayParam encodes a JSON array param (in/notIn → [T]) to binary.
func writeJSONArrayParam(enc *codec.Encoder, p Param, v any) {
	arr, ok := v.([]any)
	if !ok {
		return
	}
	switch p.Type {
	case FieldInt, FieldDuration:
		vs := make([]int64, 0, len(arr))
		for _, e := range arr {
			if fv, ok := e.(float64); ok {
				vs = append(vs, int64(fv))
			}
		}
		enc.WriteFieldIntArray(p.ID, vs)
	case FieldDateTime:
		// JSON clients may send DateTime arrays as RFC3339 strings or as raw
		// unix-seconds numbers; accept both, like the scalar param path.
		vs := make([]int64, 0, len(arr))
		for _, e := range arr {
			switch ev := e.(type) {
			case float64:
				vs = append(vs, int64(ev))
			case string:
				if t, err := time.Parse(time.RFC3339, ev); err == nil {
					vs = append(vs, t.Unix())
				}
			}
		}
		enc.WriteFieldIntArray(p.ID, vs)
	case FieldFloat:
		vs := make([]float64, 0, len(arr))
		for _, e := range arr {
			if fv, ok := e.(float64); ok {
				vs = append(vs, fv)
			}
		}
		enc.WriteFieldFloatArray(p.ID, vs)
	case FieldString, FieldEnum:
		vs := make([]string, 0, len(arr))
		for _, e := range arr {
			if sv, ok := e.(string); ok {
				vs = append(vs, sv)
			}
		}
		enc.WriteFieldStringArray(p.ID, vs)
	case FieldBool:
		vs := make([]bool, 0, len(arr))
		for _, e := range arr {
			if bv, ok := e.(bool); ok {
				vs = append(vs, bv)
			}
		}
		enc.WriteFieldBoolArray(p.ID, vs)
	case FieldUUID:
		vs := make([][16]byte, 0, len(arr))
		for _, e := range arr {
			if sv, ok := e.(string); ok {
				if u, valid := parseUUID(sv); valid {
					vs = append(vs, u)
				}
			}
		}
		enc.WriteFieldUUIDArray(p.ID, vs)
	}
}
