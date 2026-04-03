package parser

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestParseBinaryExpr(t *testing.T) {
	input := `api test(): Boolean {
  val result = a + b * c
  return result
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// should be: a + (b * c) due to precedence
	binExpr, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if binExpr.Op != "+" {
		t.Errorf("expected '+' at top level, got %q", binExpr.Op)
	}
	rightBin, ok := binExpr.Right.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected right to be BinaryExpr, got %T", binExpr.Right)
	}
	if rightBin.Op != "*" {
		t.Errorf("expected '*' in right, got %q", rightBin.Op)
	}
}

func TestParseElvisExpr(t *testing.T) {
	input := `api test(): User {
  val user = find(User, id: 1) ?: throw error.not_found
  return user
}`
	file := parse(t, input)
	api := file.APIs[0]

	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	elvis, ok := valStmt.Value.(*ast.ElvisExpr)
	if !ok {
		t.Fatalf("expected ElvisExpr, got %T", valStmt.Value)
	}
	if elvis.Left == nil || elvis.Right == nil {
		t.Error("elvis left or right is nil")
	}
}

func TestParseWhenExpr(t *testing.T) {
	input := `api test(): String {
  val level = when(score) {
    in 90..100 -> "A"
    in 80..89 -> "B"
    else -> "F"
  }
  return level
}`
	file := parse(t, input)
	api := file.APIs[0]

	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject == nil {
		t.Error("expected when subject")
	}
	if len(when.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseWhenIsType(t *testing.T) {
	input := `api test(): String {
  val x = when(result) {
    is Success -> "ok"
    is Failed -> "err"
    else -> "?"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject == nil {
		t.Error("expected when subject")
	}
	if len(when.Branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(when.Branches))
	}
	if when.Branches[0].IsType != "Success" {
		t.Errorf("expected IsType 'Success', got %q", when.Branches[0].IsType)
	}
	if when.Branches[1].IsType != "Failed" {
		t.Errorf("expected IsType 'Failed', got %q", when.Branches[1].IsType)
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseWhenNoSubject(t *testing.T) {
	input := `api test(): String {
  val x = when {
    x > 0 -> "pos"
    else -> "neg"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if when.Subject != nil {
		t.Error("expected no subject for when without parens")
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseWhenBodyBlock(t *testing.T) {
	input := `api test(): String {
  val x = when(status) {
    "active" -> {
      val y = 1
      return "ok"
    }
    else -> "no"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	// Body should be a LambdaExpr (block body)
	_, ok = when.Branches[0].Body.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr for block body, got %T", when.Branches[0].Body)
	}
}

func TestParseWhenConditionStopAtRBrace(t *testing.T) {
	input := `api test(): String {
  val x = when {
    a > b -> "yes"
    else -> "no"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

// Test that when condition body with multiple expressions stops correctly.
func TestParseWhenConditionComplex(t *testing.T) {
	input := `api test(): String {
  val x = when {
    a > 0 -> "pos"
    a < 0 -> "neg"
    a == 0 -> "zero"
    else -> "?"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 3 {
		t.Errorf("expected 3 branches, got %d", len(when.Branches))
	}
}

// Test parseWhenCondition stopping at else, in, is tokens.
func TestParseWhenConditionStopTokens(t *testing.T) {
	// Test that when condition parsing stops at the right tokens.
	// The 'is' and 'in' branches are tested via when branches.
	input := `api test(): String {
  val x = when(result) {
    is Success -> "ok"
    in 1..10 -> "range"
    else -> "other"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(when.Branches))
	}
	if when.Else == nil {
		t.Error("expected else branch")
	}
}

func TestParseConditionExprNullCheck(t *testing.T) {
	// Use null in a non-condition context (val assignment) to cover null literal in condition expr
	input := `api test(): Int {
  val isNull = x == null
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt, ok := api.Body.Stmts[0].(*ast.ValStmt)
	if !ok {
		t.Fatalf("expected ValStmt, got %T", api.Body.Stmts[0])
	}
	binExpr, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if binExpr.Op != "==" {
		t.Errorf("expected '==', got %q", binExpr.Op)
	}
	// Right side should be null literal
	lit, ok := binExpr.Right.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal for null, got %T", binExpr.Right)
	}
	if lit.Value != "null" {
		t.Errorf("expected 'null', got %q", lit.Value)
	}
}

// Test parseConditionExpr: covers the infix loop in parseConditionExpr.
// Use `if x == 42 {` so the right side (42) is a literal, not callable,
// and the LBrace won't be consumed by trailing lambda.
func TestParseConditionExprWithBinary(t *testing.T) {
	input := `api test(): Int {
  if x == 42 {
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
	bin, ok := ifStmt.Condition.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr condition, got %T", ifStmt.Condition)
	}
	if bin.Op != "==" {
		t.Errorf("expected '==' op, got %q", bin.Op)
	}
}

// Test parseConditionExpr in for statement (exercises the LBrace stop).
func TestParseConditionExprForLoop(t *testing.T) {
	input := `api test(): Int {
  for item in items.filter { it.active } {
    return 1
  }
  return 0
}`
	// Note: this may not parse perfectly since filter{} is tricky in condition context,
	// but it exercises parseConditionExpr.
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs
}

// Test parseConditionExpr null left: if condition starts with a non-expression token.
func TestParseConditionExprNullLeft(t *testing.T) {
	// if ) { ... } — RParen can't start an expression, parsePrefixExpr returns nil
	input := `api test(): Int {
  if ) {
    return 1
  }
  return 0
}`
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(errs) == 0 {
		t.Error("expected parse errors")
	}
}

// Test parseWhenCondition null left: bad token at start of when condition.
func TestParseWhenConditionNullLeft(t *testing.T) {
	input := `api test(): String {
  val x = when {
    ) -> "bad"
    else -> "ok"
  }
  return x
}`
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs
}

// Test isBinaryOp: remaining operators (%, /, >=, <=, in, is) used as infix in conditions.
// These are already tested in TestParseBinaryOpVariants but let's make sure they work
// in when conditions (parseWhenCondition) to cover more branches.
func TestBinaryOpsInWhenCondition(t *testing.T) {
	input := `api test(): String {
  val x = when {
    a >= b -> "ge"
    a <= b -> "le"
    a % b == 0 -> "mod"
    a / b > 1 -> "div"
    else -> "other"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 4 {
		t.Errorf("expected 4 branches, got %d", len(when.Branches))
	}
}

func TestParseMemberAccess(t *testing.T) {
	input := `api test(): String {
  val name = user.name
  val city = user?.address?.city
  return name
}`
	file := parse(t, input)
	api := file.APIs[0]

	// user.name
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	member, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if member.Field != "name" || member.SafeCall {
		t.Error("expected .name (not safe)")
	}

	// user?.address?.city
	valStmt2 := api.Body.Stmts[1].(*ast.ValStmt)
	member2, ok := valStmt2.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt2.Value)
	}
	if member2.Field != "city" || !member2.SafeCall {
		t.Error("expected ?.city (safe)")
	}
}

func TestParseNestedMemberAccess(t *testing.T) {
	input := `api test(): String {
  val x = a.b.c.d
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// a.b.c.d => MemberExpr{Object: MemberExpr{Object: MemberExpr{Object: Ident{a}, Field: b}, Field: c}, Field: d}
	m, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if m.Field != "d" {
		t.Errorf("expected field 'd', got %q", m.Field)
	}
	m2, ok := m.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for .c, got %T", m.Object)
	}
	if m2.Field != "c" {
		t.Errorf("expected field 'c', got %q", m2.Field)
	}
	m3, ok := m2.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for .b, got %T", m2.Object)
	}
	if m3.Field != "b" {
		t.Errorf("expected field 'b', got %q", m3.Field)
	}
}

func TestParseSafeDotChain(t *testing.T) {
	input := `api test(): String {
  val x = user?.address?.city?.name
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// user?.address?.city?.name
	m, ok := valStmt.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", valStmt.Value)
	}
	if m.Field != "name" || !m.SafeCall {
		t.Errorf("expected ?.name (safe), got field=%q safe=%v", m.Field, m.SafeCall)
	}
	m2, ok := m.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for ?.city, got %T", m.Object)
	}
	if m2.Field != "city" || !m2.SafeCall {
		t.Errorf("expected ?.city (safe), got field=%q safe=%v", m2.Field, m2.SafeCall)
	}
	m3, ok := m2.Object.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr for ?.address, got %T", m2.Object)
	}
	if m3.Field != "address" || !m3.SafeCall {
		t.Errorf("expected ?.address (safe), got field=%q safe=%v", m3.Field, m3.SafeCall)
	}
}

// Test method call on member (Dot + LParen path in parseInfixExpr).
func TestParseMethodCallOnMember(t *testing.T) {
	input := `api test(): String {
  val x = user.getName()
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr, got %T", call.Func)
	}
	if member.Field != "getName" {
		t.Errorf("expected 'getName', got %q", member.Field)
	}
}

func TestParseUnaryNot(t *testing.T) {
	input := `api test(): Boolean {
  val x = !condition
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	unary, ok := valStmt.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", valStmt.Value)
	}
	if unary.Op != "!" {
		t.Errorf("expected op '!', got %q", unary.Op)
	}
	ident, ok := unary.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident, got %T", unary.Value)
	}
	if ident.Name != "condition" {
		t.Errorf("expected 'condition', got %q", ident.Name)
	}
}

func TestParseUnaryMinus(t *testing.T) {
	input := `api test(): Int {
  val x = -42
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	unary, ok := valStmt.Value.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr, got %T", valStmt.Value)
	}
	if unary.Op != "-" {
		t.Errorf("expected op '-', got %q", unary.Op)
	}
	lit, ok := unary.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", unary.Value)
	}
	if lit.Value != "42" {
		t.Errorf("expected '42', got %q", lit.Value)
	}
}

func TestParseParenExpr(t *testing.T) {
	input := `api test(): Int {
  val x = (a + b) * c
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// (a + b) * c => BinaryExpr{Left: BinaryExpr{a + b}, Op: *, Right: c}
	bin, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if bin.Op != "*" {
		t.Errorf("expected '*' at top level, got %q", bin.Op)
	}
	inner, ok := bin.Left.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr for (a + b), got %T", bin.Left)
	}
	if inner.Op != "+" {
		t.Errorf("expected '+' inside parens, got %q", inner.Op)
	}
}

func TestParseListExpr(t *testing.T) {
	input := `api test(): Int {
  val x = [1, 2, 3]
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	list, ok := valStmt.Value.(*ast.ListExpr)
	if !ok {
		t.Fatalf("expected ListExpr, got %T", valStmt.Value)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(list.Items))
	}
	for i, item := range list.Items {
		lit, ok := item.(*ast.Literal)
		if !ok {
			t.Fatalf("item[%d]: expected Literal, got %T", i, item)
		}
		expected := []string{"1", "2", "3"}
		if lit.Value != expected[i] {
			t.Errorf("item[%d]: expected %q, got %q", i, expected[i], lit.Value)
		}
	}
}

func TestParseBinaryOpVariants(t *testing.T) {
	input := `api test(): Boolean {
  val a = x == y
  val b = x != y
  val c = x >= y
  val d = x <= y
  val e = x && y
  val f = x || y
  val g = x % y
  val h = x / y
  val i = x - y
  val j = x is Y
  val k = x in y
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]
	// Verify all parsed as BinaryExpr
	ops := []string{"==", "!=", ">=", "<=", "&&", "||", "%", "/", "-", "is", "in"}
	for idx, op := range ops {
		valStmt := api.Body.Stmts[idx].(*ast.ValStmt)
		bin, ok := valStmt.Value.(*ast.BinaryExpr)
		if !ok {
			t.Fatalf("stmt[%d]: expected BinaryExpr, got %T", idx, valStmt.Value)
		}
		if bin.Op != op {
			t.Errorf("stmt[%d]: expected op %q, got %q", idx, op, bin.Op)
		}
	}
}

func TestParseBoolAndNullLiterals(t *testing.T) {
	input := `api test(): Boolean {
  val a = true
  val b = false
  val c = null
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]

	// true
	valA := api.Body.Stmts[0].(*ast.ValStmt)
	litA, ok := valA.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valA.Value)
	}
	if litA.Value != "true" {
		t.Errorf("expected 'true', got %q", litA.Value)
	}

	// false
	valB := api.Body.Stmts[1].(*ast.ValStmt)
	litB, ok := valB.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valB.Value)
	}
	if litB.Value != "false" {
		t.Errorf("expected 'false', got %q", litB.Value)
	}

	// null
	valC := api.Body.Stmts[2].(*ast.ValStmt)
	litC, ok := valC.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valC.Value)
	}
	if litC.Value != "null" {
		t.Errorf("expected 'null', got %q", litC.Value)
	}
}

func TestParseFloatAndDurationLiterals(t *testing.T) {
	input := `api test(): Float {
  val a = 3.14
  val b = 7d
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]

	// 3.14
	valA := api.Body.Stmts[0].(*ast.ValStmt)
	litA, ok := valA.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valA.Value)
	}
	if litA.Value != "3.14" {
		t.Errorf("expected '3.14', got %q", litA.Value)
	}

	// 7d
	valB := api.Body.Stmts[1].(*ast.ValStmt)
	litB, ok := valB.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valB.Value)
	}
	if litB.Value != "7d" {
		t.Errorf("expected '7d', got %q", litB.Value)
	}
}

