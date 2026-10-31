package api

import (
	"encoding/json"
	"testing"
)

func BenchmarkWSJSONSequence(b *testing.B) {
	for _, token := range []string{"1", "9007199254740993", "9223372036854775807"} {
		raw := []byte(token)
		b.Run(token+"/before", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var id int64
				if err := json.Unmarshal(raw, &id); err != nil || id < 0 {
					b.Fatal("invalid benchmark fixture")
				}
			}
		})
		b.Run(token+"/after", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := parseWSJSONSequence(raw); !ok {
					b.Fatal("valid fixture rejected")
				}
			}
		})
	}
	b.Run("18446744073709551615/after", func(b *testing.B) {
		raw := []byte("18446744073709551615")
		b.ReportAllocs()
		for b.Loop() {
			if id, ok := parseWSJSONSequence(raw); !ok || id != ^uint64(0) {
				b.Fatal("full-width fixture changed")
			}
		}
	})
}
