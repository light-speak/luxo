package schema

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// BinaryToJSON converts a Luxo binary-encoded model to JSON using schema metadata.
// No model struct needed — pure schema-driven conversion.
// Returns the JSON bytes appended to dst.
func BinaryToJSON(dst []byte, data []byte, model *Model) []byte {
	dst = append(dst, '{')
	dec := codec.NewDecoder(data)
	first := true
	for dec.NextField() {
		f := model.FieldByID(dec.FieldID())
		if f == nil {
			// Unknown field — schema should be complete, but just in case
			// we can't skip because Luxo binary isn't self-describing.
			// Break to avoid corrupting the stream.
			break
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = append(dst, f.JSONPrefix...)
		dst = appendFieldValueJSON(dst, dec, f)
	}
	dst = append(dst, '}')
	return dst
}

// BinaryListToJSON converts a columnar-encoded list of models to JSON.
// Columnar format: [count][fieldID][val0..valN][fieldID][val0..valN]...[0x00]
func BinaryListToJSON(dst []byte, data []byte, model *Model) []byte {
	return columnarToJSON(dst, data, model)
}

// columnarToJSON decodes columnar binary to JSON array.
func columnarToJSON(dst []byte, data []byte, model *Model) []byte {
	r := codec.NewColumnarReader(data)
	count := r.Count()
	if count == 0 {
		return append(dst, '[', ']')
	}

	// Read all columns into per-record maps
	records := make([]map[string]any, count)
	for i := range records {
		records[i] = make(map[string]any)
	}

	for r.NextColumn() {
		f := model.FieldByID(r.FieldID())
		if f == nil {
			break
		}
		readColumnIntoRecords(r, f, records)
	}

	// Write JSON array
	dst = append(dst, '[')
	for i, rec := range records {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		first := true
		for _, f := range model.Fields {
			v, ok := rec[f.Name]
			if !ok {
				continue
			}
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = append(dst, f.JSONPrefix...)
			dst = appendAnyJSON(dst, v, &f)
		}
		dst = append(dst, '}')
	}
	dst = append(dst, ']')
	return dst
}

func readColumnIntoRecords(r *codec.ColumnarReader, f *Field, records []map[string]any) {
	switch f.Type {
	case FieldInt, FieldDateTime, FieldDuration:
		if f.Nullable {
			vals := r.ReadColumnIntPtr()
			for i, v := range vals {
				records[i][f.Name] = v
			}
		} else {
			vals := r.ReadColumnInt()
			for i, v := range vals {
				records[i][f.Name] = v
			}
		}
	case FieldFloat:
		vals := r.ReadColumnFloat()
		for i, v := range vals {
			records[i][f.Name] = v
		}
	case FieldString, FieldEnum:
		if f.Nullable {
			vals := r.ReadColumnStringPtr()
			for i, v := range vals {
				records[i][f.Name] = v
			}
		} else {
			vals := r.ReadColumnString()
			for i, v := range vals {
				records[i][f.Name] = v
			}
		}
	case FieldBool:
		vals := r.ReadColumnBool()
		for i, v := range vals {
			records[i][f.Name] = v
		}
	}
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
		return append(dst, `{"items":[],"total":0,"page":1,"pageSize":20}`...)
	}

	// Read columns
	records := make([]map[string]any, count)
	for i := range records {
		records[i] = make(map[string]any)
	}
	for r.NextColumn() {
		f := model.FieldByID(r.FieldID())
		if f == nil {
			break
		}
		readColumnIntoRecords(r, f, records)
	}

	// Write JSON items
	dst = append(dst, `{"items":[`...)
	for i, rec := range records {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		first := true
		for _, f := range model.Fields {
			v, ok := rec[f.Name]
			if !ok {
				continue
			}
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = append(dst, f.JSONPrefix...)
			dst = appendAnyJSON(dst, v, &f)
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

// binaryModelToJSON decodes one model from a streaming decoder and writes JSON.
func binaryModelToJSON(dst []byte, dec *codec.Decoder, model *Model) []byte {
	dst = append(dst, '{')
	first := true
	for dec.NextField() {
		f := model.FieldByID(dec.FieldID())
		if f == nil {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = append(dst, f.JSONPrefix...)
		dst = appendFieldValueJSON(dst, dec, f)
	}
	dst = append(dst, '}')
	return dst
}

// appendFieldValueJSON reads the next value from decoder and appends as JSON.
func appendFieldValueJSON(dst []byte, dec *codec.Decoder, f *Field) []byte {
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
	case FieldBytes:
		_ = dec.ReadBytes()
		return append(dst, "null"...) // TODO: base64 encode
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
	default:
		return append(dst, "null"...)
	}
}

// BinaryScalarToJSON converts a single scalar binary value to JSON.
// Tries svarint (Int), then falls back to raw byte output.
// Used for delete (count), fn @service returns, etc.
func BinaryScalarToJSON(dst []byte, data []byte) []byte {
	if len(data) == 0 {
		return append(dst, "null"...)
	}
	// Try svarint (most common scalar: Int, count, etc.)
	v, n := codec.ReadSvarint(data, 0)
	if n > 0 && n == len(data) {
		return strconv.AppendInt(dst, v, 10)
	}
	// Try bool (1 byte)
	if len(data) == 1 {
		if data[0] == 1 {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	}
	// Try fixed64 (float, 8 bytes)
	if len(data) == 8 {
		fv, fn := codec.ReadFixed64(data, 0)
		if fn == 8 {
			return strconv.AppendFloat(dst, fv, 'f', -1, 64)
		}
	}
	// Try string (length-prefixed)
	sv, sn := codec.ReadString(data, 0)
	if sn > 0 && sn == len(data) {
		return appendJSONString(dst, sv)
	}
	// Fallback: raw number
	return strconv.AppendInt(dst, v, 10)
}

// appendJSONString appends a JSON-escaped string.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
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
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0')
				dst = append(dst, "0123456789abcdef"[c>>4])
				dst = append(dst, "0123456789abcdef"[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
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
		}
	}
	enc.WriteEnd()
	return enc.Bytes()
}

// Suppress unused import warning
var _ = math.Float64frombits
var _ = fmt.Sprintf