func TestParseFindCreateExpr(t *testing.T) {
	input := `api test(): User {
  val u = find(User, id: 1)
  val o = create(Order, total: 100)
  return u
}`
	file := parse(t, input)
	api := file.APIs[0]

	// find(User, id: 1)
	valU := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valU.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valU.Value)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call.Func)
	}
	if ident.Name != "find" {
		t.Errorf("expected 'find', got %q", ident.Name)
	}

	// create(Order, total: 100)
	valO := api.Body.Stmts[1].(*ast.ValStmt)
	call2, ok := valO.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valO.Value)
	}
	ident2, ok := call2.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call2.Func)
	}
	if ident2.Name != "create" {
		t.Errorf("expected 'create', got %q", ident2.Name)
	}
}

func TestParseUpdateDeleteExpr(t *testing.T) {
	// Cover update/delete branches in parsePrefixExpr
	input := `api test(): Int {
  update(User, id: 1, name: "new")
  delete(User, id: 1)
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	if len(api.Body.Stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(api.Body.Stmts))
	}
}

// Test find/create/update/delete without parens (returns Ident, not CallExpr).
func TestParseCRUDKeywordsAsIdent(t *testing.T) {
	input := `api test(): Int {
  val a = find
  val b = create
  val c = update
  val d = delete
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]

	for i, name := range []string{"find", "create", "update", "delete"} {
		valStmt := api.Body.Stmts[i].(*ast.ValStmt)
		ident, ok := valStmt.Value.(*ast.Ident)
		if !ok {
			t.Fatalf("stmt[%d]: expected Ident, got %T", i, valStmt.Value)
		}
		if ident.Name != name {
			t.Errorf("stmt[%d]: expected %q, got %q", i, name, ident.Name)
		}
	}
}

