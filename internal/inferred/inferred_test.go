package inferred

import (
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestParseOrderExpression(t *testing.T) {
	id := func(name string) ast.Expr { return &ast.Ident{Name: name} }
	member := func(object ast.Expr, field string) ast.Expr { return &ast.MemberExpr{Object: object, Field: field} }
	for _, sample := range []struct {
		expression ast.Expr
		field      string
		desc       bool
		alternate  string
	}{
		{id("id"), "id", false, ""}, {member(id("id"), "asc"), "id", false, ""}, {member(id("id"), "desc"), "id", true, ""},
		{member(id("it"), "id"), "id", false, ""}, {member(id("it"), "asc"), "it", false, "asc"}, {member(id("it"), "desc"), "it", true, "desc"},
		{member(member(id("it"), "asc"), "desc"), "asc", true, ""}, {member(member(id("it"), "desc"), "asc"), "desc", false, ""},
		{nil, "", false, ""}, {&ast.CallExpr{Func: member(id("id"), "desc")}, "", false, ""},
		{member(id("id"), "DESC"), "", false, ""}, {member(member(id("other"), "id"), "desc"), "", false, ""},
		{&ast.MemberExpr{Object: id("it"), Field: "id", SafeCall: true}, "", false, ""},
		{member(&ast.MemberExpr{Object: id("it"), Field: "id", SafeCall: true}, "desc"), "", false, ""},
		{&ast.Literal{}, "", false, ""},
	} {
		field, descending, alternate := ParseOrderExpression(sample.expression)
		if field != sample.field || descending != sample.desc || alternate != sample.alternate {
			t.Errorf("%#v: got (%q, %v), want (%q, %v)", sample.expression, field, descending, sample.field, sample.desc)
		}
	}
}

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

func TestSeparatorScansPastFieldTextAndReadsUnicode(t *testing.T) {
	for _, sample := range []struct {
		input, separator string
		want             []string
	}{
		{"OrderIdOrName", "Or", []string{"OrderId", "Name"}},
		{"NameOridOrAge", "Or", []string{"NameOrid", "Age"}},
		{"NameAnd𐐀value", "And", []string{"Name", "𐐀value"}},
		{"NameOrécoleOrAge", "Or", []string{"NameOrécole", "Age"}},
		{"NameAndÉcole", "And", []string{"Name", "École"}},
		{"NameAnd", "And", []string{"NameAnd"}},
		{"Name", "", []string{"Name"}},
	} {
		if got := SplitBySeparator(sample.input, sample.separator); !slices.Equal(got, sample.want) {
			t.Errorf("SplitBySeparator(%q, %q) = %v, want %v", sample.input, sample.separator, got, sample.want)
		}
	}
}

func TestParseUnicodeCompoundGroups(t *testing.T) {
	model := &ast.ModelDecl{Name: "User"}
	for _, name := range []string{"orderId", "name", "école", "𐐨value", "nameOrécole"} {
		model.Fields = append(model.Fields, &ast.FieldDecl{Name: name, Type: &ast.TypeRef{Name: "String"}})
	}
	for api, want := range map[string][][]string{
		"getUserByOrderIdOrName":        {{"orderId"}, {"name"}},
		"getUserByNameAnd𐐀value":        {{"name", "𐐨value"}},
		"getUserByNameAndÉcoleOr𐐀value": {{"name", "école"}, {"𐐨value"}},
		"getUserByNameOrécoleOrName":    {{"nameOrécole"}, {"name"}},
	} {
		query := Parse(api, map[string]*ast.ModelDecl{"User": model})
		if query == nil || len(query.Groups) != len(want) {
			t.Fatalf("%s: %+v", api, query)
		}
		for index, group := range query.Groups {
			var fields []string
			for _, clause := range group.Clauses {
				fields = append(fields, clause.Field)
			}
			if !slices.Equal(fields, want[index]) {
				t.Errorf("%s group %d: %v, want %v", api, index, fields, want[index])
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

func TestInvalidSoftDeleteTimestamp(t *testing.T) {
	for _, sample := range []struct {
		name    string
		field   *ast.FieldDecl
		invalid bool
	}{
		{"absent", &ast.FieldDecl{Name: "id"}, false},
		{"valid", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}}, false},
		{"missing type", &ast.FieldDecl{Name: "deletedAt"}, true},
		{"wrong type", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "String", Nullable: true}}, true},
		{"nonnullable", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime"}}, true},
		{"list", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true, IsList: true}}, true},
		{"computed", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}, Computed: &ast.ComputedField{}}, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			model := &ast.ModelDecl{Fields: []*ast.FieldDecl{sample.field}}
			if InvalidSoftDeleteTimestamp(model) != nil {
				t.Fatal("checked a model without @soft")
			}
			model.Directives = []*ast.Directive{{Name: "soft"}}
			got := InvalidSoftDeleteTimestamp(model)
			if (got != nil) != sample.invalid || got != nil && got != sample.field {
				t.Fatalf("invalid field=%v want=%v", got, sample.invalid)
			}
		})
	}
}

