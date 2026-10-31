package rpc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/json/jsontext"
	stderrors "errors"
	"math"
	"testing"
	"unicode/utf8"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func FuzzRPCErrorEnvelope(f *testing.F) {
	fields := rpcErrorFields(422, "PrecisionRejected", "error.precision")
	for _, raw := range []string{rpcPrecisionJSON, "null", "1e9999", "", "{", `{"n":"\ud800"}`} {
		data := append([]byte{4}, binary.AppendUvarint(nil, uint64(len(raw)))...)
		data = append(data, raw...)
		body := rpcErrorBody(fields[0], fields[1], fields[2], data)
		f.Add(body)
		f.Add(rpcErrorBody(fields[0], fields[1], fields[2], data, []byte{6, 1}))
		for size := range len(body) {
			f.Add(body[:size])
		}
	}
	for _, overrides := range []string{`{"label":"精度 café"}`, `{"label":null}`, `{"label":3}`, `{"label":"\ud800"}`, `{"n":null,"n":null}`} {
		text := append([]byte{5}, binary.AppendUvarint(nil, uint64(len(overrides)))...)
		text = append(text, overrides...)
		data := append([]byte{4}, binary.AppendUvarint(nil, uint64(len(rpcPrecisionJSON)))...)
		data = append(data, rpcPrecisionJSON...)
		f.Add(rpcErrorBody(text, fields[2], data, fields[0], fields[1]))
	}
	for _, code := range []int64{422, 503, math.MinInt64, math.MaxInt64} {
		fields := rpcErrorFields(code, "Rejected", "精度 café")
		body := rpcErrorBody(fields[:]...)
		f.Add(body)
		f.Add(rpcErrorBody(fields[2], fields[0], fields[1]))
		f.Add(rpcErrorBody(fields[0], fields[1], fields[2], fields[0]))
		for length := range len(body) {
			f.Add(body[:length])
		}
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	for _, field := range []uint64{1 << 32, 1<<32 + 1, 1<<32 + 2, 1<<32 + 3, math.MaxUint64} {
		f.Add(binary.AppendUvarint(nil, field))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		original := bytes.Clone(body)
		want, valid := referenceRPCError(body)
		err := decodeError(body)
		got, decoded := stderrors.AsType[*luxerrors.AppError](err)
		if !bytes.Equal(body, original) || err == nil || valid != decoded {
			t.Fatalf("acceptance/input mismatch: %x, valid=%t, decoded=%+v, err=%v", body, valid, got, err)
		}
		if !valid {
			return
		}
		if want.Code >= 500 || want.Internal {
			want.Message = "error.internal"
			want.Internal = true
			want.Data = nil
		}
		if got.Code != want.Code || got.Name != want.Name || got.Message != want.Message || got.Internal != want.Internal {
			t.Fatalf("value changed: got %+v, want %+v", got, want)
		}
		if (got.Data == nil) != (want.Data == nil) {
			t.Fatal("data presence changed")
		}
		if got.Data != nil {
			x, xerr := got.Data.AppendJSON(nil)
			y, yerr := want.Data.AppendJSON(nil)
			if xerr != nil || yerr != nil || !bytes.Equal(x, y) {
				t.Fatalf("data changed: %s != %s", x, y)
			}
		}
	})
}

// Independent reference uses encoding/binary, not the production codec decoder.
func referenceRPCError(body []byte) (luxerrors.AppError, bool) {
	var value luxerrors.AppError
	var data, text []byte
	var seen uint8
	for len(body) > 0 {
		field, size := binary.Uvarint(body)
		if size <= 0 {
			return value, false
		}
		body = body[size:]
		if field == 0 {
			valid := len(body) == 0 && seen&0b1110 == 0b1110 && referenceRPCDataValid(data, text)
			if data != nil {
				value.Data = rpcNativeData{raw: string(data)}
			}
			return value, valid
		}
		if field > 6 || seen&(1<<field) != 0 {
			return value, false
		}
		seen |= 1 << field
		if field == 6 {
			if len(body) == 0 || body[0] > 1 {
				return value, false
			}
			value.Internal, body = body[0] == 1, body[1:]
			continue
		}
		if field == 1 {
			code, size := binary.Varint(body)
			if size <= 0 || int64(int(code)) != code {
				return value, false
			}
			value.Code, body = int(code), body[size:]
			continue
		}
		content, rest, ok := referenceRPCErrorText(body)
		if !ok {
			return value, false
		}
		if field == 2 {
			value.Name = content
		} else if field == 3 {
			value.Message = content
		} else if field == 4 {
			data = []byte(content)
		} else {
			text = []byte(content)
		}
		body = rest
	}
	return value, false
}

func referenceRPCDataValid(data, text []byte) bool {
	if data == nil {
		return text == nil
	}
	if !jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true)) {
		return false
	}
	if text == nil {
		return true
	}
	if !jsontext.Value(text).IsValid() {
		return false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(text, &values) != nil || values == nil {
		return false
	}
	for _, value := range values {
		if !bytes.Equal(value, []byte("null")) && (len(value) == 0 || value[0] != '"') {
			return false
		}
	}
	return true
}

func referenceRPCErrorText(body []byte) (string, []byte, bool) {
	length, size := binary.Uvarint(body)
	if size <= 0 || length > uint64(len(body)-size) {
		return "", nil, false
	}
	end := size + int(length)
	text := body[size:end]
	return string(text), body[end:], utf8.Valid(text)
}