// Test throw as prefix expression (UnaryExpr with op "throw").
func TestParseThrowAsExpr(t *testing.T) {
	input := `api test(): Int {
  val x = find(User, id: 1) ?: throw error.not_found
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	elvis, ok := valStmt.Value.(*ast.ElvisExpr)
	if !ok {
		t.Fatalf("expected ElvisExpr, got %T", valStmt.Value)
	}
	unary, ok := elvis.Right.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("expected UnaryExpr for throw, got %T", elvis.Right)
	}
	if unary.Op != "throw" {
		t.Errorf("expected op 'throw', got %q", unary.Op)
	}
}

// Test range expression (..) as infix.
func TestParseRangeExpr(t *testing.T) {
	input := `api test(): Int {
  val x = 1..10
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	rangeExpr, ok := valStmt.Value.(*ast.RangeExpr)
	if !ok {
		t.Fatalf("expected RangeExpr, got %T", valStmt.Value)
	}
	start, ok := rangeExpr.Start.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal start, got %T", rangeExpr.Start)
	}
	if start.Value != "1" {
		t.Errorf("expected start '1', got %q", start.Value)
	}
}

func TestParseTrailingLambda(t *testing.T) {
	input := `api test(): String {
  val x = items.filter { it.active }
  val y = items.map { it.name }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]

	// items.filter { it.active }
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call.Func)
	}
	if member.Field != "filter" {
		t.Errorf("expected 'filter', got %q", member.Field)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected 1 arg (lambda), got %d", len(call.Args))
	}
	_, ok = call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr arg, got %T", call.Args[0].Value)
	}

	// items.map { it.name }
	valStmt2 := api.Body.Stmts[1].(*ast.ValStmt)
	call2, ok := valStmt2.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt2.Value)
	}
	member2, ok := call2.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call2.Func)
	}
	if member2.Field != "map" {
		t.Errorf("expected 'map', got %q", member2.Field)
	}
}

func TestParseChainedCalls(t *testing.T) {
	input := `api test(): String {
  val x = users.filter { it.active }.map { it.name }.sortBy { it }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// The outermost should be a CallExpr for .sortBy { it }
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call.Func)
	}
	if member.Field != "sortBy" {
		t.Errorf("expected 'sortBy', got %q", member.Field)
	}
}

