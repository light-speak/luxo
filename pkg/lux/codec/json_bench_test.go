package codec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

var jsonWireSink []byte

// Before is the generated JSON field's previous opaque-byte path, not an
// equivalent JSON validator. Both variants retain their original wire checks.
func BenchmarkRawJSONBoundary(b *testing.B) {
	for _, sample := range []struct {
		name, raw string
		count     int
	}{
		{"small", `{"ok":true}`, 1},
		{"precision", `{"max":9223372036854775807,"min":-9223372036854775808,"wide":123456789012345678901234567890.123456789,"text":"精度 café","zero":-0,"exp":1e9999,"array":[null,1e-9999]}`, 1},
		{"ascii72KiB", `"` + strings.Repeat("sensitive", 8192) + `"`, 1},
		{"unicode72KiB", `"` + strings.Repeat("中文", 12288) + `"`, 1},
		{"surrogate128", `"` + strings.Repeat(`\uD800\uDC00`, 128) + `"`, 1},
		{"small128", `{"ok":true}`, 128},
		{"precision128", `{"n":123456789012345678901234567890,"f":1e-9999}`, 128},
	} {
		raw := []byte(sample.raw)
		wire, err := AppendJSON(nil, raw)
		if err != nil || !bytes.Equal(wire, AppendBytes(nil, raw)) {
			b.Fatal("invalid benchmark fixture")
		}
		data := bytes.Repeat(wire, sample.count)
		for _, strict := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/read/strict=%t", sample.name, strict), func(b *testing.B) {
				benchmarkJSONRead(b, data, sample.count, strict)
			})
			b.Run(fmt.Sprintf("%s/append/strict=%t", sample.name, strict), func(b *testing.B) {
				benchmarkJSONAppend(b, raw, sample.count, strict)
			})
		}
	}
}

func benchmarkJSONRead(b *testing.B, data []byte, count int, strict bool) {
	b.ReportAllocs()
	for b.Loop() {
		dec := NewDecoder(data)
		for range count {
			if strict {
				jsonWireSink = dec.ReadJSON()
			} else {
				jsonWireSink = dec.ReadBytes()
			}
		}
		if err := dec.Finish(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkJSONAppend(b *testing.B, raw []byte, count int, strict bool) {
	buf := make([]byte, 0, (len(raw)+10)*count)
	b.ReportAllocs()
	for b.Loop() {
		buf = buf[:0]
		for range count {
			if strict {
				var err error
				buf, err = AppendJSON(buf, raw)
				if err != nil {
					b.Fatal(err)
				}
			} else {
				buf = AppendBytes(buf, raw)
			}
		}
		jsonWireSink = buf
	}
}
