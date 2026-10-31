package codegen

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestHandlerMetadataUsesCurrentDeclarations(t *testing.T) {
	model := &ast.ModelDecl{Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
		{Name: "key", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "id"}}},
	}}
	input := result(&ast.File{Models: []*ast.ModelDecl{model}, APIs: []*ast.ApiDecl{{
		Name: "check", ReturnType: &ast.TypeRef{Name: "Boolean"}, Directives: []*ast.Directive{{Name: "native"}},
		Params: []*ast.ParamDecl{{Name: "active", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}}},
	}}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{
		APIs:        map[string]int{"deleteUsers": 1, "check": 2},
		APIParams:   map[string]map[string]int{"deleteUsers": {"ids": 1}, "check": {"active": 2, "removed": 3}},
		ModelFields: map[string]map[string]int{"User": {"key": 1}},
	})})
	generated, err := g.Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	code := string(generated.Files["handler.gen.go"])
	for _, want := range []string{
		`Name: "ids", Type: "UUID", FieldID: 1, IsList: true`,
		`Name: "active", Type: "String", FieldID: 2, IsList: true, Nullable: true`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(code, `Name: "removed"`) {
		t.Error("retired parameter entered wire metadata")
	}
}

func TestRequestDeclarationsDoNotMutateGenerator(t *testing.T) {
	g := defaultGenerator()
	var workers sync.WaitGroup
	for _, name := range []string{"Int", "String", "UUID", "Decimal"} {
		workers.Go(func() {
			input := result(&ast.File{APIs: []*ast.ApiDecl{{Name: "echo", Params: []*ast.ParamDecl{
				{Name: "value", Type: &ast.TypeRef{Name: name}},
			}}}})
			bound := g.withRequestDeclarations(input, nil)
			if got := bound.requestParams["echo"][0].Type.Name; got != name {
				t.Errorf("type crossed compilation: %s != %s", got, name)
			}
		})
	}
	workers.Wait()
	if g.requestParams != nil {
		t.Fatal("compilation changed reusable generator")
	}
}

func TestRequestDeclarationsRespectExplicitCRUDOverrideAcrossFiles(t *testing.T) {
	apiFile := &ast.File{APIs: []*ast.ApiDecl{{Name: "getUser", Params: []*ast.ParamDecl{
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}}}
	modelFile := &ast.File{Models: []*ast.ModelDecl{{Name: "User", Directives: []*ast.Directive{{Name: "crud"}}}}}
	for _, files := range [][]*ast.File{{apiFile, modelFile}, {modelFile, apiFile}} {
		g := defaultGenerator()
		apis := g.collectSchemaAPIs(result(files...), nil)
		count := 0
		for _, api := range apis {
			if api.name != "getUser" {
				continue
			}
			count++
			if len(api.params) != 1 || api.params[0].Name != "name" {
				t.Error("CRUD shadowed explicit API")
			}
		}
		if count != 1 {
			t.Errorf("registered getUser %d times", count)
		}
		g = mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(result(files...), StableIDs{})})
		data, err := g.BuildSchemaJSON(result(files...), nil)
		if err != nil {
			t.Fatal(err)
		}
		var exported schema.Schema
		if err := json.Unmarshal(data, &exported); err != nil {
			t.Fatal(err)
		}
		if params := exported.APIs["getUser"].Params; len(params) != 1 || params[0].Name != "name" {
			t.Errorf("SDK schema disagrees with explicit API: %v", params)
		}
	}
}
