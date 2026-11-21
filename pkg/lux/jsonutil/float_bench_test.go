package jsonutil

import (
	"encoding/json"
	"testing"
)

var floatSink float64

// Compare identical valid tokens; the former conversion is not a correctness baseline.
func BenchmarkParseJSONFloat64(b *testing.B) {
	for _, token := range []string{"0", "-0.0", "1.25", "123456.789", "1.7976931348623157e308", "5e-324"} {
		raw := []byte(token)
		b.Run(token+"/before", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var value float64
				if err := json.Unmarshal(raw, &value); err != nil {
					b.Fatal(err)
				}
				floatSink = value
			}
		})
		b.Run(token+"/after", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, err := ParseFloat64(raw)
				if err != nil {
					b.Fatal(err)
				}
				floatSink = value
			}
		})
	}
}
