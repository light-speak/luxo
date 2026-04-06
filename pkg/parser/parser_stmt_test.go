package parser

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestParseForStmt(t *testing.T) {
	input := `api test(): Int {
  for item in items {
    item.update(status: "done")
  }
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]

	forStmt, ok := api.Body.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected ForStmt, got %T", api.Body.Stmts[0])
	}
	if forStmt.VarName != "item" {
		t.Errorf("expected 'item', got %q", forStmt.VarName)
	}
}

func TestParseIfStmt(t *testing.T) {
	input := `api test(): Int {
  if condition {
    return 1
  }
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]

	ifStmt, ok := api.Body.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected IfStmt, got %T", api.Body.Stmts[0])
	}
	if ifStmt.Then == nil {
		t.Error("expected then block")
	}
}

func TestParseThrowStmt(t *testing.T) {
	input := `api test(): Int {
  throw error.not_found
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
	throwStmt, ok := api.Body.Stmts[0].(*ast.ThrowStmt)
	if !ok {
		t.Fatalf("expected ThrowStmt, got %T", api.Body.Stmts[0])
	}
	if throwStmt.Error == nil {
		t.Error("expected non-nil error expression")
	}
}

func TestParseValWithType(t *testing.T) {
	input := `api test(): Int {
  val x: Int = 42
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt, ok := api.Body.Stmts[0].(*ast.ValStmt)
	if !ok {
		t.Fatalf("expected ValStmt, got %T", api.Body.Stmts[0])
	}
	if valStmt.Name != "x" {
		t.Errorf("expected 'x', got %q", valStmt.Name)
	}
	if valStmt.Type == nil {
		t.Fatal("expected type annotation")
	}
	if valStmt.Type.Name != "Int" {
		t.Errorf("expected type 'Int', got %q", valStmt.Type.Name)
	}
}

func TestStatementBoundaryAfterCall(t *testing.T) {
	input := `api test(): Int {
  val x = User.create(name: "test")
  val y = 42
  y
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
	}
	if len(api.Body.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestStatementBoundaryAfterObjectExpr(t *testing.T) {
	input := `api test(): Int {
  val x = AuthResult { token: "abc", user: "test" }
  val y = 42
  y
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
	}
	if len(api.Body.Stmts) != 3 {
		t.Errorf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

func TestStatementBoundaryCreateThenObjectExpr(t *testing.T) {
	input := `api test(): Int {
  val user = User.create(name: "test")
  AuthResult { token: "abc", user: user }
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	t.Logf("stmts count: %d", len(api.Body.Stmts))
	for i, s := range api.Body.Stmts {
		t.Logf("stmt[%d]: %T", i, s)
		if vs, ok := s.(*ast.ValStmt); ok {
			t.Logf("  val %s = %T", vs.Name, vs.Value)
		}
		if es, ok := s.(*ast.ExprStmt); ok {
			t.Logf("  expr: %T", es.Expr)
		}
	}
	if len(api.Body.Stmts) != 2 {
		t.Errorf("expected 2 statements, got %d", len(api.Body.Stmts))
	}
}

func TestParseCommentTopLevel(t *testing.T) {
	input := `// this is a comment
api getUser(id: Int): User`
	file := parse(t, input)
	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
}

func TestParseCommentInBlock(t *testing.T) {
	// Cover Comment/DocComment skip in parseStmt
	input := `api test(): Int {
  // a comment inside a block
  return 1
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	// Comment should be skipped, only return remains
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
}

func TestParseDocCommentInBlock(t *testing.T) {
	// Cover DocComment skip in parseStmt
	input := `api test(): Int {
  /// a doc comment inside a block
  return 1
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement, got %d", len(api.Body.Stmts))
	}
}

func TestParseBlockStuckRecovery(t *testing.T) {
	// A block that might get stuck on an unexpected token
	input := `api test(): Int {
  )
  return 1
}`
	file, _ := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// Test parseBlock stuck recovery and nil stmt paths via direct token manipulation.
func TestParseBlockStuckRecoveryDirect(t *testing.T) {
	// Create a block that contains a Comment token (returns nil from parseStmt)
	// followed by a token that would cause parseExprStmt to struggle.
	// Comment in block -> parseStmt returns nil (nil stmt path in parseBlock covered).
	input := `api test(): Int {
  // comment producing nil stmt
  return 1
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Body == nil {
		t.Fatal("expected body")
	}
	// Only return stmt should remain
	if len(api.Body.Stmts) != 1 {
		t.Errorf("expected 1 statement (nil stmt filtered), got %d", len(api.Body.Stmts))
	}
}

// Test Parse main loop with comma token (adjacent to stuck guard path).

// Test Parse main loop stuck detection using direct token manipulation.
// This creates a scenario where a top-level match doesn't advance the position.
func TestParseMainLoopStuckDetection(t *testing.T) {
	// Create a parser with tokens that hit the default case, which always advances.
	// The stuck guard at line 99 fires when p.pos == startPos after the switch.
	// Since all switch cases either advance or return, this guard is defensive.
	// We test the boundary: unexpected token at top level triggers default + advance.
	p := New([]token.Token{
		{Type: token.RParen, Val: ")"},
		{Type: token.EOF},
	})
	file, errs := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(errs) == 0 {
		t.Error("expected errors for unexpected token")
	}
}

func TestParseMainLoopCommaToken(t *testing.T) {
	// The stuck guard at line 99 fires when p.pos == startPos after the switch.
	// All switch cases either advance or return. The guard is truly unreachable.
	// We test the adjacent default-case error path with a comma token.
	p := New([]token.Token{
		{Type: token.Comma, Val: ","},
		{Type: token.EOF},
	})
	file, errs := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(errs) == 0 {
		t.Error("expected errors")
	}
}

func TestParseExpectIdentError(t *testing.T) {
	// Test expectIdent error: override without api produces an error via expectIdent
	// The "override" consumes one token, then the parser checks for "api" and errors
	// This covers the error path in expectIdent indirectly
	input := `override fn test(): Int`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected parser errors for override without api")
	}
}

func TestParseOverrideNonApi(t *testing.T) {
	// override followed by non-api should produce error
	input := `override model Foo {
  name: String
}`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected error for 'override' without 'api'")
	}
}

func TestParseUnexpectedTopLevelToken(t *testing.T) {
	input := `12345`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected error for unexpected top-level token")
	}
}

func TestParseCurrentEOF(t *testing.T) {
	// Parse an empty input - exercises current() EOF check
	input := ``
	file := parse(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

func TestParsePeekEOF(t *testing.T) {
	// Single token then EOF - exercises peek() EOF check
	input := `api getUser(id: Int): User`
	file := parse(t, input)
	if len(file.APIs) != 1 {
		t.Fatalf("expected 1 api, got %d", len(file.APIs))
	}
}

// ========== Additional coverage tests ==========

// Test current() and peek() EOF guard by creating a parser with an empty token list.
func TestCurrentAndPeekEOFGuard(t *testing.T) {
	p := New([]token.Token{})
	tok := p.current()
	if tok.Type != token.EOF {
		t.Errorf("expected EOF from current(), got %s", tok.Type)
	}
	ptok := p.peek()
	if ptok.Type != token.EOF {
		t.Errorf("expected EOF from peek(), got %s", ptok.Type)
	}
}

// Test peek() EOF guard when only one token exists.
func TestPeekSingleToken(t *testing.T) {
	p := New([]token.Token{{Type: token.Ident, Val: "x"}})
	ptok := p.peek()
	if ptok.Type != token.EOF {
		t.Errorf("expected EOF from peek() with single token, got %s", ptok.Type)
	}
}

// Test expectIdent error path: when a non-ident, non-keyword token is at current position.
// expectIdent panics with bailout for error recovery; verify the error is recorded.
func TestExpectIdentErrorPathDirect(t *testing.T) {
	p := New([]token.Token{
		{Type: token.Int, Val: "42"},
		{Type: token.EOF},
	})
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		p.expectIdent()
	}()
	if !panicked {
		t.Error("expected expectIdent to panic with bailout")
	}
	if len(p.errors) == 0 {
		t.Error("expected error from expectIdent")
	}
}

// ========== Compound Assignment Operators ==========

func TestParseCompoundAssignStmt(t *testing.T) {
	tests := []struct {
		name string
		code string
		op   string
	}{
		{"PlusAssign", "x += 1", "+="},
		{"MinusAssign", "x -= 1", "-="},
		{"StarAssign", "x *= 2", "*="},
		{"SlashAssign", "x /= 2", "/="},
		{"PercentAssign", "x %= 3", "%="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := `api test(): Int {
  val x = 10
  ` + tt.code + `
  x
}`
			file := parse(t, input)
			api := file.APIs[0]
			assign, ok := api.Body.Stmts[1].(*ast.AssignStmt)
			if !ok {
				t.Fatalf("expected AssignStmt, got %T", api.Body.Stmts[1])
			}
			if assign.Op != tt.op {
				t.Errorf("expected op %q, got %q", tt.op, assign.Op)
			}
		})
	}
}

func TestParseMemberAssignStmt(t *testing.T) {
	input := `api test(): Int {
  user.name = "John"
  user.score += 10
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	assign1, ok := api.Body.Stmts[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", api.Body.Stmts[0])
	}
	if assign1.Op != "=" {
		t.Errorf("expected op '=', got %q", assign1.Op)
	}
	member, ok := assign1.Target.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr target, got %T", assign1.Target)
	}
	if member.Field != "name" {
		t.Errorf("expected field 'name', got %q", member.Field)
	}

	assign2, ok := api.Body.Stmts[1].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("expected AssignStmt, got %T", api.Body.Stmts[1])
	}
	if assign2.Op != "+=" {
		t.Errorf("expected op '+=', got %q", assign2.Op)
	}
}

func TestParseModelScope(t *testing.T) {
	input := `model Post {
  title: String
  scope published = where(status == "PUBLISHED")
}`
	file := parse(t, input)
	model := file.Models[0]
	if len(model.Scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(model.Scopes))
	}
}

func TestParseModelComputedField(t *testing.T) {
	input := `model Post {
  title: String
  val commentCount: Int get @count
}`
	file := parse(t, input)
	model := file.Models[0]
	// regular field + computed field
	if len(model.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(model.Fields))
	}
}

func TestParseVarStmt(t *testing.T) {
	input := `api test(): Int {
  var x = 1
  x += 2
  x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	if valStmt.Name != "x" {
		t.Errorf("expected 'x', got %q", valStmt.Name)
	}
	if !valStmt.Mutable {
		t.Error("expected Mutable = true for var")
	}
}

func TestParseValIsImmutable(t *testing.T) {
	input := `api test(): Int {
  val x = 1
  x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	if valStmt.Mutable {
		t.Error("expected Mutable = false for val")
	}
}

func TestParseGlobalValVar(t *testing.T) {
	input := `val APP_NAME = "test"
var counter = 0`
	file := parse(t, input)
	if len(file.Globals) != 2 {
		t.Fatalf("expected 2 globals, got %d", len(file.Globals))
	}
	if file.Globals[0].Mutable {
		t.Error("expected val to be immutable")
	}
	if !file.Globals[1].Mutable {
		t.Error("expected var to be mutable")
	}
}

func TestParseModelCommentInBody(t *testing.T) {
	// exercises the comment-skipping branch in parseModel
	input := `model Post {
  // this is a comment inside model body
  title: String
  // another comment
  content: String
}`
	file := parse(t, input)
	model := file.Models[0]
	if len(model.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(model.Fields))
	}
}

func TestParseEmitStmtNoArgs(t *testing.T) {
	input := `event UserCreated(user: String)
api test(): Int {
  emit UserCreated
  0
}`
	file := parse(t, input)
	api := file.APIs[0]
	emitStmt, ok := api.Body.Stmts[0].(*ast.EmitStmt)
	if !ok {
		t.Fatalf("expected EmitStmt, got %T", api.Body.Stmts[0])
	}
	if emitStmt.EventName != "UserCreated" {
		t.Errorf("expected event name 'UserCreated', got %q", emitStmt.EventName)
	}
	if len(emitStmt.Args) != 0 {
		t.Errorf("expected 0 args, got %d", len(emitStmt.Args))
	}
}

func TestParseEmitStmtWithArgs(t *testing.T) {
	input := `event OrderCreated(orderId: Int)
api test(): Int {
  emit OrderCreated(orderId: 42)
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
	if len(emitStmt.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(emitStmt.Args))
	}
	if emitStmt.Args[0].Name != "orderId" {
		t.Errorf("expected arg name 'orderId', got %q", emitStmt.Args[0].Name)
	}
}

func TestExpectIdentOrKeyword(t *testing.T) {
	// "use model { Base }" — "model" is a keyword used as identifier
	input := `use model { Base }`
	file := parse(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	// should parse without error — "model" used as module name
}

func TestExpectIdentOrKeywordEvent(t *testing.T) {
	// "use event { PostCreated }" — "event" is a keyword used as module name
	input := `use event { PostCreated }`
	file := parse(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

func TestExpectIdentOrKeywordError(t *testing.T) {
	// "use 123" — number is not ident or keyword, should trigger error branch
	input := `use 123`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Error("expected parser errors for 'use 123'")
	}
	foundExpected := false
	for _, e := range errs {
		if strings.Contains(e.Message, "expected identifier") {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Errorf("expected 'expected identifier' error, got: %v", errs)
	}
}
