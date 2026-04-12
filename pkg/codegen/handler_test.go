package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func crudDirective(args ...*ast.NamedArg) *ast.Directive {
	return &ast.Directive{
		Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name: "crud",
		Args: args,
	}
}

func testModel(name string, directives []*ast.Directive, fields []*ast.FieldDecl) *ast.ModelDecl {
	return &ast.ModelDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Directives: directives,
		Fields:     fields,
	}
}

func testField(name, typeName string, directives ...*ast.Directive) *ast.FieldDecl {
	return &ast.FieldDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Type:       &ast.TypeRef{Name: typeName},
		Directives: directives,
	}
}

func directive(name string) *ast.Directive {
	return &ast.Directive{Name: name}
}

func TestGenerateHandlerFileNoCrud(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int"),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src != nil {
		t.Fatal("should return nil when no @crud models")
	}
}

func TestGenerateHandlerFileAllCrud(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					testField("email", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// Should have all 5 CRUD handlers
	for _, name := range []string{"handleGetUser", "handleListUsers", "handleCreateUser", "handleUpdateUser", "handleDeleteUser"} {
		if !strings.Contains(code, name) {
			t.Errorf("missing handler: %s", name)
		}
	}

	// Should have RegisterHandlers
	if !strings.Contains(code, "func RegisterHandlers") {
		t.Error("missing RegisterHandlers function")
	}

	// Should register all 5 APIs
	for _, api := range []string{"getUser", "listUsers", "createUser", "updateUser", "deleteUser"} {
		if !strings.Contains(code, `"`+api+`"`) {
			t.Errorf("missing API registration: %s", api)
		}
	}
}

func TestGenerateHandlerCrudOnlyGetList(t *testing.T) {
	onlyArg := &ast.NamedArg{
		Name: "only",
		Value: &ast.ListExpr{
			Items: []ast.Expr{
				&ast.Ident{Name: "get"},
				&ast.Ident{Name: "list"},
			},
		},
	}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post", []*ast.Directive{crudDirective(onlyArg)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("title", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "handleGetPost") {
		t.Error("should have getPost")
	}
	if !strings.Contains(code, "handleListPosts") {
		t.Error("should have listPosts")
	}
	if strings.Contains(code, "handleCreatePost") {
		t.Error("should NOT have createPost")
	}
	if strings.Contains(code, "handleDeletePost") {
		t.Error("should NOT have deletePost")
	}
}

func TestGenerateHandlerCrudExceptDelete(t *testing.T) {
	exceptArg := &ast.NamedArg{
		Name: "except",
		Value: &ast.ListExpr{
			Items: []ast.Expr{
				&ast.Ident{Name: "delete"},
			},
		},
	}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective(exceptArg)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "handleGetUser") {
		t.Error("should have getUser")
	}
	if strings.Contains(code, "handleDeleteUser(") {
		t.Error("should NOT have deleteUser")
	}
}

func TestGenerateHandlerSoftDelete(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective(), {Name: "soft"}},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "SoftDelete") {
		t.Error("@soft model should use SoftDelete")
	}
}

func TestGenerateHandlerNullableField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					{
						Name:       "avatar",
						Type:       &ast.TypeRef{Name: "String", Nullable: true},
						Directives: nil,
					},
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Nullable field should have HasParam check in create handler
	if !strings.Contains(code, `req.HasParam("avatar")`) {
		t.Error("nullable field should check HasParam in create handler")
	}
}

func TestGenerateHandlerImmutableField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("email", "String", directive("immutable")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Update handler should NOT include immutable fields
	updatePart := code[strings.Index(code, "handleUpdateUser"):]
	if strings.Contains(updatePart, `"email"`) {
		t.Error("immutable field should be excluded from update handler")
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"User", "Users"},
		{"Post", "Posts"},
		{"Address", "Addresses"},
		{"Box", "Boxes"},
		{"Quiz", "Quizes"},
		{"Category", "Categories"},
	}
	for _, tt := range tests {
		got := pluralize(tt.input)
		if got != tt.want {
			t.Errorf("pluralize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCrudOperationsNoArgs(t *testing.T) {
	m := testModel("User", []*ast.Directive{crudDirective()}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6", len(ops))
	}
}

func TestCrudOperationsNoCrud(t *testing.T) {
	m := testModel("User", nil, nil)
	ops := crudOperations(m)
	if ops != nil {
		t.Error("should return nil for non-crud model")
	}
}

func TestExtractListArgNonList(t *testing.T) {
	result := extractListArg(&ast.Ident{Name: "get"})
	if result != nil {
		t.Error("should return nil for non-list arg")
	}
}

func TestCrudOperationsMultipleDirectives(t *testing.T) {
	// Model with multiple directives including @crud — tests the continue branch
	m := testModel("User", []*ast.Directive{
		{Name: "soft"},
		crudDirective(),
		{Name: "noTime"},
	}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6 (should skip non-crud directives)", len(ops))
	}
}

func TestCrudOperationsUnknownArg(t *testing.T) {
	// @crud with unknown arg (not only/except) falls through to default crudOps
	unknownArg := &ast.NamedArg{
		Name:  "unknown",
		Value: &ast.Ident{Name: "something"},
	}
	m := testModel("User", []*ast.Directive{crudDirective(unknownArg)}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6 (should fall through to default)", len(ops))
	}
}

func TestSkipHandlerFieldComputed(t *testing.T) {
	f := &ast.FieldDecl{
		Name:     "commentCount",
		Type:     &ast.TypeRef{Name: "Int"},
		Computed: &ast.ComputedField{},
	}
	if !skipHandlerField(f, nil) {
		t.Error("computed field should be skipped")
	}
}

func TestSkipHandlerFieldIDNoAuto(t *testing.T) {
	// id field with @id but without @auto should NOT be skipped
	f := &ast.FieldDecl{
		Name:       "id",
		Type:       &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "id"}},
	}
	if skipHandlerField(f, nil) {
		t.Error("id without @auto should not be skipped")
	}
}

