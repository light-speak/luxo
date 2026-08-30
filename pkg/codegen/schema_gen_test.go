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
		`Module: "user"`,
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

func TestGenerateSchemaFileRegistersEnumsWithModule(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name:  "origin/auth/member.luxo",
		Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER", "ADMIN"}}},
	}}}
	src := string(generateSchemaFile(result, "luxo", map[string]bool{"Role": true}))
	for _, want := range []string{`RegisterEnum`, `Name: "Role"`, `Module: "auth"`, `"USER", "ADMIN"`} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated schema missing %q:\n%s", want, src)
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
		{Name: "email", Type: &ast.TypeRef{Name: "String", Nullable: true}},
		{Name: "role", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Value: "MEMBER"}},
	}
	retType := &ast.TypeRef{Name: "User"}

	writeAPIRegistrationSchema(&b, "createUser", "user", params, retType, false, false)
	src := b.String()

	checks := []string{
		"RegisterAPI",
		`Name: "createUser"`,
		`Module: "user"`,
		`ReturnType: "User"`,
		"schema.Param{",
		`Name: "name"`,
		`Name: "email"`,
		`Name: "email", Type: schema.FieldString, TypeName: "String", Nullable: true`,
		`Name: "role", Type: schema.FieldString, TypeName: "String", HasDefault: true`,
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q:\n%s", check, src)
		}
	}
}

func TestBuildSchemaJSON_PreservesOptionalParams(t *testing.T) {
	oldAPIIDs := apiIDs
	oldParamIDs := apiParamIDs
	apiIDs = map[string]int{"createProject": 10}
	apiParamIDs = map[string]map[string]int{
		"createProject": {"name": 1, "description": 2, "environment": 3},
	}
	defer func() {
		apiIDs = oldAPIIDs
		apiParamIDs = oldParamIDs
	}()

	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/project.luxo",
		APIs: []*ast.ApiDecl{{
			Name:       "createProject",
			ReturnType: &ast.TypeRef{Name: "Project"},
			Params: []*ast.ParamDecl{
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				{Name: "description", Type: &ast.TypeRef{Name: "String", Nullable: true}},
				{Name: "environment", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Value: "development"}},
			},
		}},
	}}}

	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"name":"description","type":"String","typeName":"String","nullable":true`) {
		t.Errorf("nullable parameter metadata missing: %s", s)
	}
	if !strings.Contains(s, `"name":"environment","type":"String","typeName":"String","hasDefault":true`) {
		t.Errorf("default parameter metadata missing: %s", s)
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
	writeAPIRegistrationSchema(&b, "listUser", "user", nil, &ast.TypeRef{Name: "User", IsList: true}, true, false)
	src := b.String()

	if !strings.Contains(src, "Paginated: true") {
		t.Errorf("missing Paginated:\n%s", src)
	}
	if !strings.Contains(src, "ReturnList: true") {
		t.Errorf("missing ReturnList:\n%s", src)
	}
}

func TestBuildSchemaJSON_WithEnumsAndTypes(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User":        {"id": 1, "name": 2, "role": 3},
		"AuthPayload": {"member": 1, "token": 2},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/auth.luxo",
			Enums: []*ast.EnumDecl{
				{Name: "MemberRole", Values: []string{"OWNER", "ADMIN", "VIEWER"}},
			},
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "role", Type: &ast.TypeRef{Name: "MemberRole"}},
				},
			}},
			Types: []*ast.TypeDecl{{
				Name: "AuthPayload",
				Fields: []*ast.FieldDecl{
					{Name: "member", Type: &ast.TypeRef{Name: "User"}},
					{Name: "token", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		}},
	}

	enums := map[string]bool{"MemberRole": true}
	data, err := BuildSchemaJSON(result, enums)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)

	// Enum
	if !strings.Contains(s, `"MemberRole"`) {
		t.Error("should contain enum name")
	}
	if !strings.Contains(s, `"OWNER"`) {
		t.Error("should contain enum value")
	}

	// Type
	if !strings.Contains(s, `"AuthPayload"`) {
		t.Error("should contain type name")
	}
	if !strings.Contains(s, `"token"`) {
		t.Error("should contain type field")
	}

	// Model field with enum type
	if !strings.Contains(s, `"typeName":"MemberRole"`) {
		t.Error("should contain typeName for enum field")
	}

	// Relation flag
	if !strings.Contains(s, `"typeName":"User"`) {
		t.Error("should contain typeName for type field referencing model")
	}
}

func TestBuildSchemaTypes_NilTypeSkipped(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Types: []*ast.TypeDecl{{
				Name: "Broken",
				Fields: []*ast.FieldDecl{
					{Name: "ok", Type: &ast.TypeRef{Name: "String"}},
					{Name: "noType", Type: nil},
				},
			}},
		}},
	}
	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "ok") {
		t.Error("should contain ok field")
	}
	if strings.Contains(s, "noType") {
		t.Error("nil type field should be skipped")
	}
}

func TestBuildSchemaModels_IsList(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"Post": {"id": 1, "tags": 2},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/post.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "Post",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}},
				},
			}},
		}},
	}
	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"isList":true`) {
		t.Error("should have isList:true for list field")
	}
}

