package codegen

import (
	"bytes"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestCheckedEntriesReturnCanonicalSource(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{Name: "User"}}}}}
	entries, err := GenerateModuleEntryFilesChecked(result, "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	for name, generate := range map[string]func(*semantic.Result, string, ...EntryBackends) ([]byte, error){
		"embedded": GenerateEntryFileChecked, "gateway": GenerateGatewayEntryChecked,
	} {
		source, err := generate(result, "example.com/app")
		if err != nil {
			t.Fatal(err)
		}
		entries[name] = source
	}
	for name, source := range entries {
		canonical, err := formatGeneratedChecked(source)
		if err != nil || !bytes.Equal(source, canonical) || len(source) == 0 {
			t.Errorf("%s is not canonical Go: %v", name, err)
		}
	}
}

func TestEntryGeneratorsExposeOnlyCheckedFunctions(t *testing.T) {
	file, err := goparser.ParseFile(gotoken.NewFileSet(), "entry.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	functions := make(map[string]bool)
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*goast.FuncDecl); ok && function.Recv == nil {
			functions[function.Name.Name] = true
		}
	}
	for _, name := range []string{"GenerateEntryFile", "GenerateModuleEntryFiles", "GenerateGatewayEntry"} {
		t.Run(name, func(t *testing.T) {
			if functions[name] {
				t.Errorf("unchecked entry generator %s must not be exported", name)
			}
			if !functions[name+"Checked"] {
				t.Errorf("checked entry generator %sChecked must remain public", name)
			}
		})
	}
}

func TestEntryEmissionDefersFormattingToCheckedBoundary(t *testing.T) {
	file, err := goparser.ParseFile(gotoken.NewFileSet(), "entry.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	emitters := map[string]bool{
		"generateEntryFile": false, "generateSingleModuleEntry": false, "generateGatewayEntry": false,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if !ok {
			continue
		}
		if _, tracked := emitters[function.Name.Name]; !tracked {
			continue
		}
		emitters[function.Name.Name] = true
		goast.Inspect(function.Body, func(node goast.Node) bool {
			call, ok := node.(*goast.CallExpr)
			if !ok {
				return true
			}
			if name, ok := call.Fun.(*goast.Ident); ok && (name.Name == "formatGenerated" || name.Name == "formatGeneratedChecked") {
				t.Errorf("%s formats twice through its Checked boundary", function.Name.Name)
			}
			return true
		})
	}
	for name, found := range emitters {
		if !found {
			t.Errorf("missing emitter %s", name)
		}
	}
}

func TestCheckedEntriesRejectUnresolvedCacheContracts(t *testing.T) {
	declaration := cachedTestAPI(true)
	for _, args := range [][]*ast.NamedArg{
		{{Name: "ttl", Value: &ast.Literal{Kind: token.Int, Value: "60"}}},
		{{Name: "policy", Value: &ast.Literal{Kind: token.String, Value: "missing"}}},
	} {
		declaration.Directives[1].Args = args
		input := &semantic.Result{Files: []*ast.File{{APIs: []*ast.ApiDecl{declaration}}}}
		if source, err := GenerateEntryFileChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err == nil || source != nil {
			t.Fatal("embedded entry accepted unresolved policy")
		}
		if source, err := GenerateModuleEntryFilesChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err == nil || source != nil {
			t.Fatal("module entries accepted unresolved policy")
		}
		if source, err := GenerateGatewayEntryChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err == nil || source != nil {
			t.Fatal("gateway entry accepted unresolved policy")
		}
	}
}

