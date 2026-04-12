package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func inferModels(ms ...*ast.ModelDecl) map[string]*ast.ModelDecl {
	m := make(map[string]*ast.ModelDecl)
	for _, model := range ms {
		m[model.Name] = model
	}
	return m
}

func TestInferAPIBasic(t *testing.T) {
	models := inferModels(
		testModel("User", nil, []*ast.FieldDecl{
			testField("id", "Int"),
			testField("email", "String"),
			testField("name", "String"),
			testField("role", "String"),
			testField("age", "Int"),
			testField("createdAt", "DateTime"),
		}),
	)

	tests := []struct {
		name       string
		wantAction string
		wantModel  string
		wantFields int
	}{
		{"getUserByEmail", "get", "User", 1},
		{"listUsersByRole", "list", "User", 1},
		{"countUsersByRole", "count", "User", 1},
		{"existsUserByEmail", "exists", "User", 1},
		{"deleteUserByEmail", "delete", "User", 1},
		{"getUserByNameAndRole", "get", "User", 2},
		{"getUserByEmailOrName", "get", "User", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inf := inferAPI(tt.name, models)
			if inf == nil {
				t.Fatal("inferAPI returned nil")
			}
			if inf.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", inf.Action, tt.wantAction)
			}
			if inf.ModelName != tt.wantModel {
				t.Errorf("model = %q, want %q", inf.ModelName, tt.wantModel)
			}
			total := 0
			for _, g := range inf.Groups {
				total += len(g.Clauses)
			}
			if total != tt.wantFields {
				t.Errorf("fields = %d, want %d", total, tt.wantFields)
			}
		})
	}
}

func TestInferAPIOperators(t *testing.T) {
	models := inferModels(
		testModel("User", nil, []*ast.FieldDecl{
			testField("id", "Int"),
			testField("name", "String"),
			testField("age", "Int"),
			testField("createdAt", "DateTime"),
			testField("active", "Boolean"),
			testField("deletedAt", "DateTime"),
		}),
	)

	tests := []struct {
		name   string
		wantOp string
	}{
		{"listUsersByNameContaining", "containing"},
		{"listUsersByAgeGreaterThan", "gt"},
		{"listUsersByAgeLessThan", "lt"},
		{"listUsersByActiveTrue", "true"},
		{"listUsersByActiveFalse", "false"},
		{"listUsersByDeletedAtIsNull", "isNull"},
		{"listUsersByDeletedAtIsNotNull", "isNotNull"},
		{"listUsersByAgeBetween", "between"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inf := inferAPI(tt.name, models)
			if inf == nil {
				t.Fatal("inferAPI returned nil")
			}
			if len(inf.Groups) == 0 || len(inf.Groups[0].Clauses) == 0 {
				t.Fatal("no clauses")
			}
			op := inf.Groups[0].Clauses[0].Op
			if op != tt.wantOp {
				t.Errorf("op = %q, want %q", op, tt.wantOp)
			}
		})
	}
}

func TestBuildInferredParamsBasic(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("email", "String"),
		testField("name", "String"),
		testField("age", "Int"),
		testField("createdAt", "DateTime"),
	})
	models := inferModels(m)

	tests := []struct {
		apiName    string
		wantParams []struct {
			name     string
			typeName string
		}
	}{
		{
			"getUserByEmail",
			[]struct{ name, typeName string }{{"email", "String"}},
		},
		{
			"listUsersByNameContaining",
			[]struct{ name, typeName string }{{"keyword", "String"}},
		},
		{
			"listUsersByAgeBetween",
			[]struct{ name, typeName string }{{"ageFrom", "Int"}, {"ageTo", "Int"}},
		},
		{
			"getUserByCreatedAtBetween",
			[]struct{ name, typeName string }{{"createdAtFrom", "DateTime"}, {"createdAtTo", "DateTime"}},
		},
		{
			"getUserByNameAndEmail",
			[]struct{ name, typeName string }{{"name", "String"}, {"email", "String"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.apiName, func(t *testing.T) {
			inf := inferAPI(tt.apiName, models)
			if inf == nil {
				t.Fatal("inferAPI returned nil")
			}
			params := buildInferredParams(inf, m)
			if len(params) != len(tt.wantParams) {
				t.Fatalf("params = %d, want %d", len(params), len(tt.wantParams))
			}
			for i, want := range tt.wantParams {
				if params[i].Name != want.name {
					t.Errorf("params[%d].Name = %q, want %q", i, params[i].Name, want.name)
				}
				if params[i].Type.Name != want.typeName {
					t.Errorf("params[%d].Type = %q, want %q", i, params[i].Type.Name, want.typeName)
				}
			}
		})
	}
}

func TestBuildInferredParamsZeroParamOps(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("active", "Boolean"),
		testField("deletedAt", "DateTime"),
	})
	models := inferModels(m)

	tests := []string{
		"listUsersByActiveTrue",
		"listUsersByActiveFalse",
		"listUsersByDeletedAtIsNull",
		"listUsersByDeletedAtIsNotNull",
	}

	for _, apiName := range tests {
		t.Run(apiName, func(t *testing.T) {
			inf := inferAPI(apiName, models)
			if inf == nil {
				t.Fatal("inferAPI returned nil")
			}
			params := buildInferredParams(inf, m)
			if len(params) != 0 {
				t.Errorf("params = %d, want 0", len(params))
			}
		})
	}
}

