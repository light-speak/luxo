package errors

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// Freeze the old inline validation to isolate the shared-validator refactor.
func (w *DataWriter) jsonBeforeSharedValidation(value json.RawMessage) {
	if w.err != nil {
		return
	}
	if value == nil {
		w.Token("null")
		return
	}
	if !utf8.Valid(value) || !json.Valid(value) {
		w.err = stderrors.New("error data: invalid JSON")
		return
	}
	w.dst = append(w.dst, value...)
}

func BenchmarkErrorJSONSharedValidation(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"small", `{"ok":true}`},
		{"precision", `{"n":123456789012345678901234567890.123456789,"text":"精度 café","exp":1e9999}`},
		{"72KiB", `"` + strings.Repeat("sensitive", 8192) + `"`},
	} {
		for _, version := range []struct {
			name  string
			write func(*DataWriter, json.RawMessage)
		}{
			{"before", (*DataWriter).jsonBeforeSharedValidation}, {"shared", (*DataWriter).JSON},
		} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				raw, dst := []byte(sample.raw), make([]byte, 0, len(sample.raw))
				w := NewDataWriter(dst)
				b.ReportAllocs()
				for b.Loop() {
					w.dst = w.dst[:0]
					version.write(&w, raw)
					var err error
					dst, err = w.Result()
					if err != nil {
						b.Fatal(err)
					}
				}
				if !bytes.Equal(dst, raw) {
					b.Fatal("changed raw JSON")
				}
			})
		}
	}
}

// The map/json.Marshal baseline is test-only. Both paths construct and encode
// the same error data, including the full-width integer and escaped Unicode.
func BenchmarkStaticErrorData(b *testing.B) {
	for _, label := range []struct{ name, text string }{
		{"small", "精度 café\n"},
		{"4KiB", strings.Repeat("abcdefghijklmnop", 256)},
	} {
		b.Run(label.name, func(b *testing.B) {
			for _, former := range []bool{true, false} {
				name := "static"
				if former {
					name = "former_map"
				}
				b.Run(name, func(b *testing.B) { benchmarkErrorDataConstruction(b, label.text, former) })
			}
		})
	}
}

func benchmarkErrorDataConstruction(b *testing.B, label string, former bool) {
	var output []byte
	var err error
	b.ReportAllocs()
	for b.Loop() {
		if former {
			output, err = json.Marshal(map[string]any{"resource": label, "id": int64(math.MaxInt64)})
		} else {
			data := ResourceError{Resource: label, ID: IntID(math.MaxInt64)}
			output, err = data.AppendJSON(nil)
		}
	}
	if err != nil || !json.Valid(output) || !bytes.Contains(output, []byte("9223372036854775807")) {
		b.Fatalf("invalid error data: %s, %v", output, err)
	}
	var result struct {
		Resource string
		ID       int64
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Resource != label || result.ID != math.MaxInt64 {
		b.Fatal("baseline and static values differ")
	}
}
