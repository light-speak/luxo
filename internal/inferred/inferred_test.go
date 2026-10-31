package inferred

import (
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func queryModels() map[string]*ast.ModelDecl {
	return map[string]*ast.ModelDecl{"User": {Name: "User", Fields: []*ast.FieldDecl{
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}},
	}}}
}

func TestModelsForAPIsOnlyBuildsRequiredIndex(t *testing.T) {
	model := queryModels()["User"]
	for _, apis := range [][]*ast.ApiDecl{
		nil,
		{{Name: "body", Body: &ast.Block{}}, {Name: "native", Directives: []*ast.Directive{{Name: "native"}}}, {Name: "stream", Directives: []*ast.Directive{{Name: "stream"}}}},
	} {
		if got := ModelsForAPIs([]*ast.File{{Models: []*ast.ModelDecl{model}, APIs: apis}}); got != nil {
			t.Fatal("constructed an unused model index")
		}
	}
	files := []*ast.File{{APIs: []*ast.ApiDecl{{Name: "getUserById"}}}, {Models: []*ast.ModelDecl{model}}}
	if got := ModelsForAPIs(files); got["User"] != model {
		t.Fatal("inferred API lost a model from another file")
	}
}

func TestParsePreservesDeclaredModelNames(t *testing.T) {
	for _, name := range []string{"User", "Status", "News", "Series", "Address", "Box", "History", "Gateway"} {
		models := map[string]*ast.ModelDecl{name: {Name: name, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}}
		for _, api := range []string{"get" + name + "ById", "list" + ast.PluralModelName(name)} {
			parsed := Parse(api, models)
			if parsed == nil || parsed.ModelName != name {
				t.Errorf("%s lost declared model %s: %+v", api, name, parsed)
			}
		}
	}
}

func TestParseAndParameters(t *testing.T) {
	models := queryModels()
	for _, test := range []struct {
		name   string
		params []string
	}{
		{"getUserByName", []string{"name"}},
		{"getUserByIdBetween", []string{"idFrom", "idTo"}},
		{"getUserByIdIn", []string{"ids"}},
		{"getUserByIdNotIn", []string{"ids"}},
		{"getUserByNameContaining", []string{"keyword"}},
		{"getUserByNameNotContaining", []string{"keyword"}},
		{"getUserByNameStartingWith", []string{"namePrefix"}},
		{"getUserByNameEndingWith", []string{"nameSuffix"}},
		{"getUserByNameIgnoreCase", []string{"name"}},
		{"getUserByNameLike", []string{"name"}},
		{"getUserByNameNotLike", []string{"name"}},
		{"getUserByActiveTrue", nil},
		{"getUserByActiveFalse", nil},
		{"getUserByNameIsNull", nil},
		{"getUserByNameIsNotNull", nil},
		{"getUserByNameAndIdOrActive", []string{"name", "id", "active"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := Parse(test.name, models)
			if query == nil {
				t.Fatal("valid query rejected")
			}
			var names []string
			for _, param := range Parameters(query, models["User"]) {
				names = append(names, param.Name)
				if param.Type == nil {
					t.Fatal("parameter has no type")
				}
			}
			if !slices.Equal(names, test.params) {
				t.Fatalf("params=%v want=%v", names, test.params)
			}
		})
	}
}

func TestParseInvalidNamesAndOrder(t *testing.T) {
	models := queryModels()
	for _, name := range []string{"", "get", "getuser", "customUser", "getMissing", "getUserByMissing", "listUsersOrderByMissing", "listUsersByNameOrMissing"} {
		if query := Parse(name, models); query != nil {
			t.Fatalf("accepted %q: %+v", name, query)
		}
	}
	for _, name := range []string{"listTop5UsersOrderByIdDesc", "listFirst5UsersOrderByIdAsc"} {
		query := Parse(name, models)
		if query == nil || query.TopN != 5 || query.OrderBy != "id" {
			t.Fatalf("wrong sorted limit: %+v", query)
		}
	}
	if field, desc := ParseOrderBy("Name"); field != "name" || desc {
		t.Fatal(field, desc)
	}
	for input, want := range map[string]string{"Categories": "Category", "Boxes": "Box", "Class": "Class", "Users": "User", "User": "User"} {
		if actual := Singularize(input); actual != want {
			t.Fatal(input, actual, want)
		}
	}
	if parts := SplitBySeparator("NameOrid", "Or"); !slices.Equal(parts, []string{"NameOrid"}) {
		t.Fatal(parts)
	}
	if parts := SplitBySeparator("", "Or"); len(parts) != 0 {
		t.Fatal(parts)
	}
}

func TestModelsAndFieldTypeOwnership(t *testing.T) {
	local := queryModels()["User"]
	files := []*ast.File{{Models: []*ast.ModelDecl{local}, Extends: []*ast.ExtendDecl{
		{Name: "User", Fields: []*ast.FieldDecl{{Name: "ignored"}}},
		{Name: "Remote", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}},
	}}}
	models := Models(files)
	if models["User"] != local || models["Remote"].Fields[0].Name != "id" {
		t.Fatal(models)
	}
	copy := FieldType(local, "name")
	copy.Name = "Boolean"
	if local.Fields[0].Type.Name != "String" {
		t.Fatal("modified original field")
	}
	if fallback := FieldType(local, "missing"); fallback.Name != "String" {
		t.Fatal(fallback)
	}
}

func TestContractMetadata(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}}
	for _, test := range []struct {
		name, result string
		list, page   bool
		params       int
	}{
		{"getUserByName", "User", false, false, 1},
		{"listUsers", "User", true, true, 2},
		{"listTop5UsersByName", "User", true, false, 1},
		{"existsUserByIdIn", "Boolean", false, false, 1},
		{"countUsersByIdBetween", "Int", false, false, 2},
		{"deleteUserById", "", false, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := &ast.ApiDecl{Name: test.name}
			contract := Contract(original, models)
			if original.ReturnType != nil || len(original.Params) != 0 || len(original.Directives) != 0 {
				t.Fatal("contract inference mutated the source")
			}
			if test.result == "" {
				if contract.ReturnType != nil {
					t.Fatal("void delete acquired a result")
				}
			} else if contract.ReturnType == nil || contract.ReturnType.Name != test.result || contract.ReturnType.IsList != test.list {
				t.Fatalf("unexpected result: %+v", contract.ReturnType)
			}
			if len(contract.EffectiveParams()) != test.params || hasDirective(contract.Directives, "paginate") != test.page {
				t.Fatalf("incorrect parameters or pagination: %+v", contract)
			}
		})
	}
}

func TestContractPreservesExplicitAndNonInferredDeclarations(t *testing.T) {
	models := map[string]*ast.ModelDecl{"User": {Name: "User"}}
	for _, api := range []*ast.ApiDecl{
		{Name: "custom"},
		{Name: "getUser", Body: &ast.Block{}},
		{Name: "getUser", Directives: []*ast.Directive{{Name: "native"}}},
		{Name: "getUser", Directives: []*ast.Directive{{Name: "stream"}}},
	} {
		if Contract(api, models) != api {
			t.Fatalf("rewrote non-inferred declaration: %+v", api)
		}
	}
	api := &ast.ApiDecl{Name: "listUsers", ReturnType: &ast.TypeRef{Name: "User", IsList: true},
		Params:     []*ast.ParamDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}},
		Directives: []*ast.Directive{{Name: "paginate"}}}
	contract := Contract(api, models)
	if contract.ReturnType != api.ReturnType || contract.Params[0] != api.Params[0] || len(contract.Directives) != 1 {
		t.Fatal("explicit contract was replaced or pagination duplicated")
	}
}