func TestSkipHandlerFieldIDWithAuto(t *testing.T) {
	// id field with @auto should be skipped
	f := &ast.FieldDecl{
		Name:       "id",
		Type:       &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "auto"}},
	}
	if !skipHandlerField(f, nil) {
		t.Error("id with @auto should be skipped")
	}
}

func TestSkipHandlerFieldRelation(t *testing.T) {
	// Non-builtin, non-enum type is a relation → skipped
	f := &ast.FieldDecl{
		Name: "user",
		Type: &ast.TypeRef{Name: "User"},
	}
	if !skipHandlerField(f, nil) {
		t.Error("relation field should be skipped")
	}
}

func TestSkipHandlerFieldInternal(t *testing.T) {
	f := &ast.FieldDecl{
		Name:       "secret",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "internal"}},
	}
	if !skipHandlerField(f, nil) {
		t.Error("@internal field should be skipped")
	}
}

func TestSkipHandlerFieldNormal(t *testing.T) {
	// A normal field with no special directives should NOT be skipped
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	if skipHandlerField(f, nil) {
		t.Error("normal field should not be skipped")
	}
}

func TestSkipHandlerFieldEnumNotSkipped(t *testing.T) {
	// Enum fields are NOT relation fields, so should NOT be skipped
	enums := map[string]bool{"Role": true}
	f := &ast.FieldDecl{
		Name: "role",
		Type: &ast.TypeRef{Name: "Role"},
	}
	if skipHandlerField(f, enums) {
		t.Error("enum field should not be skipped")
	}
}

func TestIdGoTypeNoIdField(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("name", "String"),
	})
	if got := idGoType(m); got != "int64" {
		t.Errorf("idGoType = %q, want int64 (default)", got)
	}
}

func TestIdGoTypeUUID(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "UUID"),
		testField("name", "String"),
	})
	if got := idGoType(m); got != "uuid.UUID" {
		t.Errorf("idGoType = %q, want uuid.UUID", got)
	}
}

func TestIdGoTypeString(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "String"),
		testField("name", "String"),
	})
	if got := idGoType(m); got != "string" {
		t.Errorf("idGoType = %q, want string", got)
	}
}

func TestParamMethodBool(t *testing.T) {
	if got := paramMethod("bool"); got != "Bool" {
		t.Errorf("paramMethod(bool) = %q, want Bool", got)
	}
}

func TestParamMethodFloat(t *testing.T) {
	if got := paramMethod("float64"); got != "Float" {
		t.Errorf("paramMethod(float64) = %q, want Float", got)
	}
}

func TestParamMethodDateTime(t *testing.T) {
	if got := paramMethod("time.Time"); got != "DateTime" {
		t.Errorf("paramMethod(time.Time) = %q, want DateTime", got)
	}
}

func TestParamMethodCustomType(t *testing.T) {
	// Custom types return empty for ParamJSON dispatch
	if got := paramMethod("uuid.UUID"); got != "" {
		t.Errorf("paramMethod(uuid.UUID) = %q, want empty", got)
	}
}

func TestParamMethodArrayTypes(t *testing.T) {
	if got := paramMethod("[]int64"); got != "IntArray" {
		t.Errorf("paramMethod([]int64) = %q, want IntArray", got)
	}
	if got := paramMethod("[]string"); got != "StringArray" {
		t.Errorf("paramMethod([]string) = %q, want StringArray", got)
	}
}

