package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// ========== val 赋值 ==========

func TestParseAssignment(t *testing.T) {
	input := `api test(): Int {
  val x = 1
  x = 2
  x
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	// x = 2 should be an assignment statement (ExprStmt with BinaryExpr =)
	if len(api.Body.Stmts) < 3 {
		t.Fatalf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestParseCompoundAssignment(t *testing.T) {
	input := `api test(): Int {
  val x = 1
  x += 1
  x -= 2
  x *= 3
  x /= 4
  x %= 5
  x
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) < 7 {
		t.Fatalf("expected 7 statements, got %d", len(api.Body.Stmts))
	}
}

// ========== for 条件循环（替代 while） ==========

func TestParseForCondition(t *testing.T) {
	input := `api test(): Int {
  val count = 0
  for count < 10 {
    count += 1
  }
  count
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	// for condition { } should be a ForStmt with Collection as condition expr
	forStmt, ok := api.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", api.Body.Stmts[1])
	}
	// conditional for has no VarName
	if forStmt.VarName != "" {
		t.Errorf("expected empty VarName for conditional for, got %q", forStmt.VarName)
	}
}

func TestParseForInfinite(t *testing.T) {
	input := `api test(): Int {
  val i = 0
  for {
    i += 1
    if i >= 10 { break }
  }
  i
}`
	file := parse(t, input)
	api := file.APIs[0]
	forStmt, ok := api.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", api.Body.Stmts[1])
	}
	if forStmt.Collection != nil {
		t.Error("expected nil collection for infinite for")
	}
	if forStmt.VarName != "" {
		t.Error("expected empty VarName for infinite for")
	}
}

// ========== break ==========

func TestParseBreakStmt(t *testing.T) {
	input := `api test(): Int {
  for {
    break
  }
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	forStmt := api.Body.Stmts[0].(*ast.ForStmt)
	// break should be in the for body
	if len(forStmt.Body.Stmts) < 1 {
		t.Fatal("expected break stmt in for body")
	}
}

// ========== yield ==========

func TestParseYieldExpr(t *testing.T) {
	input := `api test(): Int {
  val found = for item in items {
    if item > 0 { yield item }
  }
  found
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

// ========== async ==========

func TestParseAsync(t *testing.T) {
	input := `api test(): Int {
  async {
    sendEmail("test")
  }
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

// ========== await ==========

func TestParseAwait(t *testing.T) {
	input := `api test(): Int {
  val result = await {
    fetchUser(1)
    fetchPosts(1)
  }
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

// ========== Channel + <- ==========

func TestParseChanSend(t *testing.T) {
	input := `api test(): Int {
  val ch = Channel(10)
  ch <- 42
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

func TestParseChanReceive(t *testing.T) {
	input := `api test(): Int {
  val ch = Channel(10)
  val value = <-ch
  value
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

// ========== ... 不定参数 ==========

func TestParseSpreadParam(t *testing.T) {
	input := `fn log(level: String, ...messages: String) {
  val x = 1
}`
	file := parse(t, input)
	if len(file.Functions) != 1 {
		t.Fatalf("expected 1 fn, got %d", len(file.Functions))
	}
	fn := file.Functions[0]
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}
}

// ========== use (stdlib import) ==========

func TestParseUseStdlib(t *testing.T) {
	input := `use http
use json`
	file := parse(t, input)
	if len(file.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d", len(file.Imports))
	}
	if file.Imports[0].Module != "http" {
		t.Errorf("expected 'http', got %q", file.Imports[0].Module)
	}
	if file.Imports[1].Module != "json" {
		t.Errorf("expected 'json', got %q", file.Imports[1].Module)
	}
}

// ========== for 作为表达式（带 yield）==========

func TestParseForAsExpression(t *testing.T) {
	input := `api test(): Int {
  val result = for i in 0..9 {
    if i > 5 { yield i }
  }
  result
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	if valStmt.Name != "result" {
		t.Errorf("expected 'result', got %q", valStmt.Name)
	}
}

// ========== 复合场景 ==========

func TestParseComplexNewSyntax(t *testing.T) {
	input := `api processItems(items: [Int]): Int {
  val total = 0

  val first = for item in items {
    if item > 100 { yield item }
  }

  for item in items {
    total += item
    if total > 1000 { break }
  }

  async {
    log("processed")
  }

  val (user, posts) = await {
    fetchUser(1)
    fetchPosts(1)
  }

  total
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

// ========== event / on / emit ==========

func TestParseEventDecl(t *testing.T) {
	input := `event PostCreated(post: Post, userId: Int)`
	file := parse(t, input)
	if len(file.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(file.Events))
	}
	ev := file.Events[0]
	if ev.Name != "PostCreated" {
		t.Errorf("expected 'PostCreated', got %q", ev.Name)
	}
	if len(ev.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(ev.Params))
	}
}

func TestParseEventNoParams(t *testing.T) {
	input := `event SystemReady`
	file := parse(t, input)
	if len(file.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(file.Events))
	}
}

func TestParseOnDeclWithBody(t *testing.T) {
	input := `on PostCreated {
  val x = 1
}`
	file := parse(t, input)
	if len(file.Listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(file.Listeners))
	}
	on := file.Listeners[0]
	if on.EventName != "PostCreated" {
		t.Errorf("expected 'PostCreated', got %q", on.EventName)
	}
	if on.Body == nil {
		t.Error("expected body")
	}
}

func TestParseOnNative(t *testing.T) {
	input := `on PostCreated @native`
	file := parse(t, input)
	if len(file.Listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(file.Listeners))
	}
	if !file.Listeners[0].Native {
		t.Error("expected native")
	}
}

func TestParseOnWithLambdaParams(t *testing.T) {
	input := `on PostCreated { post -> val x = 1 }`
	file := parse(t, input)
	if len(file.Listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(file.Listeners))
	}
	on := file.Listeners[0]
	if on.EventName != "PostCreated" {
		t.Errorf("expected 'PostCreated', got %q", on.EventName)
	}
	if len(on.Params) != 1 || on.Params[0] != "post" {
		t.Errorf("expected params [post], got %v", on.Params)
	}
	if on.Body == nil {
		t.Error("expected body")
	}
}

func TestParseOnWithMultipleLambdaParams(t *testing.T) {
	input := `on ChatMessage { msg, roomId -> val x = 1 }`
	file := parse(t, input)
	on := file.Listeners[0]
	if len(on.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(on.Params))
	}
	if on.Params[0] != "msg" || on.Params[1] != "roomId" {
		t.Errorf("expected params [msg, roomId], got %v", on.Params)
	}
}

func TestParseUseDestructured(t *testing.T) {
	input := `use http { get, post }`
	file := parse(t, input)
	if len(file.Uses) != 1 {
		t.Fatalf("expected 1 use, got %d", len(file.Uses))
	}
	u := file.Uses[0]
	if u.Module != "http" {
		t.Errorf("expected module 'http', got %q", u.Module)
	}
	if len(u.Names) != 2 || u.Names[0] != "get" || u.Names[1] != "post" {
		t.Errorf("expected names [get, post], got %v", u.Names)
	}
}

func TestParseUseKeywordAsModule(t *testing.T) {
	input := `use model { Base }`
	file := parse(t, input)
	if len(file.Uses) != 1 {
		t.Fatalf("expected 1 use, got %d", len(file.Uses))
	}
	u := file.Uses[0]
	if u.Module != "model" {
		t.Errorf("expected module 'model', got %q", u.Module)
	}
	if len(u.Names) != 1 || u.Names[0] != "Base" {
		t.Errorf("expected names [Base], got %v", u.Names)
	}
}

func TestParseEmitTyped(t *testing.T) {
	input := `api test(): Int {
  emit OrderCreated(order: order, userId: 1)
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	emitStmt, ok := api.Body.Stmts[0].(*ast.EmitStmt)
	if !ok {
		t.Fatalf("expected EmitStmt, got %T", api.Body.Stmts[0])
	}
	if emitStmt.EventName != "OrderCreated" {
		t.Errorf("expected 'OrderCreated', got %q", emitStmt.EventName)
	}
}

func TestParseMyKeyword(t *testing.T) {
	input := `api test(): Int {
  my.id
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil || len(api.Body.Stmts) == 0 {
		t.Fatal("expected body with statements")
	}
}
