package semantic

import (
	"cmp"
	"slices"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/ast"
)

func TestCacheDependenciesIncludeCompiledReads(t *testing.T) {
	for _, test := range []struct{ name, declarations, body string }{
		{"count", "", `return User.count()`},
		{"function count", "", `return count(User)`},
		{"aggregate alias", `fn total(): Int {
val query = User.where(score > 0)
return query.sum(score)
}`, `return total()`},
		{"recursive helper", `fn first(): Int { if false { return second() }
return User.count() }
fn second(): Int { return first() }`, `return second()`},
		{"relation", "", `val user = User.find(id: 1)
return user.team.id`},
	} {
		t.Run(test.name, func(t *testing.T) {
			returnType := "Int"
			source := `model Team { id: Int @id }
model User { id: Int @id
score: Int
teamId: Int
team: Team @by(teamId) }
` + test.declarations + "\napi cached(): " + returnType + " @cache(policy: \"preview\") {\n" + test.body + "\n}"
			result := analyzeCached(t, source)
			expectNoErrors(t, result)
			want := []CacheDependency{{Kind: CacheModelDependency, Name: "User"}}
			if test.name == "relation" {
				want = append([]CacheDependency{{Kind: CacheModelDependency, Name: "Team"}}, want...)
			}
			if got := result.ExecutionCacheDependencies[result.Files[0].APIs[0]]; !slices.Equal(got, want) {
				t.Fatalf("compiled read dependencies: got %v, want %v", got, want)
			}
		})
	}
}

func TestCacheDependenciesIncludeTypedGroups(t *testing.T) {
	for _, query := range []string{"User", "User.where(score > 0)"} {
		result := analyzeCached(t, `model User { id: Int @id
score: Int }
type Summary { score: Int
total: Int }
api cached(): [Summary] @cache(policy: "preview") {
 return `+query+`.groupBy { it.score }.select { Summary { score: it.key, total: it.count() } }
}`)
		expectNoErrors(t, result)
		want := []CacheDependency{{Kind: CacheModelDependency, Name: "User"}}
		if got := result.ExecutionCacheDependencies[result.Files[0].APIs[0]]; !slices.Equal(got, want) {
			t.Fatalf("group input %s lost its read dependency: %v", query, got)
		}
	}
}

func TestCacheDependenciesIncludeInferredReads(t *testing.T) {
	result := analyzeCached(t, `model User { id: Int @id }
api countUsers @cache(policy: "preview")
api existsUserById @cache(policy: "preview")
api listTop10Users @cache(policy: "preview")
api getUser @cache(policy: "preview")
api pure(): Int @cache(policy: "preview") { return 1 }`)
	expectNoErrors(t, result)
	for _, api := range result.Files[0].APIs[:4] {
		want := []CacheDependency{{Kind: CacheModelDependency, Name: "User"}}
		if got := result.ExecutionCacheDependencies[api]; !slices.Equal(got, want) {
			t.Fatalf("%s: got %v, want %v", api.Name, got, want)
		}
	}
}

func TestCacheDependenciesMergeNativeAndModuleReads(t *testing.T) {
	owner := parseFileWithName(t, `model User { id: Int @id }`, "origin/users/model.luxo")
	reader := parseFileWithName(t, `extend User { id: Int @id }
model Audit { id: Int @id }
fn rates(): Result<Int> @native @cacheable(dependsOn: [Audit, "rates"])
fn helper(): Int { return User.load(1).id + rates()? }
api cached(): Int @cache(policy: "preview") { return helper() + Audit.count() }`, "origin/reports/api.luxo")
	want := []CacheDependency{
		{Kind: CacheModelDependency, Name: "Audit", Module: "reports"},
		{Kind: CacheModelDependency, Name: "User", Module: "users"},
		{Kind: CacheExternalDependency, Name: "rates"},
	}
	for _, files := range [][]*ast.File{{owner, reader}, {reader, owner}} {
		result := analyzeCachedModules(t, files)
		expectNoErrors(t, result)
		if got := result.ExecutionCacheDependencies[reader.APIs[0]]; !slices.Equal(got, want) {
			t.Fatalf("module read dependencies: got %v, want %v", got, want)
		}
	}
}

func TestCacheDependenciesDoNotGuessFromValueTypes(t *testing.T) {
	result := analyzeCached(t, `model User { id: Int @id }
api cached(user: User): Int @cache(policy: "preview") { return user.id }
api native(): User @native @cache(policy: "preview") @cacheable(dependsOn: [])
api query(): Int { return User.count() }`)
	expectNoErrors(t, result)
	for _, api := range result.Files[0].APIs[:2] {
		if deps, ok := result.ExecutionCacheDependencies[api]; !ok || len(deps) != 0 {
			t.Fatalf("value type incorrectly became a database read: %v", deps)
		}
	}
	if _, ok := result.ExecutionCacheDependencies[result.Files[0].APIs[2]]; ok {
		t.Fatal("uncached query acquired cache metadata")
	}
}

