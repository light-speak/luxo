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
	if strings.Contains(code, "handleDeleteUser") {
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
	if len(ops) != 5 {
		t.Errorf("got %d ops, want 5", len(ops))
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
