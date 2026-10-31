package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/parser"
)

func enterCacheGenerationScratch(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "cache-module-generation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	t.Chdir(directory)
}

func TestInitPinsCoreAndDefaultBackend(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := runInit(nil, []string{"example"}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("example/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"github.com/light-speak/luxo", "github.com/light-speak/luxo/pkg/lux/pg"} {
		if !strings.Contains(string(manifest), module+" v"+Version+"\n") {
			t.Fatalf("CLI generated mismatched dependency: %s", manifest)
		}
	}
	settings, err := os.ReadFile("example/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "LUXO_EVENT_BACKEND=none") {
		t.Fatal("missing explicit build selection")
	}
	if err := runInit(nil, []string{"example"}); err == nil {
		t.Fatal("existing project overwritten")
	}
}

func TestCRUDLockAndGeneratedSchemaUseTheSameNames(t *testing.T) {
	enterCacheGenerationScratch(t)
	file := &ast.File{Name: "origin/main/model.luxo"}
	for _, name := range []string{"TimeEntry", "Address", "Box", "Quiz", "Gateway"} {
		file.Models = append(file.Models, &ast.ModelDecl{
			Name: name, Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}},
		})
	}
	files := []*ast.File{file}
	result, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(result, generator); err != nil {
		t.Fatal(err)
	}
	raw, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var published schema.Schema
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	if err := published.Prepare(); err != nil {
		t.Fatal(err)
	}
	for name, definition := range published.APIs {
		if locked := lf.APIs[name]; locked == nil || locked.ID != definition.ID {
			t.Fatalf("exported API %s differs from its locked ID", name)
		}
	}
}

func TestCachePolicyReadFailure(t *testing.T) {
	if _, err := loadCachePolicies("."); err == nil {
		t.Fatal("unreadable policy file accepted")
	}
}

func parseGenerationSource(t *testing.T, source, path string) *ast.File {
	t.Helper()
	tokens, lexErrors := lexer.New(source, path).Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse(path)
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	return file
}

func TestModuleGenerationPreservesInheritedFields(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	var files []*ast.File
	for name, source := range map[string]string{
		"common/base": "model Base @noTime { id: Int @id }",
		"item/model":  "model Item : Base @noTime { name: String } api getItemById(key: Int): Item",
	} {
		path := "origin/" + name + ".luxo"
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("service/item/luxo/db.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `case "id":`) {
		t.Fatal("module generation lost the inherited primary key")
	}
	command := exec.Command("go", "test", "./service/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated inherited modules do not compile: %v\n%s", err, output)
	}
}

