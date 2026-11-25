package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestRelease91ValueAwaitInFunctionReturn(t *testing.T) {
	runGeneratedFixture(
		t,
		"fn answer(): Int { return await { 42 } }",
		"package generated\nimport \"testing\"\nfunc TestAwaitReturn(t *testing.T) { got, err := (&App{}).answer(t.Context()); if err != nil || got != 42 { t.Fatal(got, err) } }",
		StableIDs{},
	)
}

func TestRelease91AwaitLocalBindingsAreInScope(t *testing.T) {
	runGeneratedFixture(
		t,
		"fn answer(): Int { val answer = await { val seed = 41\nseed + 1 }\nreturn answer }",
		"package generated\nimport \"testing\"\nfunc TestAwaitLocal(t *testing.T) { got, err := (&App{}).answer(t.Context()); if err != nil || got != 42 { t.Fatal(got, err) } }",
		StableIDs{},
	)
}

func TestGeneratedAwaitValueLoweringComposition(t *testing.T) {
	runGeneratedFixture(t, `
model Item @noTime { id: Int @id value: Int }
fn mark(value: Int): Result<Int> @native
fn valid(value: Int): Result<Boolean> @native
fn combine(first: Int, second: Int): Int { return first * 10 + second }
fn argument(value: Int): Int {
  return combine(mark(1)?, await {
    val seed = mark(value)?
    seed + 1
  })
}
fn named(value: Int): Int {
  val result = await {
    val value = value + 1
    value + 1
  }
  return result + value
}
fn ordered(start: Int): Int {
  var total = 0
  val (first, second) = await {
    start
    val seed = start + 1
    total = seed
    total + 1
  }
  return first + second
}
fn discard(start: Int): Int {
  val (_, answer) = await {
    val seed = mark(start)?
    mark(seed + 1)?
    seed + 2
  }
  return answer
}
fn standalone(): Int {
  await { mark(7)? }
  return 6
}
fn condition(first: Boolean): Int {
  return when {
    first -> 1
    await { true } -> 2
    else -> 3
  }
}
fn logical(left: Boolean, value: Int): Boolean { return left && await { valid(value)? } }
fn maybe(input: Int?): Int? { return await { input } }
fn absent(): Int? { return await { null } }
fn empty(): [Int] { return await { [] } }
fn localQuery(): [Item] {
  val result = await {
    val rows = Item.select().all()
    rows
  }
  return result
}
`, `package generated
import ("context"; "errors"; "slices"; "testing")
var errAwaitFixture = errors.New("await fixture failed")
type awaitFixtureResolver struct { calls []int64 }
func (r *awaitFixtureResolver) Mark(_ context.Context, value int64) (int64, error) {
  r.calls = append(r.calls, value)
  if value < 0 { return 0, errAwaitFixture }
  return value, nil
}
func (r *awaitFixtureResolver) Valid(ctx context.Context, value int64) (bool, error) {
  checked, err := r.Mark(ctx, value)
  return checked > 0, err
}
func TestAwaitComposition(t *testing.T) {
  ctx := t.Context()
  resolver := &awaitFixtureResolver{}
  app := &App{Resolver: resolver}
  checkCalls := func(want ...int64) {
    t.Helper()
    if !slices.Equal(resolver.calls, want) { t.Fatal("wrong evaluation order", resolver.calls, want) }
    resolver.calls = nil
  }
  if value, err := app.argument(ctx, 2); value != 13 || err != nil { t.Fatal(value, err) }
  checkCalls(1, 2)
  if _, err := app.argument(ctx, -2); !errors.Is(err, errAwaitFixture) { t.Fatal(err) }
  checkCalls(1, -2)
  if value, err := app.named(ctx, 20); value != 42 || err != nil { t.Fatal(value, err) }
  if value, err := app.ordered(ctx, 20); value != 42 || err != nil { t.Fatal(value, err) }
  if _, err := app.discard(ctx, -2); !errors.Is(err, errAwaitFixture) { t.Fatal(err) }
  checkCalls(-2)
  if value, err := app.discard(ctx, 40); value != 42 || err != nil { t.Fatal(value, err) }
  checkCalls(40, 41)
  if value, err := app.standalone(ctx); value != 6 || err != nil { t.Fatal(value, err) }
  checkCalls(7)
  if value, err := app.condition(ctx, true); value != 1 || err != nil { t.Fatal(value, err) }
  if value, err := app.condition(ctx, false); value != 2 || err != nil { t.Fatal(value, err) }
  if value, err := app.logical(ctx, false, -3); value || err != nil { t.Fatal(value, err) }
  checkCalls()
  if _, err := app.logical(ctx, true, -3); !errors.Is(err, errAwaitFixture) { t.Fatal(err) }
  checkCalls(-3)
  input := int64(42)
  if value, err := app.maybe(ctx, &input); value != &input || err != nil { t.Fatal(value, err) }
  if value, err := app.absent(ctx); value != nil || err != nil { t.Fatal(value, err) }
  if value, err := app.empty(ctx); value == nil || len(value) != 0 || err != nil { t.Fatal(value, err) }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Item": {"id": 1, "value": 2}}})
}

func TestGeneratedSingleAwaitNullContexts(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn compareNull(value: Int?): Boolean { return value == await { null } }
fn inverseNull(value: Int?): Boolean { return (await { null }) != value }
fn nullPair(): Boolean { return (await { null }) == await { null } }
fn coalesceNull(): Int { return (await { null }) ?: 19 }
fn chooseNull(enabled: Boolean): Int? { return when { enabled -> await { null } else -> 42 } }
fn listNull(): [Int]? { return await { null } }
fn guardNull(): Int? { return await { null } ?: throw error.NotFound }
api nullableValue(): Int? { return await { null } }
api guardedValue(): Int? { return await { null } ?: throw error.NotFound }
`, `package generated
import ("net/http/httptest"; "strings"; "testing"; "github.com/light-speak/luxo/pkg/lux/luvia")
func TestGeneratedValueAwaitNulls(t *testing.T) {
 ctx := t.Context()
 app := &App{}
 sample := int64(1)
 for _, tc := range []struct { value *int64; null bool }{{nil, true},{&sample, false}} {
  if result, err := app.compareNull(ctx, tc.value); err != nil || result != tc.null { t.Fatal("comparison", result, err) }
  if result, err := app.inverseNull(ctx, tc.value); err != nil || result == tc.null { t.Fatal("inverse comparison", result, err) }
 }
 if result, err := app.nullPair(ctx); err != nil || !result { t.Fatal("null pair", result, err) }
 if result, err := app.coalesceNull(ctx); err != nil || result != 19 { t.Fatal("null fallback", result, err) }
 if result, err := app.chooseNull(ctx, true); err != nil || result != nil { t.Fatal("branch null", result, err) }
 if result, err := app.chooseNull(ctx, false); err != nil || result == nil || *result != 42 { t.Fatal("branch value", result, err) }
 if result, err := app.listNull(ctx); err != nil || result != nil { t.Fatal("list null", result, err) }
 if result, err := app.guardNull(ctx); err == nil || result != nil { t.Fatal("null guard lost error", result, err) }

 router := luvia.New().Router
 if err := RegisterSchema(router.Schema); err != nil { t.Fatal(err) }
 if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil { t.Fatal(err) }
 if err := RegisterHandlers(router, app); err != nil { t.Fatal(err) }
 if err := router.Prepare(); err != nil { t.Fatal(err) }
 for _, tc := range []struct { name string; status int }{{"nullableValue", 200}, {"guardedValue", 404}} {
  request := httptest.NewRequest("POST", "/luvia", strings.NewReader("{\"$api\":\""+tc.name+"\"}"))
  request.Header.Set("Content-Type", "application/json")
  response := httptest.NewRecorder()
  router.ServeHTTP(response,request)
  if response.Code != tc.status { t.Fatal(tc.name, response.Code, response.Body.String()) }
  if tc.status == 200 && !strings.Contains(response.Body.String(), "null") { t.Fatal("null API result", response.Body.String()) }
 }
}
`, StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestAwaitValueRequiresOneRecoverableResult(t *testing.T) {
	result := &ast.Ident{Name: "value"}
	statement := &ast.ExprStmt{Expr: result}
	if actual := singleAwaitValue(&ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{nil, &ast.ExprStmt{}, statement}}}); actual != result {
		t.Fatalf("result = %v, want %v", actual, result)
	}
	for _, sample := range []struct {
		name string
		expr *ast.AwaitExpr
	}{
		{"missing body", &ast.AwaitExpr{}},
		{"void result", &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{statement}}, NoAwaitResult: true}},
		{"empty body", &ast.AwaitExpr{Body: &ast.Block{}}},
		{"tuple", &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{statement, statement}}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			defer func() {
				err, ok := recover().(*InternalCompilerError)
				if !ok || err.Detail != "await in a value position requires a single result" {
					t.Fatalf("unexpected value await error: %v", err)
				}
			}()
			singleAwaitValue(sample.expr)
		})
	}
}

