package codegen

import (
	"encoding/json"
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
		{"Decimal", nil, "FieldDecimal"},
		{"JSON", nil, "FieldJSON"},
		{"UnknownType", nil, "FieldModel"}, // nested model/type
		{"Status", map[string]bool{"Status": true}, "FieldEnum"},
		{"Status", nil, "FieldModel"}, // not in enums: nested model/type
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
		{"Decimal", nil, schema.FieldDecimal},
		{"JSON", nil, schema.FieldJSON},
		{"Unknown", nil, schema.FieldModel}, // nested model/type
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2, "email": 3}},
		APIs:        map[string]int{"getUser": 10, "listUser": 11},
	}})

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

	code := generator.generateSchemaFile(result, "luxo", nil)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "password": 2},
	}}})

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

	code := generator.generateSchemaFile(result, "luxo", nil)
	src := string(code)

	if strings.Contains(src, "password") {
		t.Error("@hidden field should be excluded from schema")
	}
}

func TestGenerateSchemaFile_IncludesComputedFields(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "fullName": 2},
	}}})

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

	code := generator.generateSchemaFile(result, "luxo", nil)
	src := string(code)
	if !strings.Contains(src, `Name: "fullName"`) || !strings.Contains(src, "Computed: true") {
		t.Error("computed field should be included in schema")
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{
		"svc:getUserScore": 50,
	}}})

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

	code := generator.generateSchemaFile(result, "luxo", nil)
	if code == nil {
		t.Fatal("should generate schema for service fns")
	}
	src := string(code)

	if !strings.Contains(src, "svc:getUserScore") {
		t.Errorf("missing service fn API:\n%s", src)
	}
}

func TestGetAPIParamID_NilMap(t *testing.T) {
	id := defaultGenerator().apiParamID("getUser", "id")
	if id != 0 {
		t.Errorf("nil map should return 0, got %d", id)
	}
}

func TestGetAPIParamID_MissingAPI(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIParams: map[string]map[string]int{
		"getUser": {"id": 1},
	}}})

	id := generator.apiParamID("nonexistent", "id")
	if id != 0 {
		t.Errorf("missing API should return 0, got %d", id)
	}
}

func TestGetAPIParamID_Found(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIParams: map[string]map[string]int{
		"getUser": {"id": 1, "name": 2},
	}}})

	if id := generator.apiParamID("getUser", "id"); id != 1 {
		t.Errorf("got %d, want 1", id)
	}
	if id := generator.apiParamID("getUser", "name"); id != 2 {
		t.Errorf("got %d, want 2", id)
	}
}

func TestWriteAPIRegistrationSchema(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:      map[string]int{"createUser": 20},
		APIParams: map[string]map[string]int{"createUser": {"name": 1, "email": 2}},
	}})

	var b strings.Builder
	params := []*ast.ParamDecl{
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		{Name: "email", Type: &ast.TypeRef{Name: "String", Nullable: true}},
		{Name: "role", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Value: "MEMBER"}},
	}
	retType := &ast.TypeRef{Name: "User"}

	generator.writeAPIRegistrationSchema(&b, "createUser", "user", params, retType, false, false, nil, nil)
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

func TestWriteAPIRegistrationSchemaListParam(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:      map[string]int{"search": 21},
		APIParams: map[string]map[string]int{"search": {"tags": 1}},
	}})

	var b strings.Builder
	params := []*ast.ParamDecl{{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true}}}
	generator.writeAPIRegistrationSchema(&b, "search", "search", params, nil, false, false, nil, nil)
	if src := b.String(); !strings.Contains(src, `Name: "tags", Type: schema.FieldString, TypeName: "String", IsList: true`) {
		t.Fatalf("list parameter metadata missing:\n%s", src)
	}
}

func TestBuildSchemaJSON_PreservesOptionalParams(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs: map[string]int{"createProject": 10},
		APIParams: map[string]map[string]int{
			"createProject": {"name": 1, "description": 2, "environment": 3},
		}}})

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

	data, err := generator.BuildSchemaJSON(result, nil)
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

