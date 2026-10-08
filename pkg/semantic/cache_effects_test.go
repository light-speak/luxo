package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestCachedAPIRejectsKnownSideEffects(t *testing.T) {
	for _, test := range []struct{ name, declarations, body string }{
		{"create", "", `User.create(name: "changed", score: 1)`},
		{"query update", "", `User.where(id: 1).update(name: "changed")`},
		{"builder alias", "", `val query = User.where(id: 1)
query.delete()`},
		{"function CRUD", "", `delete(User, id: 1)`},
		{"emit", "", `emit Changed(id: 1)`},
		{"nested emit", "", `transaction { if true { emit Changed(id: 1) } }`},
		{"helper", `fn mutate() { User.where(id: 1).delete() }`, `mutate()`},
		{"transitive helper", `fn first() { second() }
fn second() { emit Changed(id: 1) }`, `first()`},
		{"recursive helper", `fn first() { second() }
fn second() { first()
emit Changed(id: 1) }`, `first()`},
		{"async", "", `async { emit Changed(id: 1) }`},
		{"atomic", "", `var user = User.find(id: 1)
user.score += 1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `model User { id: Int @id
name: String
score: Int }
event Changed(id: Int)
` + test.declarations + "\napi cached(): Boolean @cache(policy: \"preview\") {\n" + test.body + "\nreturn true\n}"
			uncached := analyzeCached(t, strings.Replace(source, `@cache(policy: "preview")`, "", 1))
			expectNoErrors(t, uncached)
			expectError(t, analyzeCached(t, source), "@cache cannot skip side effects")
		})
	}
}

func TestCacheEffectsRecursiveReadOnlyGraph(t *testing.T) {
	expectNoErrors(t, analyzeCached(t, `
fn first(): Int { return second() }
fn second(): Int { return first() }
api cached(): Int @cache(policy: "preview") { return first() }
`))
}

func TestCacheEffectsKeepModuleLocalHelpersSeparate(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		read := parseFileWithName(t, `fn helper() {}
api read(): Boolean @cache(policy: "preview") { helper()
return true }`, "origin/read/actions.luxo")
		write := parseFileWithName(t, `event Changed(id: Int)
fn helper() { emit Changed(id: 1) }
api write(): Boolean @cache(policy: "preview") { helper()
return true }`, "origin/write/actions.luxo")
		files := []*ast.File{read, write}
		if reverse {
			files[0], files[1] = files[1], files[0]
		}
		result := analyzeCachedModules(t, files)
		if len(result.Errors) != 1 || result.Errors[0].Pos.File != write.Name ||
			!strings.Contains(result.Errors[0].Message, "@cache cannot skip side effects") {
			t.Fatalf("module-local effects conflated (reverse=%t): %v", reverse, result.Errors)
		}
	}
}

func TestCacheEffectsFollowCommonHelper(t *testing.T) {
	common := parseFileWithName(t, `event Changed(id: Int)
fn publish() { emit Changed(id: 1) }`, "origin/common/events.luxo")
	api := parseFileWithName(t, `api cached(): Boolean @cache(policy: "preview") {
publish()
return true
}`, "origin/read/actions.luxo")
	expectError(t, analyzeCachedModules(t, []*ast.File{common, api}), "@cache cannot skip side effects")
}

func TestCacheModelWriteClassification(t *testing.T) {
	analyzer := New()
	analyzer.types["User"] = &ResolvedType{Name: "User", Kind: TypeModel}
	model := &ast.Ident{Name: "User"}
	model.SetModelType(true)
	for _, name := range []string{"create", "createMany", "update", "updateMany", "delete", "deleteMany", "upsert", "save"} {
		call := &ast.CallExpr{Func: &ast.MemberExpr{Object: model, Field: name}}
		if !analyzer.cacheModelWrite(call) {
			t.Errorf("model write %s not classified", name)
		}
	}
	for _, call := range []*ast.CallExpr{
		{Func: &ast.LambdaExpr{}},
		{Func: &ast.Ident{Name: "create"}},
		{Func: &ast.Ident{Name: "create"}, Args: []*ast.NamedArg{{Value: &ast.Literal{}}}},
		{Func: &ast.Ident{Name: "create"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "Unknown"}}}},
		{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "custom"}, Field: "save"}},
	} {
		if analyzer.cacheModelWrite(call) {
			t.Errorf("non-model call classified as a write: %#v", call)
		}
	}
}

func TestCachedInferredDeleteRejected(t *testing.T) {
	expectError(t, analyzeCached(t, `model User { id: Int @id }
api deleteUsers @cache(policy: "preview")`), "@cache cannot skip side effects")
}

func TestCachedReadOnlyAndLocalMutationAllowed(t *testing.T) {
	expectNoErrors(t, analyzeCached(t, `
model User { id: Int @id
score: Int }
fn answer(): Int { return 42 }
api countUsers @cache(policy: "preview")
api cached(): Int @cache(policy: "preview") {
  var result = answer()
  result += 1
  return result
}
api writeUncached() { User.where(id: 1).delete() }
`))
}

func TestEffectIndexesKeepFirstDuplicateDeclaration(t *testing.T) {
	first, second := &ast.Block{}, &ast.Block{}
	file := &ast.File{Name: "test.luxo", Functions: []*ast.FnDecl{
		{Name: "helper", Body: first}, {Name: "helper", Body: second},
	}}
	analyzer := New()
	analyzer.files = []*ast.File{file}
	nodes, index := analyzer.cacheEffectNodes(analyzer.files)
	key := callableKey{name: "helper"}
	if len(nodes) != 1 || index[key].body != first || analyzer.eventCallableBodies()[key] != first {
		t.Fatal("effect indexes disagree about the first declaration")
	}
}
