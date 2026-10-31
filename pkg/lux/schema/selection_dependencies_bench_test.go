package schema

import (
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

// These are standalone plan costs, not whole-request or cache-hit deltas.
func BenchmarkSelectionDependencies(b *testing.B) {
	for _, width := range []int{1, 16, 128} {
		s, fields := dependencyBenchmarkSchema(width)
		plan, err := s.PrepareSelectionDependencies("Root")
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("selected=%d", width), func(b *testing.B) {
			dst := make([]ComputedSource, 0, width)
			b.ReportAllocs()
			for b.Loop() {
				got, err := plan.AppendSources(dst, fields)
				if err != nil || len(got) != width {
					b.Fatalf("got %d, %v", len(got), err)
				}
			}
		})
		b.Run(fmt.Sprintf("prepare=%d", width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.PrepareSelectionDependencies("Root"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func dependencyBenchmarkSchema(width int) (*Schema, []*selection.Field) {
	s := New()
	root := &Model{Name: "Root", Module: "test"}
	fields := make([]*selection.Field, width)
	for i := range width {
		name := fmt.Sprintf("Model%d", i)
		s.RegisterModel(&Model{Name: name, Module: "test", Fields: []Field{{ID: 1, Name: "id", Type: FieldInt}}})
		root.Fields = append(root.Fields, Field{ID: i + 1, Name: name, Type: FieldModel, TypeName: name, Relation: true})
		fields[i] = &selection.Field{Name: name, Children: []*selection.Field{{Name: "id"}}}
	}
	s.RegisterModel(root)
	return s, fields
}
