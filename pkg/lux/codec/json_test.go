package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func TestJSONWireConformance(t *testing.T) {
	data, err := os.ReadFile("testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		JSONUnicodeCases []struct {
			Name, Raw string
			Valid     bool
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.JSONUnicodeCases) == 0 {
		t.Fatal("missing Unicode fixtures")
	}
	for _, item := range fixture.JSONUnicodeCases {
		t.Run(item.Name, func(t *testing.T) { checkJSONWire(t, []byte(item.Raw), item.Valid) })
	}
	for _, raw := range [][]byte{nil, {}, []byte("{"), []byte("null false"), []byte("\"\xff\""), []byte("NaN")} {
		checkJSONWire(t, raw, false)
	}
	for _, raw := range []string{"null", " {} ", "123456789012345678901234567890", "-0.000", "1e-9999", "1e9999"} {
		checkJSONWire(t, []byte(raw), true)
	}
}

func checkJSONWire(t *testing.T, raw []byte, valid bool) {
	t.Helper()
	buf := append(make([]byte, 0, 4096), "prefix"...)
	got, err := AppendJSON(buf, raw)
	encoder := Encoder{buf: append(make([]byte, 0, 4096), "prefix"...)}
	writeErr := encoder.WriteJSON(raw)
	if valid {
		want := AppendBytes(buf, raw)
		if err != nil || writeErr != nil || !bytes.Equal(got, want) || !bytes.Equal(encoder.Bytes(), want) {
			t.Fatalf("valid JSON changed: %q / %v / %v", raw, err, writeErr)
		}
	} else if !errors.Is(err, ErrInvalidJSON) || !errors.Is(writeErr, ErrInvalidJSON) || string(got) != "prefix" || string(encoder.Bytes()) != "prefix" {
		t.Fatalf("invalid append changed prefix: %q / %v / %v", raw, err, writeErr)
	}
	dec := NewDecoder(AppendBytes(nil, raw))
	value := dec.ReadJSON()
	if valid {
		if err := dec.Finish(); err != nil || !bytes.Equal(value, raw) {
			t.Fatalf("valid read changed: %q / %v", raw, err)
		}
	} else if !errors.Is(dec.Err(), ErrInvalidJSON) || value != nil {
		t.Fatalf("invalid read accepted: %q / %v", raw, dec.Err())
	}
	// Bytes are opaque, even when the contents resemble malformed JSON.
	opaque := NewDecoder(AppendBytes(nil, raw))
	if !bytes.Equal(opaque.ReadBytes(), raw) || opaque.Finish() != nil {
		t.Fatal("opaque Bytes changed")
	}
}

func TestJSONNullableAndStickyFailure(t *testing.T) {
	for _, raw := range [][]byte{nil, {0}, {1, 1}, {2}, AppendBytes([]byte{1}, []byte("null")), AppendBytes([]byte{1}, nil)} {
		dec := NewDecoder(raw)
		got := dec.ReadJSONPtr()
		switch {
		case bytes.Equal(raw, []byte{0}):
			if got != nil || dec.Finish() != nil {
				t.Fatal("absent nullable JSON rejected")
			}
		case bytes.Equal(raw, []byte{1, 4, 'n', 'u', 'l', 'l'}):
			if got == nil || string(*got) != "null" || dec.Finish() != nil {
				t.Fatal("present JSON null changed")
			}
		default:
			if got != nil || dec.Err() == nil {
				t.Fatalf("invalid nullable JSON accepted: %x", raw)
			}
		}
	}
	first := errors.New("first")
	for _, read := range []func(*Decoder){func(d *Decoder) { d.ReadJSON() }, func(d *Decoder) { d.ReadJSONPtr() }} {
		dec := NewDecoder([]byte{0xff})
		dec.Fail(first)
		read(dec)
		if dec.Err() != first || dec.Offset() != 0 {
			t.Fatal("first failure or position overwritten")
		}
	}
	dec := NewDecoder([]byte{3, '{'})
	if dec.ReadJSON() != nil || dec.Err() == nil {
		t.Fatal("truncated JSON bytes accepted")
	}
}

func TestJSONReadBorrowsInput(t *testing.T) {
	raw := []byte(`{"n":123456789012345678901234567890,"f":1e-9999}`)
	data := AppendBytes(nil, raw)
	dec := NewDecoder(data)
	got := dec.ReadJSON()
	if len(got) != len(raw) || &got[0] != &data[len(data)-len(raw)] {
		t.Fatal("reader copied raw JSON")
	}
}

func FuzzJSONWire(f *testing.F) {
	for _, raw := range []string{"null", `"\uD800"`, `"\uD800\uDC00"`, "{}", "1e-9999", `{"n":123456789012345678901234567890}`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		checkJSONWire(t, raw, jsonutil.ValidUTF8JSON(raw))
	})
}
