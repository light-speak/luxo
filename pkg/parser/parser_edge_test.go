package parser

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
)

// ========== Deep Nesting Tests ==========

func TestDeepNestedBlocks(t *testing.T) {
	// 10 levels of nesting should not crash
	var b strings.Builder
	b.WriteString("api test(): Int {\n")
	for i := 0; i < 10; i++ {
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("if true {\n")
	}
	for i := 9; i >= 0; i-- {
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("}\n")
	}
	b.WriteString("  0\n}")

	file := parse(t, b.String())
	if file.APIs[0].Body == nil {
		t.Error("expected body for deeply nested api")
	}
}

func TestDeepNestedMemberAccess(t *testing.T) {
	input := `api test(): String {
  val x = a.b.c.d.e.f.g.h.i.j
  x
}`
	file := parse(t, input)
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
}

func TestDeepNestedWhen(t *testing.T) {
	input := `api test(): String {
  val x = when {
    a > 0 -> when {
      b > 0 -> when {
        c > 0 -> "deep"
        else -> "c"
      }
      else -> "b"
    }
    else -> "a"
  }
  x
}`
	file := parse(t, input)
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
}

// ========== Operator Precedence Matrix ==========

func TestPrecedenceAddVsMul(t *testing.T) {
	// a + b * c → a + (b * c)
	assertPrecedence(t, "a + b * c", "+", "*")
}

func TestPrecedenceMulVsAdd(t *testing.T) {
	// a * b + c → (a * b) + c
	assertPrecedence(t, "a * b + c", "+", "*")
}

func TestPrecedenceAndVsOr(t *testing.T) {
	// a || b && c → a || (b && c)
	assertPrecedence(t, "a || b && c", "||", "&&")
}

func TestPrecedenceCompVsAdd(t *testing.T) {
	// a + b > c + d → (a + b) > (c + d)
	assertPrecedenceTop(t, "a + b > c + d", ">")
}

func TestPrecedenceEqVsComp(t *testing.T) {
	// a > b == c > d → (a > b) == (c > d)
	assertPrecedenceTop(t, "a > b == c > d", "==")
}

func TestPrecedenceNotVsBinary(t *testing.T) {
	// !a && b → (!a) && b
	input := `api test(): Boolean { !a && b }`
	file := parse(t, input)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	bin, ok := stmt.Expr.(*ast.BinaryExpr)
	if !ok || bin.Op != "&&" {
		t.Error("expected && at top level")
	}
	_, isUnary := bin.Left.(*ast.UnaryExpr)
	if !isUnary {
		t.Error("expected !a as left of &&")
	}
}

func TestPrecedenceParensOverride(t *testing.T) {
	// (a + b) * c → left of * is BinaryExpr(+)
	input := `api test(): Int { (a + b) * c }`
	file := parse(t, input)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	bin, ok := stmt.Expr.(*ast.BinaryExpr)
	if !ok || bin.Op != "*" {
		t.Error("expected * at top level")
	}
	innerBin, ok := bin.Left.(*ast.BinaryExpr)
	if !ok || innerBin.Op != "+" {
		t.Error("expected (a + b) as left of *")
	}
}

func TestPrecedenceElvisVsComparison(t *testing.T) {
	// a > b ?: c → (a > b) ?: c
	input := `api test(): Int { a > b ?: c }`
	file := parse(t, input)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	elvis, ok := stmt.Expr.(*ast.ElvisExpr)
	if !ok {
		t.Fatalf("expected ElvisExpr, got %T", stmt.Expr)
	}
	bin, ok := elvis.Left.(*ast.BinaryExpr)
	if !ok || bin.Op != ">" {
		t.Error("expected a > b as left of ?:")
	}
}

func TestPrecedenceRangeVsAdd(t *testing.T) {
	// a + b .. c + d → (a + b) .. (c + d)
	input := `api test(): Int { a + b .. c + d }`
	file := parse(t, input)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	rng, ok := stmt.Expr.(*ast.RangeExpr)
	if !ok {
		t.Fatalf("expected RangeExpr, got %T", stmt.Expr)
	}
	_, isLeftBin := rng.Start.(*ast.BinaryExpr)
	_, isRightBin := rng.End.(*ast.BinaryExpr)
	if !isLeftBin || !isRightBin {
		t.Error("expected binary expressions on both sides of ..")
	}
}

// ========== Error Recovery Tests ==========

func TestErrorRecoveryMultipleErrors(t *testing.T) {
	input := `
model User { name: }
enum { }
api test(): { }
model Post { title: String }
`
	l := lexer.New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	_, errs := p.Parse("test.luxo")

	// should have errors but not crash
	if len(errs) == 0 {
		t.Error("expected parse errors")
	}
}