func TestGeneratedMixedAwaitKnownNullKeepsEffects(t *testing.T) {
	runGeneratedFixture(t, `
fn observe(value: Int): Result<Int> @native
fn equality(value: Int): Boolean {
  return (await { val observed = observe(value)?
    null
  }) == null
}
`, `package generated
import ("context"; "errors"; "testing")
var errAwaitNull = errors.New("null fixture failure")
type nullAwaitResolver struct { calls int }
func (r *nullAwaitResolver) Observe(ctx context.Context, value int64) (int64,error) {
 r.calls++
 if value < 0 { return 0,errAwaitNull }
 return value,ctx.Err()
}
func TestMixedNull(t *testing.T) {
 resolver := &nullAwaitResolver{}
 app := &App{Resolver:resolver}
 if result,err := app.equality(t.Context(),1); !result || err != nil || resolver.calls != 1 { t.Fatal(result,err,resolver.calls) }
 if _,err := app.equality(t.Context(),-1); !errors.Is(err,errAwaitNull) || resolver.calls != 2 { t.Fatal(err,resolver.calls) }
}
`, StableIDs{})
}

func TestCompileMixedAwaitBindingsRecovery(t *testing.T) {
	literal := &ast.Literal{Kind: token.Int, Value: "42", TypeTagged: ast.TypeTagged{TypeTag: "Int"}}
	declaration := &ast.ValStmt{Name: "seed", Value: literal}
	result := &ast.ExprStmt{Expr: literal}
	for _, example := range []struct {
		name       string
		statements []ast.Stmt
		names      []string
		emit       bool
	}{
		{"ignore-recovery-nodes", []ast.Stmt{declaration, nil, &ast.ExprStmt{}, result}, []string{"value"}, true},
		{"extra-result", []ast.Stmt{declaration, result, result}, []string{"value"}, false},
		{"missing-result", []ast.Stmt{declaration, nil, &ast.ExprStmt{}}, []string{"left", "right"}, false},
	} {
		t.Run(example.name, func(t *testing.T) {
			compiler := newCompiler(nil)
			compiler.compileAwaitBindings(&ast.AwaitExpr{Body: &ast.Block{Stmts: example.statements}}, example.names)
			output := compilerOut(compiler)
			if example.emit {
				if !strings.Contains(output, "api.NewTaskGroup(ctx)") || !strings.Contains(output, "value :=") {
					t.Fatal("recovery nodes changed the valid binding", output)
				}
			} else if output != "" {
				t.Fatal("invalid result count emitted partial code", output)
			}
		})
	}
}

