package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestCompiledExpressionClosuresExecute(t *testing.T) {
	call := func() ast.Expr { return &ast.CallExpr{Func: &ast.Ident{Name: "check"}} }
	check := &ast.FnDecl{Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}
	loop := &ast.ForStmt{VarName: "i", Collection: &ast.RangeExpr{Start: &ast.Literal{Kind: token.Int, Value: "1"}, End: &ast.Literal{Kind: token.Int, Value: "2"}},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call()}}}}
	loop.SetTypeTag("Int")
	loop.SetListType(true)
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{Condition: &ast.Ident{Name: "enabled"}, Body: call()}}, Else: &ast.Literal{Kind: token.Int, Value: "7"}}
	branch.SetTypeTag("Int")
	predicate := &ast.WhenExpr{Branches: []*ast.WhenBranch{
		{Condition: &ast.Ident{Name: "enabled"}, Body: &ast.Literal{Kind: token.Int, Value: "7"}},
		{Condition: &ast.CallExpr{Func: &ast.Ident{Name: "eligible"}}, Body: &ast.Literal{Kind: token.Int, Value: "9"}},
	}, Else: &ast.Literal{Kind: token.Int, Value: "11"}}
	predicate.SetTypeTag("Int")
	var generated strings.Builder
	for _, fn := range []*ast.FnDecl{
		{Name: "collect", ReturnType: &ast.TypeRef{Name: "Int", IsList: true}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: loop}}}},
		{Name: "choose", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: branch}}}},
		{Name: "choosePredicate", Params: []*ast.ParamDecl{{Name: "enabled", Type: &ast.TypeRef{Name: "Boolean"}}}, ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: predicate}}}},
	} {
		defaultGenerator().compileLocalFunction(&generated, fn, nil, nil, nil, map[string]*ast.FnDecl{"check": check,
			"eligible": {Name: "eligible", ReturnType: &ast.TypeRef{Name: "Boolean"}, Body: &ast.Block{}},
		}, nil)
	}
	generated.WriteString(compiledNativeSelect())
	runGeneratedClosureTest(t, generated.String(), `
func TestRun(t *testing.T) {
 app := &App{}
 app.Resolver = &resolver{app: app}
 ch := make(chan int64, 1)
 ch <- 5
 if value, err := app.pick(context.Background(), ch); err != nil || value != 3 { t.Fatalf("native select: %v %v", value, err) }
 if value, err := app.pick(context.Background(), ch); err != nil || value != 0 { t.Fatalf("select default: %v %v", value, err) }
 app.calls = 0
 values, err := app.collect(context.Background())
 if err != nil || len(values) != 2 || app.calls != 2 { t.Fatalf("collect: %v %v calls=%d", values, err, app.calls) }
 app.calls = 0
 value, err := app.choose(context.Background(), false)
 if err != nil || value != 7 || app.calls != 0 { t.Fatalf("inactive branch ran: %v %v calls=%d", value, err, app.calls) }
 if allocations := testing.AllocsPerRun(100, func() { _, _ = app.choose(context.Background(), true) }); allocations != 0 { t.Fatalf("value closure allocated: %v", allocations) }
 app.fail = true
 app.calls = 0
 if value, err := app.choosePredicate(context.Background(), true); value != 7 || err != nil || app.calls != 0 { t.Fatalf("inactive predicate ran: %v %v", value, err) }
 if _, err := app.choosePredicate(context.Background(), false); err != sentinel { t.Fatalf("predicate error: %v", err) }
 if _, err := app.collect(context.Background()); err != sentinel { t.Fatalf("collect error: %v", err) }
 if _, err := app.choose(context.Background(), true); err != sentinel { t.Fatalf("branch error: %v", err) }
 ch <- 5
 if _, err := app.pick(context.Background(), ch); err != sentinel { t.Fatalf("native select error: %v", err) }
}
`)
}