func TestCacheEntryRequiresExplicitBackend(t *testing.T) {
	for _, cached := range []bool{false, true} {
		api := &ast.ApiDecl{Name: "answer", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Literal{Kind: token.Int, Value: "1"}}}}}
		if cached {
			api.Directives = []*ast.Directive{{Name: "cache", Args: []*ast.NamedArg{{Name: "policy", Value: &ast.Literal{Kind: token.String, Value: "preview"}}}}}
		}
		result := analyzeCachedFixture(t, []*ast.File{{Name: "origin/task/api.luxo", APIs: []*ast.ApiDecl{api}}}, true)
		if len(result.Errors) != 0 {
			t.Fatal(result.Errors)
		}
		embedded, err := GenerateEntryFileChecked(result, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
		if err != nil {
			t.Fatal(err)
		}
		entries, err := GenerateModuleEntryFilesChecked(result, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
		if err != nil {
			t.Fatal(err)
		}
		for name, source := range map[string][]byte{"embedded": embedded, "task": entries["task"]} {
			code := string(source)
			if cached {
				for _, part := range []string{`api.NewResponseCacheFromEnv(ctx, "` + name + `", cache.OpenMemory)`, "defer responseCache.Close()", "luvia.NewWithOptions", "ResponseCache: responseCache"} {
					if !strings.Contains(code, part) {
						t.Fatalf("%s missing %s: %s", name, part, code)
					}
				}
			} else if strings.Contains(code, "ResponseCache") || strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/api"`) {
				t.Fatalf("uncached app gained cache setup: %s", code)
			}
		}
	}
}

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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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
		`env.CheckBackend("DATABASE_DRIVER", "pg")`,
		`"github.com/light-speak/luxo/pkg/lux/pg/migrate"`,
		"migrate.New(ctx",
		"migrator.Up(ctx)",
		"fatal: ensure db:",
		"fatal: migrate:",
		"AUTO_MIGRATE must be true or false",
		"DEPLOY_MODE must be embedded or cluster",
		// Shared DB — one pool, all modules use NewFromDB
		"lux.DBConfigFromEnv()",
		"pg.NewDBWithConfig(ctx",
		"defer db.Close()",
		"gw.SetRuntimeDependencyStatsProvider(db)",
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
	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	if src != nil {
		t.Error("should return nil for empty files")
	}
}

func TestGeneratedSchemaRegistrationPrecedesServing(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	for _, name := range []string{"user", "post"} {
		result.Files = append(result.Files, &ast.File{
			Name:      "origin/" + name + "/api.luxo",
			Models:    []*ast.ModelDecl{{Name: name + "Record", Directives: []*ast.Directive{{Name: "crud"}}}},
			Types:     []*ast.TypeDecl{{Name: name + "Payload", Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}},
			Functions: []*ast.FnDecl{{Name: name + "Score", Directives: []*ast.Directive{{Name: "service"}}, Body: &ast.Block{}}},
		})
	}
	modules := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	for _, entry := range []struct {
		name    string
		source  []byte
		modules []string
	}{
		{"embedded", generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}), []string{"user", "post"}},
		{"gateway", generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}), []string{"user", "post"}},
		{"user", modules["user"], []string{"user"}},
		{"post", modules["post"], []string{"post"}},
	} {
		t.Run(entry.name, func(t *testing.T) { assertSchemaBeforeServing(t, string(entry.source), entry.modules) })
	}
}

func assertSchemaBeforeServing(t *testing.T, source string, modules []string) {
	t.Helper()
	firstServe := strings.Index(source, "gw.Serve(")
	if firstServe < 0 {
		t.Fatal("entry has no HTTP serving boundary")
	}
	if rpc := strings.Index(source, "rpc.NewServer("); rpc >= 0 && rpc < firstServe {
		firstServe = rpc
	}
	for _, module := range modules {
		registration := module + "_luxo.RegisterSchema(gw.Router.Schema)"
		at := strings.Index(source, registration)
		if at < 0 || at > firstServe {
			t.Fatalf("%s must complete before publishing a server", registration)
		}
		if strings.Count(source, registration) != 1 {
			t.Fatalf("%s registered more than once", registration)
		}
	}
}

func TestGeneratedEntriesTreatDotEnvAsOptionalButRejectMalformedFiles(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/user.luxo",
		Models: []*ast.ModelDecl{{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "crud"}},
		}},
	}}}
	sources := map[string]string{
		"embedded": string(generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})),
		"module":   string(generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})["user"]),
		"gateway":  string(generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})),
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(source, `err != nil && !os.IsNotExist(err)`) ||
				!strings.Contains(source, `fatal: load .env: %v`) {
				t.Fatalf("generated entry must ignore only a missing .env file:\n%s", source)
			}
		})
	}
}

func TestCheckedEntryGenerationReportsInvalidGo(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name:   "origin/user.luxo",
		Models: []*ast.ModelDecl{{Name: "User"}},
	}}}
	if _, err := GenerateEntryFileChecked(result, `invalid"module`); err == nil {
		t.Fatal("invalid generated import must be reported")
	}
}