func TestCacheReadRecordingRequiresPersistentModel(t *testing.T) {
	analyzer := New()
	analyzer.cacheModelReads = make(map[ast.Expr]CacheDependency)
	for _, typ := range []*ResolvedType{nil, analyzer.types["Int"], analyzer.types["Identity"], {Kind: TypeModel, Name: "Missing"}} {
		analyzer.recordCacheModelRead(&ast.Ident{}, typ)
	}
	if len(analyzer.cacheModelReads) != 0 {
		t.Fatal("non-persistent type acquired a model dependency")
	}
}

func TestInvalidTypesSkipCacheMetadataButKeepBodyDiagnostics(t *testing.T) {
	file := parseFileWithName(t, `model User { id: Int @id }
type Broken { value: MissingType }
api cached(): Int @cache(policy: "preview") { return User.count() }
api broken(): Int { return missingValue }`, "invalid.luxo")
	analyzer := New()
	analyzer.cachePolicies = testCachePolicies(t, "1m")
	result := analyzer.Analyze([]*ast.File{file})
	expectError(t, result, "MissingType")
	expectError(t, result, "missingValue")
	if analyzer.cacheModelReads != nil || result.ExecutionCacheDependencies != nil {
		t.Fatal("invalid compilation retained cache metadata that cannot be published")
	}
}

func TestValidTypesStillCollectCacheReads(t *testing.T) {
	file := parseFileWithName(t, `model User { id: Int @id }
api cached(): Int @cache(policy: "preview") { return User.count() }`, "valid.luxo")
	analyzer := New()
	analyzer.cachePolicies = testCachePolicies(t, "1m")
	result := analyzer.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
	want := []CacheDependency{{Kind: CacheModelDependency, Name: "User"}}
	if len(analyzer.cacheModelReads) == 0 || !slices.Equal(result.ExecutionCacheDependencies[file.APIs[0]], want) {
		t.Fatal("valid compilation lost its verified model dependency")
	}
}

func TestSkippingDiscardedCacheMetadataPreservesHistoricalDiagnostics(t *testing.T) {
	var results []*Result
	for _, forceRecording := range []bool{false, true} {
		file := parseFileWithName(t, testutil.CompilerDemoV1(), "historical.luxo")
		files := []*ast.File{file}
		analyzer := New()
		analyzer.files = files
		analyzer.runDeclarationPass(files)
		analyzer.runTypePass(files)
		if len(analyzer.errors) == 0 {
			t.Fatal("fixture must already be invalid before checking bodies")
		}
		if forceRecording {
			analyzer.cacheModelReads = make(map[ast.Expr]CacheDependency)
		}
		analyzer.runBodyPass(files)
		analyzer.runPostAnalysisPass(files)
		if (len(analyzer.cacheModelReads) != 0) != forceRecording {
			t.Fatal("reference must record reads while the optimized path skips them")
		}
		slices.SortFunc(analyzer.errors, func(a, b Error) int {
			return cmp.Or(cmp.Compare(a.Pos.Offset, b.Pos.Offset), cmp.Compare(a.Message, b.Message))
		})
		slices.SortFunc(analyzer.warnings, func(a, b Warning) int {
			return cmp.Or(cmp.Compare(a.Pos.Offset, b.Pos.Offset), cmp.Compare(a.Message, b.Message))
		})
		results = append(results, &Result{Errors: analyzer.errors, Warnings: analyzer.warnings})
	}
	if !slices.Equal(results[0].Errors, results[1].Errors) || !slices.Equal(results[0].Warnings, results[1].Warnings) {
		t.Fatal("skipping discarded metadata changed diagnostics")
	}
}

func TestExecutionCacheDependencyCollectionCanBeRepeated(t *testing.T) {
	first := CacheDependency{Kind: CacheModelDependency, Name: "User"}
	second := CacheDependency{Kind: CacheExternalDependency, Name: "rates"}
	api := &ast.ApiDecl{Name: "cached"}
	root := &cacheEffectNode{api: api, cached: true}
	helper := &cacheEffectNode{callers: []*cacheEffectNode{root}}
	source := &cacheEffectNode{dependencies: map[CacheDependency]bool{first: true, second: true},
		callers: []*cacheEffectNode{helper, root}}
	helper.callers = append(helper.callers, source) // A cycle and a diamond share the same sources.
	nodes := []*cacheEffectNode{source, helper, root}
	want := []CacheDependency{first, second}
	result := collectExecutionCacheDependencies(nodes)
	if !slices.Equal(result[api], want) || len(helper.dependencies) != 0 || len(root.dependencies) != 0 {
		t.Fatal("collection duplicated dependencies or rewrote the declaration sets")
	}
	result[api][0].Name = "changed"
	if next := collectExecutionCacheDependencies(nodes); !slices.Equal(next[api], want) {
		t.Fatalf("repeated collection retained visit state or aliased a result: %v", next[api])
	}
}

