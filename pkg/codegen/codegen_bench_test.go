package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func buildBenchResult() *semantic.Result {
	file := &ast.File{
		Name: "bench.luxo",
		Enums: []*ast.EnumDecl{
			{Name: "Role", Values: []string{"USER", "ADMIN", "MODERATOR"}},
			{Name: "OrderStatus", Values: []string{"PENDING", "PAID", "SHIPPED", "COMPLETED"}},
		},
		Models: []*ast.ModelDecl{
			{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "unique"}}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
					{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					{Name: "bio", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
			{
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
					{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					{Name: "content", Type: &ast.TypeRef{Name: "String"}},
					{Name: "published", Type: &ast.TypeRef{Name: "Boolean"}},
					{Name: "authorId", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
					{Name: "updatedAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
			{
				Name: "Order",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "serial"}}},
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "immutable"}}},
					{Name: "status", Type: &ast.TypeRef{Name: "OrderStatus"}},
					{Name: "total", Type: &ast.TypeRef{Name: "Decimal"}},
					{Name: "note", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			},
		},
	}
	return &semantic.Result{Files: []*ast.File{file}}
}

func BenchmarkGenerate(b *testing.B) {
	result := buildBenchResult()
	b.ResetTimer()
	for b.Loop() {
		Generate(result, "gen")
	}
}

func BenchmarkGenerateModelFile(b *testing.B) {
	result := buildBenchResult()
	b.ResetTimer()
	for b.Loop() {
		generateModelFile(result, "gen", nil)
	}
}