func TestCheckedEntryGenerationCoversEmptyValidAndInvalidProjects(t *testing.T) {
	empty := &semantic.Result{}
	if source, err := GenerateEntryFileChecked(empty, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err != nil || source != nil {
		t.Fatalf("empty embedded entry = %q, %v", source, err)
	}
	if entries, err := GenerateModuleEntryFilesChecked(empty, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err != nil || entries != nil {
		t.Fatalf("empty module entries = %#v, %v", entries, err)
	}
	if source, err := GenerateGatewayEntryChecked(empty, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err != nil || source != nil {
		t.Fatalf("empty gateway entry = %q, %v", source, err)
	}

	result := &semantic.Result{Files: []*ast.File{{Name: "origin/user.luxo", Models: []*ast.ModelDecl{{Name: "User"}}}}}
	entries, err := GenerateModuleEntryFilesChecked(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	if err != nil || len(entries["user"]) == 0 {
		t.Fatalf("valid module entries = %#v, %v", entries, err)
	}
	if _, err := GenerateModuleEntryFilesChecked(result, `invalid"module`); err == nil {
		t.Fatal("invalid module entry import was not reported")
	}
	if source, err := GenerateGatewayEntryChecked(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); err != nil || len(source) == 0 {
		t.Fatalf("valid gateway entry = %q, %v", source, err)
	}
	if _, err := GenerateGatewayEntryChecked(result, `invalid"module`); err == nil {
		t.Fatal("invalid gateway import was not reported")
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
	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	code := string(src)

	// Should still generate entry but not register handlers
	if !strings.Contains(code, `gw.AddModule("config")`) {
		t.Error("should add module")
	}
	if strings.Contains(code, "RegisterHandlers") {
		t.Error("should NOT register handlers for non-crud model")
	}
}

// Regression: modules with non-CRUD APIs (e.g. schema introspection) must still
// emit RegisterHandlers wiring — otherwise their declared APIs are unreachable
// at runtime (clients see "unknown API ID"). Previously the entry generator
// only emitted the call when m.hasCrud, dropping such modules silently.
func TestGenerateEntryFileNonCrudAPIRegistersHandlers(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/schema.luxo",
			// Pure API module — no models, no @crud, no @service fns.
			APIs: []*ast.ApiDecl{{
				Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name: "listServiceSchemas",
			}},
		}},
	}
	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	code := string(src)

	if !strings.Contains(code, `gw.AddModule("schema")`) {
		t.Error("should add module")
	}
	if !strings.Contains(code, "schema_luxo.RegisterHandlers(gw.Router, schemaApp)") {
		t.Errorf("non-CRUD API module must still RegisterHandlers:\n%s", code)
	}
	if strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/rpc"`) {
		t.Errorf("entry without RPC capabilities must not import rpc:\n%s", code)
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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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
	if !strings.Contains(code, "rpc.NewNamedClient") {
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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	if src == nil {
		t.Fatal("should generate entry file")
	}
	code := string(src)

	// Should have event bus wiring via framework function
	if !strings.Contains(code, "event.NewChanBus(256)") {
		t.Errorf("should use event.NewChanBus(256):\n%s", code)
	}
	if !strings.Contains(code, "RegisterEvents") {
		t.Errorf("should call RegisterEvents:\n%s", code)
	}
	for _, required := range []string{"eventBus := event.NewChanBus(256)", "taskQueue := queue.NewMemoryQueue(", "if err := user_luxo.RegisterEvents(", "fatal: register events:"} {
		if !strings.Contains(code, required) {
			t.Errorf("embedded entry must propagate messaging failures: missing %q", required)
		}
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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	src := generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	entries := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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
		"fatal: ensure db:",
		"fatal: migrate:",
		"user_luxo.RegisterHandlers",
		"rpc.NewServer",
		"DATABASE_PREFIX",
		"AUTO_MIGRATE",
		"gw.SetRuntimeDependencyStatsProvider(app.DB)",
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
	entries := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	entries := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	entries := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	src := string(entries["order"])

	if !strings.Contains(src, "event.NewChanBus(256)") {
		t.Errorf("should have event bus:\n%s", src)
	}
	if !strings.Contains(src, "RegisterEvents") {
		t.Errorf("should register events:\n%s", src)
	}
	for _, required := range []string{"eventBus := event.NewChanBus(256)", "taskQueue := queue.NewMemoryQueue(", "if err := order_luxo.RegisterEvents(", "fatal: register events:"} {
		if !strings.Contains(src, required) {
			t.Errorf("module entry must propagate messaging failures: missing %q", required)
		}
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

	entries := generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	if code == nil {
		t.Fatal("should generate gateway entry")
	}
	src := string(code)

	checks := []string{
		"package main",
		"gateway/main.gen.go",
		`user_luxo "myapp/service/user/luxo"`,
		`post_luxo "myapp/service/post/luxo"`,
		"rpc.NewNamedClient",
		"USER_SERVICE_ADDR",
		"POST_SERVICE_ADDR",
		"user:9000",
		"post:9000",
		"RegisterSchema",
		"gw.Router.Registry.RegisterSchemaAPIs(gw.Router.Schema)",
		"proxyHandler",
		"routing",
		"gw.Serve(Version)",
		`gw.AddModule("user")`,
		`gw.AddModule("post")`,
		"params := req.BinaryParams()",
		"luvia.BearerToken(ctx)",
		"CallWithMaskTargetContext(ctx, bearerToken",
		"rpc.TraceTarget{Operation: apiName, Dependency: api.TraceDependencySequential}",
		"rpc.TraceTarget{Operation: svcName, Field: ext.FieldName, Dependency: dependency}",
		"api.TraceDependencyParallel",
		"trace.StartSpan(ctx",
		`Name: "query.plan"`,
		`Name: "response.merge"`,
		"luvia.ExtractPrimaryKeyColumn",
		"luvia.ExtractPrimaryKeys",
		"keys.EncodeResolveParams(shape)",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("gateway missing %q", check)
		}
	}
}

func TestGenerateGatewayEntry_Empty(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	if code != nil {
		t.Error("should return nil for empty project")
	}
}

func TestGenerateGatewayEntryAppliesRateLimitAtPublicEdge(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/user.luxo",
		APIs: []*ast.ApiDecl{{
			Name: "searchUsers",
			Directives: []*ast.Directive{{Name: "rateLimit", Args: []*ast.NamedArg{
				{Name: "max", Value: &ast.Literal{Kind: token.Int, Value: "20"}},
				{Name: "window", Value: &ast.Literal{Kind: token.Duration, Value: "1m"}},
			}}},
		}},
	}}}

	src := string(generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(src, `case "searchUsers":`) ||
		!strings.Contains(src, "api.WithRateLimit(20, time.Minute, handler)") {
		t.Fatalf("gateway must enforce @rateLimit before RPC forwarding:\n%s", src)
	}
}

func TestGenerateGatewayEntrySkipsRoutesWithoutRateLimits(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/user.luxo",
		APIs: []*ast.ApiDecl{
			{
				Name: "limited",
				Directives: []*ast.Directive{{Name: "rateLimit", Args: []*ast.NamedArg{
					{Name: "max", Value: &ast.Literal{Kind: token.Int, Value: "10"}},
					{Name: "window", Value: &ast.Literal{Kind: token.Duration, Value: "1s"}},
				}}},
			},
			{Name: "open"},
		},
	}}}
	source := string(generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(source, `case "limited":`) || strings.Contains(source, `case "open":`) {
		t.Fatalf("gateway rate-limit switch is incorrect:\n%s", source)
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	src := string(code)

	// Internal service functions must not be forwarded by the public gateway.
	if strings.Contains(src, "svc:getScore") {
		t.Errorf("internal service fn exposed by gateway:\n%s", src)
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

func TestCollectModulesMarksEventEmitters(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/auth/member.luxo",
		APIs: []*ast.ApiDecl{{
			Name: "login",
			Body: &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "LoginRecorded"}}},
		}},
	}}}

	modules := collectModules(result)
	if len(modules) != 1 || !modules[0].emitsEvents {
		t.Fatalf("event emitter must receive EventBus wiring: %#v", modules)
	}
	src := string(generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(src, "authApp.EventBus = eventBus") {
		t.Fatalf("missing emitter EventBus wiring:\n%s", src)
	}
}

