package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"testing"
)

func TestMergeModuleExtensionsScopesAndOwnership(t *testing.T) {
	left := &ast.FieldDecl{Name: "left", Type: &ast.TypeRef{Name: "Int"}}
	right := &ast.FieldDecl{Name: "right", Type: &ast.TypeRef{Name: "Int"}}
	first := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{left}}
	second := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{right}}
	other := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{left}}
	files := []*ast.File{
		{Name: "origin/tasks/one.luxo", Extends: []*ast.ExtendDecl{first}},
		{Name: "origin/tasks/two.luxo", Extends: []*ast.ExtendDecl{second}},
		{Name: "origin/other/one.luxo", Extends: []*ast.ExtendDecl{other}},
	}
	output := mergeModuleExtensions(files)
	if len(output[0].Extends) != 1 || len(output[0].Extends[0].Fields) != 2 || len(output[1].Extends) != 0 || output[2] != files[2] {
		t.Fatalf("wrong module merge: %+v", output)
	}
	if output[0].Extends[0].Fields[0] != left || output[0].Extends[0].Fields[1] != right {
		t.Fatal("field order changed")
	}
	if files[0].Extends[0] != first || len(first.Fields) != 1 || len(files[1].Extends) != 1 {
		t.Fatal("source declarations changed")
	}
	output[0].Extends[0].Fields[0] = right
	if first.Fields[0] != left {
		t.Fatal("merged field slice aliases source")
	}
}

func TestMergeModuleExtensionsRetainsUnchangedInput(t *testing.T) {
	if mergeModuleExtensions(nil) != nil {
		t.Fatal("nil input changed")
	}
	for _, files := range [][]*ast.File{
		{{Name: "origin/a.luxo"}},
		{{Name: "origin/a.luxo", Extends: []*ast.ExtendDecl{{Name: "Entry"}, {Name: "Other"}}}},
		{{Name: "origin/a.luxo", Extends: []*ast.ExtendDecl{{Name: "Entry"}}}, {Name: "origin/b.luxo", Extends: []*ast.ExtendDecl{{Name: "Entry"}}}},
	} {
		output := mergeModuleExtensions(files)
		if &output[0] != &files[0] {
			t.Fatal("unchanged input copied or different services merged")
		}
	}
}

func TestMergeModuleExtensionsKeepsInterleavedDeclarations(t *testing.T) {
	first := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{{Name: "a"}}}
	middle := &ast.ExtendDecl{Name: "Other"}
	second := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{{Name: "b"}}}
	third := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{{Name: "c"}}}
	files := []*ast.File{{Name: "origin/task.luxo", Extends: []*ast.ExtendDecl{middle, first, second, third, middle}}}
	output := mergeModuleExtensions(files)
	if len(output[0].Extends) != 2 || output[0].Extends[0].Name != "Other" || len(output[0].Extends[1].Fields) != 3 {
		t.Fatal("interleaved merge lost fields")
	}
	if len(first.Fields) != 1 || len(files[0].Extends) != 5 {
		t.Fatal("input changed")
	}
}

func TestMergedExtensionsRevalidateNames(t *testing.T) {
	for _, pair := range [][2]string{{"name", "Name"}, {"userID", "userId"}} {
		input := result(
			&ast.File{Name: "origin/task/one.luxo", Extends: []*ast.ExtendDecl{{Name: "Entry", Fields: []*ast.FieldDecl{{Name: pair[0], Type: &ast.TypeRef{Name: "Int"}}}}}},
			&ast.File{Name: "origin/task/two.luxo", Extends: []*ast.ExtendDecl{{Name: "Entry", Fields: []*ast.FieldDecl{{Name: pair[1], Type: &ast.TypeRef{Name: "Int"}}}}}},
		)
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, pair[0]: 2, pair[1]: 3}}}})
		if output, err := g.Generate(input, "generated", nil); err == nil || output != nil {
			t.Fatalf("merged names accepted: %v", err)
		}
		if output, err := g.BuildSchemaJSON(input, nil); err == nil || output != nil {
			t.Fatalf("merged Schema names accepted: %v", err)
		}
	}
}
