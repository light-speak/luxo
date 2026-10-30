package inferred

import (
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestExpandedFilesPreserveSourceAndUnchangedFiles(t *testing.T) {
	base := &ast.ModelDecl{Name: "Base", Fields: []*ast.FieldDecl{{Name: "id"}}}
	child := &ast.ModelDecl{Name: "Child", Parents: []string{"Base"}, Fields: []*ast.FieldDecl{{Name: "name"}}}
	files := []*ast.File{{Name: "base.luxo", Models: []*ast.ModelDecl{base}}, {Name: "child.luxo", Models: []*ast.ModelDecl{child}}}
	expanded := ExpandedFiles(files)
	if expanded[0] != files[0] || expanded[1] == files[1] || len(expanded[1].Models[0].Fields) != 2 {
		t.Fatal("expected only the inherited declaration's file to change")
	}
	if len(child.Fields) != 1 || expanded[1].Models[0].Fields[1] != base.Fields[0] {
		t.Fatal("source was changed or inherited field metadata was lost")
	}
	if again := ExpandedFiles(expanded); &again[0] != &expanded[0] || again[1].Models[0] != expanded[1].Models[0] {
		t.Fatal("already expanded declarations should be reused")
	}
	// A module slice no longer includes its parent's declaration.
	module := expanded[1:]
	if again := ExpandedFiles(module); &again[0] != &module[0] {
		t.Fatal("module-local generation should reuse inherited field snapshots")
	}
	plain := files[:1]
	if &ExpandedFiles(plain)[0] != &plain[0] || ExpandedFiles(nil) != nil {
		t.Fatal("unnecessary copy for files without inheritance")
	}
}

func TestInheritedFieldsDoNotAppendIntoSourceCapacity(t *testing.T) {
	local := &ast.FieldDecl{Name: "name"}
	sentinel := &ast.FieldDecl{Name: "sentinel"}
	storage := []*ast.FieldDecl{local, sentinel}
	child := &ast.ModelDecl{Name: "Child", Parents: []string{"Base"}, Fields: storage[:1]}
	base := &ast.ModelDecl{Name: "Base", Fields: []*ast.FieldDecl{{Name: "id"}}}
	model := Models([]*ast.File{{Models: []*ast.ModelDecl{base, child}}})["Child"]
	if len(model.Fields) != 2 || model.Fields[1].Name != "id" || storage[1] != sentinel {
		t.Fatal("inherited expansion corrupted caller-owned slice capacity")
	}
}

func TestInheritedFieldsPreserveDuplicateRemoval(t *testing.T) {
	field := &ast.FieldDecl{Name: "id"}
	child := &ast.ModelDecl{Name: "Child", Parents: []string{"Base"}, Fields: []*ast.FieldDecl{field, field}}
	base := &ast.ModelDecl{Name: "Base"}
	model := Models([]*ast.File{{Models: []*ast.ModelDecl{base, child}}})["Child"]
	if len(model.Fields) != 1 || model.Fields[0] != field || len(child.Fields) != 2 {
		t.Fatal("duplicate removal changed the original declaration")
	}
}

func TestInheritedModelsPreservePrecedenceAndAST(t *testing.T) {
	field := func(name, typ string) *ast.FieldDecl {
		return &ast.FieldDecl{Name: name, Type: &ast.TypeRef{Name: typ}}
	}
	base := &ast.ModelDecl{Name: "Base", Fields: []*ast.FieldDecl{field("id", "Int")}}
	left := &ast.ModelDecl{Name: "Left", Parents: []string{"Base"}, Fields: []*ast.FieldDecl{field("value", "Int")}}
	right := &ast.ModelDecl{Name: "Right", Parents: []string{"Base"}, Fields: []*ast.FieldDecl{field("value", "String")}}
	child := &ast.ModelDecl{Name: "Child", Parents: []string{"Left", "Right"}, Fields: []*ast.FieldDecl{field("name", "String")}}
	models := Models([]*ast.File{{Models: []*ast.ModelDecl{child, right}}, {Models: []*ast.ModelDecl{base, left}}})
	if models["Base"] != base || models["Child"] == child || len(child.Fields) != 1 || len(left.Fields) != 1 {
		t.Fatal("modified source AST or copied an uninherited model")
	}
	var names []string
	for _, f := range models["Child"].Fields {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"name", "value", "id"}) || FieldType(models["Child"], "value").Name != "Int" {
		t.Fatalf("incorrect parent precedence: %v", names)
	}
	query := Parse("getChildById", models)
	if query == nil || Parameters(query, models["Child"])[0].Type.Name != "Int" {
		t.Fatal("lost inherited parameter contract")
	}
}

func TestInheritedSourcesDoNotBecomeQueryableModels(t *testing.T) {
	field := &ast.FieldDecl{Name: "key", Type: &ast.TypeRef{Name: "Int"}}
	files := []*ast.File{{
		Models:     []*ast.ModelDecl{{Name: "Child", Parents: []string{"Named", "Value", "Missing"}}},
		Interfaces: []*ast.InterfaceDecl{{Name: "Child"}, {Name: "Named", Fields: []*ast.FieldDecl{field}}},
		Types:      []*ast.TypeDecl{{Name: "Value", Fields: []*ast.FieldDecl{field}}},
	}}
	models := Models(files)
	if Parse("getChildByKey", models) == nil || len(models["Child"].Fields) != 1 {
		t.Fatal("missing or duplicate inherited field")
	}
	if models["Named"] != nil || models["Value"] != nil {
		t.Fatal("non-model became queryable")
	}
}

func TestInheritedModelCyclesTerminateWithoutChangingAST(t *testing.T) {
	a := &ast.ModelDecl{Name: "A", Parents: []string{"B"}}
	b := &ast.ModelDecl{Name: "B", Parents: []string{"A"}}
	models := Models([]*ast.File{{Models: []*ast.ModelDecl{a, b}}})
	if len(models["A"].Fields) != 0 || len(models["B"].Fields) != 0 || a.Parents[0] != "B" {
		t.Fatal("cycle handling changed declarations")
	}
}