func TestFileEmitsEventsFromFunctionsAndMiddleware(t *testing.T) {
	emitBody := func() *ast.Block {
		return &ast.Block{Stmts: []ast.Stmt{&ast.EmitStmt{EventName: "AuditRecorded"}}}
	}
	tests := map[string]*ast.File{
		"function": {
			Functions: []*ast.FnDecl{{Name: "audit", Body: emitBody()}},
		},
		"middleware": {
			Middlewares: []*ast.MiddlewareDecl{{Name: "audit", Body: emitBody()}},
		},
	}
	for name, file := range tests {
		t.Run(name, func(t *testing.T) {
			if !fileEmitsEvents(file) {
				t.Fatal("fileEmitsEvents() = false, want true")
			}
		})
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

func TestEntryRegistersCrossModuleLoadEndpoints(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{
		{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "email", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
		},
		{
			Name: "origin/post.luxo",
			Extends: []*ast.ExtendDecl{{
				Name:   "User",
				Fields: []*ast.FieldDecl{{Name: "email", Type: &ast.TypeRef{Name: "String"}}},
			}},
			APIs: []*ast.ApiDecl{{
				Name: "lookup",
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
					Args: []*ast.NamedArg{{Name: "email", Value: &ast.Ident{Name: "email"}}},
				}}}},
			}},
		},
	}}
	modules := collectModules(result)
	if len(modules) != 2 || !modules[0].hasBatchRPC || !modules[0].hasRemoteLoad {
		t.Fatalf("module endpoint flags = %#v", modules)
	}
	code := string(generateEntryFile(result, "github.com/test", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(code, "user_luxo.RegisterBatchLoaders(gw.Router, userApp)") {
		t.Errorf("entry did not register the PK batch endpoint:\n%s", code)
	}
	if !strings.Contains(code, "user_luxo.RegisterRemoteLoaders(gw.Router, userApp)") {
		t.Errorf("entry did not register the named load endpoint:\n%s", code)
	}
	entries := generateModuleEntryFiles(result, "github.com/test", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	userEntry := string(entries["user"])
	if !strings.Contains(userEntry, "user_luxo.RegisterRemoteLoaders(gw.Router, app)") {
		t.Errorf("user service did not register the named load endpoint:\n%s", userEntry)
	}
}

func TestCollectModulesIgnoresPrimaryKeyLoadEndpoint(t *testing.T) {
	load := &ast.CallExpr{
		Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
		Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}},
	}
	result := &semantic.Result{Files: []*ast.File{
		{
			Name:   "origin/user.luxo",
			Models: []*ast.ModelDecl{{Name: "User"}},
			APIs: []*ast.ApiDecl{{
				Name: "lookup",
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: load}}},
			}},
		},
	}}
	modules := collectModules(result)
	if len(modules) != 1 || modules[0].hasRemoteLoad {
		t.Fatalf("module flags = %#v", modules)
	}
}

