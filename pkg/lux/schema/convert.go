package schema

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

var errInvalidJSONPayload = errors.New("schema: invalid UTF-8 JSON payload")

// BinaryToJSON converts a Luxo binary-encoded model to JSON using schema metadata.
// No model struct needed — pure schema-driven conversion.
// Returns the JSON bytes appended to dst.
// Schema is optional for flat values, but required for present nested values.
func BinaryToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) ([]byte, error) {
	dec := codec.NewDecoder(data)
	start := len(dst)
	dst = binaryToJSONFromDecoder(dst, dec, model, schemas...)
	if err := dec.Finish(); err != nil {
		return dst[:start], err
	}
	return dst, nil
}

// binaryToJSONFromDecoder reads fields from a decoder and produces JSON.
// Used by top-level and independently framed nested model decoding.
// Skips the arena header (totalStringLen varint) that WriteLuxo prepends.
func binaryToJSONFromDecoder(dst []byte, dec *codec.Decoder, model *Model, schemas ...*Schema) []byte {
	// Skip arena header — Binary→JSON doesn't use arena allocation
	dec.SkipArenaHeader()
	dst = append(dst, '{')
	first := true
	for dec.NextField() {
		f := model.FieldByID(dec.FieldID())
		if f == nil {
			dec.Fail(fmt.Errorf("schema: unknown field %d in %s", dec.FieldID(), model.Name))
			break
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = append(dst, f.JSONPrefix...)

		// Read one child frame; missing metadata must not fabricate null or [].
		if f.Relation {
			var nestedSchema *Schema
			if len(schemas) > 0 {
				nestedSchema = schemas[0]
			}
			var err error
			dst, err = appendNestedModelJSON(dst, dec, f, nestedSchema)
			if err != nil {
				dec.Fail(err)
			}
		} else {
			dst = appendFieldValueJSON(dst, dec, f)
		}
	}
	dst = append(dst, '}')
	return dst
}

// appendNestedModelJSON reads one length-delimited child response. Nested
// objects contain row bytes; nested lists contain columnar bytes. Nullable
// children use one outer presence flag, including nullable relation lists.
func appendNestedModelJSON(dst []byte, dec *codec.Decoder, f *Field, s *Schema) ([]byte, error) {
	if f.Nullable && !dec.ReadBool() {
		return append(dst, "null"...), dec.Err()
	}
	return appendNestedColumnBlobJSON(dst, dec.ReadBytes(), f, s)
}

// BinaryListToJSON converts a columnar-encoded list of models to JSON.
// Columnar format: [count][fieldID][val0..valN][fieldID][val0..valN]...[0x00]
// Optional schema enables federation blob column expansion.
func BinaryListToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) ([]byte, error) {
	return columnarToJSON(dst, data, model, schemas...)
}

// typedColumn holds a decoded column's values in typed slices (no map[string]any).
type typedColumn struct {
	field     *Field
	ints      []int64
	intPtrs   []*int64
	times     []time.Time
	timePtrs  []*time.Time
	floats    []float64
	floatPtrs []*float64
	strings   []string
	strPtrs   []*string
	bools     []bool
	boolPtrs  []*bool
	blobs     [][]byte    // nested model/list binary data (federation extend fields)
	blobPtrs  []*[]byte   // nullable bytes/JSON column
	uuids     [][16]byte  // 16-byte UUID column
	uuidPtrs  []*[16]byte // nullable UUID column
}

// columnarToJSON decodes columnar binary to JSON array.
// Uses typed column slices instead of map[string]any per record — zero map allocation.
func columnarToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) ([]byte, error) {
	r, columns, err := readJSONColumns(data, model)
	if err != nil {
		return dst, err
	}
	if r.Offset() != len(data) {
		return dst, fmt.Errorf("schema: trailing columnar data")
	}
	return appendJSONRows(dst, r.Count(), columns, schemas...)
}

