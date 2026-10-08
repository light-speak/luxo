package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestInferredSchemaUsesFormalLockParameterIDs(t *testing.T) {
	for _, name := range []string{"User", "Status", "News", "Series"} {
		t.Run(name, func(t *testing.T) { testInferredSchemaLockParameters(t, name) })
	}
}

func testInferredSchemaLockParameters(t *testing.T, name string) {
	t.Helper()
	plural := ast.PluralModelName(name)
	list, top, get := "list"+plural+"ByName", "listTop5"+plural+"ByName", "get"+name+"ByName"
	model := testModel(name, []*ast.Directive{{Name: "noTime"}}, []*ast.FieldDecl{
		testField("id", "Int", &ast.Directive{Name: "id"}), testField("name", "String"),
	})
	file := &ast.File{Models: []*ast.ModelDecl{model}, APIs: []*ast.ApiDecl{
		{Name: list}, {Name: top}, {Name: get},
	}}
	input := result(file)
	lf := lockfile.New()
	if err := lf.Update(input.Files); err != nil {
		t.Fatal(err)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{name: lf.Models[name].Fields}, APIs: map[string]int{}, APIParams: map[string]map[string]int{}}
	for name, api := range lf.APIs {
		ids.APIs[name], ids.APIParams[name] = api.ID, api.Params
	}
	g := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids})
	if _, err := g.Generate(input, "generated", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := g.BuildSchemaJSON(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	var exported schema.Schema
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	for apiName, wantParams := range map[string]int{list: 3, top: 1, get: 1} {
		api := exported.APIs[apiName]
		if api == nil || api.ReturnType != name || api.ReturnList != (apiName != get) || len(api.Params) != wantParams || api.Paginated != (wantParams == 3) {
			t.Fatalf("incorrect exported contract: %+v", api)
		}
		for _, param := range api.Params {
			if param.ID <= 0 || param.ID != lf.APIParamID(apiName, param.Name) {
				t.Fatalf("exported parameter lost stable ID: %+v", param)
			}
		}
	}
}

func TestFormalGenerationRequiresStableIdentifiers(t *testing.T) {
	field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "Int"}}
	for name, file := range map[string]*ast.File{
		"model":  {Models: []*ast.ModelDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		"type":   {Types: []*ast.TypeDecl{{Name: "T", Fields: []*ast.FieldDecl{field}}}},
		"extend": {Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		"event":  {Events: []*ast.EventDecl{{Name: "E", Params: []*ast.ParamDecl{{Name: "value", Type: field.Type}}}}},
		"API":    {APIs: []*ast.ApiDecl{{Name: "ping", ReturnType: field.Type, Directives: []*ast.Directive{{Name: "native"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			input := result(file)
			if generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG}); err == nil || generated != nil || !strings.Contains(err.Error(), "stable ID") {
				t.Fatalf("public generation accepted unbound declarations: %v", err)
			}
			if data, err := BuildSchemaJSON(input, nil, GeneratorConfig{}); err == nil || data != nil || !strings.Contains(err.Error(), "stable ID") {
				t.Fatalf("public export accepted unbound declarations: %v", err)
			}
			g := mustNewGenerator(t, GeneratorConfig{})
			if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil {
				t.Fatal("context generation accepted unbound declarations")
			}
			if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil {
				t.Fatal("context export accepted unbound declarations")
			}
		})
	}
}

func TestFormalEntrypointsPreserveExplicitIdentifiers(t *testing.T) {
	input := result(&ast.File{
		Models: []*ast.ModelDecl{{Name: "Record", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}}, {Name: "name", Type: &ast.TypeRef{Name: "String"}},
		}}},
		APIs: []*ast.ApiDecl{{Name: "read", ReturnType: &ast.TypeRef{Name: "Record"}, Directives: []*ast.Directive{{Name: "native"}}, Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}},
	})
	config := GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{"Record": {"id": 17, "name": 42}},
		APIs:        map[string]int{"read": 73}, APIParams: map[string]map[string]int{"read": {"id": 9}},
	}}
	generated, err := Generate(input, "generated", config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated.Files["writejson.gen.go"]), "AppendVarint(buf.B, 42)") {
		t.Fatal("explicit sparse field ID disappeared")
	}
	data, err := BuildSchemaJSON(input, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	var exported schema.Schema
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.APIs["read"].ID != 73 || exported.APIs["read"].Params[0].ID != 9 || exported.Models["Record"].Fields[1].ID != 42 {
		t.Fatal("SDK export renumbered explicit identifiers")
	}
	if data, err := BuildSchemaJSON(input, nil, GeneratorConfig{Driver: "invalid"}); err == nil || data != nil {
		t.Fatal("export ignored invalid configuration")
	}
}

