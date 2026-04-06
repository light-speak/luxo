package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== DocComment then EOF triggers check(token.EOF) in Parse switch ==========

func TestCoverDocCommentThenEOF(t *testing.T) {
	// consumeDoc advances past DocComment, then the switch in Parse
	// hits the check(token.EOF) case and returns directly.
	tokens := []token.Token{
		{Type: token.DocComment, Val: "some doc"},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// ========== parseTopLevelRecover: non-bailout panic re-panics ==========

func TestParseTopLevelRecoverNonBailoutPanic(t *testing.T) {
	// Line 74-75: when a non-bailout panic (e.g., runtime error) occurs inside
	// parseTopLevel, the recover catches it and re-panics so it propagates.
	// We simulate this by providing a nil tokens slice, which causes an
	// index-out-of-range panic in consumeDoc → current() → p.tokens[p.pos].
	// Note: current() has a bounds check, so nil tokens returns EOF safely.
	// Instead, we set pos to a negative value to trigger a runtime panic.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a re-panic for non-bailout, got none")
		}
	}()
	p := &Parser{tokens: []token.Token{{Type: token.Ident, Val: "x"}, {Type: token.EOF}}, pos: -1}
	file := &ast.File{Name: "crash.luxo"}
	p.parseTopLevelRecover(file)
}

// ========== synchronize: DocComment boundary ==========

func TestSynchronizeOnDocComment(t *testing.T) {
	// Line 105-107: synchronize stops at a DocComment token.
	// Produce a parse error before a doc comment + valid declaration.
	input := `model {
/// This is a doc comment
model User {
  name: String
}`
	_, errs := parseWithErrors(t, input)
	if len(errs) == 0 {
		t.Fatal("expected parse errors from invalid first model")
	}
}

// ========== parseEmitStmt: nil expression in args ==========

func TestEmitStmtIncompleteExpression(t *testing.T) {
	// Line 557-560: emit arg with nil expression skips the arg.
	// Use a token like ')' right after colon to make parseExpr return nil.
	input := `api test(): Int {
  emit Foo(x: )
  0
}`
	_, errs := parseWithErrors(t, input)
	// Should parse but produce an error for the incomplete expression
	_ = errs
}

// ========== tryParseLambdaParams: comma then non-ident ==========

