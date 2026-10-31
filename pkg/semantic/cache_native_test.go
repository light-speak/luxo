package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestCacheableRequiresExplicitNativeDependencyContract(t *testing.T) {
	for _, annotation := range []string{
		"@cacheable", "@cacheable()", "@cacheable(dependsOn: User)",
		"@cacheable(dependsOn: null)", "@cacheable(dependsOn: [42])",
		"@cacheable(dependsOn: [Missing])", "@cacheable(dependsOn: [String])",
		"@cacheable(dependsOn: [Identity])", "@cacheable(dependsOn: [[User]])",
		"@cacheable(dependsOn: [Record])", "@cacheable(dependsOn: [State])",
		`@cacheable(dependsOn: [""])`, `@cacheable(dependsOn: [" "])`,
		"@cacheable(dependsOn: [User, User])",
		`@cacheable(dependsOn: ["rates", "rates"])`,
		"@cacheable(dependsOn: [], dependsOn: [])", "@cacheable(other: [])",
		"@cacheable([])", "@cacheable(dependsOn: [User.id])",
		"@cacheable(dependsOn: []) @cacheable(dependsOn: [])",
	} {
		t.Run(annotation, func(t *testing.T) {
			source := `model User { id: Int @id }
type Record { value: Int }
enum State { OPEN }
fn lookup(): Result<Int> @native ` + annotation
			expectError(t, analyzeCached(t, source), "cacheable")
		})
	}
	for _, source := range []string{
		`fn lookup(): Int @cacheable(dependsOn: []) { return 1 }`,
		`api lookup(): Int @cacheable(dependsOn: []) { return 1 }`,
	} {
		expectError(t, analyzeCached(t, source), "requires @native")
	}
}

func TestCacheableAcceptsExplicitEmptyModelAndExternalDependencies(t *testing.T) {
	for _, test := range []struct{ declaration, call string }{
		{"fn lookup(): Result<Int> @native", "lookup()?"},
		{"api lookup(): Int @native", "lookup()"},
	} {
		for _, dependencies := range []string{"[]", "[User]", `["pricing/rates"]`, `[User, "User", "pricing/rates"]`} {
			expectNoErrors(t, analyzeCached(t, `model User { id: Int @id }
`+test.declaration+` @cacheable(dependsOn: `+dependencies+`)
api cached(): Int @cache(policy: "preview") { return `+test.call+` }`))
		}
	}
	expectNoErrors(t, analyzeCached(t, `fn pure(): Result<Int> @native @cacheable(dependsOn: [])`))
	expectNoErrors(t, analyzeCached(t, `fn endpoint(): Int @native @service @cacheable(dependsOn: [])`))
}

func TestCacheRejectsUndeclaredNativeDependenciesTransitively(t *testing.T) {
	for _, test := range []struct{ declarations, call string }{
		{`fn lookup(): Result<Int> @native`, "lookup()?"},
		{`api lookup(): Int @native`, "lookup()"},
		{`fn lookup(): Int { return helper()? }
fn helper(): Result<Int> @native`, "lookup()"},
		{`fn lookup(): Int { return next() }
fn next(): Int { if false { return lookup() }
return helper()? }
fn helper(): Result<Int> @native`, "lookup()"},
	} {
		source := test.declarations + "\napi cached(): Int @cache(policy: \"preview\") { return " + test.call + " }"
		expectError(t, analyzeCached(t, source), "native requires @cacheable")
		expectNoErrors(t, analyzeCached(t, strings.Replace(source, `@cache(policy: "preview")`, "", 1)))
	}
	expectError(t, analyzeCached(t, `api cached(): Int @native @cache(policy: "preview")`), "native requires @cacheable")
	expectNoErrors(t, analyzeCached(t, `api cached(): Int @native @cache(policy: "preview") @cacheable(dependsOn: [])`))
}

func TestCacheNativeContractUsesModuleLocalAndCommonLookup(t *testing.T) {
	common := parseFileWithName(t, `fn helper(): Result<Int> @native`, "origin/common/native.luxo")
	read := parseFileWithName(t, `fn helper(): Result<Int> @native @cacheable(dependsOn: [])
api read(): Int @cache(policy: "preview") { return helper()? }`, "origin/read/actions.luxo")
	other := parseFileWithName(t, `api other(): Int @cache(policy: "preview") { return helper()? }`, "origin/other/actions.luxo")
	for _, files := range [][]*ast.File{{common, read, other}, {other, read, common}} {
		result := analyzeCachedModules(t, files)
		if len(result.Errors) != 1 || result.Errors[0].Pos.File != other.Name ||
			!strings.Contains(result.Errors[0].Message, "native requires @cacheable") {
			t.Fatalf("native contracts conflated across modules: %v", result.Errors)
		}
	}
}