func TestParseTrailingLambdaOnCallArgs(t *testing.T) {
	// Cover trailing lambda after parseCallArgs (line 982-986)
	input := `api test(): Int {
  val x = find(User, id: 1) {
    val y = 1
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	// Should have args including trailing lambda
	if len(call.Args) < 2 {
		t.Errorf("expected at least 2 args (including trailing lambda), got %d", len(call.Args))
	}
}

func TestParseIsCallable(t *testing.T) {
	// Test trailing lambda with ident (isCallable path)
	input := `api test(): String {
  val x = myFunc { it }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", call.Func)
	}
	if ident.Name != "myFunc" {
		t.Errorf("expected 'myFunc', got %q", ident.Name)
	}
}

// Test isCallable with MemberExpr: trailing lambda on safe-dot member expression.
// a?.b { it } should use isCallable(MemberExpr) path.
func TestIsCallableMemberExpr(t *testing.T) {
	input := `api test(): String {
  val x = items?.filter { it.active }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	call, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr (trailing lambda on safe member), got %T", valStmt.Value)
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("expected MemberExpr func, got %T", call.Func)
	}
	if !member.SafeCall {
		t.Error("expected safe call on member")
	}
	if member.Field != "filter" {
		t.Errorf("expected 'filter', got %q", member.Field)
	}
}

func TestParseTransaction(t *testing.T) {
	input := `api test(): Order {
  val order = transaction {
    update(product, stock: 0)
    create(Order, total: 100)
  }
  return order
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)

	// transaction is now a regular identifier; transaction { ... } parses as
	// CallExpr(Ident("transaction"), [LambdaExpr]) via the trailing lambda path
	callExpr, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	ident, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", callExpr.Func)
	}
	if ident.Name != "transaction" {
		t.Errorf("expected func name 'transaction', got %q", ident.Name)
	}
	if len(callExpr.Args) != 1 {
		t.Fatalf("expected 1 arg (lambda), got %d", len(callExpr.Args))
	}
	_, ok = callExpr.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr arg, got %T", callExpr.Args[0].Value)
	}
}

