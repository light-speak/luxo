package schema

import (
	"fmt"
	"slices"
	"testing"
)

// Keep declarations preconstructed and measure registration, not parsing or I/O.
func BenchmarkSchemaRegistration(b *testing.B) {
	for _, width := range []int{2, 16, 128} {
		decl := runtimeBenchmarkType(width)
		b.Run(fmt.Sprintf("fields=%d/type", width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s := New()
				if err := s.RegisterType(decl); err != nil {
					b.Fatal(err)
				}
				runtimeBenchmarkSchema = s
			}
		})
		b.Run(fmt.Sprintf("fields=%d/model", width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s := New()
				if err := s.RegisterModel(&Model{Name: decl.Name, Fields: slices.Clone(decl.Fields)}); err != nil {
					b.Fatal(err)
				}
				runtimeBenchmarkSchema = s
			}
		})
	}
	for _, count := range []int{1, 16, 128} {
		definitions := make([]*API, count)
		for i := range count {
			definitions[i] = &API{Name: fmt.Sprintf("read%d", i), ID: i + 1, Params: []Param{{Name: "id", ID: 1, Type: FieldInt}}}
		}
		b.Run(fmt.Sprintf("apis=%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s := New()
				for _, definition := range definitions {
					if err := s.RegisterAPI(definition); err != nil {
						b.Fatal(err)
					}
				}
				runtimeBenchmarkSchema = s
			}
		})
	}
}