func TestBuildSchemaJSONUsesJSONWireTypeForStructuredParams(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:      map[string]int{"createProject": 10},
		APIParams: map[string]map[string]int{"createProject": {"input": 1}},
	}})

	result := &semantic.Result{Files: []*ast.File{{
		Name:  "origin/project.luxo",
		Types: []*ast.TypeDecl{{Name: "CreateProjectInput"}},
		APIs: []*ast.ApiDecl{{
			Name: "createProject",
			Params: []*ast.ParamDecl{{
				Name: "input", Type: &ast.TypeRef{Name: "CreateProjectInput"},
			}},
		}},
	}}}

	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"input","type":"JSON","typeName":"CreateProjectInput"`) {
		t.Fatalf("structured param metadata = %s", data)
	}
}

func TestLuxoParamToSchemaTypeUsesJSONForStructuredValues(t *testing.T) {
	if got := luxoParamToSchemaType("Payload", nil); got != "FieldJSON" {
		t.Fatalf("structured parameter type = %q", got)
	}
	if got := luxoParamToSchemaType("Int", nil); got != "FieldInt" {
		t.Fatalf("integer parameter type = %q", got)
	}
}

func TestInferFederationForeignKeyUsesExplicitRemoteField(t *testing.T) {
	field := &ast.FieldDecl{Directives: []*ast.Directive{{
		Name: "by", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "tenantId"}}},
	}}}
	if got := defaultGenerator().inferFederationForeignKey(&ast.ModelDecl{Name: "User"}, field); got != "tenantId" {
		t.Fatalf("federation foreign key = %q", got)
	}
}

func TestBuildSchemaJSONSkipsInvalidExtensionField(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/post.luxo",
		Extends: []*ast.ExtendDecl{{
			Name:   "User",
			Fields: []*ast.FieldDecl{{Name: "invalid"}},
		}},
	}}}
	if _, err := defaultGenerator().BuildSchemaJSON(result, nil); err != nil {
		t.Fatalf("BuildSchemaJSON() error = %v", err)
	}
}

func TestBuildSchemaJSONIncludesCompleteCRUDParams(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{
		"getProject": 1, "listProjects": 2, "createProject": 3,
		"updateProject": 4, "deleteProject": 5, "deleteProjects": 6,
	}, APIParams: map[string]map[string]int{
		"getProject": {"id": 1}, "listProjects": {"page": 1, "pageSize": 2},
		"createProject": {"name": 1, "description": 2},
		"updateProject": {"id": 1, "name": 2, "description": 3},
		"deleteProject": {"id": 1}, "deleteProjects": {"ids": 1},
	}}})

	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/project.luxo",
		Models: []*ast.ModelDecl{{
			Name:       "Project",
			Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "UUID"}, Directives: []*ast.Directive{{Name: "auto"}}},
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				{Name: "description", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			},
		}},
	}}}
	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got schema.Schema
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	create := got.APIs["createProject"]
	update := got.APIs["updateProject"]
	deleteMany := got.APIs["deleteProjects"]
	if len(create.Params) != 2 || create.Params[1].Name != "description" || !create.Params[1].Nullable || !create.Params[1].HasDefault {
		t.Fatalf("create params = %+v", create.Params)
	}
	if len(update.Params) != 3 || update.Params[0].Type != schema.FieldUUID {
		t.Fatalf("update params = %+v", update.Params)
	}
	if update.Params[0].HasDefault || !update.Params[1].HasDefault || !update.Params[2].HasDefault {
		t.Fatalf("only mutable update fields should be optional: %+v", update.Params)
	}
	if len(deleteMany.Params) != 1 || !deleteMany.Params[0].IsList || deleteMany.ReturnType != "Int" {
		t.Fatalf("deleteMany = %+v", deleteMany)
	}
}

func TestBuildSchemaJSON_Full(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1, "name": 2}},
		APIs:        map[string]int{"getUser": 10, "listUser": 11},
		APIParams:   map[string]map[string]int{"getUser": {"id": 1}},
	}})

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

	data, err := generator.BuildSchemaJSON(result, nil)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "password": 2, "posts": 3},
	}}})

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

	data, err := generator.BuildSchemaJSON(result, nil)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "phone": 2},
		"Post": {"id": 1},
	}}})

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

	data, err := generator.BuildSchemaJSON(result, nil)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"search": 1}}})

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

	data, _ := generator.BuildSchemaJSON(result, nil)
	s := string(data)
	if !strings.Contains(s, `"paginated":true`) {
		t.Errorf("should mark API as paginated: %s", s)
	}
}

func TestBuildSchemaJSONMarksDeclaredPrimaryKey(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Product": {"sku": 7, "name": 8}}}})

	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/product.luxo",
		Models: []*ast.ModelDecl{{
			Name: "Product",
			Fields: []*ast.FieldDecl{
				{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			},
		}},
	}}}

	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"sku"`) || !strings.Contains(string(data), `"primaryKey":true`) {
		t.Fatalf("schema does not mark declared primary key: %s", data)
	}
}