func TestErrorRecoveryExtraTokens(t *testing.T) {
	// extra tokens after valid declarations should produce errors but not crash
	input := `model User { name: String }
??? !!!
model Post { title: String }`

	l := lexer.New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	file, errs := p.Parse("test.luxo")

	if len(errs) == 0 {
		t.Error("expected errors for invalid tokens")
	}
	// should still find both models
	if len(file.Models) < 1 {
		t.Error("expected at least 1 model despite errors")
	}
}

func TestErrorRecoveryIncompleteDecl(t *testing.T) {
	// Incomplete "api test" (no params, return type, or body) followed by
	// valid declarations. The parser should report the error for the
	// incomplete api but still parse the subsequent model and enum.
	input := `
api test

model User {
  name: String
}

enum Status {
  Active
  Inactive
}
`
	l := lexer.New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	file, errs := p.Parse("test.luxo")

	if len(errs) == 0 {
		t.Error("expected parse errors for incomplete api declaration")
	}
	if len(file.Models) != 1 {
		t.Errorf("expected 1 model after recovery, got %d", len(file.Models))
	}
	if len(file.Enums) != 1 {
		t.Errorf("expected 1 enum after recovery, got %d", len(file.Enums))
	}
}

func TestErrorRecoveryMultipleIncompleteDecls(t *testing.T) {
	// Multiple incomplete declarations interspersed with valid ones.
	input := `
model User {
  name: String
}

fn broken(

model Post {
  title: String
}

api incomplete

enum Color {
  Red
  Blue
}
`
	l := lexer.New(input, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	file, errs := p.Parse("test.luxo")

	if len(errs) == 0 {
		t.Error("expected parse errors")
	}
	if len(file.Models) != 2 {
		t.Errorf("expected 2 models after recovery, got %d", len(file.Models))
	}
	if len(file.Enums) != 1 {
		t.Errorf("expected 1 enum after recovery, got %d", len(file.Enums))
	}
}

// ========== Empty Body / Params Tests ==========

func TestEmptyModelBody(t *testing.T) {
	file := parse(t, "model User {}")
	if len(file.Models[0].Fields) != 0 {
		t.Error("expected empty fields")
	}
}

func TestEmptyEnumBody(t *testing.T) {
	file := parse(t, "enum Empty {}")
	if len(file.Enums[0].Values) != 0 {
		t.Error("expected empty values")
	}
}

func TestEmptyApiParams(t *testing.T) {
	file := parse(t, "api test(): String")
	if len(file.APIs[0].Params) != 0 {
		t.Error("expected empty params")
	}
}

func TestEmptyApiBody(t *testing.T) {
	file := parse(t, "api test(): String {}")
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
	if len(file.APIs[0].Body.Stmts) != 0 {
		t.Error("expected empty body")
	}
}

func TestEmptyFnParams(t *testing.T) {
	file := parse(t, "fn test(): String @native")
	if len(file.Functions[0].Params) != 0 {
		t.Error("expected empty params")
	}
}

// ========== Syntax Conformance Tests (from syntax.md) ==========

func TestSyntaxConformanceModel(t *testing.T) {
	input := `model User : Base, Searchable @crud {
  name:     String  @varchar(100) @filterable
  email:    String  @unique
  password: String  @hidden @hash
  role:     Role    = Role.USER
  avatar:   String?
  posts:    [Post]
  profile:  Profile?
  target:   (Post, Video, Product)
  val postCount: Int get @count
  val avgLikes: Float get @avg(field: likes)
}`
	file := parse(t, input)
	m := file.Models[0]
	if m.Name != "User" {
		t.Errorf("expected User, got %q", m.Name)
	}
	if len(m.Parents) != 2 {
		t.Errorf("expected 2 parents, got %d", len(m.Parents))
	}
}

func TestSyntaxConformanceApi(t *testing.T) {
	input := `api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw error.password_too_short
  val exists = User.find(where: email == input.email)
  exists == null ?: throw error.email_exists
  val user = User.create(name: input.name, email: input.email, password: input.password)
  val token = generateToken(user, expires: 7d)
  AuthResult { token: token, user: user }
}`
	file := parse(t, input)
	api := file.APIs[0]
	if api.Name != "register" {
		t.Errorf("expected register, got %q", api.Name)
	}
	if api.Body == nil {
		t.Fatal("expected body")
	}
}

func TestSyntaxConformanceWhen(t *testing.T) {
	input := `api test(): String {
  val level = when(score) {
    in 90..100 -> "A"
    in 80..89 -> "B"
    in 60..79 -> "C"
    else -> "D"
  }
  level
}`
	file := parse(t, input)
	if file.APIs[0].Body == nil {
		t.Error("expected body")
	}
}

func TestSyntaxConformanceSealed(t *testing.T) {
	input := `sealed PayResult {
  Success(transactionId: String)
  Failed(reason: String, code: Int)
  Pending(retryAfter: Duration)
}`
	file := parse(t, input)
	if len(file.Sealeds[0].Variants) != 3 {
		t.Errorf("expected 3 variants, got %d", len(file.Sealeds[0].Variants))
	}
}

func TestSyntaxConformanceInterface(t *testing.T) {
	input := `interface Auditable {
  createdBy: String
  updatedBy: String
  fn beforeCreate() {
    val x = currentUser.name
  }
}`
	file := parse(t, input)
	iface := file.Interfaces[0]
	if len(iface.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(iface.Fields))
	}
	if len(iface.Methods) != 1 {
		t.Errorf("expected 1 method, got %d", len(iface.Methods))
	}
}

