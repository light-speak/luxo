package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestInit(t *testing.T) {
	file := &ast.File{
		Name: "test.luxo",
		Models: []*ast.ModelDecl{
			{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				},
			},
		},
	}

	ir := Init(result(file), "myapp", "luxo")

	if len(ir.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(ir.Files))
	}
	if _, ok := ir.Files["cmd/app/main.go"]; !ok {
		t.Error("missing cmd/app/main.go")
	}
	if _, ok := ir.Files["resolver/resolver.go"]; !ok {
		t.Error("missing resolver/resolver.go")
	}
}

func TestGenerateMainGo(t *testing.T) {
	src := string(generateMainGo("github.com/example/myapp", "luxo"))

	checks := []string{
		"package main",
		`"github.com/example/myapp/luxo"`,
		`"github.com/example/myapp/resolver"`,
		`"github.com/light-speak/luxo/pkg/lux/env"`,
		`env.Load(".env")`,
		"luxo.New(ctx)",
		"defer app.Close()",
		"resolver.Setup(app)",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in main.go:\n%s", check, src)
		}
	}
}

func TestGenerateResolverGo(t *testing.T) {
	src := string(generateResolverGo())

	checks := []string{
		"package resolver",
		"type Resolver struct",
		"func Setup(app any)",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in resolver.go:\n%s", check, src)
		}
	}
}
