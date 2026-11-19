package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func relationBenchmarkInput(b *testing.B, count int) *semantic.Result {
	b.Helper()
	file := &ast.File{Name: "bench.luxo"}
	for i := range count {
		source, target := "Source"+strconv.Itoa(i), "Target"+strconv.Itoa(i)
		file.Models = append(file.Models,
			&ast.ModelDecl{Name: source, Directives: []*ast.Directive{{Name: "noTime"}}, Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
				{Name: "targets", Type: &ast.TypeRef{Name: target, IsList: true}},
			}},
			&ast.ModelDecl{Name: target, Directives: []*ast.Directive{{Name: "noTime"}}, Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
				{Name: "source" + strconv.Itoa(i) + "Id", Type: &ast.TypeRef{Name: "Int"}},
			}},
		)
	}
	input := semantic.Analyze([]*ast.File{file})
	if len(input.Errors) != 0 {
		b.Fatal(input.Errors)
	}
	return input
}

func BenchmarkRelationLoaderGeneration(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			input, generator := relationBenchmarkInput(b, count), defaultGenerator()
			b.Run("metadata", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if len(generator.collectDataLoaderInputs(input, nil, nil).relations) != count {
						b.Fatal("missing relation")
					}
				}
			})
			b.Run("source", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if len(generator.generateDataLoaderFile(input, "generated", nil, nil)) == 0 {
						b.Fatal("missing loader source")
					}
				}
			})
		})
	}
}

func BenchmarkRelationResolverEmission(b *testing.B) {
	model := &ast.ModelDecl{Name: "Source"}
	for _, sample := range []struct {
		name                       string
		nullable, list, descendant bool
	}{
		{"nullable-single", true, false, true},
		{"required-list", false, true, true},
		{"leaf-list", true, true, false},
		{"nullable-list", true, true, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			relations := []Relation{{FieldName: "targets", TargetName: "Target", LocalKey: "groupKey", RemoteKey: "groupKey", KeyGoType: "string", FKNullable: sample.nullable, IsList: sample.list}}
			descendants := map[string]bool{"Target": sample.descendant}
			b.ReportAllocs()
			for b.Loop() {
				var output strings.Builder
				generateRelationResolver(&output, model, relations, descendants)
				if output.Len() == 0 {
					b.Fatal("missing generated relation resolver")
				}
			}
		})
	}
}