func TestBuildSchemaJSONMergesExtensionAndKeepsProjectionLocal(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "userId": 2},
	}}})

	user := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}}
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
	}}
	result := &semantic.Result{Files: []*ast.File{
		{Name: "origin/user.luxo", Models: []*ast.ModelDecl{user}},
		{Name: "origin/post.luxo", Models: []*ast.ModelDecl{post}, Extends: []*ast.ExtendDecl{{
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			},
		}}},
	}}

	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got schema.Schema
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	fields := make(map[string]schema.Field)
	for _, field := range got.Models["User"].Fields {
		fields[field.Name] = field
	}
	if len(fields) != 3 || fields["name"].Module != "" || fields["posts"].Module != "post" {
		t.Fatalf("merged User fields = %#v", fields)
	}
}

func TestWriteAPIRegistrationSchema_Paginated(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"listUser": 30}}})

	var b strings.Builder
	generator.writeAPIRegistrationSchema(&b, "listUser", "user", nil, &ast.TypeRef{Name: "User", IsList: true}, true, false, nil, nil)
	src := b.String()

	if !strings.Contains(src, "Paginated: true") {
		t.Errorf("missing Paginated:\n%s", src)
	}
	if !strings.Contains(src, "ReturnList: true") {
		t.Errorf("missing ReturnList:\n%s", src)
	}
}

func TestBuildSchemaJSON_WithEnumsAndTypes(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User":        {"id": 1, "name": 2, "role": 3},
		"AuthPayload": {"member": 1, "token": 2},
	}}})

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
	data, err := generator.BuildSchemaJSON(result, enums)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"Post": {"id": 1, "tags": 2},
	}}})

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
	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"isList":true`) {
		t.Error("should have isList:true for list field")
	}
}

func TestBuildSchemaModels_IncludesComputed(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "fullName": 3},
	}}})

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
	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"name":"fullName"`) || !strings.Contains(string(data), `"computed":true`) {
		t.Error("computed field should be included")
	}
}

func TestWriteTypeRegistration(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"AuthPayload": {"token": 1, "user": 2},
	}}})

	td := &ast.TypeDecl{
		Name: "AuthPayload",
		Fields: []*ast.FieldDecl{
			{Name: "token", Type: &ast.TypeRef{Name: "String"}},
			{Name: "user", Type: &ast.TypeRef{Name: "User", IsList: false}},
		},
	}
	enums := map[string]bool{}

	var b strings.Builder
	generator.writeTypeRegistration(&b, td, "test", enums)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"MetricTimeSeries": {"apiName": 1, "points": 2},
	}}})

	td := &ast.TypeDecl{
		Name: "MetricTimeSeries",
		Fields: []*ast.FieldDecl{
			{Name: "apiName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "points", Type: &ast.TypeRef{Name: "MetricPoint", IsList: true}},
		},
	}

	var b strings.Builder
	generator.writeTypeRegistration(&b, td, "test", map[string]bool{})
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User":        {"id": 1},
		"AuthPayload": {"token": 1, "expiresAt": 2},
	}}})

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

	code := generator.generateSchemaFile(result, "luxo", nil)
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
	if !strings.Contains(src, "s.InferTypeUsage()") {
		t.Errorf("generated schema must finalize type usage:\n%s", src)
	}
}

func TestBuildSchemaJSONInfersTypeUsage(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{
			"CreateInput": {"name": 1},
			"Payload":     {"name": 1},
		},
		APIs: map[string]int{"create": 1},
		APIParams: map[string]map[string]int{
			"create": {"input": 1},
		},
	}})
	result := &semantic.Result{Files: []*ast.File{{
		Name: "api.luxo",
		Types: []*ast.TypeDecl{
			{Name: "CreateInput", Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}}},
			{Name: "Payload", Fields: []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}}},
		},
		APIs: []*ast.ApiDecl{{
			Name:       "create",
			Params:     []*ast.ParamDecl{{Name: "input", Type: &ast.TypeRef{Name: "CreateInput"}}},
			ReturnType: &ast.TypeRef{Name: "Payload"},
		}},
	}}}

	data, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var decoded schema.Schema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Types["CreateInput"].Usage != schema.TypeUsageInput {
		t.Fatalf("CreateInput usage = %q", decoded.Types["CreateInput"].Usage)
	}
	if decoded.Types["Payload"].Usage != schema.TypeUsageOutput {
		t.Fatalf("Payload usage = %q", decoded.Types["Payload"].Usage)
	}
}

