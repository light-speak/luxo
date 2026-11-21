package api

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Frozen before collection payload preflight, including the former UUID loop.
func readBinaryListParamBeforePayload(buf []byte, off int, meta ParamMeta) (any, int, error) {
	count, n := codec.ReadArrayHeader(buf, off)
	if n == 0 {
		return nil, 0, fmt.Errorf("param %s: invalid array header (limit %d)", meta.Name, codec.MaxArrayElements)
	}
	start := off
	off += n
	var value any
	var end int
	var err error
	switch meta.Type {
	case "Int":
		value, end, err = readBinaryIntArray(buf, off, count, meta, false)
	case "Duration":
		value, end, err = readBinaryIntArray(buf, off, count, meta, true)
	case "Float":
		value, end, err = readBinaryFloatArray(buf, off, count, meta)
	case "DateTime":
		value, end, err = readBinaryDateTimeArray(buf, off, count, meta)
	case "String", "Enum":
		value, end, err = readBinaryStringArray(buf, off, count, meta, false)
	case "Decimal":
		value, end, err = readBinaryStringArray(buf, off, count, meta, true)
	case "Boolean":
		value, end, err = readBinaryBoolArray(buf, off, count, meta)
	case "UUID":
		value, end, err = readBinaryUUIDArrayBeforePayload(buf, off, count, meta)
	case "Bytes":
		value, end, err = readBinaryBytesArray(buf, off, count, meta, false)
	case "JSON":
		value, end, err = readBinaryBytesArray(buf, off, count, meta, true)
	case "Model":
		value, end, err = readBinaryMessageArray(buf, off, count, meta)
	default:
		return nil, 0, fmt.Errorf("param %s: unknown list type %s", meta.Name, meta.Type)
	}
	if err != nil {
		return nil, 0, err
	}
	return value, end - start, nil
}

func readBinaryUUIDArrayBeforePayload(buf []byte, off, count int, meta ParamMeta) (any, int, error) {
	values := make([]uuid.UUID, count)
	for i := range values {
		value, consumed := codec.ReadUUID(buf, off)
		if consumed == 0 {
			return nil, 0, fmt.Errorf("param %s: truncated uuid array", meta.Name)
		}
		values[i], off = uuid.UUID(value), off+consumed
	}
	return values, off, nil
}
