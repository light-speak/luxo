package codegen

import (
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func primaryLoaderBenchmarkInput(count int) *semantic.Result {
	file := &ast.File{Name: "origin/product/model.luxo"}
	for index := range count {
		name := "Product" + strconv.Itoa(index)
		file.Models = append(file.Models, &ast.ModelDecl{Name: name, Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		}})
		file.Functions = append(file.Functions, &ast.FnDecl{Name: "lookup" + strconv.Itoa(index), Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ReturnStmt{Value: &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: name}, Field: "load"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}}},
		}}})
	}
	return &semantic.Result{Files: []*ast.File{file}}
}

func BenchmarkPrimaryKeyLoaderMetadata(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			input, generator := primaryLoaderBenchmarkInput(count), defaultGenerator()
			b.Run("loaders", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = generator.collectDataLoaderInputs(input, nil, nil)
				}
			})
			b.Run("startup", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if len(collectModules(input)) != 1 {
						b.Fatal("module omitted")
					}
				}
			})
		})
	}
}
