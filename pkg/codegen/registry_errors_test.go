package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestRegistryErrorsPropagateFromGeneratedHandlers(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs: map[string]int{"read": 1}, APIParams: map[string]map[string]int{"read": {"id": 1}},
	}})
	g.requestParams = requestParamDeclarations(map[string]map[string]string{"read": {"id": "Int"}})
	var b strings.Builder
	g.generateRegisterHandlers(&b, nil, []string{"read"}, nil, nil, nil)
	for _, want := range []string{
		"func RegisterHandlers(router *api.Router, app *App) error",
		"if err := router.Registry.Register(\"read\", 1); err != nil { return err }",
		"if err := router.Registry.RegisterParams(\"read\", []api.ParamMeta{",
		"}); err != nil { return err }",
		"return nil",
	} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("missing %q:\n%s", want, b.String())
		}
	}
}

func TestSchemaExportPropagatesRegistrationErrors(t *testing.T) {
	field := &ast.FieldDecl{Name: "value", Type: &ast.TypeRef{Name: "Int"}}
	param := &ast.ParamDecl{Name: "value", Type: field.Type}
	for name, file := range map[string]*ast.File{
		"model":  {Models: []*ast.ModelDecl{{Name: "Record", Fields: []*ast.FieldDecl{field, field}}}},
		"type":   {Types: []*ast.TypeDecl{{Name: "Record", Fields: []*ast.FieldDecl{field, field}}}},
		"API":    {APIs: []*ast.ApiDecl{{Name: "read", ReturnType: field.Type, Params: []*ast.ParamDecl{param, param}}}},
		"extend": {Models: []*ast.ModelDecl{{Name: "Record", Fields: []*ast.FieldDecl{{Name: "id", Type: field.Type}}}}, Extends: []*ast.ExtendDecl{{Name: "Record", Fields: []*ast.FieldDecl{field, field}}}},
	} {
		t.Run(name, func(t *testing.T) {
			g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
				ModelFields: map[string]map[string]int{"Record": {"value": 1, "id": 2}},
				APIs:        map[string]int{"read": 1}, APIParams: map[string]map[string]int{"read": {"value": 1}},
			}})
			if data, err := g.BuildSchemaJSON(buildResult(file), nil); err == nil || data != nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("export ignored %s registration error: %s / %v", name, data, err)
			}
		})
	}
}

func TestSchemaCRUDRegistrationPropagatesConflicts(t *testing.T) {
	model := &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}
	input := buildResult(&ast.File{Models: []*ast.ModelDecl{model}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1}}})})
	s := schema.New()
	if err := s.RegisterAPI(&schema.API{Name: "occupied", ID: g.apiID(ast.CRUDAPIName("Record", "get"))}); err != nil {
		t.Fatal(err)
	}
	if err := g.buildSchemaAPIs(s, input, nil); err == nil {
		t.Fatal("CRUD registry conflict ignored")
	}
}

func TestEntryStopsOnRegistryFailure(t *testing.T) {
	var b strings.Builder
	writeEmbeddedGatewayWiring(&b, []moduleInfo{{name: "user", hasAPI: true}}, false)
	if !strings.Contains(b.String(), "if err := user_luxo.RegisterHandlers(gw.Router, userApp); err != nil") || !strings.Contains(b.String(), "os.Exit(1)") {
		t.Fatalf("startup ignores registration errors:\n%s", b.String())
	}
}
