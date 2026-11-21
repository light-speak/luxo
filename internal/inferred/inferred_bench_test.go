package inferred

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func BenchmarkParseQueryName(b *testing.B) {
	model := &ast.ModelDecl{Name: "User"}
	for _, field := range []string{"name", "role", "email"} {
		model.Fields = append(model.Fields, &ast.FieldDecl{Name: field, Type: &ast.TypeRef{Name: "String"}})
	}
	models := map[string]*ast.ModelDecl{"User": model}
	for _, name := range []string{"getUserByName", "listUsersByNameAndRoleOrEmail"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result := Parse(name, models); result == nil || result.ModelName != "User" {
					b.Fatal("valid query lost its model")
				}
			}
		})
	}
}

func BenchmarkQueryWhereArguments(b *testing.B) {
	predicate := &ast.Ident{Name: "allowed"}
	predicate.SetTypeTag("Boolean")
	where := &ast.NamedArg{Name: "where", Value: predicate}
	for _, sample := range []struct {
		name    string
		args    []*ast.NamedArg
		want    ast.Expr
		invalid bool
	}{
		{"none", nil, nil, false}, {"Boolean", []*ast.NamedArg{where}, predicate, false},
		{"unknown", []*ast.NamedArg{{Name: "id", Value: predicate}}, nil, true},
		{"duplicate", []*ast.NamedArg{where, where}, nil, true},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				value, _, message := QueryWhere(sample.args)
				if value != sample.want || (message != "") != sample.invalid {
					b.Fatal("optional-where contract changed", value, message)
				}
			}
		})
	}
}
