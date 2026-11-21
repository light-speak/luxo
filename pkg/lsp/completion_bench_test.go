package lsp

import (
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkInferredFieldCompletion(b *testing.B) {
	for _, count := range []int{1, 128} {
		model := &ast.ModelDecl{Name: "User"}
		for index := range count {
			model.Fields = append(model.Fields, &ast.FieldDecl{
				Name: fmt.Sprintf("field%d", index), Type: &ast.TypeRef{Name: "String"},
			})
		}
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if items := buildModelAPICompletions(model, nil, "get"); len(items) < count {
					b.Fatal("completion lost declared fields")
				}
			}
		})
	}
}
