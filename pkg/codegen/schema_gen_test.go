package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestLuxoTypeToSchemaType(t *testing.T) {
	tests := []struct {
		typeName string
		enums    map[string]bool
		want     string
	}{
		{"Int", nil, "FieldInt"},
		{"Float", nil, "FieldFloat"},
		{"String", nil, "FieldString"},
		{"Boolean", nil, "FieldBool"},
		{"DateTime", nil, "FieldDateTime"},
		{"Duration", nil, "FieldDuration"},
		{"Bytes", nil, "FieldBytes"},
		{"UnknownType", nil, "FieldString"}, // default
		{"Status", map[string]bool{"Status": true}, "FieldEnum"},
		{"Status", nil, "FieldString"}, // not in enums
	}
	for _, tt := range tests {
		got := luxoTypeToSchemaType(tt.typeName, tt.enums)
		if got != tt.want {
			t.Errorf("luxoTypeToSchemaType(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestLuxoTypeToSchemaFieldType(t *testing.T) {
	tests := []struct {
		typeName string
		enums    map[string]bool
		want     schema.FieldType
	}{
		{"Int", nil, schema.FieldInt},
		{"Float", nil, schema.FieldFloat},
		{"String", nil, schema.FieldString},
		{"Boolean", nil, schema.FieldBool},
		{"DateTime", nil, schema.FieldDateTime},
		{"Duration", nil, schema.FieldDuration},
		{"Bytes", nil, schema.FieldBytes},
		{"Unknown", nil, schema.FieldString}, // default
		{"Status", map[string]bool{"Status": true}, schema.FieldEnum},
	}
	for _, tt := range tests {
		got := luxoTypeToSchemaFieldType(tt.typeName, tt.enums)
		if got != tt.want {
			t.Errorf("luxoTypeToSchemaFieldType(%q) = %v, want %v", tt.typeName, got, tt.want)
		}
	}
}

func TestGenerateSchemaFile_Basic(t *testing.T) {
	// Set up field IDs via lockfile globals
	oldFieldIDs := modelFieldIDs
	oldAPIIDs := apiIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "name": 2, "email": 3},
	}
	apiIDs = map[string]int{
		"getUser":  10,
		"listUser": 11,
	}
	defer func() {
		modelFieldIDs = oldFieldIDs
		apiIDs = oldAPIIDs
	}()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Directives: []*ast.Directive{
					{Name: "crud"},
				},
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}

	code := generateSchemaFile(result, "luxo", nil)
	if code == nil {
		t.Fatal("should generate schema file")
	}
	src := string(code)

	checks := []string{
		"RegisterSchema",
		"schema.Schema",
		`Name: "User"`,
		"schema.Field{",
		`Name: "id"`,
		"schema.FieldInt",
		`Name: "name"`,
		"schema.FieldString",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in schema:\n%s", check, src)
		}
	}
}

func TestGenerateSchemaFile_SkipsHiddenFields(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "password": 2},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
				},
			}},
		}},
	}

	code := generateSchemaFile(result, "luxo", nil)
	src := string(code)

	if strings.Contains(src, "password") {
		t.Error("@hidden field should be excluded from schema")
	}
}

func TestGenerateSchemaFile_SkipsComputedFields(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "fullName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
				},
			}},
		}},
	}

	code := generateSchemaFile(result, "luxo", nil)
	src := string(code)
	if strings.Contains(src, "fullName") {
		t.Error("computed field should be excluded from schema")
	}
}

func TestGenerateSchemaFile_Nil(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	code := generateSchemaFile(result, "luxo", nil)
	if code != nil {
		t.Error("should return nil for empty result")
	}
}

func TestGenerateSchemaFile_WithServiceFns(t *testing.T) {
	oldAPIIDs := apiIDs
	apiIDs = map[string]int{
		"svc:getUserScore": 50,
	}
	defer func() { apiIDs = oldAPIIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Functions: []*ast.FnDecl{{
				Name:       "getUserScore",
				Directives: []*ast.Directive{{Name: "service"}},
				ReturnType: &ast.TypeRef{Name: "Float"},
				Params: []*ast.ParamDecl{
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
				},
				Body: &ast.Block{},
			}},
		}},
	}

	code := generateSchemaFile(result, "luxo", nil)
	if code == nil {
		t.Fatal("should generate schema for service fns")
	}
	src := string(code)

	if !strings.Contains(src, "svc:getUserScore") {
		t.Errorf("missing service fn API:\n%s", src)
	}
}

func TestGetAPIParamID_NilMap(t *testing.T) {
	old := apiParamIDs
	apiParamIDs = nil
	defer func() { apiParamIDs = old }()

	id := getAPIParamID("getUser", "id")
	if id != 0 {
		t.Errorf("nil map should return 0, got %d", id)
	}
}