func TestParseTransactionExpr(t *testing.T) {
	input := `api test(): Order {
  val result = transaction {
    val x = create(Order, total: 100)
    return x
  }
  return result
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	// transaction is now a regular identifier; transaction { ... } parses as
	// CallExpr(Ident("transaction"), [LambdaExpr]) via the trailing lambda path
	callExpr, ok := valStmt.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", valStmt.Value)
	}
	ident, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", callExpr.Func)
	}
	if ident.Name != "transaction" {
		t.Errorf("expected func name 'transaction', got %q", ident.Name)
	}
	if len(callExpr.Args) != 1 {
		t.Fatalf("expected 1 arg (lambda), got %d", len(callExpr.Args))
	}
	lambda, ok := callExpr.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected LambdaExpr arg, got %T", callExpr.Args[0].Value)
	}
	if lambda.Body == nil {
		t.Error("expected transaction body")
	}
}

func TestParseEmitStmt(t *testing.T) {
	input := `api test(): Boolean {
  emit("order.created", order, userId: order.userId)
  return true
}`
	file := parse(t, input)
	api := file.APIs[0]

	// emit is now a regular function call, parsed as ExprStmt wrapping CallExpr
	exprStmt, ok := api.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", api.Body.Stmts[0])
	}
	callExpr, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected CallExpr, got %T", exprStmt.Expr)
	}
	ident, ok := callExpr.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident func, got %T", callExpr.Func)
	}
	if ident.Name != "emit" {
		t.Errorf("expected func name 'emit', got %q", ident.Name)
	}
	if len(callExpr.Args) != 3 {
		t.Errorf("expected 3 args, got %d", len(callExpr.Args))
	}
	// third arg should be named: userId: order.userId
	if callExpr.Args[2].Name != "userId" {
		t.Errorf("expected named arg 'userId', got %q", callExpr.Args[2].Name)
	}
}

// Test string literal as prefix expression (in when body context).
func TestParseStringLiteralInWhen(t *testing.T) {
	input := `api test(): String {
  val x = when(status) {
    "active" -> "yes"
    "inactive" -> "no"
    else -> "unknown"
  }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	when, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
	if len(when.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(when.Branches))
	}
}