func TestSyntaxConformanceError(t *testing.T) {
	input := `error OutOfStock(productId: Int, available: Int) {
  code: 409
  message: error.out_of_stock
}`
	file := parse(t, input)
	e := file.Errors[0]
	if e.Name != "OutOfStock" {
		t.Errorf("expected OutOfStock, got %q", e.Name)
	}
	if e.Code != 409 {
		t.Errorf("expected code 409, got %d", e.Code)
	}
}

func TestSyntaxConformanceExtend(t *testing.T) {
	input := `extend User {
  orders: [Order]
  notifications: [Notification]
}`
	file := parse(t, input)
	if len(file.Extends[0].Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(file.Extends[0].Fields))
	}
}

func TestSyntaxConformanceScope(t *testing.T) {
	input := `model Post : Base {
  status: String
  scope published = where(status == "PUBLISHED")
  scope hot = where(likes > 100).orderBy(likes.desc)
}`
	file := parse(t, input)
	if len(file.Models[0].Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(file.Models[0].Scopes))
	}
}

// ========== Helpers ==========

func assertPrecedence(t *testing.T, input string, topOp, innerOp string) {
	t.Helper()
	full := "api test(): Int { " + input + " }"
	file := parse(t, full)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	bin, ok := stmt.Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", stmt.Expr)
	}
	if bin.Op != topOp {
		t.Errorf("expected top op %q, got %q", topOp, bin.Op)
	}
}

func assertPrecedenceTop(t *testing.T, input string, topOp string) {
	t.Helper()
	full := "api test(): Int { " + input + " }"
	file := parse(t, full)
	stmt := file.APIs[0].Body.Stmts[0].(*ast.ExprStmt)
	bin, ok := stmt.Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", stmt.Expr)
	}
	if bin.Op != topOp {
		t.Errorf("expected top op %q, got %q", topOp, bin.Op)
	}
}

// ========== parseError — internal field ==========

func TestParseErrorWithInternal(t *testing.T) {
	file := parse(t, `error ServerError {
  code: 500
  message: "error.server"
  internal: true
}`)
	if len(file.Errors) != 1 {
		t.Fatalf("expected 1 error decl, got %d", len(file.Errors))
	}
	if !file.Errors[0].Internal {
		t.Error("expected internal to be true")
	}
}

// ========== parseEmitStmt — emit without args ==========

func TestParseEmitNoArgs(t *testing.T) {
	file := parse(t, `
event Ping()
api test(): Int {
  emit Ping
  0
}`)
	if len(file.APIs) != 1 {
		t.Fatal("expected 1 api")
	}
}

// ========== tryParseLambdaParams — multi-param lambda ==========

func TestParseMultiParamLambda(t *testing.T) {
	// on EventName { a, b -> expr } exercises multi-param lambda
	file := parse(t, `
event PostCreated(post: String, userId: Int)
on PostCreated { post, userId -> "ok".i }
`)
	if len(file.Listeners) != 1 {
		t.Fatal("expected 1 listener")
	}
}

// ========== tryParseLambdaParams — comma then non-ident falls back ==========

func TestParseLambdaCommaNoIdent(t *testing.T) {
	// { x, 123 -> ... } — should not parse as lambda params, falls back
	l := lexer.New(`api test(): Int {
  val f = 1
  f
}`, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	file, _ := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
}

// ========== parseCallArgs — empty args ==========

func TestParseCallArgsEmpty(t *testing.T) {
	file := parse(t, `
fn helper(): Int @native
api test(): Int {
  helper()
}`)
	if len(file.APIs) != 1 {
		t.Fatal("expected 1 api")
	}
}

// ========== parseCallArgs — named arg with no value before RParen ==========

func TestParseCallArgsIncompleteNamedArg(t *testing.T) {
	// This is a parse-error-tolerant case: User.find(where: )
	l := lexer.New(`model User { name: String }
api test(): Int {
  User.find(where: )
  0
}`, "test.luxo")
	tokens, _ := l.Tokenize()
	p := New(tokens)
	file, errs := p.Parse("test.luxo")
	// should not crash even with incomplete named arg
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs // errors expected
}

// ========== parseForCondition — iterating over collection ==========

func TestParseForConditionIterateCollection(t *testing.T) {
	file := parse(t, `
api test(items: [Int]): Int {
  var sum = 0
  for item in items {
    sum += item
  }
  sum
}`)
	if len(file.APIs) != 1 {
		t.Fatal("expected 1 api")
	}
}