func TestBuildSchemaModels_SkipsComputed(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "name": 2},
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
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
					{Name: "fullName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
				},
			}},
		}},
	}
	data, err := BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fullName") {
		t.Error("computed field should be skipped")
	}
}

func TestWriteTypeRegistration(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"AuthPayload": {"token": 1, "user": 2},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	td := &ast.TypeDecl{
		Name: "AuthPayload",
		Fields: []*ast.FieldDecl{
			{Name: "token", Type: &ast.TypeRef{Name: "String"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User", IsList: false}},
		},
	}
	enums := map[string]bool{}

	var b strings.Builder
	writeTypeRegistration(&b, td, "test", enums)
	src := b.String()

	checks := []string{
		"RegisterType",
		`Name: "AuthPayload"`,
		`Name: "token"`,
		"schema.FieldString",
		`Name: "user"`,
		`TypeName: "User"`,
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in output:\n%s", check, src)
		}
	}
}

func TestWriteTypeRegistrationRelationFieldModel(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	modelFieldIDs = map[string]map[string]int{
		"MetricTimeSeries": {"apiName": 1, "points": 2},
	}
	defer func() { modelFieldIDs = oldFieldIDs }()

	td := &ast.TypeDecl{
		Name: "MetricTimeSeries",
		Fields: []*ast.FieldDecl{
			{Name: "apiName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "points", Type: &ast.TypeRef{Name: "MetricPoint", IsList: true}},
		},
	}

	var b strings.Builder
	writeTypeRegistration(&b, td, "test", map[string]bool{})
	src := b.String()

	// Nested type/model references must register as FieldModel — the columnar
	// converter dispatches blob decoding on Type==FieldModel, and FieldString
	// would make it misread the blob as a scalar string array.
	if !strings.Contains(src, `Name: "points", Type: schema.FieldModel, TypeName: "MetricPoint"`) {
		t.Errorf("relation field should register as FieldModel:\n%s", src)
	}
	if !strings.Contains(src, `Name: "apiName", Type: schema.FieldString`) {
		t.Errorf("scalar field should stay FieldString:\n%s", src)
	}
}

func TestGenerateSchemaFile_WithTypes(t *testing.T) {
	oldFieldIDs := modelFieldIDs
	oldAPIIDs := apiIDs
	modelFieldIDs = map[string]map[string]int{
		"User":        {"id": 1},
		"AuthPayload": {"token": 1, "expiresAt": 2},
	}
	apiIDs = map[string]int{}
	defer func() {
		modelFieldIDs = oldFieldIDs
		apiIDs = oldAPIIDs
	}()

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/auth.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
			Types: []*ast.TypeDecl{{
				Name: "AuthPayload",
				Fields: []*ast.FieldDecl{
					{Name: "token", Type: &ast.TypeRef{Name: "String"}},
					{Name: "expiresAt", Type: &ast.TypeRef{Name: "DateTime"}},
				},
			}},
		}},
	}

	code := generateSchemaFile(result, "luxo", nil)
	if code == nil {
		t.Fatal("should generate schema file with types")
	}
	src := string(code)

	checks := []string{
		"RegisterType",
		`Name: "AuthPayload"`,
		`Name: "token"`,
		"schema.FieldString",
		`Name: "expiresAt"`,
		"schema.FieldDateTime",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in schema:\n%s", check, src)
		}
	}
}

// --- Federation tests ---

func TestBuildSchemaModels_ExtendFieldModule(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	})

	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "user.luxo",
				Models: []*ast.ModelDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "name", Type: &ast.TypeRef{Name: "String"}},
						// posts is added by extend from post.luxo, but appears here after semantic merge
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				}},
			},
			{
				Name: "post.luxo",
				Models: []*ast.ModelDecl{{
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					},
				}},
				Extends: []*ast.ExtendDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				}},
			},
		},
	}

	s := schema.New()
	buildSchemaModels(s, result, nil)

	user := s.Models["User"]
	if user == nil {
		t.Fatal("User model not registered")
	}

	// Find the posts field
	var postsField *schema.Field
	for i := range user.Fields {
		if user.Fields[i].Name == "posts" {
			postsField = &user.Fields[i]
			break
		}
	}
	if postsField == nil {
		t.Fatal("posts field not found in User model")
	}
	if postsField.Module != "post" {
		t.Errorf("posts.Module = %q, want %q", postsField.Module, "post")
	}
	if postsField.ForeignKey != "userId" {
		t.Errorf("posts.ForeignKey = %q, want %q", postsField.ForeignKey, "userId")
	}
	if !postsField.Relation {
		t.Error("posts should be a relation field")
	}

	// name field should have no module (same module as model)
	nameField := user.FieldByName("name")
	if nameField == nil {
		t.Fatal("name field not found")
	}
	if nameField.Module != "" {
		t.Errorf("name.Module should be empty, got %q", nameField.Module)
	}
}

