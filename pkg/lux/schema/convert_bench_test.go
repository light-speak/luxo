package schema

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func BenchmarkColumnarJSONCapacity(b *testing.B) {
	for _, fields := range []int{1, 16, 128} {
		for _, rows := range []int{0, 1, 128} {
			for _, large := range []bool{false, true} {
				model, data, want := columnarJSONCapacityFixture(b, fields, rows, large)
				for _, reuse := range []bool{false, true} {
					b.Run(fmt.Sprintf("fields=%d/rows=%d/large=%t/reuse=%t", fields, rows, large, reuse), func(b *testing.B) {
						var dst []byte
						if reuse {
							dst = make([]byte, 0, len(want))
						}
						b.ReportAllocs()
						for b.Loop() {
							got, err := BinaryListToJSON(dst[:0], data, model)
							if err != nil || !bytes.Equal(got, want) {
								b.Fatalf("invalid output: %v, bytes=%d", err, len(got))
							}
							if reuse {
								dst = got
							}
						}
					})
				}
			}
		}
	}
}

func columnarJSONCapacityFixture(t testing.TB, fields, rows int, large bool) (*Model, []byte, []byte) {
	t.Helper()
	model := &Model{Name: "Capacity"}
	var writer codec.ColumnarWriter
	writer.SetCount(rows)
	for index := range fields {
		model.Fields = append(model.Fields, Field{ID: index + 1, Name: "field" + strconv.Itoa(index), Type: FieldInt})
		if rows > 0 {
			values := make([]int64, rows)
			for row := range values {
				values[row] = capacityInteger(index, row, large)
			}
			writer.WriteColumnInt(index+1, values)
		}
	}
	if err := New().RegisterModel(model); err != nil {
		t.Fatal(err)
	}
	data, err := writer.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return model, data, capacityJSONExpected(fields, rows, large)
}

func capacityInteger(field, row int, large bool) int64 {
	value := int64(field + row)
	if large {
		return math.MaxInt64 - value
	}
	return value
}

func capacityJSONExpected(fields, rows int, large bool) []byte {
	dst := []byte{'['}
	for row := range rows {
		if row > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		for field := range fields {
			if field > 0 {
				dst = append(dst, ',')
			}
			dst = strconv.AppendQuote(dst, "field"+strconv.Itoa(field))
			dst = append(dst, ':')
			dst = strconv.AppendInt(dst, capacityInteger(field, row, large), 10)
		}
		dst = append(dst, '}')
	}
	return append(dst, ']')
}

func BenchmarkStructuredJSONInput(b *testing.B) {
	for _, item := range []struct {
		name, token string
		kind        FieldType
	}{
		{"integer", "9223372036854775807", FieldInt},
		{"float", "1.25", FieldFloat},
		{"boolean", "true", FieldBool},
		{"string", `"東京"`, FieldString},
		{"escaped", `"\u6771\u4eac"`, FieldString},
		{"date", `"1969-12-31T23:59:59.999Z"`, FieldDateTime},
		{"uuid", `"550e8400-e29b-41d4-a716-446655440000"`, FieldUUID},
		{"decimal", `"12345678901234567890.12345678901234567890"`, FieldDecimal},
		{"bytes", `"AAECA/8="`, FieldBytes},
		{"escapedBytes", `"\u0041QI="`, FieldBytes},
		{"json", `{"n":123456789012345678901234567890,"f":0.12345678901234567890}`, FieldJSON},
		{"nested", `{"city":"東京"}`, FieldModel},
		{"chinese72KB", `"` + strings.Repeat("東京", 12000) + `"`, FieldString},
		{"bytes72KB", `"` + base64.StdEncoding.EncodeToString(make([]byte, 72000)) + `"`, FieldBytes},
	} {
		for _, count := range []int{-1, 1, 128} {
			if strings.HasSuffix(item.name, "72KB") && count != -1 {
				continue
			}
			s := newJSONEncodingSchema()
			s.RegisterType(&TypeDecl{Name: "Sample", Fields: []Field{
				{ID: 1, Name: "value", Type: item.kind, TypeName: "Address", IsList: count >= 0},
			}})
			token := item.token
			if count >= 0 {
				token = "[" + strings.Repeat(token+",", count-1) + token + "]"
			}
			raw := []byte(`{"value":` + token + `}`)
			b.Run(fmt.Sprintf("%s/%d", item.name, count), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.JSONToBinary("Sample", raw); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Isolate completion/error propagation from the existing field decoder. This
// is not a baseline for all converter changes or for an end-to-end HTTP request.
func jsonWithoutCompletionCheck(dst, data []byte, model *Model) []byte {
	return binaryToJSONFromDecoder(dst, codec.NewDecoder(data), model)
}

func BenchmarkJSONCompletionCheck(b *testing.B) {
	s := New()
	m := &Model{Name: "Sample", Fields: []Field{
		{ID: 1, Name: "id", Type: FieldInt},
		{ID: 2, Name: "value", Type: FieldFloat},
	}}
	s.RegisterModel(m)
	var enc codec.Encoder
	enc.WriteVarint(0)
	enc.WriteFieldInt(1, 1)
	if err := enc.WriteFieldFloat(2, 1.25); err != nil {
		b.Fatal(err)
	}
	enc.WriteEnd()
	data := enc.Bytes()
	baseline := jsonWithoutCompletionCheck(nil, data, m)
	current, err := BinaryToJSON(nil, data, m)
	if err != nil || !bytes.Equal(baseline, current) {
		b.Fatalf("invalid baseline: %q / %q, %v", baseline, current, err)
	}
	for _, count := range []int{1, 128} {
		for _, checked := range []bool{false, true} {
			b.Run(fmt.Sprintf("rows=%d/checked=%t", count, checked), func(b *testing.B) {
				dst := make([]byte, 0, count*64)
				b.ReportAllocs()
				for b.Loop() {
					dst = dst[:0]
					for range count {
						if checked {
							dst, err = BinaryToJSON(dst, data, m)
							if err != nil {
								b.Fatal(err)
							}
						} else {
							dst = jsonWithoutCompletionCheck(dst, data, m)
						}
					}
				}
			})
		}
	}
}
