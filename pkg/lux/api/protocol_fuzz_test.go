package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json/jsontext"
	"testing"
	"unicode/utf8"
)

// The reference uses the standard library's unsigned reader, not the Luxo codec.
func FuzzDecodeBinaryError(f *testing.F) {
	for _, seed := range [][]byte{
		{}, {0}, {1, 0xcc, 6, 0}, {1, 0x80}, {7, 0},
		{1, 0xcc, 6, 2, 0, 3, 0, 0},
		{1, 0xcc, 6, 2, 1, 0xff, 3, 0, 0},
		mustBinaryError(f, nil, wireError{Code: 422, Name: "Invalid", Message: "精度 café",
			TraceID: "trace", Data: []byte(`{"n":18446744073709551616}`), Cause: "cause"}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		expected, valid := referenceBinaryError(data)
		actual, err := DecodeBinaryError(data, 500)
		if (err == nil) != valid {
			t.Fatalf("acceptance mismatch for %x: reference=%t actual=%v", data, valid, err)
		}
		if !valid {
			if actual != nil {
				t.Fatal("invalid envelope exposed a partial result")
			}
			return
		}
		if actual.Code != expected.Code || actual.Name != expected.Name || actual.Message != expected.Message ||
			actual.TraceID != expected.TraceID || actual.Cause != expected.Cause || !bytes.Equal(actual.Data, expected.Data) {
			t.Fatalf("value mismatch for %x: %+v / %+v", data, actual, expected)
		}
	})
}

func referenceBinaryError(data []byte) (BinaryError, bool) {
	var result BinaryError
	var seen uint8
	for len(data) > 0 {
		field, length := binary.Uvarint(data)
		if length <= 0 || field > 6 {
			return result, false
		}
		data = data[length:]
		if field == 0 {
			return result, len(data) == 0 && seen&14 == 14
		}
		bit := uint8(1 << field)
		if seen&bit != 0 {
			return result, false
		}
		seen |= bit
		var valid bool
		data, valid = referenceBinaryErrorField(&result, field, data)
		if !valid {
			return result, false
		}
	}
	return result, false
}

func referenceBinaryErrorField(result *BinaryError, field uint64, data []byte) ([]byte, bool) {
	value, length := binary.Uvarint(data)
	if length <= 0 {
		return nil, false
	}
	data = data[length:]
	if field == 1 {
		code := int64(value>>1) ^ -int64(value&1)
		result.Code = int(code)
		return data, int64(result.Code) == code
	}
	if value > uint64(len(data)) || !utf8.Valid(data[:value]) {
		return nil, false
	}
	text := data[:value]
	switch field {
	case 2:
		result.Name = string(text)
	case 3:
		result.Message = string(text)
	case 4:
		result.TraceID = string(text)
	case 5:
		if !jsontext.Value(text).IsValid(jsontext.AllowDuplicateNames(true)) {
			return nil, false
		}
		result.Data = text
	case 6:
		result.Cause = string(text)
	}
	return data[value:], true
}