func TestModuleGenerationPreservesExternalStoredFields(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	var files []*ast.File
	for name, source := range map[string]string{
		"common/types": "enum Role { USER ADMIN }",
		"user/model":   "model User @noTime { id: Int @id role: Role optionalRole: Role? tags: [String]? }",
		"post/model":   "extend User { role: Role optionalRole: Role? tags: [String]? } model Post @noTime { id: Int @id }",
		"user/stream":  "api watchRole(): Role @stream @native",
		"post/events":  "event RoleChanged(role: Role) api roleUpdates(): Role @stream(RoleChanged) { it.role == Role.ADMIN }",
	} {
		path := "origin/" + name + ".luxo"
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile(filepath.Join("internal", "luxotypes", "enum.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shared), "type Role string") {
		t.Fatal("missing canonical enum definition")
	}
	common, err := os.ReadFile(filepath.Join("service", "common", "luxo", "model.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(common), "type Role = luxotypes.Role") {
		t.Fatal("declaring module must use the canonical enum alias")
	}
	raw, err := os.ReadFile(filepath.Join("service", "post", "luxo", "db.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "optional_role", "tags"} {
		if !strings.Contains(string(raw), `case "`+field+`":`) {
			t.Fatalf("formal module generation dropped stored field %s", field)
		}
	}
	writeSharedEnumAssertions(t)
	command := exec.Command("go", "test", "-race", "./service/...", "./internal/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated modules do not compile: %v\n%s", err, output)
	}
}

func TestSharedEnumCleanupPreservesHandwrittenFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	directory := filepath.Join("internal", "luxotypes")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(directory, "enum.gen.go")
	helper := filepath.Join(directory, "helper.go")
	for path, source := range map[string]string{
		generated: "// Code generated by luxo. DO NOT EDIT.\npackage luxotypes\n",
		helper:    "package luxotypes\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("stale shared enums remain: %v", err)
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("package luxotypes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatal("unmarked file was removed", err)
	}
}

func TestSharedEnumCleanupRejectsLinkedDirectory(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("target", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", "internal"); err != nil {
		t.Fatal(err)
	}
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err == nil {
		t.Fatal("shared enum cleanup accepted a linked directory")
	}
}

func TestSharedEnumGenerationRefusesUnownedTargets(t *testing.T) {
	for _, kind := range []string{"handwritten", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			directory := filepath.Join("internal", "luxotypes")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "enum.gen.go")
			original := []byte("package luxotypes\n// handwritten\n")
			if err := os.WriteFile("original.go", original, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "handwritten":
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join("..", "..", "original.go"), path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			generator, err := codegen.NewGenerator(codegen.GeneratorConfig{Events: &codegen.EventContext{
				SharedEnumPackage: "example.com/project/internal/luxotypes",
				Enums:             map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER"}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := generateSharedEnums(generator); err == nil {
				t.Fatal("unowned output was accepted")
			}
			readPath := "original.go"
			if kind == "handwritten" {
				readPath = path
			}
			got, err := os.ReadFile(readPath)
			if err != nil || string(got) != string(original) {
				t.Fatalf("original changed: %q, %v", got, err)
			}
		})
	}
}

func writeSharedEnumAssertions(t *testing.T) {
	t.Helper()
	source := `package luxo_test
import (
 "testing"
 "slices"
 "example.com/generated/internal/luxotypes"
 common "example.com/generated/service/common/luxo"
 user "example.com/generated/service/user/luxo"
 post "example.com/generated/service/post/luxo"
 "github.com/light-speak/luxo/pkg/lux/schema"
)
func TestSharedEnums(t *testing.T) {
 var canonical luxotypes.Role = user.RoleUSER
 var projected post.Role = canonical
 var declared common.Role = projected
 if declared != common.RoleUSER {t.Fatal("alias identity changed")}
 for _, register:=range []func(*schema.Schema)error{common.RegisterSchema,user.RegisterSchema,post.RegisterSchema} {
  s:=schema.New();if err:=register(s);err!=nil {t.Fatal(err)}
  role:=s.Enums["Role"]
  if role==nil||role.Module!="common"||!slices.Equal(role.Values,[]string{"USER","ADMIN"}) {t.Fatalf("enum metadata: %#v",role)}
 }
}
`
	if err := os.WriteFile(filepath.Join("service", "post", "luxo", "shared_enum_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func configureGeneratedModuleWorkspace(t *testing.T) {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Dir(filepath.Dir(directory))
	for name, content := range map[string]string{
		"go.mod":  "module example.com/generated\n\ngo 1.27.1\n",
		"go.work": "go 1.27.1\n\nuse (\n .\n " + strconv.Quote(core) + "\n " + strconv.Quote(filepath.Join(core, "pkg/lux/pg")) + "\n)\nreplace github.com/light-speak/luxo v1.0.0-beta.1 => " + strconv.Quote(core) + "\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(directory, "go.work"))
}

func TestModuleGenerationInternalOnlyAndDataOnlyPackages(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	id := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}
	name := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	files := []*ast.File{
		{Name: "origin/common/types.luxo", Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER", "ADMIN"}}}},
		{Name: "origin/user/model.luxo", Models: []*ast.ModelDecl{{Name: "User", Fields: []*ast.FieldDecl{id, name}}}},
		{Name: "origin/post/model.luxo", Models: []*ast.ModelDecl{{Name: "Post", Fields: []*ast.FieldDecl{id}}}, Extends: []*ast.ExtendDecl{{Name: "User", Fields: []*ast.FieldDecl{name}}}},
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("service/common/resolver/resolver.go"); !os.IsNotExist(err) {
		t.Fatalf("data-only module generated a resolver: %v", err)
	}
	writeInternalSelectorFixture(t)
	writeInternalRouteFixture(t)
	command := exec.Command("go", "test", "-race", "./service/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated internal/data modules do not compile: %v\n%s", err, output)
	}
}

func TestFullGenerationInternalOnlyModules(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	if err := os.MkdirAll("lucis/common", 0755); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"main.gen.go": "// Code generated by luxo. DO NOT EDIT.\npackage main\nfunc main() { removedService() }\n",
		"helper.go":   "package main\n// Handwritten code must survive regeneration.\n",
	} {
		if err := os.WriteFile(filepath.Join("lucis/common", name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, source := range map[string]string{
		"common/types": "enum Role { USER ADMIN }",
		"user/model":   "model User @noTime { id: Int @id name: String }",
		"post/model":   "extend User { name: String } model Post @noTime { id: Int @id }",
	} {
		path := filepath.Join("origin", name+".luxo")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("lucis/common/main.gen.go"); !os.IsNotExist(err) {
		t.Fatalf("stale generated process remains: %v", err)
	}
	if _, err := os.Stat("lucis/common/helper.go"); err != nil {
		t.Fatalf("handwritten helper removed: %v", err)
	}
	writeInternalSelectorFixture(t)
	writeInternalRouteFixture(t)
	command := exec.Command("go", "test", "-race", "./...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("complete generated project does not compile: %v\n%s", err, output)
	}
}

func writeInternalSelectorFixture(t *testing.T) {
	t.Helper()
	const source = `package luxo
import (
 "testing"
 "github.com/light-speak/luxo/pkg/lux/selection"
)
func TestInternalProjection(t *testing.T) {
 fields := []*selection.Field{{Name:"name"}}
 got := selectUserSQLColumns(fields)
 if len(got)!=2 || got[0]!="name" || got[1]!="id" { t.Fatalf("projection = %v",got) }
 if len(fields)!=1 || fields[0].Name!="name" { t.Fatal("mutated selection") }
}
`
	if err := os.WriteFile("service/post/luxo/selection_test.go", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeInternalRouteFixture(t *testing.T) {
	t.Helper()
	const source = `package luxo
import (
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
)
func TestModelOnlyInternalRouting(t *testing.T) {
 router := api.NewRouter()
 if err := RegisterHandlers(router, &App{}); err != nil { t.Fatal(err) }
 if err := RegisterBatchLoaders(router, &App{}); err != nil { t.Fatal(err) }
 routes := router.ExportHandlers()
 if len(routes)!=1 || routes["svc:batchLoad:User"]==nil { t.Fatalf("RPC routes = %v",routes) }
 for _,name := range []string{"svc:batchLoad:User","createUser","getUser","listUsers"} {
  response := httptest.NewRecorder()
  router.ServeHTTP(response, httptest.NewRequest("POST","/luvia",strings.NewReader("{\"$api\":\""+name+"\"}")))
  if response.Code!=404 { t.Fatalf("public route %s: %d %s",name,response.Code,response.Body.String()) }
 }
}
`
	if err := os.WriteFile("service/user/luxo/routing_test.go", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeFilesLoadsExplicitCachePolicies(t *testing.T) {
	enterCacheGenerationScratch(t)
	path := "origin/main/api.luxo"
	tokens, lexErrors := lexer.New(`api cached(): Int @cache(policy: "preview") { return 1 }`, path).Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse(path)
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	if _, err := analyzeFiles([]*ast.File{file}); err == nil {
		t.Fatal("missing policy file guessed a contract")
	}
	for _, raw := range []string{`{}`, `{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1s"}},"backend":"redis"}`} {
		if err := os.WriteFile("luxo.cache.json", []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := analyzeFiles([]*ast.File{file}); err == nil {
			t.Fatal("invalid policy file ignored")
		}
	}
	if err := os.WriteFile("luxo.cache.json", []byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1s"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	analyzed, err := analyzeFiles([]*ast.File{file})
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("service/main/luxo/schema.gen.go")
	if err != nil || !strings.Contains(string(metadata), `CachePolicy = &schema.CachePolicy{Mode: "ttl", TTL: 1000000000, Scope: "private"}`) {
		t.Fatalf("module lost compiled contract: %s %v", metadata, err)
	}
}

func TestGenerateModulesPreservesCacheDependencyAnalysis(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("luxo.cache.json", []byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1m"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range []string{"first", "second"} {
		path := "origin/" + name + "/api.luxo"
		source := "api " + name + `(): Int @native @cache(policy: "preview") @cacheable(dependsOn: ["pricing/rates"])`
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		source, err := os.ReadFile(filepath.Join("service", name, "luxo", "handler.gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), `router.RequireCache("`+name+`")`) {
			t.Fatalf("module %s lost its cached API", name)
		}
		metadata, err := os.ReadFile(filepath.Join("service", name, "luxo", "schema.gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(metadata), `s.APIs["`+name+`"].CacheDependencies`) || !strings.Contains(string(metadata), `"pricing/rates"`) {
			t.Fatalf("module %s lost execution sources: %s", name, metadata)
		}
	}
}

func TestCallFieldMaskUsesStableSchemaIDs(t *testing.T) {
	runtimeSchema := schema.New()
	runtimeSchema.RegisterModel(&schema.Model{Name: "Payload", Fields: []schema.Field{
		{ID: 1, Name: "id", Type: schema.FieldInt},
		{ID: 12, Name: "metadata", Type: schema.FieldJSON},
	}})
	apiSchema := &schema.API{Name: "getPayload", ReturnType: "Payload"}

	mask, err := callFieldMask("metadata,id", apiSchema, runtimeSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(codec.SelectionMaskFields(mask), []byte{0x01, 0x08}) {
		t.Fatalf("mask = %x", mask)
	}
	if _, err := callFieldMask("missing", apiSchema, runtimeSchema); err == nil {
		t.Fatal("unknown field should fail")
	}
}

func TestLoadCallSchemaPreparesNestedTypes(t *testing.T) {
	tempRoot := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(tempRoot, "call-schema-*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { file.Close(); os.Remove(path) })
	data := `{"models":{},"apis":{"read":{"id":1,"name":"read","returnType":"Parent"}},"types":{
		"Parent":{"name":"Parent","fields":[{"id":1,"name":"child","type":"Model","typeName":"Child","relation":true}]},
		"Child":{"name":"Child","fields":[{"id":3,"name":"value","type":"Int"}]}}}`
	if _, err := file.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := loadCallSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := callFieldMask("child { value }", s.APIs["read"], s)
	if err != nil || len(mask) == 0 {
		t.Fatalf("decoded nested selection: %x / %v", mask, err)
	}
	child := s.LookupModel("Child")
	if child == nil || child != s.LookupModel("Child") || string(child.FieldByID(3).JSONPrefix) != `"value":` {
		t.Fatal("CLI did not prepare type metadata")
	}
	if _, err := loadCallSchema(path + ".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := os.WriteFile(path, []byte(`{"apis":{"bad":{"id":0,"name":"bad"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCallSchema(path); err == nil {
		t.Fatal("invalid schema IDs accepted")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCallSchema(path); err == nil {
		t.Fatal("malformed schema accepted")
	}
}

func TestParseBinaryCLIValueUsesSchemaType(t *testing.T) {
	value, err := parseBinaryCLIValue(`["AQI=",""]`, schema.Param{Type: schema.FieldBytes, IsList: true})
	if err != nil {
		t.Fatal(err)
	}
	items := value.([]any)
	if !bytes.Equal(items[0].([]byte), []byte{1, 2}) || len(items[1].([]byte)) != 0 {
		t.Fatalf("bytes = %#v", items)
	}
	jsonValue, err := parseBinaryCLIValue(`{"ok":true}`, schema.Param{Type: schema.FieldJSON})
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonValue.(json.RawMessage)) != `{"ok":true}` {
		t.Fatalf("JSON = %#v", jsonValue)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc", 3},
		{"a\nb\n", 2},
		{"single", 1},
		{"", 0},
		{"\n\n", 2},
	}
	for _, tt := range tests {
		lines := splitLines([]byte(tt.input))
		if len(lines) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(lines), tt.want)
		}
	}
}

func TestSplitLinesContent(t *testing.T) {
	lines := splitLines([]byte("hello\nworld"))
	if string(lines[0]) != "hello" || string(lines[1]) != "world" {
		t.Errorf("got %q, %q", string(lines[0]), string(lines[1]))
	}
}

func TestCleanStaleGenFiles(t *testing.T) {
	dir := t.TempDir()
	// Stale generated file — its declaration was removed from the origin,
	// so this run's Files map no longer contains it.
	stale := filepath.Join(dir, "error.gen.go")
	if err := os.WriteFile(stale, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Freshly written generated file — must be kept.
	kept := filepath.Join(dir, "model.gen.go")
	if err := os.WriteFile(kept, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Handwritten file — never touched, even without .gen. suffix match.
	hand := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(hand, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	removed, err := cleanStaleGenFiles(dir, map[string][]byte{"model.gen.go": nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "error.gen.go" {
		t.Errorf("removed = %v, want [error.gen.go]", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale error.gen.go should be deleted")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("freshly written model.gen.go must be kept")
	}
	if _, err := os.Stat(hand); err != nil {
		t.Error("handwritten helper.go must never be touched")
	}
}

func TestEntryCleanupPreservesUnownedFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := cleanStaleEntryFiles(nil, false); err != nil {
		t.Fatal(err)
	}
	const marked = "// Code generated by luxo. DO NOT EDIT.\npackage main\n"
	for name, content := range map[string]string{
		"old": marked, "active": marked, "app": marked, "gateway": marked,
		"manual": "package main\n", "other": "// Code generated by another tool. DO NOT EDIT.\npackage main\n",
	} {
		if err := os.MkdirAll(filepath.Join("lucis", name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("lucis", name, "main.gen.go"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("active", "lucis/linked"); err != nil {
		t.Fatal(err)
	}
	if err := cleanStaleEntryFiles(map[string][]byte{"active": nil}, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"active", "app", "gateway", "manual", "other", "linked"} {
		if _, err := os.Stat(filepath.Join("lucis", name, "main.gen.go")); err != nil {
			t.Fatalf("entry %s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat("lucis/old/main.gen.go"); !os.IsNotExist(err) {
		t.Fatalf("stale entry remains: %v", err)
	}
	if err := cleanStaleEntryFiles(nil, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"active", "app", "gateway"} {
		if _, err := os.Stat(filepath.Join("lucis", name, "main.gen.go")); !os.IsNotExist(err) {
			t.Fatalf("data-only project kept process %s: %v", name, err)
		}
	}
}

func TestMarkedEntryCleanupIgnoresNonRegularFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("directory", 0755); err != nil {
		t.Fatal(err)
	}
	const marker = "// Code generated by luxo. DO NOT EDIT.\n"
	if err := os.WriteFile("target", []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", "linked"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"missing", "directory", "linked"} {
		if removed, err := removeMarkedEntry(path); removed || err != nil {
			t.Fatalf("cleanup %s = %v, %v", path, removed, err)
		}
	}
	if raw, err := os.ReadFile("target"); err != nil || string(raw) != marker {
		t.Fatalf("link target changed: %q, %v", raw, err)
	}
	if removed, err := removeMarkedEntry("target/child"); removed || err == nil {
		t.Fatalf("invalid parent error lost: %v, %v", removed, err)
	}
}

func TestEntryCleanupRejectsLinkedRoot(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("outside", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("outside", "lucis"); err != nil {
		t.Fatal(err)
	}
	if err := cleanStaleEntryFiles(nil, false); err == nil {
		t.Fatal("cleanup followed a linked root")
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("module github.com/test/myapp\n\ngo 1.27.1\n"), 0644)

	path, err := readModulePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "github.com/test/myapp" {
		t.Errorf("got %q", path)
	}
}

func TestReadModulePathNotFound(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No go.mod
	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when go.mod missing")
	}
}

func TestReadModulePathNoDirective(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	os.WriteFile("go.mod", []byte("go 1.27.1\n"), 0644)

	_, err := readModulePath()
	if err == nil {
		t.Error("expected error when module directive missing")
	}
}

func TestLoadDialect(t *testing.T) {
	d, err := loadDialect()
	if err != nil || d == nil {
		t.Error("default dialect should not be nil")
	}
}

func TestLoadDialectMySQL(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "mysql")
	d, err := loadDialect()
	if err == nil || d != nil {
		t.Fatalf("unsupported mysql dialect = %T, %v; want explicit error", d, err)
	}
}

func TestValidateWireCompatibilityRequiresExplicitOverride(t *testing.T) {
	lf := lockfile.New()
	base := []*ast.File{{Models: []*ast.ModelDecl{{
		Name:   "User",
		Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
	}}}}
	if err := lf.Update(base); err != nil {
		t.Fatal(err)
	}
	removed := []*ast.File{{}}

	if err := validateWireCompatibility(lf, removed, false); err == nil || !strings.Contains(err.Error(), "--allow-breaking") {
		t.Fatalf("breaking change must require explicit override: %v", err)
	}
	if err := validateWireCompatibility(lf, removed, true); err != nil {
		t.Fatalf("explicit override rejected: %v", err)
	}
}

func TestLockUpdateFailurePreservesPersistedFile(t *testing.T) {
	enterCacheGenerationScratch(t)
	lf := lockfile.New()
	lf.Models["User"] = &lockfile.ModelLock{NextID: 65536, Fields: map[string]int{}}
	if err := lf.Save("luxo.lock"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile("luxo.lock")
	if err != nil {
		t.Fatal(err)
	}
	input := []*ast.File{{Models: []*ast.ModelDecl{{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}}}}
	got, err := updateLockFileAndReturn(input)
	if err == nil || got != nil || !strings.Contains(err.Error(), "update luxo.lock") {
		t.Fatalf("exhausted lock update succeeded: %v", err)
	}
	after, err := os.ReadFile("luxo.lock")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed update replaced file: %v", err)
	}
}

func TestLockReadFailurePreservesPersistedFile(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":2}`,
		`{"apis":{"read":{"id":1,"id":2}}}`,
		`{"apis":{"read":{"id":1,"contrcat":true}}}`,
		`{"models":{"\ud800":{"next_id":1,"fields":{}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.WriteFile("luxo.lock", []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := updateLockFileAndReturn([]*ast.File{{}})
			if err == nil || got != nil {
				t.Fatalf("invalid metadata reached update: %#v %v", got, err)
			}
			after, err := os.ReadFile("luxo.lock")
			if err != nil || string(after) != raw {
				t.Fatalf("failed read replaced file: %v", err)
			}
		})
	}
}

func TestLoadGenerationEnvironment(t *testing.T) {
	tempRoot := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(tempRoot, "gen-env-*.env")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { os.Remove(path) })
	if _, err := file.WriteString("LUXO_TEST_GENERATION_ENV=loaded\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	previous, existed := os.LookupEnv("LUXO_TEST_GENERATION_ENV")
	os.Unsetenv("LUXO_TEST_GENERATION_ENV")
	t.Cleanup(func() {
		if existed {
			os.Setenv("LUXO_TEST_GENERATION_ENV", previous)
		} else {
			os.Unsetenv("LUXO_TEST_GENERATION_ENV")
		}
	})

	if err := loadGenerationEnvironment(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("LUXO_TEST_GENERATION_ENV"); got != "loaded" {
		t.Fatalf("loaded environment = %q, want loaded", got)
	}
	if err := loadGenerationEnvironment(filepath.Join(tempRoot, "missing.env")); err != nil {
		t.Fatalf("missing optional environment file = %v", err)
	}
}

func TestNextMigrationSeq(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No migrations dir
	seq := nextMigrationSeq()
	if seq != 1 {
		t.Errorf("got %d, want 1", seq)
	}

	// Create some migration files
	os.Mkdir("migrations", 0755)
	os.WriteFile(filepath.Join("migrations", "001_init.sql"), []byte("--"), 0644)
	os.WriteFile(filepath.Join("migrations", "002_add_user.sql"), []byte("--"), 0644)

	seq = nextMigrationSeq()
	if seq != 3 {
		t.Errorf("got %d, want 3", seq)
	}
}

func TestPrintDiffOps(t *testing.T) {
	// Just ensure it doesn't panic for all op types
	ops := []codegen.DiffOp{
		{Kind: codegen.CreateTable, Table: "users"},
		{Kind: codegen.DropTable, Table: "old_table"},
		{Kind: codegen.AddColumn, Table: "users", Column: "email"},
		{Kind: codegen.DropColumn, Table: "users", Column: "old_col"},
		{Kind: codegen.RenameColumn, Table: "users", Column: "new_name", OldName: "old_name"},
		{Kind: codegen.AlterColumn, Table: "users", Column: "age"},
		{Kind: codegen.AddIndex, Index: &lux.IndexInfo{Name: "idx_email"}},
		{Kind: codegen.DropIndex, Index: &lux.IndexInfo{Name: "idx_old"}},
		{Kind: codegen.AddColumn, Table: "users", Column: "x", Warning: "breaking change"},
	}
	// Redirect stdout to discard
	old := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = old }()

	printDiffOps(ops)
}
