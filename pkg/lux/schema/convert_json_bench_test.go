package schema

import (
	"bytes"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func BenchmarkRawJSONValidation(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"small", `{"ok":true}`},
		{"precision", `{"max":9223372036854775807,"min":-9223372036854775808,"wide":123456789012345678901234567890.123456789,"text":"精度 café","zero":-0,"exp":1e9999,"array":[null,1e-9999]}`},
		{"72KiB", `"` + strings.Repeat("sensitive", 8192) + `"`},
	} {
		data := codec.AppendBytes(nil, []byte(sample.raw))
		b.Run(sample.name, func(b *testing.B) { benchmarkRawJSONConverters(b, data, "JSON", false) })
		if sample.name == "small" {
			list := codec.AppendArrayHeader(nil, 128)
			for range 128 {
				list = append(list, data...)
			}
			b.Run("list128", func(b *testing.B) { benchmarkRawJSONConverters(b, list, "JSON", true) })
		}
	}
	b.Run("Int_control", func(b *testing.B) { benchmarkRawJSONConverters(b, []byte{2}, "Int", false) })
	b.Run("Bytes_control", func(b *testing.B) {
		benchmarkRawJSONConverters(b, codec.AppendBytes(nil, []byte("abcd")), "Bytes", false)
	})
}

func benchmarkRawJSONConverters(b *testing.B, data []byte, kind string, list bool) {
	before, after := BinaryScalarToJSONBeforeRawJSON, BinaryScalarToJSON
	if list {
		before, after = BinaryScalarListToJSONBeforeRawJSON, BinaryScalarListToJSON
	}
	want, err := before(nil, data, kind)
	got, currentErr := after(nil, data, kind)
	if err != nil || currentErr != nil || !bytes.Equal(want, got) {
		b.Fatalf("invalid frozen baseline: %v / %v", err, currentErr)
	}
	for _, version := range []struct {
		name    string
		convert func([]byte, []byte, string) ([]byte, error)
	}{{"before", before}, {"checked", after}} {
		b.Run(version.name, func(b *testing.B) {
			dst := make([]byte, 0, len(want))
			b.ReportAllocs()
			for b.Loop() {
				dst, err = version.convert(dst[:0], data, kind)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
