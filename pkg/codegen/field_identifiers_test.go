package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestConfiguredFieldsCannotSilentlyDisappear(t *testing.T) {
	fields := []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "name", Type: &ast.TypeRef{Name: "String"}}}
	for _, kind := range []string{"model", "type", "extend", "event"} {
		t.Run(kind, func(t *testing.T) {
			file := &ast.File{Name: "origin/example.luxo"}
			ids := StableIDs{ModelFields: map[string]map[string]int{"M": {"id": 1}}}
			switch kind {
			case "model":
				file.Models = []*ast.ModelDecl{{Name: "M", Fields: fields}}
			case "type":
				file.Types = []*ast.TypeDecl{{Name: "M", Fields: fields}}
			case "extend":
				file.Extends = []*ast.ExtendDecl{{Name: "M", Fields: fields}}
			case "event":
				file.Events = []*ast.EventDecl{{Name: "M", Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "name", Type: &ast.TypeRef{Name: "String"}}}}}
				ids = StableIDs{EventFields: map[string]map[string]int{"M": {"id": 1}}}
			}
			g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			input := result(file)
			generated, err := g.Generate(input, "generated", nil)
			if err == nil || generated != nil || !strings.Contains(err.Error(), "M.name") {
				t.Errorf("missing field ID did not stop generation: %v", err)
			}
			data, err := g.BuildSchemaJSON(input, nil)
			if err == nil || data != nil || !strings.Contains(err.Error(), "M.name") {
				t.Errorf("missing field ID did not stop schema export: %v", err)
			}
			if kind == "event" {
				ids.EventFields["M"]["name"] = 2
			} else {
				ids.ModelFields["M"]["name"] = 2
			}
			complete := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			if _, err := complete.Generate(input, "generated", nil); err != nil {
				t.Fatalf("complete catalog rejected: %v", err)
			}
			if _, err := complete.BuildSchemaJSON(input, nil); err != nil {
				t.Fatalf("complete SDK catalog rejected: %v", err)
			}
		})
	}
}

func TestEventSnapshotRequiresNestedTypeIdentifiers(t *testing.T) {
	input := result(&ast.File{
		Events: []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{{Name: "data", Type: &ast.TypeRef{Name: "Snapshot"}}}}},
		Types:  []*ast.TypeDecl{{Name: "Snapshot", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}},
	})
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"Changed": {"data": 1}}}})
	if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), "Snapshot.value") {
		t.Fatalf("event silently lost nested snapshot fields: %v", err)
	}
}

func TestConfiguredFieldCatalogRequiresEveryDeclaration(t *testing.T) {
	input := result(&ast.File{Models: []*ast.ModelDecl{{Name: "Missing", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}}})
	for _, fields := range []map[string]map[string]int{{}, {"Other": {"id": 1}}} {
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: fields}})
		if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil {
			t.Fatal("configured catalog silently omitted an entire model")
		}
	}
}