func TestValidateInferredReturnTypeNil(t *testing.T) {
	api := &ast.ApiDecl{Name: "getUserByEmail"}
	inf := &InferredAPI{Action: "get", ModelName: "User"}
	msg := ValidateInferredReturnType(api, inf)
	if msg != "" {
		t.Errorf("expected empty, got %q", msg)
	}
}

func TestValidateInferredReturnTypeValid(t *testing.T) {
	tests := []struct {
		name   string
		action string
		rt     *ast.TypeRef
	}{
		{"get", "get", &ast.TypeRef{Name: "User"}},
		{"list", "list", &ast.TypeRef{Name: "User", IsList: true}},
		{"count", "count", &ast.TypeRef{Name: "Int"}},
		{"exists", "exists", &ast.TypeRef{Name: "Boolean"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &ast.ApiDecl{Name: "test", ReturnType: tt.rt}
			inf := &InferredAPI{Action: tt.action, ModelName: "User"}
			msg := ValidateInferredReturnType(api, inf)
			if msg != "" {
				t.Errorf("expected valid, got %q", msg)
			}
		})
	}
}

func TestValidateInferredReturnTypeInvalid(t *testing.T) {
	tests := []struct {
		name   string
		action string
		rt     *ast.TypeRef
	}{
		{"get returns list", "get", &ast.TypeRef{Name: "User", IsList: true}},
		{"get wrong model", "get", &ast.TypeRef{Name: "Post"}},
		{"list not list", "list", &ast.TypeRef{Name: "User"}},
		{"list wrong model", "list", &ast.TypeRef{Name: "Post", IsList: true}},
		{"count not int", "count", &ast.TypeRef{Name: "String"}},
		{"exists not bool", "exists", &ast.TypeRef{Name: "Int"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &ast.ApiDecl{Name: "test", ReturnType: tt.rt}
			inf := &InferredAPI{Action: tt.action, ModelName: "User"}
			msg := ValidateInferredReturnType(api, inf)
			if msg == "" {
				t.Error("expected error message")
			}
		})
	}
}

func TestInferAPIWithOrderBy(t *testing.T) {
	models := inferModels(
		testModel("User", nil, []*ast.FieldDecl{
			testField("name", "String"),
			testField("createdAt", "DateTime"),
		}),
	)
	inf := inferAPI("listUsersByNameOrderByCreatedAtDesc", models)
	if inf == nil {
		t.Fatal("inferAPI returned nil")
	}
	if inf.OrderBy != "createdAt" {
		t.Errorf("orderBy = %q, want createdAt", inf.OrderBy)
	}
	if !inf.OrderDesc {
		t.Error("expected OrderDesc = true")
	}
}

func TestInferAPITopN(t *testing.T) {
	models := inferModels(
		testModel("User", nil, []*ast.FieldDecl{
			testField("name", "String"),
		}),
	)
	inf := inferAPI("listTop5UsersByName", models)
	if inf == nil {
		t.Fatal("inferAPI returned nil")
	}
	if inf.TopN != 5 {
		t.Errorf("topN = %d, want 5", inf.TopN)
	}
}

