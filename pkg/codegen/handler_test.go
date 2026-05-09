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

// TestGenerateFilterParserAllTypes covers Float, DateTime, and Boolean
// branches of generateFilterParser.
func TestGenerateFilterParserAllTypes(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Event", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "NewFloatField") {
		t.Errorf("missing NewFloatField in filter parser:\n%s", code)
	}
	if !strings.Contains(code, "NewBoolField") {
		t.Errorf("missing NewBoolField in filter parser:\n%s", code)
	}
	if !strings.Contains(code, "NewTimeField") {
		t.Errorf("missing NewTimeField in filter parser:\n%s", code)
	}
}

// TestGenerateFilterParserSkipsRelation ensures relation fields with @filterable
// are skipped (isRelationField returns true → continue).
func TestGenerateFilterParserSkipsRelation(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		testField("title", "String", filterDirective),
		// relation field with @filterable — should be skipped
		{Name: "user", Type: &ast.TypeRef{Name: "User"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// "user" relation should not appear as a filter case
	if strings.Contains(code, `case "user"`) {
		t.Errorf("relation field should be skipped in filter parser:\n%s", code)
	}
}

// TestGenerateSorterParserHasSortableField ensures generateSorterParser emits
// a case for @sortable fields (covering the per-field emit branch at line 732).
func TestGenerateSorterParserHasSortableField(t *testing.T) {
	sortDirective := &ast.Directive{Name: "sortable"}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}, Directives: []*ast.Directive{sortDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, `case "createdAt"`) {
		t.Errorf("missing sortable field case in sorter parser:\n%s", code)
	}
	if !strings.Contains(code, "created_at") {
		t.Errorf("missing snake_case column in sorter parser:\n%s", code)
	}
}

// TestSkipHandlerFieldNilTypeFalse ensures a field with nil type and no
// other skip conditions is NOT skipped (the unexercised branch in skipHandlerField
// where isAutoManaged, @internal, computed are all false but type is nil).
func TestSkipHandlerFieldNilTypeFalse(t *testing.T) {
	// A field with no directives and nil type — isAutoManaged=false, @internal=false,
	// computed=nil, and isRelationField returns false for nil Type.
	f := &ast.FieldDecl{
		Name: "freeField",
		Type: nil,
	}
	// Should NOT be skipped (nil type is not a relation).
	if skipHandlerField(f, nil) {
		t.Error("field with nil type and no skip conditions should not be skipped")
	}
}

// TestCollectInferredAPIsWithBody ensures APIs with a Body are excluded from
// the inferred list (covers the body-check branch at line 145).
func TestCollectInferredAPIsWithBody(t *testing.T) {
	body := &ast.Block{} // non-nil body
	result := &semantic.Result{
		Files: []*ast.File{{
			APIs: []*ast.ApiDecl{
				{Name: "withBody", Body: body},
				{Name: "noBody", Body: nil},
			},
		}},
	}
	_, apis := collectInferredAPIs(result)
	for _, a := range apis {
		if a.Body != nil {
			t.Errorf("API with Body should be excluded from inferred list: %s", a.Name)
		}
	}
	found := false
	for _, a := range apis {
		if a.Name == "noBody" {
			found = true
		}
	}
	if !found {
		t.Error("no-body API should be in inferred list")
	}
}

// TestGenerateHandlerWithInferredAPI exercises generateInferredHandlers
// (covering the non-nil inferAPI + ValidateInferredReturnType paths).
func TestGenerateHandlerWithInferredAPI(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("email", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:   "getUserByEmail",
					Body:   nil,
					Params: []*ast.ParamDecl{{Name: "email", Type: &ast.TypeRef{Name: "String"}}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)
	if !strings.Contains(code, "handleGetUserByEmail") {
		t.Errorf("missing inferred handler:\n%s", code)
	}
}

// TestGenerateRegisterFuncWithInferredNames covers the inferred-names loop
// in generateRegisterFuncWithInferred (line 619).
func TestGenerateRegisterFuncWithInferredNames(t *testing.T) {
	var b strings.Builder
	models := []*ast.ModelDecl{
		testModel("User", []*ast.Directive{crudDirective()}, nil),
	}
	inferredNames := []string{"getUserByEmail", "listUsersByRole"}
	generateRegisterFuncWithInferred(&b, models, inferredNames, nil)
	out := b.String()

	if !strings.Contains(out, `"getUserByEmail"`) {
		t.Errorf("missing inferred handler registration for getUserByEmail:\n%s", out)
	}
	if !strings.Contains(out, `"listUsersByRole"`) {
		t.Errorf("missing inferred handler registration for listUsersByRole:\n%s", out)
	}
}

// TestGenerateFilterParserIntAndStringTypes covers the "Int" and "String"
// branches of generateFilterParser (lines 694-695 and 698-699).
func TestGenerateFilterParserIntAndStringTypes(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Article", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "views", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// Int @filterable → NewIntField
	if !strings.Contains(code, "NewIntField") {
		t.Errorf("missing NewIntField for Int @filterable field:\n%s", code)
	}
	// String @filterable → NewStringField
	if !strings.Contains(code, "NewStringField") {
		t.Errorf("missing NewStringField for String @filterable field:\n%s", code)
	}
}

// TestGenerateFilterParserDefaultBranch covers the default branch of the
// filter type switch (enum type → NewStringField). The enum must be passed via
// the enums map so isRelationField returns false.
func TestGenerateFilterParserDefaultBranch(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}
	// "Status" is an enum — not a relation when enums map contains it.
	enums := map[string]bool{"Status": true}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "status", Type: &ast.TypeRef{Name: "Status"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", enums)
	code := string(src)

	// default branch → NewStringField
	if !strings.Contains(code, "NewStringField") {
		t.Errorf("default branch should use NewStringField for enum type:\n%s", code)
	}
}

// TestGenerateInferredHandlerWithReturnTypeWarning covers the warning branch in
// generateInferredHandlers when ValidateInferredReturnType returns a non-empty message.
func TestGenerateInferredHandlerWithReturnTypeWarning(t *testing.T) {
	// getUserByEmail but declared return type is [User] (wrong — get returns single, not list)
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int"),
					testField("email", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:       "getUserByEmail",
					ReturnType: &ast.TypeRef{Name: "User", IsList: true}, // wrong: list for "get"
					Params:     []*ast.ParamDecl{{Name: "email", Type: &ast.TypeRef{Name: "String"}}},
				},
			},
		}},
	}
	// Should not panic — warning goes to stderr, handler still generated
	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file even with return type mismatch warning")
	}
	code := string(src)
	if !strings.Contains(code, "handleGetUserByEmail") {
		t.Errorf("handler should be generated despite warning:\n%s", code)
	}
}

