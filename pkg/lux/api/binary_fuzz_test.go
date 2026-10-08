package api

import (
	"github.com/light-speak/luxo/internal/testutil"

	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func FuzzDecodeFieldMask(f *testing.F) {
	s := schema.New()
	model := &schema.Model{Name: "Record", Fields: []schema.Field{
		{ID: 9, Name: "name"},
		{ID: 1, Name: "id"},
		{ID: 17, Name: "child", Relation: true, TypeName: "Record"},
	}}
	s.RegisterModel(model)
	scalar := testSelectionMask(testutil.Bytes(f)(codec.FieldMaskSet(nil, 9)))
	nested := testSelectionMask(testutil.Bytes(f)(codec.FieldMaskSet(nil, 17)), codec.SelectionMaskChild{FieldID: 17, Mask: scalar})
	for _, mask := range [][]byte{nil, scalar, nested, {1, 0}, {1, 0xff}, {1, 1, 3}} {
		f.Add(mask)
	}
	f.Fuzz(func(t *testing.T, mask []byte) {
		if len(mask) > maxFieldMaskSize {
			t.Skip()
		}
		original := bytes.Clone(mask)
		fields, err := decodeFieldMask(mask, model, s, 0)
		if !bytes.Equal(mask, original) {
			t.Fatal("selection decoding mutated request bytes")
		}
		if err != nil {
			if fields != nil {
				t.Fatal("selection decoding published a partial tree")
			}
			return
		}
		roundTrip, err := schema.SelectToFieldMask(fields, model, s)
		if err != nil {
			t.Fatalf("decoded selection cannot be encoded: %v", err)
		}
		decoded, err := decodeFieldMask(roundTrip, model, s, 0)
		if err != nil || selection.Format(fields) != selection.Format(decoded) {
			t.Fatalf("selection changed after round trip: %x => %x, %v", mask, roundTrip, err)
		}
	})
}

func FuzzJSONBase64Parameter(f *testing.F) {
	for _, raw := range []string{`""`, `"AQI="`, `"\u0041QI="`, `"AQ\r\nI="`, `"AQI=%%"`,
		`"\uD800"`, "null", "[]", "[1,2]", "true", `"AQI=" false`, "\"AQI=\n\"",
		`["AQI=",""]`, `["AQI=",null]`, `["\u0041QI=","AA=="]`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			return
		}
		original := bytes.Clone(raw)
		for _, list := range []bool{false, true} {
			want, valid := referenceJSONBase64(raw, list)
			got, present, err := decodeJSONBytesParam(raw, ParamMeta{Name: "value", Type: "Bytes", IsList: list})
			if !bytes.Equal(raw, original) {
				t.Fatal("Bytes parser or reference modified input")
			}
			if (err == nil) != valid || present != valid || (!valid && got != nil) {
				t.Fatalf("Bytes list=%t, %q: valid=%t, %v / %v", list, raw, valid, got, err)
			}
			if valid {
				var values [][]byte
				if list {
					values = got.([][]byte)
				} else {
					values = [][]byte{got.([]byte)}
				}
				if len(values) != len(want) {
					t.Fatal("Bytes list length changed")
				}
				for i := range values {
					if !bytes.Equal(values[i], want[i]) {
						t.Fatalf("Bytes changed %q", raw)
					}
				}
			}
		}
	})
}

func referenceJSONBase64(raw []byte, list bool) ([][]byte, bool) {
	if !jsonutil.ValidUTF8JSON(raw) {
		return nil, false
	}
	items := []json.RawMessage{raw}
	if list {
		items = nil // Do not let RawMessage reuse overwrite the input buffer.
		if err := json.Unmarshal(raw, &items); err != nil || items == nil {
			return nil, false
		}
	}
	values := make([][]byte, len(items))
	for i, item := range items {
		var text *string
		if err := json.Unmarshal(item, &text); err != nil || text == nil {
			return nil, false
		}
		value, err := base64.StdEncoding.DecodeString(*text)
		if err != nil {
			return nil, false
		}
		values[i] = value
	}
	return values, true
}

