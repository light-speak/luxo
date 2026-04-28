package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateEntryFile(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Post",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
		},
	}

	src := GenerateEntryFile(result, "myapp")
	if src == nil {
		t.Fatal("should generate entry file")
	}
	code := string(src)

	checks := []string{
		"package main",
		`user_luxo "myapp/service/user/luxo"`,
		`post_luxo "myapp/service/post/luxo"`,
		`user_resolver "myapp/service/user/resolver"`,
		`post_resolver "myapp/service/post/resolver"`,
		"luvia.New()",
		"user_luxo.RegisterHandlers(gw.Router, userApp)",
		"post_luxo.RegisterHandlers(gw.Router, postApp)",
		"user_resolver.Setup(userApp)",
		"post_resolver.Setup(postApp)",
		"gw.Serve(Version)",
		`gw.AddModule("user")`,
		`gw.AddModule("post")`,
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in entry:\n%s", check, code)
		}
	}
}

func TestGenerateEntryFileNoModels(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	src := GenerateEntryFile(result, "myapp")
	if src != nil {
		t.Error("should return nil for empty files")
	}
}

func TestGenerateEntryFileNoCrud(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/config.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "Config",
				// no @crud
			}},
		}},
	}
	src := GenerateEntryFile(result, "myapp")
	code := string(src)

	// Should still generate entry but not register handlers
	if !strings.Contains(code, `gw.AddModule("config")`) {
		t.Error("should add module")
	}
	if strings.Contains(code, "RegisterHandlers") {
		t.Error("should NOT register handlers for non-crud model")
	}
}

func TestGenerateEntryFileWithLoaders(t *testing.T) {
	// Test that models with relations trigger SetLoaders + NewDefaultLoaders
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/blog.luxo",
				Models: []*ast.ModelDecl{
					{
						Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
						Name:       "User",
						Directives: []*ast.Directive{{Name: "crud"}},
						Fields: []*ast.FieldDecl{
							{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
							{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
						},
					},
					{
						Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
						Name: "Post",
						Fields: []*ast.FieldDecl{
							{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
							{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
						},
					},
				},
			},
		},
	}

	src := GenerateEntryFile(result, "myapp")
	if src == nil {
		t.Fatal("should generate entry file")
	}
	code := string(src)

	// Should have SetLoaders and NewDefaultLoaders for the module with relations
	if !strings.Contains(code, "SetLoaders") {
		t.Errorf("should call SetLoaders for module with relations:\n%s", code)
	}
	if !strings.Contains(code, "NewDefaultLoaders") {
		t.Errorf("should call NewDefaultLoaders for module with relations:\n%s", code)
	}
}

func TestGenerateEntryFileWithEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{
					{
						Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
						Name:       "User",
						Directives: []*ast.Directive{{Name: "crud"}},
						Fields: []*ast.FieldDecl{
							{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
							{Name: "name", Type: &ast.TypeRef{Name: "String"}},
						},
					},
				},
				Events: []*ast.EventDecl{
					{Name: "UserCreated", Params: []*ast.ParamDecl{
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					}},
				},
			},
		},
	}

	src := GenerateEntryFile(result, "myapp")
	if src == nil {
		t.Fatal("should generate entry file")
	}
	code := string(src)

	// Should have event bus wiring via framework function
	if !strings.Contains(code, "event.NewFromEnv()") {
		t.Errorf("should use event.NewFromEnv():\n%s", code)
	}
	if !strings.Contains(code, "RegisterEvents") {
		t.Errorf("should call RegisterEvents:\n%s", code)
	}
	if !strings.Contains(code, "eventBus.Close()") {
		t.Errorf("should defer eventBus.Close():\n%s", code)
	}
}

func TestGenerateEntryFileWithServiceFns(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
				Functions: []*ast.FnDecl{{
					Name:       "getUserScore",
					Directives: []*ast.Directive{{Name: "service"}},
					Body:       &ast.Block{},
				}},
			},
		},
	}

	src := GenerateEntryFile(result, "myapp")
	code := string(src)

	// Should register service fns
	if !strings.Contains(code, "user_luxo.RegisterServiceFns(gw.Router, userApp)") {
		t.Errorf("missing RegisterServiceFns:\n%s", code)
	}

	// Should start RPC server
	if !strings.Contains(code, "rpc.NewServer(gw.Router)") {
		t.Errorf("missing RPC server creation:\n%s", code)
	}
	if !strings.Contains(code, "rpcServer.ListenAndServe") {
		t.Errorf("missing RPC server start:\n%s", code)
	}

	// Should import rpc package
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/rpc"`) {
		t.Errorf("missing rpc import:\n%s", code)
	}

	// Should use LUXO_PORT
	if !strings.Contains(code, "LUXO_PORT") {
		t.Errorf("missing LUXO_PORT env:\n%s", code)
	}
}

func TestGenerateEntryFileNoServiceFns(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
		}},
	}

	src := GenerateEntryFile(result, "myapp")
	code := string(src)

	// Should NOT have RPC server or service fn registration
	if strings.Contains(code, "RegisterServiceFns") {
		t.Error("no service fns, should not have RegisterServiceFns")
	}
	if strings.Contains(code, "rpc.NewServer") {
		t.Error("no service fns, should not have RPC server")
	}
}