// TestGenerateHandlerWithAuth verifies that @withAuth on model injects
// luvia.Identity auth guard into all CRUD handlers.
func TestGenerateHandlerWithAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective(), {Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}}}}}}},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// All CRUD handlers should have auth check
	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("@withAuth model should inject luvia.Identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("@withAuth model should return errors.Unauthorized")
	}
	// Should import luvia
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/luvia"`) {
		t.Error("should import luvia when @withAuth is used")
	}
}

// TestGenerateHandlerWithoutAuth verifies that models without @withAuth
// do NOT inject auth guards.
func TestGenerateHandlerWithoutAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if strings.Contains(code, "luvia.Identity") {
		t.Error("model without @withAuth should NOT inject auth check")
	}
}

// TestGenerateCompiledAPIWithAuth verifies @auth on compiled API handlers.
func TestGenerateCompiledAPIWithAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("name", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:       "updateProfile",
					Directives: []*ast.Directive{{Name: "auth"}},
					Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}},
					}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("@auth API should inject luvia.Identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("@auth API should return errors.Unauthorized")
	}
}

// TestGenerateCompiledAPIWithoutAuth verifies compiled APIs without @auth
// do NOT inject auth guards.
func TestGenerateCompiledAPIWithoutAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			APIs: []*ast.ApiDecl{
				{
					Name:       "register",
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}},
					}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if strings.Contains(code, "luvia.Identity") {
		t.Error("API without @auth should NOT inject auth check")
	}
}

// --- writeAuthCheck ---

func TestWriteAuthCheckNilDirective(t *testing.T) {
	var b strings.Builder
	writeAuthCheck(&b, "\t")
	code := b.String()
	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("should have identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("should have unauthorized check")
	}
	// Without directives, should NOT have _authorized
	if strings.Contains(code, "_authorized") {
		t.Error("nil directives should not add role/own checks")
	}
}

func TestWriteAuthCheckSingleRole(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "Admin"`) {
		t.Errorf("single role check missing:\n%s", code)
	}
	if !strings.Contains(code, "errors.Forbidden") {
		t.Error("should have forbidden check")
	}
}

func TestWriteAuthCheckMultipleRoles(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
			{Value: &ast.Ident{Name: "Moderator"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, "switch identity.String(\"role\")") {
		t.Errorf("multi-role switch missing:\n%s", code)
	}
	if !strings.Contains(code, `case "Admin"`) || !strings.Contains(code, `case "Moderator"`) {
		t.Errorf("role cases missing:\n%s", code)
	}
}

func TestWriteAuthCheckOwn(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "own", Value: &ast.Literal{Value: "userId"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `req.ParamInt("userId")`) {
		t.Errorf("own param check missing:\n%s", code)
	}
	if !strings.Contains(code, "identity.ID()") {
		t.Errorf("own identity check missing:\n%s", code)
	}
}

func TestWriteAuthCheckOwnIdent(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "own", Value: &ast.Ident{Name: "id"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `req.ParamInt("id")`) {
		t.Errorf("own ident check missing:\n%s", code)
	}
}

func TestWriteAuthCheckPermission(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "permission", Value: &ast.LambdaExpr{
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.BinaryExpr{
						Left: &ast.MemberExpr{
							Object: &ast.Ident{Name: "my"},
							Field:  "role",
						},
						Op:    "==",
						Right: &ast.Literal{Value: "SUPER"},
					}},
				}},
			}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "SUPER"`) {
		t.Errorf("permission expression missing:\n%s", code)
	}
}

