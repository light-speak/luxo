package schema

// Frozen pre-fix scalar converters. Keep the original JSON validation, framing,
// and error checks; only complete, valid payloads are timed against this baseline.
import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"strconv"
	"time"
)

func BinaryScalarToJSONBeforeRawJSON(dst []byte, data []byte, typeName string) ([]byte, error) {
	if len(data) == 0 && typeName == "" {
		return append(dst, "null"...), nil
	}
	start := len(dst)
	dec := codec.NewDecoder(data)
	dst = appendScalarValueJSONBeforeRawJSON(dst, dec, typeName)
	if err := dec.Finish(); err != nil {
		return dst[:start], err
	}
	return dst, nil
}

func BinaryScalarListToJSONBeforeRawJSON(dst []byte, data []byte, typeName string) ([]byte, error) {
	start := len(dst)
	dec := codec.NewDecoder(data)
	count := dec.ReadArrayLength()
	dst = append(dst, '[')
	for i := 0; i < count && dec.Err() == nil; i++ {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendScalarValueJSONBeforeRawJSON(dst, dec, typeName)
	}
	if err := dec.Finish(); err != nil {
		return dst[:start], err
	}
	return append(dst, ']'), nil
}

func appendScalarValueJSONBeforeRawJSON(dst []byte, dec *codec.Decoder, typeName string) []byte {
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
		t := time.Unix(dec.ReadInt(), 0).UTC()
		dst = append(dst, '"')
		dst = t.AppendFormat(dst, time.RFC3339Nano)
		return append(dst, '"')
	case "UUID":
		return appendUUIDString(dst, dec.ReadUUID())
	case "Bytes":
		return appendBinaryBlobJSONBeforeRawJSON(dst, dec.ReadBytes(), &Field{Type: FieldBytes})
	case "JSON":
		return appendBinaryBlobJSONBeforeRawJSON(dst, dec.ReadBytes(), &Field{Type: FieldJSON})
	case "String", "Decimal":
		return appendJSONString(dst, dec.ReadString())
	default:
		dec.Fail(fmt.Errorf("schema: unsupported scalar type %q", typeName))
		return dst
	}
}

func appendBinaryBlobJSONBeforeRawJSON(dst, blob []byte, f *Field) []byte {
	if f.Type == FieldJSON {
		if json.Valid(blob) {
			return append(dst, blob...)
		}
		return append(dst, "null"...)
	}
	dst = append(dst, '"')
	dst = append(dst, base64.StdEncoding.EncodeToString(blob)...)
	return append(dst, '"')
}
