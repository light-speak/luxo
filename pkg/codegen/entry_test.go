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
		"migrate.EnsureDatabase",
		"migrate.New(ctx",
		"migrator.Up(ctx)",
		// Shared DB — one pool, all modules use NewFromDB
		"lux.DBConfigFromEnv()",
		"pg.NewDBWithConfig(ctx",
		"defer db.Close()",
		"user_luxo.NewFromDB(db)",
		"post_luxo.NewFromDB(db)",
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
	// Should NOT create separate pools per module
	if strings.Contains(code, "user_luxo.New(ctx)") || strings.Contains(code, "post_luxo.New(ctx)") {
		t.Errorf("embedded mode should use NewFromDB(db), not New(ctx):\n%s", code)
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

func TestGenerateEntryFileWithExtends(t *testing.T) {
	// Test that modules with cross-module extends trigger cluster/embedded mode branching
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
			{
				Name: "origin/monitoring.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Trace",
					Directives: []*ast.Directive{{Name: "crud"}},
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					},
				}},
				Extends: []*ast.ExtendDecl{{
					Name:   "User",
					Fields: []*ast.FieldDecl{{Name: "traces", Type: &ast.TypeRef{Name: "Trace", IsList: true}}},
				}},
			},
			{
				// Module without loaders — tests the !hasLoaders skip in cluster mode
				Name:   "origin/common.luxo",
				Events: []*ast.EventDecl{{Name: "PurgeRequested"}},
			},
		},
	}

	src := GenerateEntryFile(result, "myapp")
	if src == nil {
		t.Fatal("should generate entry file")
	}
	code := string(src)

	// Should have cluster/embedded mode branching
	if !strings.Contains(code, "DEPLOY_MODE") {
		t.Errorf("should have DEPLOY_MODE check:\n%s", code)
	}
	if !strings.Contains(code, `deployMode == "cluster"`) {
		t.Errorf("should have cluster mode branch:\n%s", code)
	}
	if !strings.Contains(code, "NewRemoteLoaders") {
		t.Errorf("should have NewRemoteLoaders for cluster mode:\n%s", code)
	}
	if !strings.Contains(code, "NewDefaultLoaders") {
		t.Errorf("should have NewDefaultLoaders for embedded mode:\n%s", code)
	}
	if !strings.Contains(code, "rpc.NewClient") {
		t.Errorf("should have RPC client setup:\n%s", code)
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

	// Should NOT have service fn registration
	if strings.Contains(code, "RegisterServiceFns") {
		t.Error("no service fns, should not have RegisterServiceFns")
	}
	// Should still have RPC server (batchLoad + federation resolvers need it)
	if !strings.Contains(code, "RegisterBatchLoaders") {
		t.Error("crud model should have RegisterBatchLoaders")
	}
}

func TestModuleNameFromFile(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"origin/user.luxo", "user"},
		{"origin/post.luxo", "post"},
		{"origin/user/model.luxo", "user"},
		{"origin/order/types.luxo", "order"},
		{"user.luxo", "user"}, // no origin/ prefix — still strips .luxo
	}
	for _, tt := range tests {
		got := moduleNameFromFile(tt.path)
		if got != tt.want {
			t.Errorf("moduleNameFromFile(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestGenerateModuleEntryFiles(t *testing.T) {
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

	entries := GenerateModuleEntryFiles(result, "myapp")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Check user entry
	userSrc := string(entries["user"])
	userChecks := []string{
		"package main",
		`user_luxo "myapp/service/user/luxo"`,
		`user_resolver "myapp/service/user/resolver"`,
		"migrate.EnsureDatabase",
		"user_luxo.RegisterHandlers",
		"rpc.NewServer",
		"DATABASE_PREFIX",
		"AUTO_MIGRATE",
		"gw.Serve(Version)",
	}
	for _, check := range userChecks {
		if !strings.Contains(userSrc, check) {
			t.Errorf("user entry missing %q", check)
		}
	}

	// Check post entry
	postSrc := string(entries["post"])
	if !strings.Contains(postSrc, `post_luxo "myapp/service/post/luxo"`) {
		t.Error("post entry missing import")
	}
}

func TestGenerateModuleEntryFiles_Empty(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	entries := GenerateModuleEntryFiles(result, "myapp")
	if entries != nil {
		t.Error("should return nil for empty project")
	}
}

func TestGenerateModuleEntryFiles_WithLoaders(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				}},
			},
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "Post",
					Fields: []*ast.FieldDecl{
						{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					},
				}},
			},
		},
	}

	entries := GenerateModuleEntryFiles(result, "myapp")
	userSrc := string(entries["user"])

	// Module with same-module relations (no extend) should use DefaultLoaders
	if !strings.Contains(userSrc, "NewDefaultLoaders") {
		t.Errorf("same-module relations should use NewDefaultLoaders:\n%s", userSrc)
	}
	if strings.Contains(userSrc, "NewRemoteLoaders") {
		t.Errorf("no extend — should NOT use NewRemoteLoaders:\n%s", userSrc)
	}
}

