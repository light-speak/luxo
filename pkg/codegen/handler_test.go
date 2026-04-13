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
	generateRegisterFuncWithInferred(&b, models, inferredNames)
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