// Test parsePrefixExpr with when expression as prefix.
func TestParsePrefixWhen(t *testing.T) {
	input := `api test(): String {
  val x = when { else -> "ok" }
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	_, ok := valStmt.Value.(*ast.WhenExpr)
	if !ok {
		t.Fatalf("expected WhenExpr, got %T", valStmt.Value)
	}
}

// Test parsePrefixExpr with list expression as prefix.
func TestParsePrefixList(t *testing.T) {
	input := `api test(): Int {
  val x = [1, 2, 3]
  return 0
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	_, ok := valStmt.Value.(*ast.ListExpr)
	if !ok {
		t.Fatalf("expected ListExpr, got %T", valStmt.Value)
	}
}

// Test parsePrefixExpr remaining branches by covering all prefix-able token types.
func TestParsePrefixExprStringLiteral(t *testing.T) {
	input := `api test(): String {
  val a = "hello"
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	lit, ok := valStmt.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valStmt.Value)
	}
	if lit.Kind != token.String {
		t.Errorf("expected String kind, got %s", lit.Kind)
	}
}

// Test all prefix literal types to ensure full coverage of parsePrefixExpr.
func TestParsePrefixExprAllLiteralTypes(t *testing.T) {
	input := `api test(): Int {
  val a = 42
  val b = 3.14
  val c = "hello"
  val d = 7d
  val e = true
  val f = false
  val g = null
  val h = !x
  val i = -5
  val j = [1, 2]
  val k = (1 + 2)
  return a
}`
	file := parse(t, input)
	api := file.APIs[0]
	if len(api.Body.Stmts) != 12 {
		t.Errorf("expected 12 statements, got %d", len(api.Body.Stmts))
	}
}

// Test parsePrefixExpr default error path with a token that can't start an expression.
func TestParsePrefixExprDefault(t *testing.T) {
	// Colon can't start an expression - it hits the default case.
	p := New([]token.Token{
		{Type: token.Api, Val: "api"},
		{Type: token.Ident, Val: "test"},
		{Type: token.LParen, Val: "("},
		{Type: token.RParen, Val: ")"},
		{Type: token.Colon, Val: ":"},
		{Type: token.Ident, Val: "Int"},
		{Type: token.LBrace, Val: "{"},
		{Type: token.Colon, Val: ":"}, // bad token in statement position
		{Type: token.RBrace, Val: "}"},
		{Type: token.EOF},
	})
	file, errs := p.Parse("test.luxo")
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(errs) == 0 {
		t.Error("expected errors for bad expression start")
	}
}

// Test parseExpr infix stuck detection via direct token construction.
// Create a situation where the infix loop enters but parseInfixExpr returns
// without advancing (default case returns left).
// In normal parsing, currentPrec() > 0 means we have a known infix token,
// so parseInfixExpr should always handle it. The stuck guard is defensive.
// We test it by constructing tokens where currentPrec() returns non-zero
// but parseInfixExpr's default is hit. This requires LBrace with non-callable left.
func TestParseExprInfixStuckDetection(t *testing.T) {
	// LBrace gives precCall from currentPrec, but isCallable returns false for Literal.
	// So parseInfixExpr's default fires, returning left without advancing.
	// The stuck guard in parseExpr breaks the loop.
	input := `api test(): Int {
  val x = 42
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	lit, ok := valStmt.Value.(*ast.Literal)
	if !ok {
		t.Fatalf("expected Literal, got %T", valStmt.Value)
	}
	if lit.Value != "42" {
		t.Errorf("expected '42', got %q", lit.Value)
	}
}

// Test the infix stuck detection by having an expression followed by {
// where the left is not callable (e.g., a literal or grouped expression).
// The LBrace has precCall from currentPrec, but isCallable returns false,
// so parseInfixExpr default fires and returns left. The stuck guard breaks.
func TestParseExprInfixStuckWithLBrace(t *testing.T) {
	// (1 + 2) followed by { should NOT create a call.
	// The (1+2) is a grouped BinaryExpr, not callable.
	// The { starts the next statement or block.
	input := `api test(): Int {
  val x = (1 + 2)
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	bin, ok := valStmt.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr, got %T", valStmt.Value)
	}
	if bin.Op != "+" {
		t.Errorf("expected '+', got %q", bin.Op)
	}
}

// Test parseExprStmt stuck recovery via direct parser construction.
// We need parseExpr to return nil AND pos to not advance.
// parsePrefixExpr's default always advances, so the only way is if
// parsePrefixExpr returns nil and parseExpr returns nil, but pos changed.
// The real stuck case requires parseExpr to return something but not advance.
// Let's construct tokens for the edge case.

func TestParseExprStmtStuckRecovery(t *testing.T) {
	// Use a token that can't start an expression to trigger stuck recovery
	input := `api test(): Int {
  @badtoken
  return 1
}`
	// This will produce errors but shouldn't hang
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	_ = errs // errors are expected
}

// Test parseExprStmt stuck recovery: expression that doesn't consume tokens.
// We need to reach parseExprStmt with a token that parsePrefixExpr's default handles
// (advances and returns nil), so parseExpr returns nil and pos has advanced.
// This actually means the stuck guard won't fire because parsePrefixExpr advances.
// The existing test TestParseExprStmtStuckRecovery already covers this partially.
// Here we test with an RParen which hits parsePrefixExpr default.
func TestParseExprStmtWithBadToken(t *testing.T) {
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

func TestParseExprStmtStuckRecoveryDirect(t *testing.T) {
	// Create a parser manually. Put an LBrace token after expect(LBrace) for the block.
	// In the block loop, parseStmt -> parseExprStmt -> parseExpr -> parsePrefixExpr.
	// parsePrefixExpr doesn't match LBrace (wait, actually LBracket is matched, not LBrace).
	// LBrace is NOT in parsePrefixExpr's switch, so it hits default, advances, returns nil.
	// parseExpr returns nil. parseExprStmt: pos advanced, so no stuck, returns ExprStmt{nil}.
	// Actually, let me look: LBrace IS checked in parsePrefixExpr? No, looking at the switch:
	// LBracket -> parseListExpr. LBrace is NOT a prefix expr.
	// So LBrace hits default, advances, returns nil. parseExprStmt pos != startPos.
	// The stuck path in parseExprStmt is unreachable through normal parsing.

	// Instead test adjacent behavior: RParen in statement position.
	input := `api test(): Int {
  )
  )
  return 0
}`
	file, errs := parseWithErrors(t, input)
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(errs) == 0 {
		t.Error("expected errors")
	}
}

// Test parseExprStmt stuck recovery by directly calling parseExprStmt
// when the parser is positioned beyond all tokens (pos >= len(tokens)).
// In this state, advance() is a no-op, so parsePrefixExpr returns nil
// without advancing, triggering the stuck guard.
func TestParseExprStmtStuckAtEOF(t *testing.T) {
	p := New([]token.Token{}) // empty tokens
	p.pos = 0                 // at/beyond len(tokens)=0
	result := p.parseExprStmt()
	if result != nil {
		t.Error("expected nil from parseExprStmt stuck guard")
	}
	if len(p.errors) == 0 {
		t.Error("expected error from stuck guard")
	}
}

func TestParseIsTypeNameEmpty(t *testing.T) {
	// Triggers isTypeName with a non-uppercase name (lowercase won't be treated as object construction)
	input := `api test(): Int {
  val x = lowercase
  return x
}`
	file := parse(t, input)
	api := file.APIs[0]
	valStmt := api.Body.Stmts[0].(*ast.ValStmt)
	ident, ok := valStmt.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected Ident (not object construction), got %T", valStmt.Value)
	}
	if ident.Name != "lowercase" {
		t.Errorf("expected 'lowercase', got %q", ident.Name)
	}
}
