package semantic

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
)

func testCachePolicies(t testing.TB, ttl string) *cachepolicy.Catalog {
	t.Helper()
	policies, err := cachepolicy.Parse([]byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"` + ttl + `"},"detail":{"mode":"ttl","ttl":"5m","scope":"private"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return policies
}

func analyzeCached(t *testing.T, source string) *Result {
	t.Helper()
	file := parseFileWithName(t, source, "test.luxo")
	return AnalyzeWithOptions([]*ast.File{file}, Options{CachePolicies: testCachePolicies(t, "1m")})
}

func analyzeCachedModules(t *testing.T, files []*ast.File) *Result {
	t.Helper()
	return AnalyzeWithOptions(files, Options{CachePolicies: testCachePolicies(t, "1m"), ModuleIsolation: true})
}

func TestNamedCachePolicyResolution(t *testing.T) {
	files := []*ast.File{parseFileWithName(t, `api preview(): Int @cache(policy: "preview") { return 1 }
api detail(): Int @cache(policy: "detail") { return 2 }
api uncached(): Int { return 3 }`, "origin/main/api.luxo")}
	result := AnalyzeWithOptions(files, Options{CachePolicies: testCachePolicies(t, "30s"), ModuleIsolation: true})
	expectNoErrors(t, result)
	if len(result.CachePolicies) != 2 {
		t.Fatalf("wrong compiled policy count: %d", len(result.CachePolicies))
	}
	for i, want := range []cachepolicy.Policy{
		{Mode: cachepolicy.TTL, TTL: 30 * time.Second, Scope: cachepolicy.Private},
		{Mode: cachepolicy.TTL, TTL: 5 * time.Minute, Scope: cachepolicy.Private},
	} {
		declaration := files[0].APIs[i]
		if got := result.CachePolicies[declaration.Directives[0]]; got != want {
			t.Fatalf("%s: %v want %v", declaration.Name, got, want)
		}
		if _, checked := result.ExecutionCacheDependencies[declaration]; !checked {
			t.Fatal("named cache skipped dependency analysis")
		}
	}
	if files[0].APIs[0].Directives[0].Args[0].Name != "policy" {
		t.Fatal("resolution mutated the source AST")
	}
}

func TestNamedCachePolicyRejectsInvalidReferences(t *testing.T) {
	for _, annotation := range []string{
		`@cache(policy: "missing")`, `@cache(policy: "")`, `@cache(policy: 1)`,
		`@cache(policy: null)`, `@cache(policy: preview)`, `@cache(policy: "preview", ttl: 1m)`,
		`@cache(policy: "preview", policy: "detail")`,
	} {
		file := parseFileWithName(t, "api cached(): Int "+annotation+" { return 1 }", "origin/main/api.luxo")
		result := AnalyzeWithOptions([]*ast.File{file}, Options{CachePolicies: testCachePolicies(t, "30s")})
		expectError(t, result, "cache")
		if len(result.CachePolicies) != 0 {
			t.Fatal("invalid reference published a resolved contract")
		}
	}
	file := parseFileWithName(t, `api cached(): Int @cache(policy: "preview") { return 1 }`, "origin/main/api.luxo")
	expectError(t, Analyze([]*ast.File{file}), "unknown cache policy")
}

func TestCacheRejectsLegacyTTLContracts(t *testing.T) {
	for _, annotation := range []string{`@cache(60)`, `@cache(ttl: 60)`, `@cache(ttl: 1m)`} {
		file := parseFileWithName(t, "api cached(): Int "+annotation+" { return 1 }", "origin/main/api.luxo")
		result := AnalyzeWithOptions([]*ast.File{file}, Options{CachePolicies: testCachePolicies(t, "1m")})
		expectError(t, result, "@cache requires an explicit policy")
		if len(result.CachePolicies) != 0 {
			t.Fatal("legacy TTL inferred a named policy")
		}
	}
}

func TestNamedCachePolicyPreservesReadOnlyRules(t *testing.T) {
	const source = `model User { id: Int @id }
api cached(): Int @cache(policy: "preview") { User.where(id: 1).delete()
return 1 }`
	expectNoErrors(t, analyze(t, strings.Replace(source, `@cache(policy: "preview")`, "", 1)))
	file := parseFileWithName(t, source, "origin/main/api.luxo")
	result := AnalyzeWithOptions([]*ast.File{file}, Options{CachePolicies: testCachePolicies(t, "30s")})
	expectError(t, result, "@cache cannot skip side effects")
}

func TestConcurrentPolicyCompilationsDoNotShareState(t *testing.T) {
	policies := []*cachepolicy.Catalog{testCachePolicies(t, "1s"), testCachePolicies(t, "2s")}
	var workers sync.WaitGroup
	for i := range 16 {
		file := parseFileWithName(t, `api cached(): Int @cache(policy: "preview") { return 1 }`, "origin/main/api.luxo")
		workers.Go(func() {
			result := AnalyzeWithOptions([]*ast.File{file}, Options{CachePolicies: policies[i%2]})
			if len(result.Errors) != 0 || result.CachePolicies[file.APIs[0].Directives[0]].TTL != time.Duration(i%2+1)*time.Second {
				t.Errorf("compilation leaked another policy: %v", result.Errors)
			}
		})
	}
	workers.Wait()
}