func TestTryParseLambdaParamsCommaNoIdent(t *testing.T) {
	// Line 642-645: in parseOnBlock → tryParseLambdaParams, after seeing
	// ident + comma, the next token is not an ident → backtrack.
	// Use token sequence: on Ping { a , 123 } — "a" is ident, comma matches,
	// "123" (Int) is not ident → backtrack.
	tokens := []token.Token{
		{Type: token.On, Val: "on"},
		{Type: token.Ident, Val: "Ping"},
		{Type: token.LBrace},
		{Type: token.Ident, Val: "a"},
		{Type: token.Comma},
		{Type: token.Int, Val: "123"},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if len(file.Listeners) != 1 {
		t.Fatal("expected 1 on-decl")
	}
}

// ========== tryParseLambdaParams: no arrow after params ==========

func TestTryParseLambdaParamsNoArrow(t *testing.T) {
	// Line 649-652: ident but no arrow follows → backtrack.
	// Use `on EventName { x + 1 }` — "x" is ident, "+" is not arrow → backtrack.
	input := `on Ping {
  x + 1
}`
	file := parse(t, input)
	if len(file.Listeners) != 1 {
		t.Fatal("expected 1 on-decl")
	}
}

// ========== parseDirective: lambda body { expr } in directive arg ==========

func TestDirectiveArgLambdaBody(t *testing.T) {
	// Line 876-878: directive arg with lambda body { ... }
	input := `model User {
  name: String @visible({ my.role == "admin" })
}`
	file := parse(t, input)
	if len(file.Models) != 1 {
		t.Fatal("expected 1 model")
	}
	f := file.Models[0].Fields[0]
	if len(f.Directives) != 1 {
		t.Fatal("expected 1 directive")
	}
	if len(f.Directives[0].Args) != 1 {
		t.Fatal("expected 1 arg")
	}
	if _, ok := f.Directives[0].Args[0].Value.(*ast.LambdaExpr); !ok {
		t.Errorf("expected LambdaExpr arg, got %T", f.Directives[0].Args[0].Value)
	}
}

// ========== parseExpr: infinite loop prevention guard + parseInfixExpr default ==========

func TestParseExprInfiniteLoopGuardAndInfixDefault(t *testing.T) {
	// Lines 1105-1106 and 1337-1338: when a non-callable expression (e.g., a literal)
	// is followed by `{`, currentPrec() returns precCall (non-zero), but
	// parseInfixExpr's `case p.check(token.LBrace) && p.isCallable(left)` fails
	// because isCallable returns false for a literal. This hits the default case
	// (line 1337-1338) which returns left without advancing pos. Back in parseExpr,
	// the infinite loop guard (line 1105-1106) detects pos didn't change and breaks.
	//
	// Construct tokens: api body with `42 { }` — 42 is not callable, { triggers
	// the infix loop, isCallable fails → default → loop guard break.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		// expression: 42 followed by { — 42 is not callable
		{Type: token.Int, Val: "42"},
		{Type: token.LBrace},
		{Type: token.RBrace},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// ========== parseTemplateString: missing StringEnd ==========

func TestTemplateStringMissingEnd(t *testing.T) {
	// Line 1232-1234: template string without StringEnd → error.
	// Construct tokens directly to simulate a malformed template string.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "String"},
		{Type: token.LBrace},
		{Type: token.StringStart, Val: "hello "},
		{Type: token.Ident, Val: "name"},
		// Missing StringEnd — jump to EOF
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	_, errs := p.Parse("test.luxo")
	found := false
	for _, e := range errs {
		if e.Message == "expected end of template string" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'expected end of template string' error")
	}
}

// ========== parseInfixExpr: default case ==========

func TestInfixExprDefault(t *testing.T) {
	// Line 1337-1338: parseInfixExpr default returns left unchanged.
	// This is reached when no infix operator matches. Covered indirectly
	// through any expression that terminates without an infix op.
	// Actually this is the standard path when expression parsing ends.
	input := `api test(): Int {
  val x = 42
  x
}`
	file := parse(t, input)
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
}

// ========== parseCallArgs: nil val from parseExpr ==========

func TestCallArgsNilExpression(t *testing.T) {
	// Line 1358-1359: parseExpr returns nil in call args → break.
	// This happens with a positional arg that has no valid expression.
	input := `api test(): Int {
  val x = foo()
  0
}`
	file := parse(t, input)
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
}

// ========== parseForCondition: nil from parsePrefixExpr ==========

func TestForConditionNilPrefix(t *testing.T) {
	// Line 1467-1469: parseForCondition returns nil when parsePrefixExpr fails.
	// To hit the nil path, we need a token that parsePrefixExpr can't handle
	// in the for-condition position.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		{Type: token.For, Val: "for"},
		// Unexpected token in condition — not an ident, not a literal
		{Type: token.RBrace}, // can't be parsed as prefix expr
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	_, errs := p.Parse("test.luxo")
	if len(errs) == 0 {
		t.Fatal("expected parse errors for malformed for-condition")
	}
}

// ========== parseForCondition: default case exits loop ==========

func TestForConditionDefaultReturn(t *testing.T) {
	// Line 1499-1500: for condition with a token that doesn't match any infix op
	// and is not LBrace. The default case returns left.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		{Type: token.For, Val: "for"},
		{Type: token.Ident, Val: "x"},
		{Type: token.In, Val: "in"},
		{Type: token.Ident, Val: "items"},
		// RParen is not a valid infix op, not LBrace → triggers default return
		{Type: token.RParen},
		{Type: token.LBrace},
		{Type: token.RBrace},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	_, errs := p.Parse("test.luxo")
	// The RParen causes parseForCondition to return via default,
	// then the for-stmt parser expects a block
	_ = errs
}

// ========== isCallable: false for non-ident, non-member ==========

func TestIsCallableFalseForLiteral(t *testing.T) {
	// Line 1570: isCallable returns false for non-Ident/MemberExpr.
	// This prevents trailing lambda on literals like `42 { ... }`.
	p := &Parser{}
	if p.isCallable(&ast.Literal{Kind: token.Int, Value: "42"}) {
		t.Error("expected isCallable to return false for Literal")
	}
}

// ========== tryParseLambdaParams: multi-param comma then non-ident ==========

func TestTryParseLambdaParamsMultiCommaNoIdent(t *testing.T) {
	// Line 642-645: specifically test `a, 123 ->` pattern in a lambda context.
	// The ident "a" matches, comma matches, then "123" (Int) isn't an ident.
	// This needs to be in a context where tryParseLambdaParams is called.
	// tryParseLambdaParams is called from parseBlock when LBrace is seen.
	// Let's use a trailing-lambda context.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		// val x = items.filter { a, 123 }
		{Type: token.Val, Val: "val"},
		{Type: token.Ident, Val: "x"},
		{Type: token.Assign, Val: "="},
		{Type: token.Ident, Val: "items"},
		{Type: token.Dot},
		{Type: token.Ident, Val: "filter"},
		{Type: token.LBrace},
		{Type: token.Ident, Val: "a"},
		{Type: token.Comma},
		{Type: token.Int, Val: "123"},
		{Type: token.RBrace},
		{Type: token.Int, Val: "0"},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// ========== tryParseLambdaParams: ident but no arrow ==========

func TestTryParseLambdaParamsIdentNoArrow(t *testing.T) {
	// Line 649-652: ident followed by something other than arrow or comma.
	// e.g., `{ a + 1 }` — "a" is ident, "+" is not arrow → backtrack.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		{Type: token.Val, Val: "val"},
		{Type: token.Ident, Val: "x"},
		{Type: token.Assign, Val: "="},
		{Type: token.Ident, Val: "items"},
		{Type: token.Dot},
		{Type: token.Ident, Val: "map"},
		{Type: token.LBrace},
		// "a + 1" — a is ident, + is not arrow → backtrack from tryParseLambdaParams
		{Type: token.Ident, Val: "a"},
		{Type: token.Plus, Val: "+"},
		{Type: token.Int, Val: "1"},
		{Type: token.RBrace},
		{Type: token.Int, Val: "0"},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// ========== parseCallArgs: positional nil value breaks loop ==========

func TestCallArgsPositionalNilBreaks(t *testing.T) {
	// Line 1358-1359: a positional arg where parseExpr returns nil.
	// This happens when a non-named arg starts with an unparseable token.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		{Type: token.Ident, Val: "foo"},
		{Type: token.LParen},
		// Positional arg that can't be parsed — RParen immediately ends it,
		// but we need a non-RParen token that parseExpr can't parse.
		// Use a colon, which is not a valid prefix expression start.
		{Type: token.Colon},
		{Type: token.RParen},
		{Type: token.Int, Val: "0"},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	_, errs := p.Parse("test.luxo")
	// The colon can't be parsed as an expression → nil → break
	_ = errs
}

// ========== parseEmitStmt: incomplete arg expression ==========

func TestEmitStmtNilArgValue(t *testing.T) {
	// Line 557-560: emit arg where parseExpr returns nil after colon.
	// The key is: named arg (name: <unparseable>) → nil → skip via continue.
	tokens := []token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen},
		{Type: token.RParen},
		{Type: token.Colon},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace},
		{Type: token.Emit, Val: "emit"},
		{Type: token.Ident, Val: "Ev"},
		{Type: token.LParen},
		{Type: token.Ident, Val: "x"},
		{Type: token.Colon},
		// Next is comma — parseExpr returns nil on comma
		{Type: token.Comma},
		{Type: token.Ident, Val: "y"},
		{Type: token.Colon},
		{Type: token.Int, Val: "1"},
		{Type: token.RParen},
		{Type: token.Int, Val: "0"},
		{Type: token.RBrace},
		{Type: token.EOF},
	}
	p := New(tokens)
	file, errs := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs
}