// readJSONColumns is shared by ordinary lists and paginated lists. It validates
// the decoder before any typed column is indexed by the JSON writer.
func readJSONColumns(data []byte, model *Model) (*codec.ColumnarReader, []typedColumn, error) {
	r := codec.NewColumnarReader(data)
	if err := r.Err(); err != nil {
		return r, nil, err
	}
	var columns []typedColumn
	if r.Count() > 0 {
		columns = make([]typedColumn, 0, len(model.Fields))
	}
	for {
		before := r.Offset()
		if !r.NextColumn() {
			if r.Offset() <= before || data[r.Offset()-1] != 0 {
				return r, nil, fmt.Errorf("schema: missing columnar end marker")
			}
			break
		}
		f := model.FieldByID(r.FieldID())
		if f == nil {
			return r, nil, fmt.Errorf("schema: unknown column %d in %s", r.FieldID(), model.Name)
		}
		col := typedColumn{field: f}
		readColumn(r, f, &col)
		if err := r.Err(); err != nil {
			return r, nil, err
		}
		columns = append(columns, col)
	}
	return r, columns, nil
}

func appendJSONRows(dst []byte, count int, columns []typedColumn, schemas ...*Schema) ([]byte, error) {
	start := len(dst)
	dst = append(dst, '[')
	for i := 0; i < count; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		for ci := range columns {
			if ci > 0 {
				dst = append(dst, ',')
			}
			col := &columns[ci]
			dst = append(dst, col.field.JSONPrefix...)
			var err error
			dst, err = appendColumnValueJSON(dst, col, i, schemas...)
			if err != nil {
				return dst[:start], err
			}
		}
		dst = append(dst, '}')
	}
	return append(dst, ']'), nil
}

// readColumn reads a column's values into a typedColumn using the field's type info.
func readColumn(r *codec.ColumnarReader, f *Field, col *typedColumn) {
	// List columns (scalar arrays and relation lists) are Bytes columns: each
	// cell holds an inline [count][items...] array or a nested-model blob.
	if f.IsList {
		if f.Nullable {
			col.blobPtrs = r.ReadColumnBytesPtr()
		} else {
			col.blobs = r.ReadColumnBytes()
		}
		return
	}
	switch f.Type {
	case FieldDateTime:
		if f.Nullable {
			col.timePtrs = r.ReadColumnDateTimePtr()
		} else {
			col.times = r.ReadColumnDateTime()
		}
	case FieldInt, FieldDuration:
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
	case FieldString, FieldEnum, FieldDecimal:
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
		if f.Nullable {
			col.blobPtrs = r.ReadColumnBytesPtr()
		} else {
			col.blobs = r.ReadColumnBytes()
		}
	case FieldBytes, FieldJSON:
		if f.Nullable {
			col.blobPtrs = r.ReadColumnBytesPtr()
		} else {
			col.blobs = r.ReadColumnBytes()
		}
	}
}