func TestWriteAuthCheckRoleAndOwn(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
			{Name: "own", Value: &ast.Literal{Value: "userId"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "Admin"`) {
		t.Errorf("role check missing:\n%s", code)
	}
	if !strings.Contains(code, `req.ParamInt("userId")`) {
		t.Errorf("own check missing:\n%s", code)
	}
}

// --- compilePermissionExpr ---

func TestCompilePermissionExprMemberIdent(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "==",
		Right: &ast.Ident{Name: "ADMIN"},
	}
	got := compilePermissionExpr(expr)
	if got != `identity.String("role") == "ADMIN"` {
		t.Fatalf("got %q", got)
	}
}

func TestCompilePermissionExprMemberMember(t *testing.T) {
	// my.role == Enum.VALUE
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "!=",
		Right: &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "BANNED"},
	}
	got := compilePermissionExpr(expr)
	if got != `identity.String("role") != "BANNED"` {
		t.Fatalf("got %q", got)
	}
}

func TestCompilePermissionExprNonBinary(t *testing.T) {
	got := compilePermissionExpr(&ast.Ident{Name: "foo"})
	if got != "" {
		t.Fatalf("non-binary should return empty, got %q", got)
	}
}

func TestCompilePermissionExprLeftNotMember(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.Ident{Name: "foo"},
		Op:    "==",
		Right: &ast.Literal{Value: "bar"},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("non-member left should return empty, got %q", got)
	}
}

func TestCompilePermissionExprLeftNotMy(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "other"}, Field: "role"},
		Op:    "==",
		Right: &ast.Literal{Value: "x"},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("non-my left should return empty, got %q", got)
	}
}

func TestCompilePermissionExprRightUnsupported(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "==",
		Right: &ast.CallExpr{Func: &ast.Ident{Name: "foo"}},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("unsupported right should return empty, got %q", got)
	}
}

// --- bodyContains* ---

func TestBodyContainsAwait(t *testing.T) {
	if bodyContainsAwait(nil) {
		t.Error("nil block should return false")
	}
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}}}}}},
	}}
	if !bodyContainsAwait(block) {
		t.Error("should detect await")
	}
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsAwait(block2) {
		t.Error("should not detect await in non-await stmt")
	}
	// Non-ExprStmt
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsAwait(block3) {
		t.Error("non-ExprStmt should return false")
	}
}

func TestBodyContainsTransaction(t *testing.T) {
	if bodyContainsTransaction(nil) {
		t.Error("nil block should return false")
	}
	// ExprStmt with transaction call
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "transaction"},
		}},
	}}
	if !bodyContainsTransaction(block) {
		t.Error("should detect transaction ExprStmt")
	}
	// ValStmt with transaction call
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "result", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "transaction"},
		}},
	}}
	if !bodyContainsTransaction(block2) {
		t.Error("should detect transaction ValStmt")
	}
	// ExprStmt with non-transaction call
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "other"},
		}},
	}}
	if bodyContainsTransaction(block3) {
		t.Error("should not detect non-transaction")
	}
	// Non-ident func in call
	block4 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "transaction"},
		}},
	}}
	if bodyContainsTransaction(block4) {
		t.Error("member expr func should not match")
	}
	// ValStmt with non-call value
	block5 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "x", Value: &ast.Ident{Name: "y"}},
	}}
	if bodyContainsTransaction(block5) {
		t.Error("non-call val should not match")
	}
	// ValStmt with non-ident func
	block6 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "x", Value: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "y"},
		}},
	}}
	if bodyContainsTransaction(block6) {
		t.Error("val with member func should not match")
	}
}

func TestBodyContainsTemplateString(t *testing.T) {
	if bodyContainsTemplateString(nil) {
		t.Error("nil block should return false")
	}
	// ValStmt with TemplateString
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "msg", Value: &ast.TemplateString{Parts: []ast.Expr{}}},
	}}
	if !bodyContainsTemplateString(block) {
		t.Error("should detect template in val")
	}
	// ReturnStmt with TemplateString
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{Value: &ast.TemplateString{Parts: []ast.Expr{}}},
	}}
	if !bodyContainsTemplateString(block2) {
		t.Error("should detect template in return")
	}
	// ReturnStmt with nil value
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{},
	}}
	if bodyContainsTemplateString(block3) {
		t.Error("nil return value should not match")
	}
	// Non-matching stmt types
	block4 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsTemplateString(block4) {
		t.Error("non-matching should return false")
	}
}

// --- inferParamType ---

func TestInferParamType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"id", "Int"},
		{"userId", "Int"},
		{"page", "Int"},
		{"pageSize", "Int"},
		{"limit", "Int"},
		{"offset", "Int"},
		{"priority", "Int"},
		{"minutes", "Int"},
		{"quantity", "Int"},
		{"count", "Int"},
		{"isActive", "Boolean"},
		{"active", "Boolean"},
		{"published", "Boolean"},
		{"amount", "Float"},
		{"price", "Float"},
		{"balance", "Float"},
		{"score", "Float"},
		{"total", "Float"},
		{"budget", "Float"},
		{"name", "String"},
		{"email", "String"},
		{"unknown", "String"},
	}
	for _, tt := range tests {
		if got := inferParamType(tt.name); got != tt.want {
			t.Errorf("inferParamType(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// --- resolveParamTypeFromAST ---

func TestResolveParamTypeFromAST(t *testing.T) {
	old := apiParamTypes
	defer func() { apiParamTypes = old }()

	// No AST data — falls back to heuristic
	apiParamTypes = nil
	if got := resolveParamTypeFromAST("getUser", "id"); got != "Int" {
		t.Fatalf("fallback should use inferParamType, got %q", got)
	}

	// With AST data
	apiParamTypes = map[string]map[string]string{
		"getUser": {"id": "UUID"},
	}
	if got := resolveParamTypeFromAST("getUser", "id"); got != "UUID" {
		t.Fatalf("should use AST type, got %q", got)
	}
	// Missing param in AST — fallback
	if got := resolveParamTypeFromAST("getUser", "name"); got != "String" {
		t.Fatalf("missing param should fallback, got %q", got)
	}
}

// --- writeAPIRegistration ---

func TestWriteAPIRegistration(t *testing.T) {
	oldIDs := apiIDs
	oldParamIDs := apiParamIDs
	oldParamTypes := apiParamTypes
	defer func() {
		apiIDs = oldIDs
		apiParamIDs = oldParamIDs
		apiParamTypes = oldParamTypes
	}()

	// ID = 0 → early return
	apiIDs = map[string]int{}
	var b strings.Builder
	writeAPIRegistration(&b, "noApi")
	if b.Len() != 0 {
		t.Error("zero ID should produce no output")
	}

	// With ID and params
	apiIDs = map[string]int{"getUser": 5}
	apiParamIDs = map[string]map[string]int{
		"getUser": {"id": 1},
	}
	apiParamTypes = map[string]map[string]string{
		"getUser": {"id": "Int"},
	}
	b.Reset()
	writeAPIRegistration(&b, "getUser")
	code := b.String()
	if !strings.Contains(code, `Register("getUser", 5)`) {
		t.Errorf("missing Register call:\n%s", code)
	}
	if !strings.Contains(code, `RegisterParams("getUser"`) {
		t.Errorf("missing RegisterParams:\n%s", code)
	}
	if !strings.Contains(code, `"Int"`) {
		t.Errorf("missing type Int:\n%s", code)
	}
}

func TestWriteAPIRegistrationNoParams(t *testing.T) {
	oldIDs := apiIDs
	oldParamIDs := apiParamIDs
	defer func() {
		apiIDs = oldIDs
		apiParamIDs = oldParamIDs
	}()

	apiIDs = map[string]int{"ping": 1}
	apiParamIDs = nil

	var b strings.Builder
	writeAPIRegistration(&b, "ping")
	code := b.String()
	if !strings.Contains(code, `Register("ping", 1)`) {
		t.Errorf("missing Register:\n%s", code)
	}
	if strings.Contains(code, "RegisterParams") {
		t.Error("no params should skip RegisterParams")
	}
}

// --- paramMethod ---

func TestParamMethod(t *testing.T) {
	tests := []struct {
		goType string
		want   string
	}{
		{"int64", "Int"},
		{"float64", "Float"},
		{"string", "String"},
		{"time.Time", "DateTime"},
		{"bool", "Bool"},
		{"[]int64", "IntArray"},
		{"[]string", "StringArray"},
		{"CustomType", ""},
		{"map[string]any", ""},
	}
	for _, tt := range tests {
		if got := paramMethod(tt.goType); got != tt.want {
			t.Errorf("paramMethod(%q) = %q, want %q", tt.goType, got, tt.want)
		}
	}
}

// TestGenerateCompiledHandlers covers the compiled-handler path where api.Body != nil
// and @native is not set, exercising generateCompiledHandlers lines 91-94.
func TestGenerateCompiledHandlers(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name: "myCompiledApi",
					Body: &ast.Block{Stmts: []ast.Stmt{}}, // non-nil body, no @native
				},
			},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)
	// The compiled handler should register "myCompiledApi"
	if !strings.Contains(code, "myCompiledApi") {
		t.Errorf("compiled API should be registered:\n%s", code)
	}
}

// ─── generateParamSet: nullable enum ────────────────────────────────────────

func TestGenerateParamSetNullableEnum(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "role",
		Type: &ast.TypeRef{Name: "Role", Nullable: true},
	}
	enums := map[string]bool{"Role": true}
	generateParamSet(&b, f, "SetRole", "\t\t", enums)
	out := b.String()
	if !strings.Contains(out, "ParamString") {
		t.Fatalf("nullable enum should use ParamString, got:\n%s", out)
	}
	if !strings.Contains(out, "roleValEnum") {
		t.Fatalf("nullable enum should create enum tmp var, got:\n%s", out)
	}
	if !strings.Contains(out, "&roleValEnum") {
		t.Fatalf("nullable enum should take pointer, got:\n%s", out)
	}
}

// ─── generateParamSet: custom JSON type ─────────────────────────────────────

func TestGenerateParamSetCustomType(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "metadata",
		Type: &ast.TypeRef{Name: "JSON"},
	}
	generateParamSet(&b, f, "SetMetadata", "\t\t", nil)
	out := b.String()
	if !strings.Contains(out, "ParamJSON") {
		t.Fatalf("custom type should use ParamJSON, got:\n%s", out)
	}
}

// ─── writeHandlerImports: all features enabled ──────────────────────────────

func TestWriteHandlerImportsAllFeatures(t *testing.T) {
	var b strings.Builder
	models := []*ast.ModelDecl{
		{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{
				{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
			},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{}}}
	writeHandlerImports(&b, result, models, handlerFeatures{hasOrGroups: true, hasSortable: true, hasAwait: true, hasTransaction: true, hasTemplateStr: true, hasAuth: true})
	out := b.String()
	if !strings.Contains(out, `"strconv"`) {
		t.Fatalf("hasOrGroups should add strconv import, got:\n%s", out)
	}
	if !strings.Contains(out, `"strings"`) {
		t.Fatalf("hasSortable should add strings import, got:\n%s", out)
	}
	if !strings.Contains(out, "errgroup") {
		t.Fatalf("hasAwait should add errgroup import, got:\n%s", out)
	}
	if !strings.Contains(out, "luvia") {
		t.Fatalf("hasAuth should add luvia import, got:\n%s", out)
	}
	if !strings.Contains(out, "luxocrypto") {
		t.Fatalf("hash model should add luxocrypto import, got:\n%s", out)
	}
}

// ─── writeHandlerImports: hash only when CRUD has write ops ──────

func TestWriteHandlerImportsNoHashWithoutWriteOps(t *testing.T) {
	var b strings.Builder
	result := &semantic.Result{Files: []*ast.File{{}}}
	// Model has @hash but CRUD only has get/list (no create/update) — no luxocrypto needed
	models := []*ast.ModelDecl{{
		Name: "User",
		Directives: []*ast.Directive{{
			Name: "crud",
			Args: []*ast.NamedArg{{
				Name:  "only",
				Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "get"}, &ast.Ident{Name: "list"}}},
			}},
		}},
		Fields: []*ast.FieldDecl{
			{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}}
	writeHandlerImports(&b, result, models, handlerFeatures{})
	out := b.String()
	if strings.Contains(out, "luxocrypto") {
		t.Fatalf("read-only CRUD with @hash should not add luxocrypto, got:\n%s", out)
	}
}

func TestScanModelsForHash_NoCrud(t *testing.T) {
	// Model without @crud — should not trigger hash import
	models := []*ast.ModelDecl{{
		Name: "Config",
		Fields: []*ast.FieldDecl{
			{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}}
	if scanModelsForHash(models) {
		t.Error("model without @crud should not trigger hash import")
	}
}

func TestScanModelsForHash_CrudNoHash(t *testing.T) {
	// CRUD model without @hash — should not trigger
	models := []*ast.ModelDecl{{
		Name:       "Post",
		Directives: []*ast.Directive{{Name: "crud"}},
		Fields: []*ast.FieldDecl{
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
	}}
	if scanModelsForHash(models) {
		t.Error("CRUD model without @hash should not trigger hash import")
	}
}

func TestScanForTimeImport_FnDurationParam(t *testing.T) {
	// fn with Duration param — should trigger time import
	result := &semantic.Result{Files: []*ast.File{{
		Functions: []*ast.FnDecl{{
			Name:   "sleep",
			Params: []*ast.ParamDecl{{Name: "d", Type: &ast.TypeRef{Name: "Duration"}}},
		}},
	}}}
	if !scanForTimeImport(result, nil) {
		t.Error("fn with Duration param should trigger time import")
	}
}

func TestScanForTimeImport_ApiDateTimeParam(t *testing.T) {
	// API with DateTime param — does NOT need time import (uses int64 Unix timestamp internally)
	result := &semantic.Result{Files: []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name:   "listByDate",
			Params: []*ast.ParamDecl{{Name: "since", Type: &ast.TypeRef{Name: "DateTime"}}},
		}},
	}}}
	if scanForTimeImport(result, nil) {
		t.Error("DateTime param should NOT trigger time import (uses ParamDateTime, no explicit time.Time)")
	}
}

func TestScanForTimeImport_ApiDurationParam(t *testing.T) {
	// API with Duration param — needs time import (time.Duration type in generated code)
	result := &semantic.Result{Files: []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name:   "delay",
			Params: []*ast.ParamDecl{{Name: "d", Type: &ast.TypeRef{Name: "Duration"}}},
		}},
	}}}
	if !scanForTimeImport(result, nil) {
		t.Error("Duration param should trigger time import")
	}
}

// ─── detectHandlerFeatures: sortable field ──────────────────────────────────

func TestDetectHandlerFeaturesSortable(t *testing.T) {
	models := []*ast.ModelDecl{
		{
			Name: "Post",
			Fields: []*ast.FieldDecl{
				{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "sortable"}}},
			},
		},
	}
	f := detectHandlerFeatures(&semantic.Result{}, models, nil, nil)
	if !f.hasSortable {
		t.Fatal("should detect sortable field")
	}
}

// ─── detectHandlerFeatures: withAuth on model ───────────────────────────────

func TestDetectHandlerFeaturesWithAuth(t *testing.T) {
	models := []*ast.ModelDecl{
		{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "withAuth"}},
		},
	}
	f := detectHandlerFeatures(&semantic.Result{}, models, nil, nil)
	if !f.hasAuth {
		t.Fatal("should detect withAuth")
	}
}

// ─── writeFKEnsure: dedup and snake_case ────────────────────────────────────

func TestWriteFKEnsureDedupe(t *testing.T) {
	var b strings.Builder
	rels := []Relation{
		{LocalKey: "userId"},
		{LocalKey: "userId"}, // duplicate
		{LocalKey: "postId"},
	}
	writeFKEnsure(&b, rels)
	out := b.String()
	if strings.Count(out, "user_id") != 1 {
		t.Fatalf("should dedupe user_id, got:\n%s", out)
	}
	if !strings.Contains(out, "post_id") {
		t.Fatalf("should include post_id, got:\n%s", out)
	}
}

// --- fn @service ---

func TestGenerateServiceFnHandler(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					testField("score", "Float"),
				}),
			},
			Functions: []*ast.FnDecl{
				{
					Name: "getUserScore",
					Params: []*ast.ParamDecl{
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "Float"},
					Directives: []*ast.Directive{{Name: "service"}},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ReturnStmt{Value: &ast.Literal{Kind: token.Float, Value: "1.5"}},
						},
					},
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// Should have fn handler
	if !strings.Contains(code, "handleGetUserScore") {
		t.Error("missing fn @service handler: handleGetUserScore")
	}

	// Should have RegisterServiceFns
	if !strings.Contains(code, "func RegisterServiceFns") {
		t.Error("missing RegisterServiceFns function")
	}

	// Should register with svc: prefix
	if !strings.Contains(code, `"svc:getUserScore"`) {
		t.Error("missing svc: prefix in registration")
	}
}

func TestGenerateNativeServiceFnHandler(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Functions: []*ast.FnDecl{
				{
					Name: "processPayment",
					Params: []*ast.ParamDecl{
						{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Directives: []*ast.Directive{{Name: "native"}, {Name: "service"}},
					// Body is nil for @native
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file for @native @service fn")
	}
	code := string(src)

	// Should have native handler delegating to Resolver
	if !strings.Contains(code, "handleProcessPayment") {
		t.Error("missing @native @service handler")
	}
	if !strings.Contains(code, "app.Resolver.ProcessPayment") {
		t.Error("missing NativeResolver delegation")
	}
	if !strings.Contains(code, `"svc:processPayment"`) {
		t.Error("missing svc: prefix")
	}
}

func TestServiceFnNotRegisteredWithoutAnnotation(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
				}),
			},
			Functions: []*ast.FnDecl{
				{
					Name:       "internalHelper",
					Params:     []*ast.ParamDecl{},
					ReturnType: &ast.TypeRef{Name: "Int"},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ReturnStmt{Value: &ast.Literal{Kind: token.Int, Value: "42"}},
						},
					},
					// No @service directive
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Internal fn without @service should NOT be registered
	if strings.Contains(code, "svc:internalHelper") {
		t.Error("fn without @service should not be registered as RPC")
	}
	// Should NOT have RegisterServiceFns (no service fns)
	if strings.Contains(code, "RegisterServiceFns") {
		t.Error("no service fns, should not generate RegisterServiceFns")
	}
}

func TestGenerateHandlerDeleteMany_IntID(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective(
					&ast.NamedArg{Name: "only", Value: &ast.ListExpr{Items: []ast.Expr{
						&ast.Ident{Name: "deleteMany"},
					}}},
				)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should use ParamIntArray for int64 ids
	if !strings.Contains(code, "req.ParamIntArray") {
		t.Error("deleteMany with Int ID should use ParamIntArray")
	}
	if strings.Contains(code, "json.Unmarshal") {
		t.Error("deleteMany with Int ID should NOT use json.Unmarshal")
	}
}

func TestGenerateHandlerDeleteMany_StringID(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Product", []*ast.Directive{crudDirective(
					&ast.NamedArg{Name: "only", Value: &ast.ListExpr{Items: []ast.Expr{
						&ast.Ident{Name: "deleteMany"},
					}}},
				)}, []*ast.FieldDecl{
					testField("id", "String", directive("id")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should use ParamStringArray for string ids
	if !strings.Contains(code, "req.ParamStringArray") {
		t.Error("deleteMany with String ID should use ParamStringArray")
	}
}

// ─── Validation directive tests ──────────────────────────────────────────────

func TestGenerateStringValidation_NotBlank(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "name",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "notBlank"}},
	}
	generateStringValidation(&b, f, "nameVal", "\t")
	if !strings.Contains(b.String(), "TrimSpace") {
		t.Error("@notBlank should generate TrimSpace check")
	}
}

func TestGenerateStringValidation_Email(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "email",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "email"}},
	}
	generateStringValidation(&b, f, "emailVal", "\t")
	if !strings.Contains(b.String(), "@") {
		t.Error("@email should check for @")
	}
}

func TestGenerateStringValidation_MinMaxLength(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "username",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "minLength", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "3"}}}},
			{Name: "maxLength", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "20"}}}},
		},
	}
	generateStringValidation(&b, f, "usernameVal", "\t")
	out := b.String()
	if !strings.Contains(out, "< 3") {
		t.Error("@minLength should check < 3")
	}
	if !strings.Contains(out, "> 20") {
		t.Error("@maxLength should check > 20")
	}
}

func TestGenerateStringValidation_Pattern(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "code",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "pattern", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "^[A-Z]{3}$"}}}},
		},
	}
	generateStringValidation(&b, f, "codeVal", "\t")
	if !strings.Contains(b.String(), "regexp.MatchString") {
		t.Error("@pattern should use regexp")
	}
}

func TestGenerateNumericValidation_Range(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "age",
		Type: &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{
			{Name: "range", Args: []*ast.NamedArg{
				{Value: &ast.Literal{Kind: token.Int, Value: "0"}},
				{Value: &ast.Literal{Kind: token.Int, Value: "150"}},
			}},
		},
	}
	generateNumericValidation(&b, f, "ageVal", "\t")
	if !strings.Contains(b.String(), "< 0") || !strings.Contains(b.String(), "> 150") {
		t.Error("@range should check bounds")
	}
}

func TestWriteValidationCheck(t *testing.T) {
	var b strings.Builder
	writeValidationCheck(&b, "\t", "name", `nameVal == ""`, "must not be empty")
	out := b.String()
	if !strings.Contains(out, `nameVal == ""`) || !strings.Contains(out, "must not be empty") {
		t.Errorf("unexpected output: %s", out)
	}
}

// ─── Handler registration with @cache/@rateLimit ─────────────────────────────

func TestWriteHandlerRegistration_Cache(t *testing.T) {
	var b strings.Builder
	dirs := []*ast.Directive{
		{Name: "cache", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "60"}}}},
	}
	writeHandlerRegistration(&b, "getUser", dirs)
	out := b.String()
	if !strings.Contains(out, "api.WithCache") {
		t.Errorf("@cache should wrap with WithCache: %s", out)
	}
}

func TestWriteHandlerRegistration_Plain(t *testing.T) {
	var b strings.Builder
	writeHandlerRegistration(&b, "getUser", nil)
	out := b.String()
	if strings.Contains(out, "WithCache") || strings.Contains(out, "WithRateLimit") {
		t.Errorf("no directives should not wrap: %s", out)
	}
	if !strings.Contains(out, "handleGetUser(app)") {
		t.Errorf("should register plain handler: %s", out)
	}
}

// ─── @beforeSave tests ──────────────────────────────────────────────────────

func TestGenerateBeforeSave_WithBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "trim"},
				}},
			}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if !strings.Contains(b.String(), "strings.TrimSpace") {
		t.Errorf("@beforeSave { it.trim() } should generate TrimSpace: %s", b.String())
	}
}

func TestGenerateBeforeSave_NoDirective(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("no @beforeSave should generate nothing")
	}
}

// ─── Aggregate computed fields ──────────────────────────────────────────────

func TestGenerateAggregateFields_Count(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "postCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "count", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "posts"}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	out := b.String()
	if !strings.Contains(out, "AggregateSQL") {
		t.Errorf("@count should generate AggregateSQL call: %s", out)
	}
	if !strings.Contains(out, "PostCount") {
		t.Errorf("should set PostCount: %s", out)
	}
}

func TestGenerateAggregateFields_Sum(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "totalSpent", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "sum", Args: []*ast.NamedArg{{Value: &ast.MemberExpr{
						Object: &ast.Ident{Name: "orders"},
						Field:  "amount",
					}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	out := b.String()
	if !strings.Contains(out, `"SUM"`) {
		t.Errorf("@sum should use SUM: %s", out)
	}
	if !strings.Contains(out, "amount") {
		t.Errorf("@sum(orders.amount) should target amount column: %s", out)
	}
}

func TestGenerateAggregateFields_NoComputed(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name:   "User",
		Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if b.Len() > 0 {
		t.Error("no computed fields should generate nothing")
	}
}

func TestScanModelsForValidation_Pattern(t *testing.T) {
	models := []*ast.ModelDecl{{
		Name:       "User",
		Directives: []*ast.Directive{{Name: "crud"}},
		Fields: []*ast.FieldDecl{
			{Name: "code", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "pattern"}}},
		},
	}}
	hasVal, hasPat := scanModelsForValidation(models)
	if !hasVal || !hasPat {
		t.Error("@pattern should set both hasValidation and hasPattern")
	}
}

func TestScanModelsForValidation_NoCrud(t *testing.T) {
	models := []*ast.ModelDecl{{
		Name: "Config",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "notBlank"}}},
		},
	}}
	hasVal, _ := scanModelsForValidation(models)
	if hasVal {
		t.Error("non-CRUD model should not trigger validation")
	}
}

func TestGenerateAggregateFields_NoArgs(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "score", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{{Name: "count"}}, // no args
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if b.Len() > 0 {
		t.Error("@count without args should generate nothing")
	}
}

func TestGenerateBeforeSave_EmptyBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("empty body should generate nothing")
	}
}

func TestGenerateAggregateFields_Avg(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "avgScore", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "avg", Args: []*ast.NamedArg{{Value: &ast.MemberExpr{
						Object: &ast.Ident{Name: "reviews"},
						Field:  "score",
					}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if !strings.Contains(b.String(), `"AVG"`) {
		t.Errorf("should use AVG: %s", b.String())
	}
}

func TestGenerateAggregateFields_Min(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "minPrice", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "min", Args: []*ast.NamedArg{{Value: &ast.MemberExpr{
						Object: &ast.Ident{Name: "products"},
						Field:  "price",
					}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if !strings.Contains(b.String(), `"MIN"`) {
		t.Errorf("should use MIN: %s", b.String())
	}
}

func TestGenerateAggregateFields_Max(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "maxAge", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "max", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "members"}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if !strings.Contains(b.String(), `"MAX"`) {
		t.Errorf("should use MAX: %s", b.String())
	}
}

func TestGenerateAggregateFields_UnknownDirective(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "x", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{{Name: "native"}},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if b.Len() > 0 {
		t.Error("@native computed should generate nothing")
	}
}

func TestGenerateAggregateFields_EmptyRelation(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "x", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{
				Directives: []*ast.Directive{
					{Name: "count", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "42"}}}},
				},
			}},
		},
	}
	generateAggregateFields(&b, m, "result", "\t\t")
	if b.Len() > 0 {
		t.Error("non-ident/member arg should generate nothing")
	}
}

func TestGenerateBeforeSave_NonExprBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("non-ExprStmt body should generate nothing")
	}
}

func TestCRUDHandlerSkipDuplicate(t *testing.T) {
	models := []*ast.ModelDecl{
		{Name: "Project", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}, {Name: "serial"}}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		}, Directives: []*ast.Directive{{Name: "crud"}}},
	}
	skipNames := map[string]bool{"createProject": true, "deleteProject": true}
	var b strings.Builder
	generateCRUDHandlers(&b, models, nil, skipNames)
	code := b.String()
	if strings.Contains(code, "func handleCreateProject(") {
		t.Error("should skip createProject (overridden by compiled API)")
	}
	if strings.Contains(code, "func handleDeleteProject(") {
		t.Error("should skip deleteProject (overridden by compiled API)")
	}
	if !strings.Contains(code, "func handleGetProject(") {
		t.Error("should still have getProject")
	}
}

func TestCRUDHandlerSkipDebug(t *testing.T) {
	models := []*ast.ModelDecl{
		{Name: "Project", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}, {Name: "serial"}}},
		}, Directives: []*ast.Directive{{Name: "crud"}}},
	}
	ops := crudOperations(models[0])
	t.Logf("ops: %v", ops)
	for _, op := range ops {
		t.Logf("crudAPIName(Project, %s) = %s", op, crudAPIName("Project", op))
	}
}

func TestScanBodyForBuiltins(t *testing.T) {
	// crypto.randomHex() → hasCrypto
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "crypto"}, Field: "randomHex"},
		}},
	}}
	var f handlerFeatures
	scanBodyForBuiltins(body, &f)
	if !f.hasCrypto {
		t.Error("should detect crypto usage")
	}

	// now() → hasTimeFunc
	body2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "t", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "now"},
		}},
	}}
	var f2 handlerFeatures
	scanBodyForBuiltins(body2, &f2)
	if !f2.hasTimeFunc {
		t.Error("should detect now() usage")
	}

	// n.days → hasTimeFunc
	body3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "d", Value: &ast.MemberExpr{
			Object: &ast.Ident{Name: "n"}, Field: "days",
		}},
	}}
	var f3 handlerFeatures
	scanBodyForBuiltins(body3, &f3)
	if !f3.hasTimeFunc {
		t.Error("should detect duration property usage")
	}

	// nil body
	var f4 handlerFeatures
	scanBodyForBuiltins(nil, &f4)
	if f4.hasCrypto || f4.hasTimeFunc {
		t.Error("nil body should not set flags")
	}
}
