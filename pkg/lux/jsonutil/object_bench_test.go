package jsonutil

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// The schema converter formerly used json.Unmarshal directly, without a UTF-8
// scan. Keep that faster but permissive baseline separate from HTTP parsing.
func parseObjectBeforeUnicode(raw []byte) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := json.Unmarshal(raw, &result)
	return result, err
}

func BenchmarkParseObjectUnicode(b *testing.B) {
	for _, fixture := range []struct{ name, raw string }{
		{"empty", `{}`},
		{"single", `{"id":42}`},
		{"small", `{"id":42,"name":"text"}`},
		{"escapedKeys", `{"\uD800\uDC00":1,"n\u0061me":"\uD800\uDC00"}`},
		{"unicode1026", `{"id":42,"name":"` + strings.Repeat("東京", 171) + `"}`},
		{"nested128", `{"items":[` + strings.Repeat(`{"id":42,"name":"東京"},`, 127) + `{"id":42,"name":"東京"}]}`},
		{"fields128", manyObjectFields(128)},
		{"longKey", `{"` + strings.Repeat("field", 40) + `":1}`},
		{"duplicate2048", `{` + strings.Repeat(`"field":0,`, 2047) + `"field":1}`},
	} {
		raw := []byte(fixture.raw)
		for _, version := range []struct {
			name  string
			parse func([]byte) (map[string]json.RawMessage, error)
		}{{"before", parseObjectBeforeUnicode}, {"strict", ParseObject}} {
			b.Run(fixture.name+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := version.parse(raw); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func manyObjectFields(count int) string {
	var raw strings.Builder
	raw.WriteByte('{')
	for i := range count {
		if i != 0 {
			raw.WriteByte(',')
		}
		raw.WriteString(`"field`)
		raw.WriteString(strconv.Itoa(i))
		raw.WriteString(`":42`)
	}
	raw.WriteByte('}')
	return raw.String()
}