func TestInferAPIInvalidName(t *testing.T) {
	models := inferModels(testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int"),
	}))

	tests := []string{
		"invalidPrefix",
		"getUserByNonExistent",
		"getPostByName",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			inf := inferAPI(name, models)
			if inf != nil {
				t.Errorf("expected nil for %q, got %+v", name, inf)
			}
		})
	}
}

func TestInferParamName(t *testing.T) {
	tests := []struct {
		clause InferClause
		want   string
	}{
		{InferClause{Field: "name", Op: "containing"}, "keyword"},
		{InferClause{Field: "name", Op: "startswith"}, "namePrefix"},
		{InferClause{Field: "name", Op: "endswith"}, "nameSuffix"},
		{InferClause{Field: "email", Op: "eq"}, "email"},
	}

	for _, tt := range tests {
		got := inferParamName(tt.clause)
		if got != tt.want {
			t.Errorf("inferParamName(%+v) = %q, want %q", tt.clause, got, tt.want)
		}
	}
}

func TestGetFieldTypeRef(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("email", "String"),
		testField("createdAt", "DateTime"),
	})

	tr := getFieldTypeRef(m, "id")
	if tr.Name != "Int" {
		t.Errorf("id type = %q, want Int", tr.Name)
	}

	tr = getFieldTypeRef(m, "createdAt")
	if tr.Name != "DateTime" {
		t.Errorf("createdAt type = %q, want DateTime", tr.Name)
	}

	// Unknown field falls back to String
	tr = getFieldTypeRef(m, "nonexistent")
	if tr.Name != "String" {
		t.Errorf("unknown type = %q, want String", tr.Name)
	}
}

// --- Code generation tests ---

func testApiDecl(name string, params []*ast.ParamDecl) *ast.ApiDecl {
	return &ast.ApiDecl{
		Pos:    token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:   name,
		Params: params,
	}
}

func testParamDecl(name, typeName string) *ast.ParamDecl {
	return &ast.ParamDecl{
		Name: name,
		Type: &ast.TypeRef{Name: typeName},
	}
}