func TestGenerateModuleEntryFiles_WithEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/order.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "Order",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			Events: []*ast.EventDecl{
				{Name: "OrderCreated"},
			},
		}},
	}

	entries := GenerateModuleEntryFiles(result, "myapp")
	src := string(entries["order"])

	if !strings.Contains(src, "event.NewFromEnv()") {
		t.Errorf("should have event bus:\n%s", src)
	}
	if !strings.Contains(src, "RegisterEvents") {
		t.Errorf("should register events:\n%s", src)
	}
}

func TestGenerateModuleEntryFiles_WithServiceFns(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			Functions: []*ast.FnDecl{{
				Name:       "getScore",
				Directives: []*ast.Directive{{Name: "service"}},
				Body:       &ast.Block{},
			}},
		}},
	}

	entries := GenerateModuleEntryFiles(result, "myapp")
	src := string(entries["user"])

	if !strings.Contains(src, "RegisterServiceFns") {
		t.Errorf("should register service fns:\n%s", src)
	}
}

func TestGenerateGatewayEntry(t *testing.T) {
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

	code := GenerateGatewayEntry(result, "myapp")
	if code == nil {
		t.Fatal("should generate gateway entry")
	}
	src := string(code)

	checks := []string{
		"package main",
		"gateway/main.gen.go",
		`user_luxo "myapp/service/user/luxo"`,
		`post_luxo "myapp/service/post/luxo"`,
		"rpc.NewClient",
		"USER_SERVICE_ADDR",
		"POST_SERVICE_ADDR",
		"user:9000",
		"post:9000",
		"RegisterSchema",
		"proxyHandler",
		"routing",
		"gw.Serve(Version)",
		`gw.AddModule("user")`,
		`gw.AddModule("post")`,
		"JSONParamsToBinary",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("gateway missing %q", check)
		}
	}
}

func TestGenerateGatewayEntry_Empty(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	code := GenerateGatewayEntry(result, "myapp")
	if code != nil {
		t.Error("should return nil for empty project")
	}
}

func TestGenerateGatewayEntry_WithServiceFns(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			Functions: []*ast.FnDecl{{
				Name:       "getScore",
				Directives: []*ast.Directive{{Name: "service"}},
				Body:       &ast.Block{},
			}},
		}},
	}

	code := GenerateGatewayEntry(result, "myapp")
	src := string(code)

	// Service fns should be in routing table
	if !strings.Contains(src, "svc:getScore") {
		t.Errorf("missing service fn route:\n%s", src)
	}
}

func TestCollectModules(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
				Events: []*ast.EventDecl{{Name: "E"}},
			},
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "Post",
				}},
			},
		},
	}

	modules := collectModules(result)
	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(modules))
	}

	// user should have crud and events
	user := modules[0]
	if user.name != "user" {
		t.Errorf("first module = %q, want user", user.name)
	}
	if !user.hasCrud {
		t.Error("user should have crud")
	}
	if !user.hasEvents {
		t.Error("user should have events")
	}

	// post should have no crud
	post := modules[1]
	if post.hasCrud {
		t.Error("post should not have crud")
	}
}

func TestCollectModulesWithExtends(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/monitoring/trace.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "Trace",
			}},
			Extends: []*ast.ExtendDecl{{
				Name:   "Project",
				Fields: []*ast.FieldDecl{{Name: "traces", Type: &ast.TypeRef{Name: "Trace", IsList: true}}},
			}},
		}},
	}
	modules := collectModules(result)
	if len(modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(modules))
	}
	if !modules[0].hasLoaders {
		t.Error("module with extends should have loaders")
	}
	if !modules[0].hasExtend {
		t.Error("module with extends should have hasExtend")
	}
}

