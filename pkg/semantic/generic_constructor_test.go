package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

const genericConstructorTypes = `type Box<T> { value: T label: String = "default" optional: String? }
type Pair<A, B> { first: A second: B }
type Plain { value: Int }
model User @noTime { id: Int @id }
`

func TestGenericConstructorsPreserveSourceAndInstanceFields(t *testing.T) {
	result := analyze(t, genericConstructorTypes+`
api first(): Box<Int> { return Box<Int>(value: 1) }
api second(): Box<String> { return Box<String>(value: "two") }
`)
	expectNoErrors(t, result)
	for index, expected := range []TypeKind{TypeInt, TypeString} {
		call := result.Files[0].APIs[index].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
		resolved := result.GenericExprTypes[call]
		if resolved == nil || resolved.Name != "Box" || len(resolved.TypeArgs) != 1 || resolved.Fields["value"].Type.Kind != expected {
			t.Fatal("constructor lost concrete generic arguments", resolved)
		}
		if call.Func.(*ast.Ident).Name != "Box" || call.GetTypeTag() != "Box" || len(call.TypeArgs) != 1 || call.Args[0].ExpectedType.Name != resolved.TypeArgs[0].Name {
			t.Fatal("source syntax or checked destination was rewritten")
		}
		if call.ConstructorType == nil || call.ConstructorType.Name != "Box" || call.ConstructorType.TypeArgs[0].Name != resolved.TypeArgs[0].Name {
			t.Fatal("successful constructor has no static compiler marker")
		}
	}
	if result.Types["Box"].Fields["value"].Type.Kind != TypeGeneric || result.Types["Box"].Fields["value"].Type.Name != "T" {
		t.Fatal("one constructor instance mutated the global template")
	}
}

func TestGenericConstructorNestedListsAndBuiltins(t *testing.T) {
	for _, source := range []string{
		`api nested(): Box<Box<Int>> { return Box<Box<Int>>(value: Box<Int>(value: 7)) }`,
		`api listed(): Box<[Int]> { return Box<[Int]>(value: [1, 2]) }`,
		`api nullable(): Box<Int?> { return Box<Int?>(value: null) }`,
		`api pairs(): Pair<Int, String> { return Pair<Int, String>(first: 1, second: "ok") }`,
		`api paged(user: User): Page<User> { return Page<User>(items: [user], total: 1, page: 1, pageSize: 20) }`,
		`api cursor(user: User): Cursor<User> { return Cursor<User>(items: [user], nextCursor: null, hasMore: false) }`,
		`type box<T> { value: T } api lower(): box<Int> { return box<Int>(value: 1) }`,
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, genericConstructorTypes+source)) })
	}
}

func TestGenericConstructorInvalidContracts(t *testing.T) {
	for _, test := range []struct{ source, message string }{
		{`api probe(): Box<Int> { return Box<>(value: 1) }`, "requires exactly"},
		{`api probe(): Box<Int> { return Box<Int, String>(value: 1) }`, "requires exactly"},
		{`api probe(): Box<Int> { return Box<Missing>(value: 1) }`, "unknown type"},
		{`api probe(): Plain { return Plain<Int>(value: 1) }`, "generic type constructor"},
		{`api probe(): Int { return Int<String>(value: "x") }`, "generic type constructor"},
		{`fn helper(value: Int): Int { return value } api probe(): Int { return helper<Int>(1) }`, "generic type constructor"},
		{`fn Box(value: Box<Int>): Box<Int> { return value } api probe(input: Box<Int>): Box<Int> { return Box<Int>(value: input) }`, "generic type constructor"},
		{`api probe(Box: Int): Int { return Box<Int>(value: 1) }`, "not callable"},
		{`api probe(): Int { val Box = 7 return Box<Int>(value: 1) }`, "not callable"},
		{`api probe(): Box<Int> { return Box(value: 1) }`, "explicit type arguments"},
		{`api probe(): Box<Int> { return Box { value: 1 } }`, "explicit type arguments"},
		{`api probe(): Box<Int> { return Box<Int>(value: "wrong") }`, "expects"},
		{`api probe(): Box<Int> { return Box<Int>() }`, "missing required field"},
		{`api probe(): Box<Int> { return Box<Int>(value: 1, value: 2) }`, "duplicate field"},
		{`api probe(): Box<Int> { return Box<Int>(unknown: 1) }`, "has no field"},
		{`api probe(): Box<Int> { return Box<Int>(1) }`, "has no field"},
		{`api probe(): Page<User> { return Page<User>(items: [], page: 1, pageSize: 20) }`, "missing required field 'total'"},
	} {
		t.Run(test.source, func(t *testing.T) {
			result := analyze(t, genericConstructorTypes+test.source)
			for _, diagnostic := range result.Errors {
				if strings.Contains(diagnostic.Message, test.message) {
					return
				}
			}
			t.Fatal("missing constructor diagnosis", test.message, result.Errors)
		})
	}
}

