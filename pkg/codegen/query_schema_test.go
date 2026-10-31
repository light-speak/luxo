package codegen

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestSchemaQueryExportAndOverride(t *testing.T) {
	model := testModel("User", []*ast.Directive{directive("crud")}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("filterable"), directive("sortable")),
	})
	ids := StableIDs{APIs: map[string]int{}, APIParams: map[string]map[string]int{}}
	for i, operation := range crudOperations(model) {
		info := buildCrudAPIInfo(model, operation, "user", nil)
		ids.APIs[info.name] = i + 1
		ids.APIParams[info.name] = map[string]int{}
		for j, param := range info.params {
			ids.APIParams[info.name][param.Name] = j + 1
		}
	}
	g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
	file := &ast.File{Name: "origin/user/model.luxo", Models: []*ast.ModelDecl{model}}
	result := &semantic.Result{Files: []*ast.File{file}}
	s := schema.New()
	if err := g.buildSchemaAPIs(s, result, nil); err != nil {
		t.Fatal(err)
	}
	apiName := ast.CRUDAPIName("User", "list")
	got := s.APIs[apiName].Query
	if !reflect.DeepEqual(got, modelQueryCapabilities(model, nil)) {
		t.Fatalf("export lost capabilities: %+v", got)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded schema.Schema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.APIs[apiName].Query, got) {
		t.Fatal("query metadata did not round-trip")
	}
	file.APIs = []*ast.ApiDecl{{Name: apiName, ReturnType: &ast.TypeRef{Name: "User", IsList: true}}}
	s = schema.New()
	if err := g.buildSchemaAPIs(s, result, nil); err != nil {
		t.Fatal(err)
	}
	if s.APIs[apiName].Query != nil {
		t.Fatal("custom override inherited CRUD query capabilities")
	}
	for _, info := range g.collectSchemaAPIs(result, nil) {
		if info.name == apiName && info.query != nil {
			t.Fatal("generated registration inherited overridden query controls")
		}
	}
}

func TestCRUDQueryCapabilities(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int", directive("filterable"), directive("sortable")),
		testField("state", "State", directive("filterable")),
		testField("text", "String", directive("filterable"), directive("search")),
		testField("secret", "String", directive("filterable"), directive("internal")),
		testField("raw", "JSON", directive("filterable")),
		testField("owner", "User", directive("filterable")),
	})
	enums := map[string]bool{"State": true}
	info := buildCrudAPIInfo(m, "list", "user", enums)
	if info.query == nil || len(info.query.Filters) != 3 || !reflect.DeepEqual(info.query.Sorters, []string{"id"}) {
		t.Fatalf("query capabilities: %+v", info.query)
	}
	if got := info.query.Filters[1]; got.Type != "State" || !reflect.DeepEqual(got.Operators, []string{"eq", "ne"}) {
		t.Fatalf("enum query: %+v", got)
	}
	if got := info.query.Filters[2].Operators; !reflect.DeepEqual(got, []string{"eq", "ne", "contains", "startswith", "endswith", "match"}) {
		t.Fatalf("search operators: %v", got)
	}
	for _, op := range []string{"get", "create", "update", "delete", "deleteMany"} {
		if buildCrudAPIInfo(m, op, "user", enums).query != nil {
			t.Fatalf("%s advertises query controls", op)
		}
	}
	var output strings.Builder
	writeQueryCapabilities(&output, info.query)
	if !strings.Contains(output.String(), `Field: "state", Type: "State"`) || strings.Contains(output.String(), "secret") {
		t.Fatal(output.String())
	}
}

func TestQueryOperatorCapabilities(t *testing.T) {
	for _, name := range []string{"Int", "Float", "DateTime", "Decimal", "Boolean", "UUID", "String"} {
		field := testField("value", name, directive("filterable"))
		got := queryOperators(field, nil)
		want := 6
		if name == "Boolean" || name == "UUID" {
			want = 2
		}
		if name == "String" {
			want = 5
		}
		if len(got) != want {
			t.Fatalf("%s: %v", name, got)
		}
	}
	var output strings.Builder
	writeQueryCapabilities(&output, nil)
	if output.Len() != 0 {
		t.Fatal(output.String())
	}
}