func TestGenerateInferredHandler(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("email", "String"),
		testField("name", "String"),
		testField("age", "Int"),
	})

	enums := map[string]bool{}

	t.Run("explicit params get by email", func(t *testing.T) {
		api := testApiDecl("getUserByEmail", []*ast.ParamDecl{
			testParamDecl("email", "String"),
		})
		inf := &InferredAPI{
			Action:    "get",
			ModelName: "User",
			Groups: []ClauseGroup{
				{Clauses: []InferClause{{Field: "email", Op: "eq"}}},
			},
		}
		var b strings.Builder
		generateInferredHandler(&b, api, inf, enums, m)
		out := b.String()

		if !strings.Contains(out, "func handleGetUserByEmail(app *App)") {
			t.Errorf("missing function signature, got:\n%s", out)
		}
		if !strings.Contains(out, "req.ParamString") {
			t.Errorf("missing req.ParamString, got:\n%s", out)
		}
		if !strings.Contains(out, `"email"`) {
			t.Errorf("missing param name, got:\n%s", out)
		}
		if !strings.Contains(out, "conds := []lux.Condition{") {
			t.Errorf("missing conds declaration, got:\n%s", out)
		}
		if !strings.Contains(out, "First(ctx)") {
			t.Errorf("missing First(ctx) for get action, got:\n%s", out)
		}
	})

	t.Run("auto-inferred params from get action", func(t *testing.T) {
		// No params declared — should be auto-inferred from clauses
		api := testApiDecl("getUserByAge", []*ast.ParamDecl{})
		inf := &InferredAPI{
			Action:    "get",
			ModelName: "User",
			Groups: []ClauseGroup{
				{Clauses: []InferClause{{Field: "age", Op: "eq"}}},
			},
		}
		var b strings.Builder
		generateInferredHandler(&b, api, inf, enums, m)
		out := b.String()

		if !strings.Contains(out, "func handleGetUserByAge(app *App)") {
			t.Errorf("missing function signature, got:\n%s", out)
		}
		// Auto-inferred int param should use ParamInt
		if !strings.Contains(out, "req.ParamInt") {
			t.Errorf("missing req.ParamInt for Int field, got:\n%s", out)
		}
	})

	t.Run("list action with order and pagination", func(t *testing.T) {
		api := testApiDecl("listUsersByName", []*ast.ParamDecl{
			testParamDecl("name", "String"),
		})
		inf := &InferredAPI{
			Action:    "list",
			ModelName: "User",
			Groups: []ClauseGroup{
				{Clauses: []InferClause{{Field: "name", Op: "eq"}}},
			},
			OrderBy:   "createdAt",
			OrderDesc: true,
		}
		var b strings.Builder
		generateInferredHandler(&b, api, inf, enums, m)
		out := b.String()

		if !strings.Contains(out, "AllWithCount(ctx)") {
			t.Errorf("missing AllWithCount for list action, got:\n%s", out)
		}
		if !strings.Contains(out, "created_at DESC") {
			t.Errorf("missing ORDER BY created_at DESC, got:\n%s", out)
		}
	})

	t.Run("list action topN", func(t *testing.T) {
		api := testApiDecl("listTop3UsersByName", []*ast.ParamDecl{
			testParamDecl("name", "String"),
		})
		inf := &InferredAPI{
			Action:    "list",
			ModelName: "User",
			TopN:      3,
			Groups: []ClauseGroup{
				{Clauses: []InferClause{{Field: "name", Op: "eq"}}},
			},
		}
		var b strings.Builder
		generateInferredHandler(&b, api, inf, enums, m)
		out := b.String()

		if !strings.Contains(out, "Limit(3)") {
			t.Errorf("missing Limit(3) for topN, got:\n%s", out)
		}
	})

	t.Run("OR groups generate orParts raw SQL", func(t *testing.T) {
		// Two groups → OR path in writeInferredConditions
		api := testApiDecl("getUserByEmailOrName", []*ast.ParamDecl{
			testParamDecl("email", "String"),
			testParamDecl("name", "String"),
		})
		inf := &InferredAPI{
			Action:    "get",
			ModelName: "User",
			Groups: []ClauseGroup{
				{Clauses: []InferClause{{Field: "email", Op: "eq"}}},
				{Clauses: []InferClause{{Field: "name", Op: "eq"}}},
			},
		}
		var b strings.Builder
		generateInferredHandler(&b, api, inf, enums, m)
		out := b.String()

		if !strings.Contains(out, "orParts") {
			t.Errorf("OR path: missing orParts, got:\n%s", out)
		}
		if !strings.Contains(out, "orArgs") {
			t.Errorf("OR path: missing orArgs, got:\n%s", out)
		}
		if !strings.Contains(out, "lux.RawCondition") {
			t.Errorf("OR path: missing RawCondition, got:\n%s", out)
		}
		if !strings.Contains(out, "strings.Join(orParts") {
			t.Errorf("OR path: missing strings.Join(orParts...), got:\n%s", out)
		}
	})

}

func TestGenerateInferredHandlerEnumParam(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("email", "String"),
	})
	enumMap := map[string]bool{"Role": true}
	api := testApiDecl("getUserByRole", []*ast.ParamDecl{
		{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
	})
	inf := &InferredAPI{
		Action:    "get",
		ModelName: "User",
		Groups: []ClauseGroup{
			{Clauses: []InferClause{{Field: "email", Op: "eq"}}},
		},
	}
	var b strings.Builder
	generateInferredHandler(&b, api, inf, enumMap, m)
	out := b.String()
	if !strings.Contains(out, "req.ParamString") {
		t.Errorf("enum param: expected ParamString for enum type, got:\n%s", out)
	}
}

func TestGenerateInferredHandlerParamJSON(t *testing.T) {
	mWithUUID := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "UUID"),
		testField("email", "String"),
	})
	api := testApiDecl("getUserById", []*ast.ParamDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "UUID"}},
	})
	inf := &InferredAPI{
		Action:    "get",
		ModelName: "User",
		Groups: []ClauseGroup{
			{Clauses: []InferClause{{Field: "id", Op: "eq"}}},
		},
	}
	var b strings.Builder
	generateInferredHandler(&b, api, inf, map[string]bool{}, mWithUUID)
	out := b.String()
	if !strings.Contains(out, "req.ParamJSON") {
		t.Errorf("UUID param: expected ParamJSON for UUID type, got:\n%s", out)
	}
}

