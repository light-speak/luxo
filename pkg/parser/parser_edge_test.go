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
  roles:    [Role] through UserRole
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
  val exists = find(User, where: email == input.email)
  exists == null ?: throw error.email_exists
  val user = create(User, name: input.name, email: input.email, password: input.password)
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
  scope published {
    val x = 1
  }
  scope hot {
    val y = 2
  }
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