func TestDeleteManyWhereContract(t *testing.T) {
	predicate := &ast.Ident{Name: "allowed"}
	predicate.SetTypeTag("Boolean")
	where := &ast.NamedArg{Name: "where", Value: predicate}
	lambda := &ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: predicate}}}}
	for _, sample := range []struct {
		name      string
		arguments []*ast.NamedArg
		want      ast.Expr
	}{
		{"all-records", nil, nil}, {"predicate", []*ast.NamedArg{where}, predicate},
		{"predicate-lambda", []*ast.NamedArg{{Name: "where", Value: lambda}}, lambda},
	} {
		t.Run(sample.name, func(t *testing.T) {
			value, index, message := QueryWhere(sample.arguments)
			if value != sample.want || index != -1 || message != "" {
				t.Fatal("valid bulk delete predicate was changed", value, index, message)
			}
		})
	}
	for _, sample := range []struct {
		name      string
		arguments []*ast.NamedArg
		index     int
	}{
		{"nil-argument", []*ast.NamedArg{nil}, 0},
		{"missing-value", []*ast.NamedArg{{Name: "where"}}, 0},
		{"empty-lambda", []*ast.NamedArg{{Name: "where", Value: &ast.LambdaExpr{Body: &ast.Block{}}}}, 0},
		{"unknown-name", []*ast.NamedArg{{Name: "id", Value: predicate}}, 0},
		{"duplicate", []*ast.NamedArg{where, where}, 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			value, index, message := QueryWhere(sample.arguments)
			if value != nil || index != sample.index || message == "" {
				t.Fatal("invalid bulk delete argument lost its location or became a filter", value, index, message)
			}
		})
	}
}

func TestRequiresModelQueryReceiverScope(t *testing.T) {
	for _, method := range []string{"where", "select", "orderBy", "limit", "offset", "groupBy", "deleteMany"} {
		if !RequiresModelQueryReceiver(method) {
			t.Fatal("query modifier escaped receiver validation", method)
		}
	}
	for _, method := range []string{"update", "delete", "save", "createToken", "verifyPassword", "trim"} {
		if RequiresModelQueryReceiver(method) {
			t.Fatal("instance or ordinary method entered the query receiver guard", method)
		}
	}
}

func TestQueryWhereTerminalContracts(t *testing.T) {
	predicate := &ast.Ident{Name: "allowed"}
	predicate.SetTypeTag("Boolean")
	for _, method := range []string{"deleteMany", "count", "exists"} {
		if !IsWhereTerminal(method) || !RequiresModelQueryReceiver(method) {
			t.Fatal("predicate terminal escaped shared classification", method)
		}
		value, index, message := QueryWhere([]*ast.NamedArg{{Name: "where", Value: predicate}})
		if value != predicate || index != -1 || message != "" {
			t.Fatal("valid predicate changed", method, value, index, message)
		}
		_, index, message = QueryWhere([]*ast.NamedArg{{Name: "id", Value: predicate}})
		if index != 0 || !strings.Contains(message, " accepts only one named where argument") {
			t.Fatal("invalid predicate lost its method or argument", method, index, message)
		}
	}
	for _, method := range []string{"where", "updateMany", "sum", "save", "", "Count"} {
		if IsWhereTerminal(method) {
			t.Fatal("unrelated call acquired optional-where terminal rules", method)
		}
	}
}