func TestWriteAndClause(t *testing.T) {
	m := testModel("Post", nil, []*ast.FieldDecl{
		testField("title", "String"),
		testField("views", "Int"),
		testField("score", "Float"),
		testField("active", "Boolean"),
		testField("deletedAt", "DateTime"),
		testField("age", "Int"),
	})

	params := []*ast.ParamDecl{
		testParamDecl("p0", "String"),
		testParamDecl("p1", "Int"),
	}

	cases := []struct {
		clause  InferClause
		want    string
		noParam bool // true ops that consume no param
	}{
		{InferClause{Field: "active", Op: "true"}, "IsTrue()", true},
		{InferClause{Field: "active", Op: "false"}, "IsFalse()", true},
		{InferClause{Field: "deletedAt", Op: "isNull"}, "IsNull()", true},
		{InferClause{Field: "deletedAt", Op: "isNotNull"}, "IsNotNull()", true},
		{InferClause{Field: "title", Op: "containing"}, `Like("%" + p0 + "%")`, false},
		{InferClause{Field: "title", Op: "startswith"}, `Like(p0 + "%")`, false},
		{InferClause{Field: "title", Op: "endswith"}, `Like("%" + p0)`, false},
		{InferClause{Field: "title", Op: "like"}, "Like(p0)", false},
		{InferClause{Field: "views", Op: "eq"}, "Eq(p0)", false},
		{InferClause{Field: "views", Op: "ne"}, "Neq(p0)", false},
		{InferClause{Field: "views", Op: "gt"}, "Gt(p0)", false},
	}

	for _, tc := range cases {
		t.Run(tc.clause.Field+"_"+tc.clause.Op, func(t *testing.T) {
			var b strings.Builder
			writeAndClause(&b, tc.clause, m, params, 0)
			out := b.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("writeAndClause(%+v) output missing %q:\n%s", tc.clause, tc.want, out)
			}
		})
	}

	t.Run("between uses native Between", func(t *testing.T) {
		var b strings.Builder
		writeAndClause(&b, InferClause{Field: "age", Op: "between"}, m, params, 0)
		out := b.String()
		if !strings.Contains(out, "Between(p0, p1)") {
			t.Errorf("between missing Between(p0, p1):\n%s", out)
		}
	})
}

func TestWriteInferredAction(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		inf := &InferredAPI{Action: "count", ModelName: "User"}
		var b strings.Builder
		writeInferredAction(&b, inf, "User", false)
		out := b.String()
		if !strings.Contains(out, "app.User.Where(conds...).Count(ctx)") {
			t.Errorf("count: missing Count call:\n%s", out)
		}
		if !strings.Contains(out, `"count"`) {
			t.Errorf("count: missing count JSON key:\n%s", out)
		}
	})

	t.Run("exists", func(t *testing.T) {
		inf := &InferredAPI{Action: "exists", ModelName: "User"}
		var b strings.Builder
		writeInferredAction(&b, inf, "User", false)
		out := b.String()
		if !strings.Contains(out, "app.User.Where(conds...).Exists(ctx)") {
			t.Errorf("exists: missing Exists call:\n%s", out)
		}
		if !strings.Contains(out, `"exists"`) {
			t.Errorf("exists: missing exists JSON key:\n%s", out)
		}
	})

	t.Run("delete hard", func(t *testing.T) {
		inf := &InferredAPI{Action: "delete", ModelName: "Post"}
		var b strings.Builder
		writeInferredAction(&b, inf, "Post", false)
		out := b.String()
		if !strings.Contains(out, "app.Post.Where(conds...).Delete(ctx)") {
			t.Errorf("delete hard: missing Delete call:\n%s", out)
		}
	})

	t.Run("delete soft", func(t *testing.T) {
		inf := &InferredAPI{Action: "delete", ModelName: "Post"}
		var b strings.Builder
		writeInferredAction(&b, inf, "Post", true)
		out := b.String()
		if !strings.Contains(out, "app.Post.SoftDelete(ctx, conds...)") {
			t.Errorf("delete soft: missing SoftDelete call:\n%s", out)
		}
	})

	t.Run("get default", func(t *testing.T) {
		inf := &InferredAPI{Action: "get", ModelName: "User"}
		var b strings.Builder
		writeInferredAction(&b, inf, "User", false)
		out := b.String()
		if !strings.Contains(out, "First(ctx)") {
			t.Errorf("get: missing First(ctx):\n%s", out)
		}
		if !strings.Contains(out, "errors.NotFound") {
			t.Errorf("get: missing NotFound error:\n%s", out)
		}
	})

	t.Run("list with orderBy asc", func(t *testing.T) {
		inf := &InferredAPI{Action: "list", ModelName: "User", OrderBy: "name", OrderDesc: false}
		var b strings.Builder
		writeInferredAction(&b, inf, "User", false)
		out := b.String()
		if !strings.Contains(out, "name ASC") {
			t.Errorf("list: missing ASC order:\n%s", out)
		}
		if !strings.Contains(out, "AllWithCount(ctx)") {
			t.Errorf("list: missing AllWithCount:\n%s", out)
		}
	})

	t.Run("list topN no pagination", func(t *testing.T) {
		inf := &InferredAPI{Action: "list", ModelName: "User", TopN: 10}
		var b strings.Builder
		writeInferredAction(&b, inf, "User", false)
		out := b.String()
		if !strings.Contains(out, "Limit(10)") {
			t.Errorf("list topN: missing Limit(10):\n%s", out)
		}
		if strings.Contains(out, "AllWithCount") {
			t.Errorf("list topN: should not have AllWithCount:\n%s", out)
		}
	})
}

