package jsonutil

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

var unicodeValidationSink bool

// Frozen pre-Unicode policy: syntax and raw UTF-8 were checked, but escaped
// lone surrogates were accepted. Only valid payloads are compared for cost.
func validJSONBeforeUnicode(raw []byte) bool {
	return utf8.Valid(raw) && json.Valid(raw)
}

func BenchmarkJSONUnicodeValidation(b *testing.B) {
	for _, sample := range []struct{ name, raw string }{
		{"small", `{"ok":true}`},
		{"precision", `{"max":9223372036854775807,"min":-9223372036854775808,"wide":123456789012345678901234567890.123456789,"text":"精度 café","zero":-0,"exp":1e9999,"array":[null,1e-9999]}`},
		{"ascii72KiB", `"` + strings.Repeat("sensitive", 8192) + `"`},
		{"unicode72KiB", `"` + strings.Repeat("中文", 12288) + `"`},
		{"escaped128", `"` + strings.Repeat(`\uD800\uDC00`, 128) + `"`},
		{"literalSlashes128", `"` + strings.Repeat(`\\uD800`, 128) + `"`},
		{"list128", `[` + strings.Repeat(`{"name":"café","id":42},`, 127) + `{"name":"café","id":42}]`},
	} {
		raw := []byte(sample.raw)
		if !validJSONBeforeUnicode(raw) || !ValidUTF8JSON(raw) {
			b.Fatalf("invalid fixture %s", sample.name)
		}
		for _, version := range []struct {
			name     string
			validate func([]byte) bool
		}{{"before", validJSONBeforeUnicode}, {"syntaxOnly", json.Valid}, {"strict", ValidUTF8JSON}} {
			b.Run(sample.name+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					unicodeValidationSink = version.validate(raw)
				}
			})
		}
	}
}
