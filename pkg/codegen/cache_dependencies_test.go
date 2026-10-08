package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedSchemaCarriesCacheExecutionDependencies(t *testing.T) {
	model := testModel("User", nil, []*ast.FieldDecl{testField("id", "Int")})
	cached, pure, plain := cachedTestAPI(true), cachedTestAPI(true), cachedTestAPI(true)
	cached.Directives = cached.Directives[1:]
	cached.Directives[2].Args[0].Value = &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "User"}, &ast.Literal{Kind: token.String, Value: "pricing/rates"}}}
	pure.Name, pure.Directives = "pure", pure.Directives[1:]
	plain.Name, plain.Directives = "plain", plain.Directives[2:]
	input := analyzeCachedFixture(t, []*ast.File{{Name: "origin/s/cache.luxo", Models: []*ast.ModelDecl{model}, APIs: []*ast.ApiDecl{cached, pure, plain}}}, true)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		ModelFields: map[string]map[string]int{"User": {"id": 1}},
		APIs:        map[string]int{"cachedNative": 1, "pure": 2, "plain": 3},
		APIParams:   map[string]map[string]int{"cachedNative": {"id": 1}, "pure": {"id": 1}, "plain": {"id": 1}},
	}})
	code := string(g.generateSchemaFile(input, "generated", nil))
	if !strings.Contains(code, `s.APIs["cachedNative"].CacheDependencies`) || !strings.Contains(code, `s.APIs["pure"].CacheDependencies`) || strings.Contains(code, `s.APIs["plain"].CacheDependencies`) {
		t.Fatalf("lost checked/unchecked distinction:\n%s", code)
	}
	exported, err := g.BuildSchemaJSON(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	runGeneratedProtocolPackage(t, code, cacheDependencySchemaAssertions+"\nconst exportedSchema = "+strconv.Quote(string(exported)))
}

func TestGenerationRejectsUnknownCacheDependencyKind(t *testing.T) {
	declaration := cachedTestAPI(true)
	declaration.Directives = declaration.Directives[1:]
	input := analyzeCachedFixture(t, []*ast.File{{APIs: []*ast.ApiDecl{declaration}}}, false)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	input.ExecutionCacheDependencies[declaration] = []semantic.CacheDependency{{Kind: semantic.CacheDependencyKind(255), Name: "unknown"}}
	generated, err := Generate(input, "generated", cachedGenerationConfig())
	if err == nil || generated != nil || !strings.Contains(err.Error(), "unknown cache dependency kind") {
		t.Fatalf("invalid dependency became empty execution: %v", err)
	}
}

func TestPublicGenerationRejectsDuplicateCachePolicy(t *testing.T) {
	declaration := cachedTestAPI(true)
	declaration.Directives = declaration.Directives[1:]
	input := analyzeCachedFixture(t, []*ast.File{{Name: "origin/task/api.luxo", APIs: []*ast.ApiDecl{declaration}}}, false)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	declaration.Directives = append(declaration.Directives, declaration.Directives[0])
	assertRejected := func(err error, empty bool) {
		t.Helper()
		if err == nil || !empty || !strings.Contains(err.Error(), "requires exactly one @cache policy reference") {
			t.Fatalf("duplicate cache policy generated source: %v", err)
		}
	}
	generated, err := Generate(input, "generated", cachedGenerationConfig())
	assertRejected(err, generated == nil)
	source, err := GenerateEntryFileChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	assertRejected(err, source == nil)
	entries, err := GenerateModuleEntryFilesChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	assertRejected(err, entries == nil)
	source, err = GenerateGatewayEntryChecked(input, "example.com/cache", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"})
	assertRejected(err, source == nil)
	declaration.Directives = declaration.Directives[:len(declaration.Directives)-1]
	if generated, err = Generate(input, "generated", cachedGenerationConfig()); err != nil || generated == nil {
		t.Fatalf("repaired cache contract was rejected: %v", err)
	}
}

const cacheDependencySchemaAssertions = `package generated
import (
  "strings"
  "testing"
  "github.com/light-speak/luxo/pkg/lux/api"
  "github.com/light-speak/luxo/pkg/lux/cache"
  "github.com/light-speak/luxo/pkg/lux/schema"
)
func TestGeneratedCacheMetadataStartup(t *testing.T) {
  binding, err := api.NewResponseCache(cache.Nop{}, api.CacheNamespace{Project:"p", Environment:"e", Service:"s", Revision:"r"},api.ResponseOriginOptions{MaxConcurrent:16})
  if err != nil { t.Fatal(err) }
  rt := api.NewRouterWithOptions(api.RouterOptions{ResponseCache:binding})
  RegisterSchema(rt.Schema)
  metadata := rt.Schema.APIs["cachedNative"].CacheDependencies
  if metadata == nil || len(metadata.Models) != 1 || metadata.Models[0] != (schema.ComputedSource{Model:"User", Module:"s"}) || len(metadata.External) != 1 || metadata.External[0] != "pricing/rates" { t.Fatalf("wrong metadata: %+v", metadata) }
  if rt.Schema.APIs["pure"].CacheDependencies == nil || rt.Schema.APIs["plain"].CacheDependencies != nil { t.Fatal("pure execution confused with missing analysis") }
  data, err := rt.Schema.ToJSON()
  if err != nil || strings.Contains(string(data), "CacheDependencies") || strings.Contains(string(data), "cacheDependencies") { t.Fatalf("server metadata entered introspection: %s/%v",data,err) }
  if string(data) != exportedSchema { t.Fatalf("server cache registration changed exported Schema: %s", data) }
  rt.RequireCache("pure")
  if err := rt.Validate(); err != nil { t.Fatal(err) }
  rt.RequireCache("cachedNative")
  if err := rt.Validate(); err != nil { t.Fatalf("TTL dependency incorrectly required invalidation infrastructure: %v", err) }
}
`