func TestDataOnlyModulesHaveNoRuntimeEntry(t *testing.T) {
	common := &ast.File{Name: "origin/common/types.luxo", Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER"}}}}
	result := &semantic.Result{Files: []*ast.File{common}}
	if modules := collectModules(result); len(modules) != 0 {
		t.Fatalf("data-only runtime modules = %v", modules)
	}
	if src := generateEntryFile(result, "example.com/app", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}); src != nil {
		t.Fatal("data-only project generated a process")
	}
	result.Files = append(result.Files, &ast.File{Name: "origin/common/events.luxo", Events: []*ast.EventDecl{{Name: "Refresh"}}})
	if modules := collectModules(result); len(modules) != 1 || modules[0].name != "common" || !modules[0].hasEvents {
		t.Fatalf("event-bearing module lost its runtime: %v", modules)
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
	code := generateSingleModuleEntry(target, modules, result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	src := string(code)

	// Should have RPC-backed loader wiring with project service
	if !strings.Contains(src, "rpc.NewNamedClient") {
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	src := string(code)

	if strings.Contains(src, `"watchUsers"`) {
		t.Errorf("stream API should not be in routing table:\n%s", src)
	}
}

func TestGeneratedEntriesRegisterStreams(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name:   "origin/alert/event.luxo",
		Events: []*ast.EventDecl{{Name: "AlertFired"}},
		APIs: []*ast.ApiDecl{{
			Name:       "liveAlerts",
			Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "AlertFired"}}}}},
		}},
	}}}

	embedded := string(generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(embedded, "alert_luxo.RegisterStreams(gw.Router, eventBus, nil)") {
		t.Fatalf("embedded entry does not register streams:\n%s", embedded)
	}

	module := string(generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})["alert"])
	if !strings.Contains(module, "alert_luxo.RegisterStreams(gw.Router, eventBus, nil)") {
		t.Fatalf("module entry does not register streams:\n%s", module)
	}

	gateway := string(generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(gateway, "alert_luxo.RegisterStreams(gw.Router, eventBus, nil)") {
		t.Fatalf("gateway entry does not register streams:\n%s", gateway)
	}
	for name, source := range map[string]string{"embedded": embedded, "module": module, "gateway": gateway} {
		if !strings.Contains(source, "if err := alert_luxo.RegisterStreams(gw.Router, eventBus, nil); err != nil {") || !strings.Contains(source, "fatal: register streams:") {
			t.Errorf("%s ignores stream listener registration failure", name)
		}
	}
}