func TestGenericConstructorRejectsMissingASTValues(t *testing.T) {
	for _, mutate := range []func(*ast.CallExpr){
		func(call *ast.CallExpr) { call.Args = []*ast.NamedArg{nil} },
		func(call *ast.CallExpr) { call.Args[0].Value = nil },
		func(call *ast.CallExpr) { call.TypeArgs[0] = nil },
	} {
		result := analyze(t, genericConstructorTypes+`api probe(): Box<Int> { return Box<Int>(value: 1) }`)
		expectNoErrors(t, result)
		call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
		mutate(call)
		if checked := Analyze(result.Files); len(checked.Errors) == 0 {
			t.Fatal("damaged constructor arguments passed semantic analysis")
		}
	}
}

func TestGenericConstructorMarkerClearedForOrdinaryCalls(t *testing.T) {
	result := analyze(t, genericConstructorTypes+`
fn helper(): Box<Int> { return Box<Int>(value: 1) }
api probe(): Box<Int> { return Box<Int>(value: 2) }
`)
	expectNoErrors(t, result)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if call.ConstructorType == nil {
		t.Fatal("valid constructor not marked")
	}
	call.Func = &ast.Ident{Pos: call.Pos, Name: "helper"}
	call.TypeArgs, call.Args = nil, nil
	expectNoErrors(t, Analyze(result.Files))
	if call.ConstructorType != nil {
		t.Fatal("ordinary call kept stale constructor metadata")
	}
}

func TestGenericConstructorDoesNotMarkRejectedFields(t *testing.T) {
	result := analyze(t, genericConstructorTypes+`api probe(): Box<Int> { return Box<Int>(value: "bad") }`)
	if len(result.Errors) == 0 {
		t.Fatal("invalid constructor accepted")
	}
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if call.ConstructorType != nil {
		t.Fatal("invalid constructor marked as codegen-safe")
	}
}

func TestCursorConstructorNullableFieldsDoNotMutateBuiltin(t *testing.T) {
	result := analyze(t, genericConstructorTypes+`api probe(): Cursor<Int> { return Cursor<Int>(items: [], nextCursor: null, hasMore: false) }`)
	expectNoErrors(t, result)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if !result.GenericExprTypes[call].Fields["nextCursor"].Type.Nullable || result.Types["Cursor"].Fields["nextCursor"].Type.Nullable {
		t.Fatal("nullable instance field changed its builtin template")
	}
}

func TestGenericConstructorMethodTypeArgumentsAreRejected(t *testing.T) {
	result := analyze(t, `api probe(): String { return "ABC".lowercase() }`)
	expectNoErrors(t, result)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	// The current parser has no generic-method syntax; public AST callers still get a diagnostic.
	call.TypeArgs = []*ast.TypeRef{{Name: "Int"}}
	checked := Analyze(result.Files)
	expectError(t, checked, "generic type constructor")
	if call.ConstructorType != nil {
		t.Fatal("ordinary method became a constructor")
	}
}

func TestGenericConstructorModuleVisibility(t *testing.T) {
	common := parseFileWithName(t, `type Box<T> { value: T }`, "origin/common/box.luxo")
	app := parseFileWithName(t, `api probe(): Int { val box = Box<Int>(value: 1) return box.value }`, "origin/app/main.luxo")
	expectNoErrors(t, analyzeModules(t, []*ast.File{common, app}))
	private := parseFileWithName(t, `type Secret<T> { value: T }`, "origin/private/box.luxo")
	other := parseFileWithName(t, `api probe(): Int { val box = Secret<Int>(value: 1) return box.value }`, "origin/other/main.luxo")
	expectError(t, analyzeModules(t, []*ast.File{private, other}), "is from module")
}

func TestGenericConstructorNestedArgumentVisibility(t *testing.T) {
	common := parseFileWithName(t, `type Maybe<T> { value: T? }`, "origin/common/maybe.luxo")
	private := parseFileWithName(t, `type Secret<T> { value: T } model Hidden @noTime { id: Int @id }`, "origin/private/secret.luxo")
	for _, expression := range []string{
		`Maybe<Secret<Int>>(value: null)`,
		`Page<Hidden>(items: [], total: 0, page: 1, pageSize: 20)`,
	} {
		other := parseFileWithName(t, `api probe(): Int { val ignored = `+expression+` return 1 }`, "origin/other/main.luxo")
		expectError(t, analyzeModules(t, []*ast.File{common, private, other}), "is from module")
	}
}

func TestGenericConstructorNamesDoNotCaptureOrdinaryFunctions(t *testing.T) {
	for _, name := range []string{"Box", "Page"} {
		result := analyze(t, genericConstructorTypes+`fn `+name+`(): Int { return 7 }
api probe(): Int { return `+name+`() }`)
		expectNoErrors(t, result)
		call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
		if call.ConstructorType != nil {
			t.Fatal("ordinary function captured by a generic type name")
		}
	}
}

func TestGenericConstructorNameKeepsGenericReturningFunction(t *testing.T) {
	result := analyze(t, genericConstructorTypes+`
fn Box(value: Box<Int>): Box<Int> { return value }
api probe(input: Box<Int>): Box<Int> { return Box(input) }
`)
	expectNoErrors(t, result)
	call := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if call.ConstructorType != nil {
		t.Fatal("generic-returning ordinary fn became a constructor")
	}
}