func TestGetAPIParamID_MissingAPI(t *testing.T) {
	old := apiParamIDs
	apiParamIDs = map[string]map[string]int{
		"getUser": {"id": 1},
	}
	defer func() { apiParamIDs = old }()

	id := getAPIParamID("nonexistent", "id")
	if id != 0 {
		t.Errorf("missing API should return 0, got %d", id)
	}
}

func TestGetAPIParamID_Found(t *testing.T) {
	old := apiParamIDs
	apiParamIDs = map[string]map[string]int{
		"getUser": {"id": 1, "name": 2},
	}
	defer func() { apiParamIDs = old }()

	if id := getAPIParamID("getUser", "id"); id != 1 {
		t.Errorf("got %d, want 1", id)
	}
	if id := getAPIParamID("getUser", "name"); id != 2 {
		t.Errorf("got %d, want 2", id)
	}
}

func TestWriteAPIRegistrationSchema(t *testing.T) {
	oldAPIIDs := apiIDs
	oldParamIDs := apiParamIDs
	apiIDs = map[string]int{"createUser": 20}
	apiParamIDs = map[string]map[string]int{
		"createUser": {"name": 1, "email": 2},
	}
	defer func() {
		apiIDs = oldAPIIDs
		apiParamIDs = oldParamIDs
	}()

	var b strings.Builder
	params := []*ast.ParamDecl{
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String"}},
	}
	retType := &ast.TypeRef{Name: "User"}

	writeAPIRegistrationSchema(&b, "createUser", "user", params, retType, false)
	src := b.String()

	checks := []string{
		"RegisterAPI",
		`Name: "createUser"`,
		`Module: "user"`,
		`ReturnType: "User"`,
		"schema.Param{",
		`Name: "name"`,
		`Name: "email"`,
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q:\n%s", check, src)
		}
	}
}

func TestBuildSchemaJSON_Full(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	oldAPIIDs := apiIDs
	oldParamIDs := apiParamIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "name": 2},
	}
	apiIDs = map[string]int{
		"getUser":  10,
		"listUser": 11,
	}
	apiParamIDs = map[string]map[string]int{
		"getUser": {"id": 1},
	}
	defer func() {
		modelFieldIDs = oldFieldIDs
		apiIDs = oldAPIIDs
		apiParamIDs = oldParamIDs
	}()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
			APIs: []*ast.ApiDecl{{
				Name:       "getUser",
				ReturnType: &ast.TypeRef{Name: "User"},
				Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
			}},
		}},
	}

	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("should produce JSON")
	}
	s := string(data)
	if !strings.Contains(s, "User") {
		t.Error("missing User model")
	}
	if !strings.Contains(s, "getUser") {
		t.Error("missing getUser API")
	}
}

func TestBuildSchemaJSON_SkipsHiddenAndRelation(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "password": 2, "posts": 3},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "internal"}}},
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}}, // relation
				},
			}},
		}},
	}

	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "password") {
		t.Error("@internal field should be excluded")
	}
	// "posts" is a relation field — should be included but marked relation:true
	if !strings.Contains(s, "posts") {
		t.Error("relation field should be included in schema")
	}
	if !strings.Contains(s, `"relation":true`) {
		t.Error("relation field should have relation:true flag")
	}
}

func TestBuildSchemaJSON_WithExtendStubs(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "phone": 2},
		"Post": {"id": 1},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					},
				}},
				Extends: []*ast.ExtendDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "phone", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
			},
		},
	}

	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// User from extend stub should be in schema
	if !strings.Contains(s, "User") {
		t.Error("extend stub User should be in schema")
	}
}

func TestBuildSchemaJSON_WithAPIPaginated(t *testing.T) {
	oldAPIIDs := apiIDs
	apiIDs = map[string]int{"search": 1}
	defer func() { apiIDs = oldAPIIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			APIs: []*ast.ApiDecl{{
				Name:       "search",
				ReturnType: &ast.TypeRef{Name: "User", IsList: true},
				Directives: []*ast.Directive{{Name: "paginate"}},
			}},
		}},
	}

	data, _ := BuildSchemaJSON(result, nil)
	s := string(data)
	if !strings.Contains(s, `"paginated":true`) {
		t.Errorf("should mark API as paginated: %s", s)
	}
}

func TestWriteAPIRegistrationSchema_Paginated(t *testing.T) {
	oldAPIIDs := apiIDs
	apiIDs = map[string]int{"listUser": 30}
	defer func() { apiIDs = oldAPIIDs }()

	var b strings.Builder
	writeAPIRegistrationSchema(&b, "listUser", "user", nil, &ast.TypeRef{Name: "User", IsList: true}, true)
	src := b.String()

	if !strings.Contains(src, "Paginated: true") {
		t.Errorf("missing Paginated:\n%s", src)
	}
	if !strings.Contains(src, "ReturnList: true") {
		t.Errorf("missing ReturnList:\n%s", src)
	}
}