func TestExecutionCacheDependenciesPropagateThroughRecursiveCalls(t *testing.T) {
	result := analyzeCached(t, `model User { id: Int @id }
fn rates(): Result<Int> @native @cacheable(dependsOn: ["pricing/rates", User])
fn tax(): Result<Int> @native @cacheable(dependsOn: ["tax", User])
fn first(): Int { if false { return second() }
return rates()? }
fn second(): Int { return first() + tax()? }
api cached(): Int @cache(policy: "preview") { return second() + rates()? }
api pure(): Int @cache(policy: "preview") { return 1 }
api uncached(): Int { return second() }`)
	expectNoErrors(t, result)
	want := []CacheDependency{
		{Kind: CacheModelDependency, Name: "User"},
		{Kind: CacheExternalDependency, Name: "pricing/rates"},
		{Kind: CacheExternalDependency, Name: "tax"},
	}
	apis := result.Files[0].APIs
	if got := result.ExecutionCacheDependencies[apis[0]]; !slices.Equal(got, want) {
		t.Fatalf("native dependency closure: got %v, want %v", got, want)
	}
	if deps, ok := result.ExecutionCacheDependencies[apis[1]]; !ok || len(deps) != 0 {
		t.Fatal("missing verified empty native dependency set")
	}
	if _, ok := result.ExecutionCacheDependencies[apis[2]]; ok {
		t.Fatal("uncached API acquired cache requirements")
	}
}

func TestExecutionCacheDependenciesKeepModuleIdentity(t *testing.T) {
	common := parseFileWithName(t, `model Shared { id: Int @id }
fn source(): Result<Int> @native @cacheable(dependsOn: [Shared, "common/data"])`, "origin/common/source.luxo")
	local := parseFileWithName(t, `model Local { id: Int @id }
fn source(): Result<Int> @native @cacheable(dependsOn: [Local, "local/data"])
api read(): Int @cache(policy: "preview") { return source()? }`, "origin/local/read.luxo")
	other := parseFileWithName(t, `api read(): Int @cache(policy: "preview") { return source()? }`, "origin/other/read.luxo")
	for _, files := range [][]*ast.File{{common, local, other}, {other, local, common}} {
		result := analyzeCachedModules(t, files)
		expectNoErrors(t, result)
		for _, test := range []struct {
			api                     *ast.ApiDecl
			model, module, resource string
		}{{local.APIs[0], "Local", "local", "local/data"}, {other.APIs[0], "Shared", "common", "common/data"}} {
			want := []CacheDependency{{Kind: CacheModelDependency, Name: test.model, Module: test.module},
				{Kind: CacheExternalDependency, Name: test.resource}}
			if got := result.ExecutionCacheDependencies[test.api]; !slices.Equal(got, want) {
				t.Fatalf("module dependencies: got %v, want %v", got, want)
			}
		}
	}
}

func TestInvalidNativeCacheGraphDoesNotPublishDependencies(t *testing.T) {
	for _, source := range []string{
		`api cached(): Int @native @cache(policy: "preview")`,
		`api cached(): Int @native @cache(policy: "preview") @cacheable(dependsOn: [Missing])`,
	} {
		result := analyzeCached(t, source)
		if len(result.Errors) == 0 || result.ExecutionCacheDependencies != nil {
			t.Fatal("invalid graph published trusted cache dependencies")
		}
	}
}

func TestNativeCacheDependencyOrderAndSnapshot(t *testing.T) {
	common := parseFileWithName(t, `model Shared { id: Int @id }`, "origin/common/model.luxo")
	local := parseFileWithName(t, `model Local { id: Int @id }
api read(): Int @native @cache(policy: "preview") @cacheable(dependsOn: [Local, Shared, "Local"])`, "origin/local/read.luxo")
	files := []*ast.File{local, common}
	first := analyzeCachedModules(t, files)
	expectNoErrors(t, first)
	want := []CacheDependency{
		{Kind: CacheModelDependency, Name: "Shared", Module: "common"},
		{Kind: CacheModelDependency, Name: "Local", Module: "local"},
		{Kind: CacheExternalDependency, Name: "Local"},
	}
	api := local.APIs[0]
	if !slices.Equal(first.ExecutionCacheDependencies[api], want) {
		t.Fatalf("unstable dependency order: %v", first.ExecutionCacheDependencies[api])
	}
	api.Directives[len(api.Directives)-1].Args[0].Value = &ast.ListExpr{}
	second := analyzeCachedModules(t, files)
	expectNoErrors(t, second)
	if len(second.ExecutionCacheDependencies[api]) != 0 || !slices.Equal(first.ExecutionCacheDependencies[api], want) {
		t.Fatal("reanalysis retained stale dependencies or mutated the previous result")
	}
}
