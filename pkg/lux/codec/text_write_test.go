package codec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/light-speak/luxo/internal/testutil"
)

func TestStringWritersRejectWithoutPublishingPartialFields(t *testing.T) {
	for _, raw := range invalidTextVectors() {
		value := "valid" + string(raw) + "tail"
		for name, write := range map[string]func(*Encoder) error{
			"value":    func(e *Encoder) error { return e.WriteString(value) },
			"field":    func(e *Encoder) error { return e.WriteFieldString(256, value) },
			"nullable": func(e *Encoder) error { return e.WriteFieldStringPtr(256, &value) },
			"array":    func(e *Encoder) error { return e.WriteFieldStringArray(256, []string{"first", value, "last"}) },
		} {
			t.Run(fmt.Sprintf("%s/%x", name, raw), func(t *testing.T) {
				for _, capacity := range []int{2, 4096} {
					encoder := Encoder{buf: make([]byte, 0, capacity)}
					encoder.WriteFieldInt(1, 42)
					before := bytes.Clone(encoder.Bytes())
					if err := write(&encoder); !errors.Is(err, ErrInvalidUTF8) {
						t.Fatalf("invalid text error: %v", err)
					}
					if !bytes.Equal(before, encoder.Bytes()) {
						t.Fatalf("invalid text published a partial field: %x -> %x", before, encoder.Bytes())
					}
				}
			})
		}
	}
}

func TestAppendStringAtomicUTF8(t *testing.T) {
	for _, raw := range invalidTextVectors() {
		prefix := make([]byte, 3, 4096)
		copy(prefix, "old")
		got, err := AppendString(prefix, string(raw))
		if !errors.Is(err, ErrInvalidUTF8) || string(got) != "old" || &got[0] != &prefix[0] {
			t.Fatalf("invalid append changed destination: %x / %v", got, err)
		}
	}
}

func TestStringWritersPreserveWireAndEmptyValues(t *testing.T) {
	for _, value := range []string{"", "hello", "東京", "\x00\ufffd\ufeff", "\U0010ffff"} {
		encoder := Encoder{buf: make([]byte, 0, 256)}
		for _, err := range []error{encoder.WriteString(value), encoder.WriteFieldString(1, value),
			encoder.WriteFieldStringPtr(2, &value), encoder.WriteFieldStringPtr(3, nil),
			encoder.WriteFieldStringArray(4, []string{value, ""}), encoder.WriteFieldStringArray(5, nil)} {
			if err != nil {
				t.Fatal(err)
			}
		}
		want := AppendBytes(nil, []byte(value))
		want = AppendBytes(append(want, 1), []byte(value))
		want = AppendBytes(append(want, 2, 1), []byte(value))
		want = AppendBytes(append(want, 3, 0, 4, 2), []byte(value))
		want = append(want, 0, 5, 0)
		if !bytes.Equal(want, encoder.Bytes()) {
			t.Fatalf("valid wire changed: %x / %x", want, encoder.Bytes())
		}
		var writer ColumnarWriter
		writer.SetCount(2)
		for _, err := range []error{writer.WriteColumnString(1, []string{value, ""}), writer.WriteColumnStringPtr(2, []*string{nil, &value})} {
			if err != nil {
				t.Fatal(err)
			}
		}
		if writer.totalStringLen != len(value)*2 {
			t.Fatal("incorrect arena size")
		}
		writer.Reset()
		if err := writer.WriteColumnString(1, nil); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteColumnStringPtr(2, nil); err != nil {
			t.Fatal(err)
		}
		if writer.totalStringLen != 0 {
			t.Fatal("empty columns changed arena size")
		}
	}
}

func TestStringEncoderNoAllocation(t *testing.T) {
	encoder := Encoder{buf: make([]byte, 0, 4096)}
	for _, value := range []string{"", "ASCII", "東京", "bad\xff"} {
		values := []string{"first", value}
		allocations := testing.AllocsPerRun(100, func() {
			encoder.Reset()
			_ = encoder.WriteString(value)
			_ = encoder.WriteFieldString(1, value)
			_ = encoder.WriteFieldStringPtr(2, &value)
			_ = encoder.WriteFieldStringArray(3, values)
		})
		if allocations != 0 {
			t.Fatalf("String encoder allocated: %v", allocations)
		}
	}
}

func FuzzStringWriter(f *testing.F) {
	for _, value := range []string{"", "hello", "東京\ufeff", "\xff", "\xed\xa0\x80"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		prefix := []byte{1, 42}
		encoded, err := AppendString(bytes.Clone(prefix), value)
		if !utf8.ValidString(value) {
			if !errors.Is(err, ErrInvalidUTF8) || !bytes.Equal(encoded, prefix) {
				t.Fatalf("invalid append: %x / %v", encoded, err)
			}
			return
		}
		want := AppendBytes(prefix, []byte(value))
		if err != nil || !bytes.Equal(encoded, want) {
			t.Fatalf("valid append: %x / %x / %v", encoded, want, err)
		}
	})
}

func TestStringColumnsRejectWithoutChangingArena(t *testing.T) {
	for _, raw := range invalidTextVectors() {
		value, good := string(raw), "first"
		for name, write := range map[string]func(*ColumnarWriter) error{
			"values":   func(w *ColumnarWriter) error { return w.WriteColumnString(256, []string{good, value}) },
			"nullable": func(w *ColumnarWriter) error { return w.WriteColumnStringPtr(256, []*string{&good, &value}) },
		} {
			t.Run(fmt.Sprintf("%s/%x", name, raw), func(t *testing.T) {
				var writer ColumnarWriter
				writer.SetCount(2)
				if err := writer.WriteColumnString(1, []string{"one", "two"}); err != nil {
					t.Fatal(err)
				}
				before := bytes.Clone(testutil.Bytes(t)(writer.Bytes()))
				arena, columns := writer.totalStringLen, len(writer.columns)
				if err := write(&writer); !errors.Is(err, ErrInvalidUTF8) {
					t.Fatalf("invalid text error: %v", err)
				}
				if writer.totalStringLen != arena || len(writer.columns) != columns || !bytes.Equal(before, testutil.Bytes(t)(writer.Bytes())) {
					t.Fatal("failed text write changed columns or arena size")
				}
			})
		}
	}
}