func TestGenerateHandlerEnumField(t *testing.T) {
	enums := map[string]bool{"Role": true}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("role", "Role"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", enums)
	code := string(src)

	// Enum field should have cast: Role(roleValVal)
	if !strings.Contains(code, "Role(") {
		t.Errorf("enum field should be cast to enum type:\n%s", code)
	}
}

func TestGenerateHandlerUUIDId(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Token", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "UUID", directive("id"), directive("auto")),
					testField("value", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// UUID id should use ParamString
	if !strings.Contains(code, "ParamString") {
		t.Errorf("UUID id should use ParamString:\n%s", code)
	}
}

func TestGenerateHandlerBooleanField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("active", "Boolean"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Boolean field should use ParamBool
	if !strings.Contains(code, "ParamBool") {
		t.Errorf("Boolean field should use ParamBool:\n%s", code)
	}
}

func TestGenerateHandlerHardDelete(t *testing.T) {
	// Test delete without @soft (hard delete path)
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Hard delete should use .Where().Delete()
	if strings.Contains(code, "SoftDelete") {
		t.Error("non-@soft model should NOT use SoftDelete")
	}
	if !strings.Contains(code, ".Delete(ctx)") {
		t.Error("should use .Delete(ctx)")
	}
}

func TestSkipHandlerFieldNilType(t *testing.T) {
	// Field with nil type should not panic in isRelationField
	f := &ast.FieldDecl{
		Name: "unknown",
		Type: nil,
	}
	// Should not be skipped (nil type → isRelationField returns false)
	if skipHandlerField(f, nil) {
		t.Error("nil-type field should not be skipped")
	}
}

func TestGenerateHandlerWithRelations(t *testing.T) {
	// Test get/list handlers with relation resolvers
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should have relation resolver function
	if !strings.Contains(code, "resolveUserRelations") {
		t.Errorf("should have relation resolver:\n%s", code)
	}
	// Get handler should call resolve
	if !strings.Contains(code, "resolveUserRelations(ctx, app, result, req.Select)") {
		t.Errorf("get handler should call resolveRelations:\n%s", code)
	}
}

func TestGenerateHandlerNullableRelation(t *testing.T) {
	// Nullable FK should generate nil check before Load
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
						{Name: "user", Type: &ast.TypeRef{Name: "User", Nullable: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Nullable FK should have nil check: if post.UserId != nil {
	if !strings.Contains(code, "if post.UserId != nil") {
		t.Errorf("nullable FK should have nil check before Load:\n%s", code)
	}
	// Should dereference the pointer: *post.UserId
	if !strings.Contains(code, "*post.UserId") {
		t.Errorf("nullable FK should dereference pointer in Load call:\n%s", code)
	}
}

func TestGenerateHandlerNonNullableRelation(t *testing.T) {
	// Non-nullable FK should NOT have nil check
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						testField("userId", "Int"),
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Non-nullable FK should NOT have nil check
	if strings.Contains(code, "if post.UserId != nil") {
		t.Errorf("non-nullable FK should NOT have nil check:\n%s", code)
	}
	// Should NOT dereference pointer
	if strings.Contains(code, "*post.UserId") {
		t.Errorf("non-nullable FK should NOT dereference:\n%s", code)
	}
	// Should directly pass post.UserId
	if !strings.Contains(code, "post.UserId, childCols") {
		t.Errorf("should directly pass FK value:\n%s", code)
	}
}

func TestGenerateHandlerDefaultFieldValue(t *testing.T) {
	// Field with a default value should use HasParam in create handler
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					{
						Name:    "role",
						Type:    &ast.TypeRef{Name: "String"},
						Default: &ast.Literal{Value: "user"},
					},
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Field with default should have HasParam check
	if !strings.Contains(code, `req.HasParam("role")`) {
		t.Errorf("field with default should use HasParam:\n%s", code)
	}
}

func TestGenerateHandlerHiddenField(t *testing.T) {
	// @hidden fields should generate defaultCols excluding hidden fields
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "password", Type: &ast.TypeRef{Name: "String"},
							Directives: []*ast.Directive{{Name: "hidden"}}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// Should generate defaultUserCols
	if !strings.Contains(code, "defaultUserCols") {
		t.Errorf("should generate defaultCols for model with @hidden:\n%s", code)
	}
	// defaultCols should not contain password
	if strings.Contains(code, `"password"`) && strings.Contains(code, "defaultUserCols") {
		// check that password is not in the defaultCols var
		idx := strings.Index(code, "defaultUserCols")
		line := code[idx : idx+200]
		if strings.Contains(line, "password") {
			t.Errorf("defaultCols should not contain @hidden field password:\n%s", line)
		}
	}
}

func TestGenerateHandlerHashField(t *testing.T) {
	// @hash field should auto-hash in create handler
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "password", Type: &ast.TypeRef{Name: "String"},
							Directives: []*ast.Directive{{Name: "hash"}}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "luxocrypto.HashPassword") {
		t.Errorf("@hash field should generate HashPassword call:\n%s", code)
	}
}

func TestGenerateHandlerNullableParam(t *testing.T) {
	// Nullable param should use & prefix in setter
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						{Name: "subtitle", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "&subtitleVal") {
		t.Errorf("nullable param should use & prefix:\n%s", code)
	}
}

func TestGenerateHandlerEnumParam(t *testing.T) {
	// Enum param should cast to enum type
	result := &semantic.Result{
		Files: []*ast.File{{
			Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"ADMIN", "USER"}}},
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					},
				),
			},
		}},
	}
	enums := collectEnums(result)
	src := generateHandlerFile(result, "app", enums)
	code := string(src)

	if !strings.Contains(code, "Role(roleVal)") {
		t.Errorf("enum param should cast to enum type:\n%s", code)
	}
}