func TestCollectModulesHasSchema(t *testing.T) {
	// Event-only module: no models, no APIs → hasSchema = false
	result := &semantic.Result{
		Files: []*ast.File{{
			Name:   "origin/common/events.luxo",
			Events: []*ast.EventDecl{{Name: "PurgeRequested"}},
		}},
	}
	modules := collectModules(result)
	if len(modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(modules))
	}
	if modules[0].hasSchema {
		t.Error("event-only module should not have schema")
	}

	// Module with models → hasSchema = true
	result2 := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
			}},
		}},
	}
	modules2 := collectModules(result2)
	if !modules2[0].hasSchema {
		t.Error("module with models should have schema")
	}
}

func TestGenerateSingleModuleEntryWithExtends(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/monitoring/trace.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Trace",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
				Extends: []*ast.ExtendDecl{{
					Name:   "Project",
					Fields: []*ast.FieldDecl{{Name: "traces", Type: &ast.TypeRef{Name: "Trace", IsList: true}}},
				}},
			},
			{
				Name: "origin/project/project.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Project",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
		},
	}

	modules := collectModules(result)
	// Find monitoring module
	var target moduleInfo
	for _, m := range modules {
		if m.name == "monitoring" {
			target = m
			break
		}
	}
	code := generateSingleModuleEntry(target, modules, result, "myapp")
	src := string(code)

	// Should have RPC-backed loader wiring with project service
	if !strings.Contains(src, "rpc.NewClient") {
		t.Errorf("extends module should use RPC client:\n%s", src)
	}
	if !strings.Contains(src, "NewRemoteLoaders") {
		t.Errorf("extends module should use NewRemoteLoaders:\n%s", src)
	}
	if !strings.Contains(src, "PROJECT_SERVICE_ADDR") {
		t.Errorf("should reference project service addr:\n%s", src)
	}
}

func TestGenerateGatewayEntryEventOnly(t *testing.T) {
	// Gateway with an event-only module — should NOT generate RegisterSchema for it
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
				Name:   "origin/common/events.luxo",
				Events: []*ast.EventDecl{{Name: "PurgeRequested"}},
			},
		},
	}

	code := GenerateGatewayEntry(result, "myapp")
	src := string(code)

	if !strings.Contains(src, "user_luxo.RegisterSchema") {
		t.Errorf("should have RegisterSchema for user module:\n%s", src)
	}
	// common module is event-only, should not appear in gateway routing
	if strings.Contains(src, "common_luxo.RegisterSchema") {
		t.Errorf("event-only module should NOT have RegisterSchema:\n%s", src)
	}
}

func TestGenerateGatewayEntryStreamSkip(t *testing.T) {
	// Stream APIs should not appear in normal routing
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			APIs: []*ast.ApiDecl{{
				Name:       "watchUsers",
				Directives: []*ast.Directive{{Name: "stream"}},
				Body:       &ast.Block{Stmts: []ast.Stmt{}},
			}},
		}},
	}

	code := GenerateGatewayEntry(result, "myapp")
	src := string(code)

	if strings.Contains(src, `"watchUsers"`) {
		t.Errorf("stream API should not be in routing table:\n%s", src)
	}
}

func TestGenerateGatewayEntryNoCrudModel(t *testing.T) {
	// Model without @crud should not generate routes
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "User",
				// no @crud directive
			}},
		}},
	}

	code := GenerateGatewayEntry(result, "myapp")
	src := string(code)

	// Should not have CRUD routes
	if strings.Contains(src, `"getUser"`) {
		t.Errorf("non-crud model should not have routes:\n%s", src)
	}
}

func TestGenerateGatewayEntryCompiledAPIDedup(t *testing.T) {
	// When a compiled API has the same name as a CRUD API, compiled wins
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
			APIs: []*ast.ApiDecl{{
				Name:       "getUser",
				ReturnType: &ast.TypeRef{Name: "User"},
				Body:       &ast.Block{Stmts: []ast.Stmt{}},
			}},
		}},
	}

	code := GenerateGatewayEntry(result, "myapp")
	src := string(code)

	// getUser should appear exactly once in routing
	count := strings.Count(src, `"getUser"`)
	if count != 1 {
		t.Errorf("getUser should appear exactly once (dedup), appeared %d times:\n%s", count, src)
	}
}