func TestWriteModelRegistration_ExtendRelation(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
	})

	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
		},
	}

	extendModules := map[string]string{
		"posts": "post",
	}

	var b strings.Builder
	writeModelRegistration(&b, m, "base", nil, extendModules)
	code := b.String()

	// Should include relation field with Module and ForeignKey
	if !strings.Contains(code, `Module: "post"`) {
		t.Errorf("missing Module in relation field:\n%s", code)
	}
	if !strings.Contains(code, `ForeignKey: "userId"`) {
		t.Errorf("missing ForeignKey in relation field:\n%s", code)
	}
	if !strings.Contains(code, `Relation: true`) {
		t.Errorf("missing Relation: true:\n%s", code)
	}
	if !strings.Contains(code, `IsList: true`) {
		t.Errorf("missing IsList: true:\n%s", code)
	}
}

func TestBuildSchemaModels_SameModuleRelationNoModule(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	})

	// User and Post in same file — relation is NOT cross-module
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "user.luxo",
			Models: []*ast.ModelDecl{
				{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "name", Type: &ast.TypeRef{Name: "String"}},
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				},
				{
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					},
				},
			},
		}},
	}

	s := schema.New()
	buildSchemaModels(s, result, nil)

	user := s.Models["User"]
	for i := range user.Fields {
		if user.Fields[i].Name == "posts" {
			if user.Fields[i].Module != "" {
				t.Errorf("same-module relation should have empty Module, got %q", user.Fields[i].Module)
			}
			return
		}
	}
}

func TestBuildSchemaModels_HasExtendFields(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 10, Name: "posts", Type: schema.FieldModel, Relation: true, Module: "post"},
		},
	})

	if !s.Models["User"].HasExtendFields() {
		t.Error("User should have extend fields")
	}

	s.RegisterModel(&schema.Model{
		Name: "Post",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
		},
	})

	if s.Models["Post"].HasExtendFields() {
		t.Error("Post should not have extend fields")
	}
}

func TestInferForeignKey_ExplicitBy(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "posts",
		Type: &ast.TypeRef{Name: "Post", IsList: true},
		Directives: []*ast.Directive{{
			Name: "by",
			Args: []*ast.NamedArg{
				{Value: &ast.Ident{Name: "authorId"}},
				{Value: &ast.Ident{Name: "id"}},
			},
		}},
	}
	fk := inferForeignKey(&ast.ModelDecl{Name: "User"}, f, nil)
	if fk != "authorId" {
		t.Errorf("expected authorId, got %q", fk)
	}
}

func TestInferForeignKey_BelongsTo(t *testing.T) {
	f := &ast.FieldDecl{
		Name: "author",
		Type: &ast.TypeRef{Name: "User"},
	}
	fk := inferForeignKey(&ast.ModelDecl{Name: "Post"}, f, nil)
	if fk != "id" {
		t.Errorf("belongsTo should return 'id', got %q", fk)
	}
}

func TestGenerateSchemaFile_WithExtendResolve(t *testing.T) {
	old := modelFieldIDs
	oldAPIs := apiIDs
	defer func() { modelFieldIDs = old; apiIDs = oldAPIs }()

	SetModelFieldIDs(map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	})
	SetAPIIDs(map[string]int{
		"getUser":                 1,
		"svc:batchLoad:Post":      50,
		"svc:resolve:Post:userId": 51,
	})

	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "user.luxo",
				Models: []*ast.ModelDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "name", Type: &ast.TypeRef{Name: "String"}},
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
			{
				Name: "post.luxo",
				Models: []*ast.ModelDecl{{
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "title", Type: &ast.TypeRef{Name: "String"}},
					},
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
				Extends: []*ast.ExtendDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				}},
			},
		},
	}

	src := generateSchemaFile(result, "app", nil)
	code := string(src)

	// Should register svc:batchLoad and svc:resolve APIs
	if !strings.Contains(code, "svc:batchLoad:Post") {
		t.Errorf("missing batchLoad API registration:\n%s", code)
	}
	if !strings.Contains(code, "svc:resolve:Post:userId") {
		t.Errorf("missing resolve API registration:\n%s", code)
	}
}