func TestOpToMethod(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{"eq", "Eq"},
		{"ne", "Neq"},
		{"gt", "Gt"},
		{"gte", "Gte"},
		{"lt", "Lt"},
		{"lte", "Lte"},
		{"unknown", "Eq"}, // default fallback
	}
	for _, tc := range cases {
		got := opToMethod(tc.op)
		if got != tc.want {
			t.Errorf("opToMethod(%q) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

func TestWriteCondition(t *testing.T) {
	cases := []struct {
		condType  string
		col       string
		method    string
		paramExpr string
		want      string
	}{
		{"IntField", "age", "Gt", "age", "lux.NewIntField(\"age\").Gt(age)"},
		{"FloatField", "score", "Lte", "score", "lux.NewFloatField(\"score\").Lte(score)"},
		{"BoolField", "active", "Eq", "active", "lux.NewBoolField(\"active\").Eq(active)"},
		{"StringField", "email", "Eq", "email", "lux.NewStringField(\"email\").Eq(email)"},
		{"TimeField", "created_at", "Eq", "t", "lux.NewTimeField(\"created_at\").Eq(t)"},
	}
	for _, tc := range cases {
		t.Run(tc.condType+"_"+tc.method, func(t *testing.T) {
			var b strings.Builder
			writeCondition(&b, tc.condType, tc.col, tc.method, tc.paramExpr)
			out := b.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("writeCondition(%q,%q,%q,%q) missing %q:\n%s",
					tc.condType, tc.col, tc.method, tc.paramExpr, tc.want, out)
			}
		})
	}
}

func TestFieldGoType(t *testing.T) {
	m := testModel("Post", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("title", "String"),
		testField("score", "Float"),
		testField("active", "Boolean"),
		testField("createdAt", "DateTime"),
	})

	cases := []struct{ field, want string }{
		{"id", "int64"},
		{"title", "string"},
		{"score", "float64"},
		{"active", "bool"},
		{"createdAt", "time.Time"},
		{"nonexistent", "string"}, // unknown → default string
	}
	for _, tc := range cases {
		got := fieldGoType(m, tc.field)
		if got != tc.want {
			t.Errorf("fieldGoType(m, %q) = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestWriteClauseSQL(t *testing.T) {
	m := testModel("Post", nil, []*ast.FieldDecl{
		testField("title", "String"),
		testField("views", "Int"),
		testField("active", "Boolean"),
		testField("deletedAt", "DateTime"),
	})
	params := []*ast.ParamDecl{
		testParamDecl("p0", "String"),
		testParamDecl("p1", "Int"),
	}

	cases := []struct {
		clause InferClause
		want   string
	}{
		{InferClause{Field: "active", Op: "true"}, `"active = true"`},
		{InferClause{Field: "active", Op: "false"}, `"active = false"`},
		{InferClause{Field: "deletedAt", Op: "isNull"}, `"deleted_at IS NULL"`},
		{InferClause{Field: "deletedAt", Op: "isNotNull"}, `"deleted_at IS NOT NULL"`},
		{InferClause{Field: "title", Op: "containing"}, "LIKE"},
		{InferClause{Field: "title", Op: "like"}, "LIKE"},
		{InferClause{Field: "views", Op: "ne"}, "!="},
		{InferClause{Field: "views", Op: "gt"}, ">"},
		{InferClause{Field: "views", Op: "gte"}, ">="},
		{InferClause{Field: "views", Op: "lt"}, "<"},
		{InferClause{Field: "views", Op: "lte"}, "<="},
		{InferClause{Field: "views", Op: "eq"}, "="},
	}

	for _, tc := range cases {
		t.Run(tc.clause.Field+"_"+tc.clause.Op, func(t *testing.T) {
			var b strings.Builder
			writeClauseSQL(&b, tc.clause, m, params, 0)
			out := b.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("writeClauseSQL(%+v) missing %q:\n%s", tc.clause, tc.want, out)
			}
		})
	}
}

func TestSingularize(t *testing.T) {
	cases := []struct{ input, want string }{
		// "ies" → strip 3 + "y"
		{"Categories", "Category"},
		// "ses"/"xes"/"zes" → strip 2 (removes only "es", keeps first consonant)
		{"Addresses", "Address"},
		{"Boxes", "Box"},
		{"Quizzes", "Quizz"}, // "zes" strips 2: known limitation, result is "Quizz" not "Quiz"
		// plain "s" (not "ss") → strip 1
		{"Users", "User"},
		{"Posts", "Post"},
		{"Tags", "Tag"},
		// "ss" suffix → no change (protected by !HasSuffix("ss") guard)
		{"Class", "Class"},
		{"Grass", "Grass"}, // ends in "ss" → protected, unchanged
		// no trailing s → unchanged
		{"User", "User"},
	}
	for _, tc := range cases {
		got := singularize(tc.input)
		if got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseOrderByAsc(t *testing.T) {
	cases := []struct {
		input     string
		wantField string
		wantDesc  bool
	}{
		{"CreatedAtDesc", "createdAt", true},
		{"CreatedAtAsc", "createdAt", false},
		{"Name", "name", false},           // no suffix → asc
		{"UpdatedAt", "updatedAt", false}, // no suffix
	}
	for _, tc := range cases {
		field, desc := parseOrderBy(tc.input)
		if field != tc.wantField {
			t.Errorf("parseOrderBy(%q) field = %q, want %q", tc.input, field, tc.wantField)
		}
		if desc != tc.wantDesc {
			t.Errorf("parseOrderBy(%q) desc = %v, want %v", tc.input, desc, tc.wantDesc)
		}
	}
}

func TestGetFieldTypeRefRaw(t *testing.T) {
	m := testModel("Post", nil, []*ast.FieldDecl{
		testField("id", "Int"),
		testField("title", "String"),
		testField("score", "Float"),
	})

	t.Run("known Int field", func(t *testing.T) {
		tr := getFieldTypeRefRaw(m, "id")
		if tr == nil {
			t.Fatal("expected non-nil TypeRef for 'id'")
		}
		if tr.Name != "Int" {
			t.Errorf("id raw type = %q, want Int", tr.Name)
		}
	})

	t.Run("known String field", func(t *testing.T) {
		tr := getFieldTypeRefRaw(m, "title")
		if tr == nil {
			t.Fatal("expected non-nil TypeRef for 'title'")
		}
		if tr.Name != "String" {
			t.Errorf("title raw type = %q, want String", tr.Name)
		}
	})

	t.Run("unknown field returns nil", func(t *testing.T) {
		tr := getFieldTypeRefRaw(m, "nonexistent")
		if tr != nil {
			t.Errorf("expected nil for unknown field, got %+v", tr)
		}
	})
}