func TestGeneratedAwaitScalarBindingsEvaluateOnce(t *testing.T) {
	const fixture = `package generated
import ("context"; "errors"; "testing")
var errScalarFixture = errors.New("scalar source failed")
type scalarResolver struct { calls int }
func (r *scalarResolver) Source(_ context.Context, value int64) (int64, error) {
 r.calls++
 if value < 0 { return 0,errScalarFixture }
 return value,nil
}
func TestScalarAwait(t *testing.T) {
 r := &scalarResolver{}
 app := &App{Resolver:r}
 if value,err := app.answer(t.Context(),20); value != 42 || err != nil || r.calls != 1 { t.Fatal(value,err,r.calls) }
 if _,err := app.answer(t.Context(),-20); !errors.Is(err,errScalarFixture) || r.calls != 2 { t.Fatal(err,r.calls) }
}
`
	for _, sample := range []struct{ name, body string }{
		{"expression", "val (left, right) = await { source(input)? + 1 }\nreturn left + right"},
		{"local then one result", "val (left, right) = await { val seed = source(input)?\nseed + 1 }\nreturn left + right"},
		{"discard", "val (_, right) = await { val seed = source(input)?\nseed + 1 }\nreturn right * 2"},
		{"unused alias", "val (left, right) = await { source(input)? + 1 }\nreturn left * 2"},
		{"ignored mixed value", "val result = await {val seed = source(input)?\nseed + 1}\nreturn 42"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := "fn source(value: Int): Result<Int> @native\nfn answer(input: Int): Int {\n" + sample.body + "\n}"
			runGeneratedFixture(t, source, fixture, StableIDs{})
		})
	}
}