func TestGeneratedEntriesBridgeNativeStreamsThroughRPC(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/score/api.luxo",
		Models: []*ast.ModelDecl{{
			Name:   "ScoreEvent",
			Fields: []*ast.FieldDecl{{Name: "score", Type: &ast.TypeRef{Name: "Int"}}},
		}},
		APIs: []*ast.ApiDecl{{
			Name:       "watchLiveScore",
			ReturnType: &ast.TypeRef{Name: "ScoreEvent"},
			Directives: []*ast.Directive{{Name: "stream"}, {Name: "native"}},
		}},
	}}}

	embedded := string(generateEntryFile(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	if !strings.Contains(embedded, "score_luxo.RegisterStreams(gw.Router, eventBus, scoreApp.Resolver)") {
		t.Fatalf("embedded native stream resolver was not injected:\n%s", embedded)
	}
	module := string(generateModuleEntryFiles(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})["score"])
	if !strings.Contains(module, "score_luxo.RegisterStreams(gw.Router, eventBus, app.Resolver)") {
		t.Fatalf("service native stream resolver was not injected:\n%s", module)
	}
	if !strings.Contains(module, "eventBus := event.NewChanBus(256)") {
		t.Fatalf("native stream service did not initialize its event bus:\n%s", module)
	}
	if !strings.Contains(module, "score_luxo.RegisterSchema(gw.Router.Schema)") {
		t.Fatalf("standalone module did not register its runtime schema:\n%s", module)
	}
	gateway := string(generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}))
	for _, want := range []string{
		"score_luxo.RegisterStreams(gw.Router, eventBus, nil)",
		`gw.Router.HandleStreamForwarded("watchLiveScore", rpcStreamProxy(rpcClients["score"]`,
		"client.SubscribeContext(ctx, luvia.BearerToken(ctx)",
		"req.BinaryParams()",
		"data, err = router.StreamPayloadJSON(apiName, data)",
		"return subscription.Err()",
	} {
		if !strings.Contains(gateway, want) {
			t.Fatalf("gateway native stream bridge missing %q:\n%s", want, gateway)
		}
	}
	if strings.Contains(gateway, "case <-subscription.Errors():") {
		t.Fatal("terminal errors must not skip buffered stream messages")
	}
}

func TestGeneratedGatewayStreamErrorInterfaceCompiles(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import (
    "context"
    "fmt"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/rpc"
    "github.com/light-speak/luxo/pkg/lux/luvia"
)
`)
	writeGatewayRPCStreamProxy(&source, []moduleInfo{{nativeStreams: []string{"watch"}}})
	runGeneratedProtocolPackage(t, source.String(), "package generated\n")
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
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

	code := generateGatewayEntry(result, "myapp", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	src := string(code)

	// getUser should appear exactly once in routing
	count := strings.Count(src, `"getUser"`)
	if count != 1 {
		t.Errorf("getUser should appear exactly once (dedup), appeared %d times:\n%s", count, src)
	}
}
