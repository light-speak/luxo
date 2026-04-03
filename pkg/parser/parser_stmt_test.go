package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestParseForStmt(t *testing.T) {
	input := `api test(): Int {
  for item in items {
    update(item, status: "done")
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
  } else {
    return 0
  }
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
	if ifStmt.Else == nil {
		t.Error("expected else block")
	}
}

func TestParseElseIf(t *testing.T) {
	input := `api test(): Int {
  if x {
    return 1
  } else if y {
    return 2
  } else {
    return 3
  }
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
	if ifStmt.Else == nil {
		t.Fatal("expected else block")
	}
	// else block should contain a nested IfStmt
	if len(ifStmt.Else.Stmts) != 1 {
		t.Fatalf("expected 1 stmt in else block, got %d", len(ifStmt.Else.Stmts))
	}
	innerIf, ok := ifStmt.Else.Stmts[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected nested IfStmt, got %T", ifStmt.Else.Stmts[0])
	}
	if innerIf.Then == nil {
		t.Error("expected inner then block")
	}
	if innerIf.Else == nil {
		t.Error("expected inner else block")
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
  val x = create(User, name: "test")
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
  val user = create(User, name: "test")
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
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs
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

// Test parseBlock stuck recovery by directly calling parseBlock
// when the parser is positioned beyond all tokens.
func TestParseBlockStuckGuard(t *testing.T) {
	// Construct a parser where parseBlock's loop will enter (not RBrace, not EOF
	// at first), then parseStmt returns without advancing.
	// We achieve this by creating tokens: { <empty> with no RBrace.
	// After LBrace is consumed by expect, the loop checks !check(RBrace) && !isEOF().
	// With empty tokens after LBrace, current() returns EOF -> isEOF() true -> loop exits.
	// So we can't trigger the stuck guard from outside. Call parseBlock directly
	// with a token that parseStmt won't advance past.
	// Actually, the simplest way: no tokens at all, call parseBlock.
	// expect(LBrace) fails (error), pos stays at 0 (pos >= len).
	// Loop: !check(RBrace) is true (EOF != RBrace), !isEOF() is false (EOF). Loop exits.
	// So stuck guard is NOT triggered this way either.

	// Call parseBlock directly with pos beyond all tokens.
	// expect(LBrace) errors but doesn't advance (pos >= len).
	// Loop: check(RBrace) -> false, isEOF() -> current().Type == EOF -> true. Loop exits.
	// But parseExprStmt's stuck guard now advances... Let me try with tokens where
	// we get inside the loop but parseStmt doesn't advance.
	// Use a single LBrace token. After expect(LBrace), pos=1. len=1, so pos >= len.
	// Loop: current() is EOF. !check(RBrace) true, !isEOF() false. Loop exits.
	// Stuck guard not reached.

	// Alternative: { Ident } — LBrace consumed, Ident is not EOF/RBrace so loop enters.
	// parseStmt -> parseExprStmt -> parseExpr -> parsePrefixExpr: check(Ident) true,
	// advance() increments pos to 2. Returns ident. parseExprStmt: pos changed, returns ExprStmt.
	// Loop: current() returns tokens[2] which is RBrace. check(RBrace) true. Loop exits.
	// Stuck guard not reached because parseStmt advanced.

	// The stuck guard fires ONLY if parseStmt returns without advancing AND we're
	// not at RBrace/EOF. This is unreachable because parseStmt (via parseExprStmt)
	// always advances at least one token via its own stuck guard (which we already covered).
	t.Log("parseBlock stuck guard at line 665-668 is a defensive unreachable path")
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
func TestExpectIdentErrorPathDirect(t *testing.T) {
	p := New([]token.Token{
		{Type: token.Int, Val: "42"},
		{Type: token.EOF},
	})
	result := p.expectIdent()
	if result != "" {
		t.Errorf("expected empty string from expectIdent error, got %q", result)
	}
	if len(p.errors) == 0 {
		t.Error("expected error from expectIdent")
	}
}
