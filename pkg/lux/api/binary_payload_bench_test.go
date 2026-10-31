package api

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

var payloadListValue any
var payloadListError error

func TestBinaryListPayloadDateTimeFixture(t *testing.T) {
	meta := ParamMeta{Name: "values", Type: "DateTime", IsList: true, FieldID: 1}
	for _, count := range []int{0, 1, 128} {
		data := binaryListPayloadFixture("DateTime", count)
		for _, read := range []func([]byte, int, ParamMeta) (any, int, error){readBinaryListParamBeforePayload, readBinaryListParam} {
			value, consumed, err := read(data, 0, meta)
			if err != nil || consumed != len(data) {
				t.Fatalf("count=%d: consumed=%d/%d, err=%v", count, consumed, len(data), err)
			}
			values, ok := value.([]time.Time)
			if !ok || len(values) != count {
				t.Fatalf("unexpected datetime list: %T", value)
			}
			for _, instant := range values {
				if !instant.Equal(time.Unix(42, 123456789)) {
					t.Fatalf("datetime fixture lost precision: %v", instant)
				}
			}
		}
	}
}

func BenchmarkBinaryListPayload(b *testing.B) {
	for _, kind := range []string{"Int", "Duration", "Float", "DateTime", "String", "Enum", "Decimal", "Boolean", "UUID", "Bytes", "JSON", "Model"} {
		meta := ParamMeta{Name: "values", Type: kind, TypeName: "Sample", IsList: true, FieldID: 1}
		for _, count := range []int{0, 1, 128} {
			data := binaryListPayloadFixture(kind, count)
			var expected []byte
			for version, read := range []func([]byte, int, ParamMeta) (any, int, error){readBinaryListParamBeforePayload, readBinaryListParam} {
				value, consumed, err := read(data, 0, meta)
				if err != nil || consumed != len(data) {
					b.Fatalf("invalid fixture: kind=%s consumed=%d err=%v", kind, consumed, err)
				}
				encoded, err := encodePayloadBenchmarkList(meta, value)
				if err != nil {
					b.Fatal(err)
				}
				if version == 0 {
					expected = bytes.Clone(encoded)
				} else if !bytes.Equal(expected, encoded) {
					b.Fatal("comparison changed valid bytes")
				}
				b.Run(fmt.Sprintf("%s/count=%d/version=%d", kind, count, version), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						payloadListValue, _, payloadListError = read(data, 0, meta)
					}
				})
			}
		}
	}
}

func encodePayloadBenchmarkList(meta ParamMeta, value any) ([]byte, error) {
	var encoded codec.Encoder
	err := encodeBinaryParam(&encoded, meta, value)
	return encoded.Bytes(), err
}

func binaryListPayloadFixture(kind string, count int) []byte {
	data := codec.AppendArrayHeader(nil, count)
	for range count {
		switch kind {
		case "Int", "Duration":
			data = codec.AppendSvarint(data, 42)
		case "DateTime":
			data = codec.AppendSvarint(data, 42)
			data = codec.AppendVarint(data, 123456789)
		case "Float":
			data, _ = codec.AppendFixed64(data, 1.25)
		case "String", "Enum", "Decimal":
			data = codec.AppendBytes(data, []byte("42"))
		case "Boolean":
			data = codec.AppendBool(data, true)
		case "UUID":
			data = codec.AppendUUID(data, [16]byte{42})
		case "Model":
			data = codec.AppendBytes(data, []byte{0, 0})
		default:
			data = codec.AppendBytes(data, []byte("42"))
		}
	}
	return data
}