func FuzzRawJSONArray(f *testing.F) {
	for _, raw := range []string{
		`[]`, `[null,true,false,"text",{},[]]`, ` [ 9007199254740993, 1e400, 0.123456789012345678901 ] `,
		`[{"a":1,"a":2}]`, `[[[1]],{"text":"\\\"東京"}]`, "[\"\xff\"]", `["\ud800"]`,
		`null`, `true`, `{}`, `1`, `[1,]`, `[,1]`, `[01]`, `[1]false`, `[`, `[1`,
	} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		// The million-element cap has a deterministic test; bound fuzz memory.
		if len(raw) > 64*1024 {
			t.Skip()
		}
		var want []json.RawMessage
		referenceError := json.Unmarshal(raw, &want)
		var enc codec.Encoder
		err := encodeRawJSONArray(&enc, ParamMeta{Name: "values", Type: "JSON", IsList: true}, raw)
		valid := referenceError == nil && want != nil && jsontext.Value(raw).IsValid(jsontext.AllowDuplicateNames(true))
		if (err == nil) != valid {
			t.Fatalf("%q: got %v, reference %v", raw, err, referenceError)
		}
		if !valid {
			return
		}
		count, off := codec.ReadArrayHeader(enc.Bytes(), 0)
		if count != len(want) || off == 0 {
			t.Fatalf("count = %d, want %d", count, len(want))
		}
		for i := range want {
			value, n := codec.ReadBytes(enc.Bytes(), off)
			if n == 0 || !bytes.Equal(value, want[i]) {
				t.Fatalf("item %d changed: got %q, want %q", i, value, want[i])
			}
			off += n
		}
		if off != len(enc.Bytes()) {
			t.Fatal("unexpected trailing bytes")
		}
	})
}

func FuzzJSONIntegerParameter(f *testing.F) {
	for _, value := range []string{"0", "-0", "9007199254740993", "9223372036854775807", "-9223372036854775808", "9223372036854775808", "null", "true", "1.0", "1e0", "+1", "01", "-01", " \n-42\t"} {
		f.Add([]byte(value))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var want *int64
		referenceError := json.Unmarshal(raw, &want)
		got, err := jsonutil.ParseInt64(raw)
		valid := referenceError == nil && want != nil
		if (err == nil) != valid || (valid && got != *want) {
			t.Fatalf("%q: got (%d, %v), reference (%v, %v)", raw, got, err, want, referenceError)
		}
	})
}

func FuzzJSONIntegerArray(f *testing.F) {
	for _, value := range []string{"[]", "[0,-0,42]", " [ 9007199254740993, -9223372036854775808 ]\n", "[9223372036854775808]", "null", "[null]", "[true]", "[1.0]", "[1e0]", "[01]", "[1,]", "[,1]", "[[1]]", "[\"1,2\"]"} {
		f.Add([]byte(value))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		// The inclusive million-element cap has a dedicated deterministic test.
		if len(raw) > 64*1024 {
			t.Skip()
		}
		var want []*int64
		referenceError := json.Unmarshal(raw, &want)
		valid := referenceError == nil && want != nil
		for _, value := range want {
			valid = valid && value != nil
		}
		got, err := decodeJSONIntegerParam(raw, ParamMeta{Name: "values", Type: "Int", IsList: true})
		if (err == nil) != valid {
			t.Fatalf("%q: got error %v, reference valid %t (%v)", raw, err, valid, referenceError)
		}
		if !valid {
			return
		}
		values, ok := got.([]int64)
		if !ok || len(values) != len(want) {
			t.Fatalf("%q: got %T with unexpected shape", raw, got)
		}
		for i, value := range values {
			if value != *want[i] {
				t.Fatalf("%q: index %d: got %d, want %d", raw, i, value, *want[i])
			}
		}
	})
}