// appendColumnValueJSON appends the JSON value for record i from a typed column.
func appendColumnValueJSON(dst []byte, col *typedColumn, i int, schemas ...*Schema) ([]byte, error) {
	f := col.field
	switch {
	case col.ints != nil:
		return strconv.AppendInt(dst, col.ints[i], 10), nil
	case col.intPtrs != nil:
		if col.intPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return strconv.AppendInt(dst, *col.intPtrs[i], 10), nil
	case col.times != nil:
		return appendDateTimeJSON(dst, col.times[i]), nil
	case col.timePtrs != nil:
		if col.timePtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return appendDateTimeJSON(dst, *col.timePtrs[i]), nil
	case col.floats != nil:
		return strconv.AppendFloat(dst, col.floats[i], 'f', -1, 64), nil
	case col.floatPtrs != nil:
		if col.floatPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return strconv.AppendFloat(dst, *col.floatPtrs[i], 'f', -1, 64), nil
	case col.strings != nil:
		return appendJSONString(dst, col.strings[i]), nil
	case col.strPtrs != nil:
		if col.strPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return appendJSONString(dst, *col.strPtrs[i]), nil
	case col.bools != nil:
		if col.bools[i] {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case col.boolPtrs != nil:
		if col.boolPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		if *col.boolPtrs[i] {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case col.uuids != nil:
		return appendUUIDString(dst, col.uuids[i]), nil
	case col.uuidPtrs != nil:
		if col.uuidPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return appendUUIDString(dst, *col.uuidPtrs[i]), nil
	case col.blobs != nil:
		return appendColumnBlobValueJSON(dst, col.blobs[i], f, schemas...)
	case col.blobPtrs != nil:
		if col.blobPtrs[i] == nil {
			return append(dst, "null"...), nil
		}
		return appendColumnBlobValueJSON(dst, *col.blobPtrs[i], f, schemas...)
	default:
		return append(dst, "null"...), nil
	}
}

// appendColumnBlobValueJSON decodes a Bytes-column cell: either a scalar array field
// ([count][items...]) or a federation extend blob (nested model / list binary).
func appendColumnBlobValueJSON(dst, blob []byte, f *Field, schemas ...*Schema) ([]byte, error) {
	// Scalar array field: each cell is an inline [count][items...] array.
	if f.IsList && f.Type != FieldModel {
		dec := codec.NewDecoder(blob)
		start := len(dst)
		dst = appendArrayFieldJSON(dst, dec, f)
		if err := dec.Finish(); err != nil {
			return dst[:start], err
		}
		return dst, nil
	}
	if f.Type != FieldModel {
		if f.Type == FieldJSON {
			return appendRawJSON(dst, blob)
		}
		return appendBase64JSON(dst, blob), nil
	}
	return appendNestedColumnBlobJSON(dst, blob, f, schemas...)
}

func appendNestedColumnBlobJSON(dst, blob []byte, f *Field, schemas ...*Schema) ([]byte, error) {
	if len(schemas) == 0 || schemas[0] == nil {
		return dst, fmt.Errorf("schema: nested type %q for field %q requires schema", f.TypeName, f.Name)
	}
	nested := schemas[0].LookupModel(f.TypeName)
	if nested == nil {
		return dst, fmt.Errorf("schema: nested type %q for field %q is not registered", f.TypeName, f.Name)
	}
	if f.IsList {
		return BinaryListToJSON(dst, blob, nested, schemas[0])
	}
	return BinaryToJSON(dst, blob, nested, schemas[0])
}

func appendRawJSON(dst, raw []byte) ([]byte, error) {
	if !jsonutil.ValidUTF8JSON(raw) {
		return dst, errInvalidJSONPayload
	}
	return append(dst, raw...), nil
}

func appendDecodedJSON(dst, raw []byte, dec *codec.Decoder) []byte {
	dst, err := appendRawJSON(dst, raw)
	if err != nil {
		dec.Fail(err)
	}
	return dst
}

func appendBase64JSON(dst, blob []byte) []byte {
	dst = append(dst, '"')
	dst = append(dst, base64.StdEncoding.EncodeToString(blob)...)
	return append(dst, '"')
}

// BinaryPaginatedListToJSON converts a paginated columnar list response to JSON.
// Binary format: [columnar data: count+columns+0x00][total svarint][page svarint][pageSize svarint]
// JSON format: {"items":[...],"total":N,"page":N,"pageSize":N}
func BinaryPaginatedListToJSON(dst []byte, data []byte, model *Model, schemas ...*Schema) ([]byte, error) {
	r, columns, err := readJSONColumns(data, model)
	if err != nil {
		return dst, err
	}
	metadata := codec.NewDecoder(data[r.Offset():])
	total, page, pageSize := metadata.ReadInt(), metadata.ReadInt(), metadata.ReadInt()
	if err := metadata.Finish(); err != nil {
		return dst, err
	}
	start := len(dst)
	dst = append(dst, `{"items":`...)
	dst, err = appendJSONRows(dst, r.Count(), columns, schemas...)
	if err != nil {
		return dst[:start], err
	}
	dst = append(dst, `,"total":`...)
	dst = strconv.AppendInt(dst, total, 10)
	dst = append(dst, `,"page":`...)
	dst = strconv.AppendInt(dst, page, 10)
	dst = append(dst, `,"pageSize":`...)
	dst = strconv.AppendInt(dst, pageSize, 10)
	return append(dst, '}'), nil
}

// appendArrayFieldJSON decodes a scalar array field ([count][items...]) and
// appends it as a JSON array. Shared by row (inline stream) and columnar
// (per-cell) decoding — the caller positions dec at the array header.
func appendArrayFieldJSON(dst []byte, dec *codec.Decoder, f *Field) []byte {
	switch f.Type {
	case FieldInt, FieldDuration:
		return appendIntArrayJSON(dst, dec.ReadIntArray())
	case FieldDateTime:
		return appendDateTimeArrayJSON(dst, dec.ReadDateTimeArray())
	case FieldFloat:
		return appendFloatArrayJSON(dst, dec.ReadFloatArray())
	case FieldString, FieldEnum, FieldDecimal:
		return appendStringArrayJSON(dst, dec.ReadStringArray())
	case FieldBool:
		return appendBoolArrayJSON(dst, dec.ReadBoolArray())
	case FieldUUID:
		return appendUUIDArrayJSON(dst, dec.ReadUUIDArray())
	case FieldBytes:
		return appendBytesArrayJSON(dst, dec.ReadBytesArray())
	case FieldJSON:
		values := dec.ReadBytesArray()
		dst = append(dst, '[')
		for i, raw := range values {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			dst, err = appendRawJSON(dst, raw)
			if err != nil {
				dec.Fail(err)
				return dst
			}
		}
		return append(dst, ']')
	}
	return append(dst, '[', ']')
}

func appendIntArrayJSON(dst []byte, vs []int64) []byte {
	dst = append(dst, '[')
	for i, v := range vs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = strconv.AppendInt(dst, v, 10)
	}
	return append(dst, ']')
}

func appendDateTimeJSON(dst []byte, value time.Time) []byte {
	dst = append(dst, '"')
	dst = value.UTC().AppendFormat(dst, time.RFC3339Nano)
	return append(dst, '"')
}

func appendDateTimeArrayJSON(dst []byte, values []time.Time) []byte {
	dst = append(dst, '[')
	for i, value := range values {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendDateTimeJSON(dst, value)
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
		if f.Nullable && !dec.ReadBool() {
			return append(dst, "null"...)
		}
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
	case FieldString, FieldEnum, FieldDecimal:
		v := dec.ReadString()
		return appendJSONString(dst, v)
	case FieldBool:
		v := dec.ReadBool()
		if v {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case FieldDateTime:
		return appendDateTimeJSON(dst, dec.ReadDateTime())
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
	case FieldJSON:
		return appendDecodedJSON(dst, dec.ReadBytes(), dec)
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
	case FieldString, FieldEnum, FieldDecimal:
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
		v := dec.ReadDateTimePtr()
		if v == nil {
			return append(dst, "null"...)
		}
		return appendDateTimeJSON(dst, *v)
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
	case FieldJSON:
		raw := dec.ReadBytesPtr()
		if raw == nil {
			return append(dst, "null"...)
		}
		return appendDecodedJSON(dst, raw, dec)
	default:
		return append(dst, "null"...)
	}
}

// BinaryScalarToJSON converts a single scalar binary value to JSON.
// Uses the declared return type for precise decoding — no guessing.
func BinaryScalarToJSON(dst []byte, data []byte, typeName string) ([]byte, error) {
	if len(data) == 0 && typeName == "" {
		return append(dst, "null"...), nil
	}
	start := len(dst)
	dec := codec.NewDecoder(data)
	dst = appendScalarValueJSON(dst, dec, typeName)
	if err := dec.Finish(); err != nil {
		return dst[:start], err
	}
	return dst, nil
}

// BinaryScalarListToJSON converts a canonical count-prefixed list of scalar
// values. Enum callers pass String because enums share the string wire type.
func BinaryScalarListToJSON(dst []byte, data []byte, typeName string) ([]byte, error) {
	start := len(dst)
	dec := codec.NewDecoder(data)
	count := dec.ReadArrayLength()
	dst = append(dst, '[')
	for i := 0; i < count && dec.Err() == nil; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendScalarValueJSON(dst, dec, typeName)
	}
	if err := dec.Finish(); err != nil {
		return dst[:start], err
	}
	return append(dst, ']'), nil
}

func appendScalarValueJSON(dst []byte, dec *codec.Decoder, typeName string) []byte {
	switch typeName {
	case "Int", "Duration", "":
		return strconv.AppendInt(dst, dec.ReadInt(), 10)
	case "Float":
		return strconv.AppendFloat(dst, dec.ReadFloat(), 'f', -1, 64)
	case "Boolean":
		if dec.ReadBool() {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case "DateTime":
		return appendDateTimeJSON(dst, dec.ReadDateTime())
	case "UUID":
		return appendUUIDString(dst, dec.ReadUUID())
	case "Bytes":
		return appendBase64JSON(dst, dec.ReadBytes())
	case "JSON":
		return appendDecodedJSON(dst, dec.ReadBytes(), dec)
	case "String", "Decimal":
		return appendJSONString(dst, dec.ReadString())
	default:
		dec.Fail(fmt.Errorf("schema: unsupported scalar type %q", typeName))
		return dst
	}
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