func compiledNativeSelect() string {
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	c.nativeFunctions = map[string]bool{"nativeCheck": true}
	branch := &ast.WhenExpr{Branches: []*ast.WhenBranch{{
		Condition: &ast.UnaryExpr{Op: "<-", Value: &ast.Ident{Name: "ch"}},
		Body: &ast.LambdaExpr{Params: []string{"message"}, Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.AssignStmt{Target: &ast.Ident{Name: "_"}, Op: "=", Value: &ast.Ident{Name: "message"}},
			&ast.ReturnStmt{Value: &ast.UnaryExpr{Op: "?", Value: &ast.CallExpr{Func: &ast.Ident{Name: "nativeCheck"}}}},
		}}},
	}}, Else: &ast.Literal{Kind: token.Int, Value: "0"}}
	branch.SetTypeTag("Int")
	c.compileReturn(&ast.ReturnStmt{Value: branch})
	return "func (app *App) pick(ctx context.Context, ch <-chan int64) (_value int64, err error) {\n" + compilerOut(c) + "}\n"
}

func runGeneratedClosureTest(t *testing.T, generated, assertions string) {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "closure-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	source := `package closuretest
import ("context"; "errors"; "testing")
var sentinel = errors.New("expected failure")
type App struct { calls int; fail bool; Resolver *resolver; entered chan struct{}; release chan struct{} }
type resolver struct { app *App }
func (r *resolver) NativeCheck(ctx context.Context) (int64, error) { return r.app.check(ctx) }
func (app *App) check(ctx context.Context) (int64, error) { app.calls++; if app.fail { return 0, sentinel }; return 3, nil }
func (app *App) eligible(ctx context.Context) (bool, error) { app.calls++; if app.fail { return false, sentinel }; return true, nil }
` + generated + assertions
	if strings.Contains(generated, "errgroup.") {
		source = strings.Replace(source, `import (`, `import ("golang.org/x/sync/errgroup";`, 1)
	}
	if strings.Contains(assertions, "time.") {
		source = strings.Replace(source, `import (`, `import ("time";`, 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "closure_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated execution: %v\n%s\n%s", err, output, source)
	}
}

func TestAwaitFunctionCallsRunInsideParallelTasks(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "waitCheck"}}
	call.SetTypeTag("Int")
	one, two := &ast.Literal{Kind: token.Int, Value: "1"}, &ast.Literal{Kind: token.Int, Value: "2"}
	one.SetTypeTag("Int")
	two.SetTypeTag("Int")
	fn := &ast.FnDecl{Name: "parallel", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Names: []string{"a", "b"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call}, &ast.ExprStmt{Expr: call}}}}},
		&ast.ValStmt{Names: []string{"c", "d"}, Value: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: one}, &ast.ExprStmt{Expr: two}}}}},
		&ast.ReturnStmt{Value: &ast.BinaryExpr{Left: &ast.Ident{Name: "a"}, Op: "+", Right: &ast.Ident{Name: "b"}}},
	}}}
	var body strings.Builder
	functions := map[string]*ast.FnDecl{"waitCheck": {Name: "waitCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	failure := &ast.CallExpr{Func: &ast.Ident{Name: "failCheck"}}
	failure.SetTypeTag("Int")
	functions["failCheck"] = &ast.FnDecl{Name: "failCheck", Body: &ast.Block{}, ReturnType: &ast.TypeRef{Name: "Int"}}
	fn.Name = "parallelFailure"
	fn.Body.Stmts[0].(*ast.ValStmt).Value.(*ast.AwaitExpr).Body.Stmts[1] = &ast.ExprStmt{Expr: failure}
	defaultGenerator().compileLocalFunction(&body, fn, nil, nil, nil, functions, nil)
	if strings.Contains(body.String(), " any") {
		t.Fatalf("typed await lost its value types:\n%s", body.String())
	}
	runGeneratedClosureTest(t, body.String(), `
func (app *App) waitCheck(ctx context.Context) (int64, error) {
 app.entered <- struct{}{}
 select { case <-app.release: return 3, nil; case <-ctx.Done(): return 0, ctx.Err() }
}
func (app *App) failCheck(ctx context.Context) (int64, error) {
 select { case <-app.entered: return 0, sentinel; case <-ctx.Done(): return 0, ctx.Err() }
}
func TestAwait(t *testing.T) {
 ctx, cancel := context.WithCancel(context.Background())
 defer cancel()
 app := &App{entered: make(chan struct{}, 2), release: make(chan struct{})}
 done := make(chan error, 1)
 go func() { value, err := app.parallel(ctx); if err == nil && value != 6 { err = errors.New("incorrect result") }; done <- err }()
 timer := time.NewTimer(time.Second)
 defer timer.Stop()
 for range 2 { select { case <-app.entered: case <-timer.C: t.Fatal("await serialized function calls") } }
 close(app.release)
 if err := <-done; err != nil { t.Fatal(err) }
}
func TestAwaitFailureCancelsSibling(t *testing.T) {
 ctx, cancel := context.WithTimeout(context.Background(), time.Second)
 defer cancel()
 app := &App{entered: make(chan struct{}, 1), release: make(chan struct{})}
 if _, err := app.parallelFailure(ctx); err != sentinel { t.Fatalf("lost task error: %v", err) }
 if ctx.Err() != nil { t.Fatal("sibling waited for the parent deadline instead of group cancellation") }
}
`)
}

func TestCompiledReturnBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		result *ast.TypeRef
		value  ast.Expr
		want   string
	}{
		{"void", nil, nil, "return nil"},
		{"list", &ast.TypeRef{Name: "Int", IsList: true}, &ast.ListExpr{Items: []ast.Expr{&ast.Literal{Kind: token.Int, Value: "1"}}}, "return []int64{1}, nil"},
		{"model", &ast.TypeRef{Name: "User"}, &ast.ObjectExpr{TypeName: "User"}, "return &User{}, nil"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := newCompiler(makeModels("User"))
			c.inFunction, c.functionResult = true, test.result
			c.compileReturn(&ast.ReturnStmt{Value: test.value})
			if !strings.Contains(compilerOut(c), test.want) {
				t.Fatalf("return: %s", compilerOut(c))
			}
		})
	}
	c := newCompiler(nil)
	c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
	async := c.compileAsync(&ast.AsyncExpr{Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}, &ast.ReturnStmt{},
	}}})
	if !strings.Contains(async, "luxolog.Error((sentinel).Error())") || strings.Contains(async, "return _value") || strings.Contains(async, "return nil") {
		t.Fatalf("async ABI: %s", async)
	}
	transaction := c.compileTransaction(&ast.TransactionExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ThrowStmt{Error: &ast.Ident{Name: "sentinel"}}}}})
	if !strings.Contains(transaction, "return sentinel") || strings.Contains(transaction, "return _value") {
		t.Fatalf("transaction ABI: %s", transaction)
	}
	sub := c.valueCompiler()
	sub.compileReturn(&ast.ReturnStmt{})
	if !strings.Contains(sub.b.String(), "return nil") {
		t.Fatalf("nullable closure ABI: %s", sub.b.String())
	}
}

func TestFallibleWhenSubjectWithoutElse(t *testing.T) {
	c := newCompiler(nil)
	c.functions = map[string]*ast.FnDecl{"check": {Name: "check", ReturnType: &ast.TypeRef{Name: "Int"}, Body: &ast.Block{}}}
	expression := &ast.WhenExpr{Subject: &ast.Ident{Name: "value"}, Branches: []*ast.WhenBranch{{
		Condition: &ast.CallExpr{Func: &ast.Ident{Name: "check"}}, Body: &ast.Literal{Kind: token.Int, Value: "1"},
	}}}
	expression.SetTypeTag("Int")
	c.compileWhen(expression)
	code := compilerOut(c)
	for _, want := range []string{"_subject := value", "if _subject == _result", "return 0", "return _closureErr"} {
		if !strings.Contains(code, want) {
			t.Fatalf("missing %q: %s", want, code)
		}
	}
	if got := c.compileLiteral(&ast.Literal{Kind: token.Duration, Value: "broken"}); got != "broken" {
		t.Fatalf("invalid duration recovery = %q", got)
	}
}
