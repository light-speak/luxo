package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
)

func TestGeneratorRejectsAmbiguousIdentifiers(t *testing.T) {
	for name, ids := range map[string]StableIDs{
		"duplicate APIs":         {APIs: map[string]int{"a": 1, "b": 1}},
		"zero API":               {APIs: map[string]int{"a": 0}},
		"negative API":           {APIs: map[string]int{"a": -1}},
		"empty API":              {APIs: map[string]int{"": 1}},
		"duplicate fields":       {ModelFields: map[string]map[string]int{"M": {"a": 1, "b": 1}}},
		"empty model":            {ModelFields: map[string]map[string]int{"": {"a": 1}}},
		"empty parameter owner":  {APIParams: map[string]map[string]int{"": {"x": 1}}},
		"duplicate event fields": {EventFields: map[string]map[string]int{"E": {"a": 1, "b": 1}}},
		"empty event field":      {EventFields: map[string]map[string]int{"E": {"": 1}}},
		"duplicate params":       {APIParams: map[string]map[string]int{"a": {"x": 1, "y": 1}}},
		"reserved param":         {APIParams: map[string]map[string]int{"a": {"x": (1 << 31) - 2}}},
		"oversized model field":  {ModelFields: map[string]map[string]int{"M": {"x": 65536}}},
		"oversized event field":  {EventFields: map[string]map[string]int{"E": {"x": 1 << 31}}},
		"oversized API":          {APIs: map[string]int{"a": 1 << 31}},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := NewGenerator(GeneratorConfig{IDs: ids})
			if err == nil || g != nil {
				t.Fatalf("invalid identifiers accepted: %v %v", g, err)
			}
		})
	}
}

func TestRegisteredAPIRequiresEveryActiveParameterID(t *testing.T) {
	input := result(&ast.File{APIs: []*ast.ApiDecl{{
		Name: "echo", ReturnType: &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "native"}},
		Params:     []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}},
	}}})
	for _, params := range []map[string]map[string]int{nil, {"echo": {}}, {"echo": {"retired": 1}}} {
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
			APIs: map[string]int{"echo": 1}, APIParams: params,
		}})
		generated, err := g.Generate(input, "generated", nil)
		if err == nil || generated != nil || !strings.Contains(err.Error(), "echo.value") {
			t.Errorf("handler silently omitted active parameter: %v", err)
		}
		data, err := g.BuildSchemaJSON(input, nil)
		if err == nil || data != nil || !strings.Contains(err.Error(), "echo.value") {
			t.Errorf("SDK schema accepted incomplete parameter IDs: %v", err)
		}
	}
}

func TestGeneratorIdentifierSpacesRemainIndependent(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:        map[string]int{"a": 1, "b": 2},
		APIParams:   map[string]map[string]int{"a": {"x": 1}, "b": {"x": 1, "last": lockfile.MaxParameterID}},
		ModelFields: map[string]map[string]int{"M": {"x": 1}, "T": {"x": 1}},
		EventFields: map[string]map[string]int{"E": {"x": 1}, "Empty": {}, "EmptyNil": nil},
	}})
	if g.apiParamID("a", "x") != g.modelFieldID("M", "x") {
		t.Fatal("independent spaces were renumbered")
	}
}

func TestGeneratorAcceptsClassifiedIdentifierMaxima(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:        map[string]int{"last": 2147483647},
		APIParams:   map[string]map[string]int{"last": {"value": 2147483645}},
		ModelFields: map[string]map[string]int{"M": {"value": 65535}},
		EventFields: map[string]map[string]int{"E": {"value": 2147483647}},
	}})
	if g.apiID("last") != 2147483647 || g.modelFieldID("M", "value") != 65535 || g.eventFieldID("E", "value") != 2147483647 || g.apiParamID("last", "value") != 2147483645 {
		t.Fatal("valid maximum identifiers were changed")
	}
}

func TestSynthesizedAPIParametersRequireStableIDs(t *testing.T) {
	model := &ast.ModelDecl{Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
	}}
	input := result(&ast.File{Name: "origin/user.luxo", Models: []*ast.ModelDecl{model}, Functions: []*ast.FnDecl{{
		Name: "score", Directives: []*ast.Directive{{Name: "service"}, {Name: "native"}},
		Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}, ReturnType: &ast.TypeRef{Name: "Int"},
	}}})
	for _, test := range []struct {
		api     string
		params  map[string]int
		missing string
	}{
		{"getUser", nil, "id"},
		{"listUsers", map[string]int{"page": 1}, "pageSize"},
		{"svc:batchLoad:User", nil, "keys"},
		{"svc:score", nil, "id"},
	} {
		t.Run(test.api, func(t *testing.T) {
			ids := completeFixtureAPIIDs(input, StableIDs{})
			ids.APIParams[test.api] = test.params
			g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			want := test.api + "." + test.missing
			if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("missing generated parameter accepted: %v", err)
			}
			if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("missing SDK parameter accepted: %v", err)
			}
			g.ids.APIParams[test.api] = map[string]int{test.missing: 2}
			for name, id := range test.params {
				g.ids.APIParams[test.api][name] = id
			}
			if err := g.validateRegisteredParameters(input, nil); err != nil {
				t.Fatalf("complete registration rejected: %v", err)
			}
		})
	}
}