func TestFormalGenerationRequiresSynthesizedAPIIdentifiers(t *testing.T) {
	input := result(&ast.File{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{
		Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}},
	}}, Functions: []*ast.FnDecl{{Name: "score", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "service"}, {Name: "native"}}}}})
	for _, missing := range []string{"getUser", "listUsers", "svc:batchLoad:User", "svc:score"} {
		ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}})
		delete(ids.APIs, missing)
		g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
		if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing synthesized ID accepted: %s %v", missing, err)
		}
		if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("missing synthesized ID exported: %s %v", missing, err)
		}
	}
}

func TestFormalGenerationRejectsUnregisteredAPI(t *testing.T) {
	input := result(&ast.File{APIs: []*ast.ApiDecl{
		{Name: "known", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}},
		{Name: "missing", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}},
	}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"known": 1}}})
	if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unregistered API generated: %v", err)
	}
	if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("unregistered API exported: %v", err)
	}
}

func TestFormalCatalogDoesNotInventLocalResolvers(t *testing.T) {
	field := &ast.FieldDecl{Name: "related", Type: &ast.TypeRef{Name: "Target"}}
	for _, module := range []string{"origin/owner.luxo", "origin/consumer.luxo"} {
		input := result(
			&ast.File{Name: "origin/owner.luxo", Models: []*ast.ModelDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
			&ast.File{Name: module, Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{field}}}},
		)
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"M": {"related": 1}}}})
		if err := g.validateRegisteredParameters(input, nil); err != nil {
			t.Fatalf("invented a resolver for owner-declared field (%s): %v", module, err)
		}
		if err := g.validateFieldIdentifiers(input); err != nil {
			t.Fatalf("local extension incorrectly required an external key (%s): %v", module, err)
		}
	}
}

func TestFormalCatalogUsesExternalOwnershipAndEnums(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{
		{Name: "related", Type: &ast.TypeRef{Name: "Target"}}, {Name: "state", Type: &ast.TypeRef{Name: "RemoteState"}},
	}}}})
	g := mustNewGenerator(t, GeneratorConfig{
		IDs: StableIDs{ModelFields: map[string]map[string]int{"M": {"related": 1, "state": 2, "id": 3}}},
		Events: &EventContext{
			ModelModule: map[string]string{"M": "owner"}, ModelFields: map[string]map[string]bool{"M": {"related": true}},
			EnumModule: map[string]string{"RemoteState": "owner"},
		},
	})
	if _, err := g.BuildSchemaJSON(input, nil); err != nil {
		t.Fatalf("invented internal API from external owner or enum: %v", err)
	}
	if len(g.events.ModelFields["M"]) != 1 || g.events.ModelFields["M"]["state"] {
		t.Fatal("catalog construction mutated external ownership")
	}
}

func TestFormalCatalogUsesLocalEnumDeclarations(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Enums: []*ast.EnumDecl{{Name: "LocalState", Values: []string{"OPEN"}}}, Extends: []*ast.ExtendDecl{{Name: "M", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "LocalState"}},
	}}}})
	g := generatorWithModelFieldIDs(map[string]map[string]int{"M": {"state": 1, "id": 2}})
	for _, hints := range []map[string]bool{nil, {"LocalState": false}, {"LocalState": true}} {
		if _, err := g.BuildSchemaJSON(input, hints); err != nil {
			t.Fatalf("local enum treated as relation: %v", err)
		}
	}
}

func TestFormalGenerationRequiresExternalPrimaryKeyIdentifier(t *testing.T) {
	input := result(&ast.File{Name: "origin/consumer.luxo", Extends: []*ast.ExtendDecl{{Name: "Remote", Fields: []*ast.FieldDecl{
		{Name: "state", Type: &ast.TypeRef{Name: "String"}},
	}}}})
	config := GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Remote": {"state": 1}}}, Events: &EventContext{
		ModelIDField: map[string]string{"Remote": "key"}, ModelIDType: map[string]string{"Remote": "UUID"},
	}}
	g := mustNewGenerator(t, config)
	if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), "Remote.key") {
		t.Fatalf("synthetic external key has no wire ID: %v", err)
	}
	if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), "Remote.key") {
		t.Fatalf("export accepted incomplete external catalog: %v", err)
	}
	config.IDs.ModelFields["Remote"]["key"] = 2
	if _, err := Generate(input, "generated", config); err != nil {
		t.Fatalf("complete external key catalog rejected: %v", err)
	}
}