func TestMutationArgumentContract(t *testing.T) {
	text := &ast.Literal{Kind: token.String, Value: "kept"}
	integer := &ast.Literal{Kind: token.Int, Value: "1"}
	boolean := &ast.Literal{Kind: token.False, Value: "false"}
	boolean.SetTypeTag("Boolean")
	missing := &ast.Literal{Kind: token.Null, Value: "null"}
	pointer := &ast.Ident{Name: "maybe"}
	pointer.SetTypeTag("String")
	pointer.SetNullable(true)
	for _, sample := range []struct {
		name         string
		arg          *ast.NamedArg
		field        MutationField
		where, valid bool
	}{
		{"nil", nil, MutationField{}, false, false},
		{"positional", &ast.NamedArg{Value: text}, MutationField{}, false, false},
		{"missing-value", &ast.NamedArg{Name: "name"}, MutationField{}, false, false},
		{"unknown", &ast.NamedArg{Name: "name", Value: text}, MutationField{}, false, false},
		{"String", &ast.NamedArg{Name: "name", Value: text}, MutationField{TypeName: "String"}, false, true},
		{"Int", &ast.NamedArg{Name: "value", Value: integer}, MutationField{TypeName: "Int"}, false, true},
		{"Float", &ast.NamedArg{Name: "value", Value: &ast.Literal{Kind: token.Float, Value: "1.5"}}, MutationField{TypeName: "Float"}, false, true},
		{"Boolean", &ast.NamedArg{Name: "value", Value: &ast.Literal{Kind: token.True}}, MutationField{TypeName: "Boolean"}, false, true},
		{"bad-type", &ast.NamedArg{Name: "name", Value: integer}, MutationField{TypeName: "String"}, false, false},
		{"null", &ast.NamedArg{Name: "name", Value: missing}, MutationField{TypeName: "String", Nullable: true}, false, true},
		{"null-required", &ast.NamedArg{Name: "name", Value: missing}, MutationField{TypeName: "String"}, false, false},
		{"nullable", &ast.NamedArg{Name: "name", Value: pointer}, MutationField{TypeName: "String", Nullable: true}, false, true},
		{"nullable-required", &ast.NamedArg{Name: "name", Value: pointer}, MutationField{TypeName: "String"}, false, false},
		{"empty-list", &ast.NamedArg{Name: "values", Value: &ast.ListExpr{}}, MutationField{TypeName: "Int", List: true}, false, true},
		{"empty-list-scalar", &ast.NamedArg{Name: "values", Value: &ast.ListExpr{}}, MutationField{TypeName: "Int"}, false, false},
		{"computed", &ast.NamedArg{Name: "name", Value: text}, MutationField{TypeName: "String", Computed: true}, false, false},
		{"immutable", &ast.NamedArg{Name: "name", Value: text}, MutationField{TypeName: "String", Immutable: true}, false, false},
		{"relation", &ast.NamedArg{Name: "name", Value: text}, MutationField{TypeName: "String", Relation: true}, false, false},
		{"where", &ast.NamedArg{Name: "where", Value: boolean}, MutationField{}, true, true},
		{"bad-where", &ast.NamedArg{Name: "where", Value: integer}, MutationField{}, true, false},
		{"untyped-value", &ast.NamedArg{Name: "name", Value: &ast.Ident{Name: "unknown"}}, MutationField{TypeName: "String"}, false, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			where, message := MutationArgument(sample.arg, sample.field)
			if where != sample.where || (message == "") != sample.valid {
				t.Fatal("mutation metadata contract changed", where, message)
			}
		})
	}
}