// --- Federation tests ---

func TestBuildSchemaModels_ExtendFieldModule(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	}}})

	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "user.luxo",
				Models: []*ast.ModelDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "name", Type: &ast.TypeRef{Name: "String"}},
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
	generator.buildSchemaModels(s, result, nil)

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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
	}}})

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
	generator.writeModelRegistration(&b, m, "base", nil, extendModules)
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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	}}})

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
	generator.buildSchemaModels(s, result, nil)

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
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "name": 2, "posts": 10},
		"Post": {"id": 1, "title": 2},
	}, APIs: map[string]int{
		"getUser":                 1,
		"svc:batchLoad:Post":      50,
		"svc:resolve:Post:userId": 51,
	}}})

	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "user.luxo",
				Models: []*ast.ModelDecl{{
					Name: "User",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "name", Type: &ast.TypeRef{Name: "String"}},
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

	src := generator.generateSchemaFile(result, "app", nil)
	code := string(src)

	// Should register svc:batchLoad and svc:resolve APIs
	if !strings.Contains(code, "svc:batchLoad:Post") {
		t.Errorf("missing batchLoad API registration:\n%s", code)
	}
	if !strings.Contains(code, "svc:resolve:Post:userId") {
		t.Errorf("missing resolve API registration:\n%s", code)
	}
}

func TestGenerateSchemaFileUsesExtendedModelPrimaryKey(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		ModelModule:  map[string]string{"Product": "product", "Review": "review"},
		ModelIDField: map[string]string{"Product": "sku", "Review": "id"},
		ModelIDType:  map[string]string{"Product": "String", "Review": "Int"},
		ModelFields: map[string]map[string]bool{
			"Product": {"sku": true, "name": true},
			"Review":  {"id": true, "productSku": true},
		},
	}, IDs: StableIDs{ModelFields: map[string]map[string]int{
		"Product": {"sku": 1, "reviews": 10},
		"Review":  {"id": 1, "productSku": 2},
	}, APIs: map[string]int{"svc:resolve:Review:productSku": 51}}})
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/review.luxo",
		Models: []*ast.ModelDecl{{
			Name: "Review",
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "productSku", Type: &ast.TypeRef{Name: "String"}},
			},
		}},
		Extends: []*ast.ExtendDecl{{
			Name:   "Product",
			Fields: []*ast.FieldDecl{{Name: "reviews", Type: &ast.TypeRef{Name: "Review", IsList: true}}},
		}},
	}}}

	code := string(generator.generateSchemaFile(result, "app", nil))
	for _, want := range []string{
		`ForeignKey: "productSku"`,
		`Name: "svc:resolve:Review:productSku"`,
		`Name: "keys", Type: schema.FieldString, TypeName: "String", IsList: true`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("custom primary-key schema missing %q:\n%s", want, code)
		}
	}
}

func TestGenerateSchemaFileWithRemoteNamedLoad(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{remoteLoadCalls: map[string][]loadCallInfo{
		"user": {{
			modelName:    "User",
			argNames:     []string{"email"},
			argTypeNames: []string{"String"},
		}},
	}}, IDs: StableIDs{
		APIs:      map[string]int{"svc:load:User:email": 73},
		APIParams: map[string]map[string]int{"svc:load:User:email": {"email": 6}},
	}})
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/user.luxo",
		Models: []*ast.ModelDecl{{
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			},
		}},
	}}}

	code := string(generator.generateSchemaFile(result, "luxo", nil))
	for _, check := range []string{
		`ID: 73, Name: "svc:load:User:email", Module: "user"`,
		`ID: 6, Name: "email", Type: schema.FieldString, TypeName: "String", IsList: true`,
	} {
		if !strings.Contains(code, check) {
			t.Errorf("named load schema missing %q:\n%s", check, code)
		}
	}
}
