package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Binary Op Type Check Tests ==========

func TestBinaryOpTypeCheck(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = 42 + "hello"
  x
}
`)
	expectError(t, result, "requires numeric types")
}

func TestBinaryOpFloat(t *testing.T) {
	result := analyze(t, `
api test(): Float {
  val x = 42 + 3.14
  x
}
`)
	expectNoErrors(t, result)
	// The result type should be Float when mixing Int and Float
}

func TestNotOperatorOnNonBool(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val x = !42
  x
}
`)
	expectError(t, result, "requires Boolean")
}

func TestComparisonOp(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val x = 42 > 10
  val y = "a" > "b"
  x
}
`)
	expectNoErrors(t, result)
}

func TestStringConcat(t *testing.T) {
	result := analyze(t, `
api test(): String {
  val x = "hello" + " world"
  x
}
`)
	expectNoErrors(t, result)
}

func TestBooleanOps(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val x = true && false
  val y = true || false
  x
}
`)
	expectNoErrors(t, result)
}

func TestBinaryEqualityOp(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val a = 1 == 2
  val b = 1 != 2
  a
}
`)
	expectNoErrors(t, result)
}

func TestBinaryOrderableError(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val x = true > false
  x
}
`)
	expectError(t, result, "requires orderable type")
}

func TestBinaryModuloFloat(t *testing.T) {
	result := analyze(t, `
api test(): Float {
  val x = 10.0 % 3.0
  x
}
`)
	// Modulo with floats should return Float
	expectNoErrors(t, result)
}

func TestBinaryModulo(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = 10 % 3
  x
}
`)
	expectNoErrors(t, result)
}

func TestBinaryIn(t *testing.T) {
	result := analyze(t, `
api test(): Boolean {
  val x = 5 in [1, 2, 3]
  x
}
`)
	// The 'in' binary op should return Boolean
	expectNoErrors(t, result)
}

func TestBinaryIs(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Boolean {
  val x = User.find(id: 1)
  val result = x is User
  result
}
`)
	expectNoErrors(t, result)
}

func TestBinaryInOperatorDirect(t *testing.T) {
	// Use AST directly to exercise the "in" binary op path in checkBinaryOp
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Boolean"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.BinaryExpr{
								Op:    "in",
								Left:  &ast.Literal{Kind: token.Int, Value: "5"},
								Right: &ast.Literal{Kind: token.Int, Value: "10"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestBinaryIsOperatorDirect(t *testing.T) {
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Boolean"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.BinaryExpr{
								Op:    "is",
								Left:  &ast.Ident{Name: "User"},
								Right: &ast.Ident{Name: "User"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestBinaryUnknownOp(t *testing.T) {
	// Exercise the default return nil in checkBinaryOp
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.BinaryExpr{
								Op:    "??",
								Left:  &ast.Literal{Kind: token.Int, Value: "1"},
								Right: &ast.Literal{Kind: token.Int, Value: "2"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// ========== Expression Type Check Tests ==========

func TestValTypeInference(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  val x = 42
  val y = "hello"
  val z = true
  val d = 7d
  User { name: "test" }
}
`)
	expectNoErrors(t, result)
}

func TestUndefinedVariable(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = unknownVar
  x
}
`)
	expectError(t, result, "undefined: 'unknownVar'")
}

func TestMemberAccess(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): String {
  val user = User.find(id: 1)
  user.name
}
`)
	// find returns a non-null User because a missing record raises NotFound.
	if len(result.Errors) > 0 {
		// filter out expected errors
		for _, err := range result.Errors {
			if !strings.Contains(err.Message, "undefined") {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}
}

func TestSafeDotWarning(t *testing.T) {
	result := analyze(t, `
model Address { city: String }
model User {
  name: String
  address: Address
}
api test(): String {
  val user = User { name: "lin", address: Address { city: "NYC" } }
  user?.name
}
`)
	// User is non-nullable, so ?. should produce a warning.
	expectWarning(t, result, "unnecessary safe call")
}

func TestElvisNarrowing(t *testing.T) {
	// use first() which returns nullable, then narrow with ?:
	result := analyze(t, `
model User { name: String }
api test(): String {
  val user = User.where(name == "test").first() ?: throw error.not_found
  user.name
}
`)
	// After ?:, user should be narrowed to non-null
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// Regression: fuzz found nil pointer in ElvisExpr when left type is nil
func TestElvisOnUnresolvedType(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val x = unknownFunc() ?: 0
  x
}
`)
	// should report error for unknownFunc but not panic
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestElvisNarrowingOnIdent(t *testing.T) {
	// Exercise the Elvis narrowing path where Left is an Ident
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name:  "user",
							Value: &ast.Literal{Kind: token.Null, Value: "null"},
						},
						&ast.ExprStmt{
							Expr: &ast.ElvisExpr{
								Left:  &ast.Ident{Name: "user"},
								Right: &ast.Literal{Kind: token.String, Value: "default"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestMemberCallExpr(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val user = User.find(id: 1)
  user.toString()
  42
}
`)
	// member call on user - the field won't be found but it should exercise the CallExpr member path
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCallExprMemberFunc(t *testing.T) {
	// Exercise the CallExpr path where Func is a MemberExpr (not just Ident)
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	userType := a.types["User"]
	userType.Fields["name"] = &FieldInfo{Name: "name", Type: &ResolvedType{Kind: TypeString, Name: "String"}}

	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.MemberExpr{
									Object: &ast.Ident{Name: "User"},
									Field:  "name",
								},
								Args: []*ast.NamedArg{},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCallExprWithKnownFn(t *testing.T) {
	// Exercise the CallExpr path where the fn is an Ident and is found in scope
	a := New()
	a.scope.Define(&Symbol{
		Name: "myFunc",
		Kind: SymFn,
		Type: &ResolvedType{Kind: TypeString, Name: "String"},
	})
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.CallExpr{
								Func: &ast.Ident{Name: "myFunc"},
								Args: []*ast.NamedArg{
									{Value: &ast.Literal{Kind: token.Int, Value: "42"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestWhenExprBranches(t *testing.T) {
	result := analyze(t, `
api test(): String {
  val score = 85
  val grade = when(score) {
    90 -> "A"
    80 -> "B"
    else -> "C"
  }
  grade
}
`)
	expectNoErrors(t, result)
}

func TestWhenExprWithIsBranch(t *testing.T) {
	// when with 'is' branch - exercises the WhenBranch.IsType path
	a := New()
	a.declareType("Dog", TypeModel, token.Position{}, "")
	a.declareType("Cat", TypeModel, token.Position{}, "")
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.WhenExpr{
								Subject: &ast.Ident{Name: "find"},
								Branches: []*ast.WhenBranch{
									{
										Condition: &ast.Literal{Kind: token.Int, Value: "1"},
										Body:      &ast.Literal{Kind: token.String, Value: "one"},
									},
								},
								Else: &ast.Literal{Kind: token.String, Value: "other"},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	// Should not panic
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestForLoopVarType(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
model User { posts: [Post] }
api test(): String {
  val items = User.find(id: 1)
  for post in items {
    val t = post
  }
  "done"
}
`)
	// The for loop variable should be inferred from the collection element type
	// This mainly tests that the analyzer doesn't panic
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestForLoopWithListType(t *testing.T) {
	// Exercise the for loop path where the collection is a list type
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ValStmt{
							Name: "items",
							Value: &ast.ListExpr{
								Items: []ast.Expr{
									&ast.Literal{Kind: token.Int, Value: "1"},
									&ast.Literal{Kind: token.Int, Value: "2"},
								},
							},
						},
						&ast.ForStmt{
							VarName:    "item",
							Collection: &ast.Ident{Name: "items"},
							Body: &ast.Block{
								Stmts: []ast.Stmt{
									&ast.ExprStmt{Expr: &ast.Ident{Name: "item"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestForStmtWithListCollection(t *testing.T) {
	// Exercise the for loop with a collection that has IsList=true
	a := New()
	a.scope.Define(&Symbol{
		Name: "users",
		Kind: SymVariable,
		Type: &ResolvedType{Kind: TypeModel, Name: "User", IsList: true, Fields: map[string]*FieldInfo{
			"name": {Name: "name", Type: &ResolvedType{Kind: TypeString, Name: "String"}},
		}},
	})
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ForStmt{
							VarName:    "user",
							Collection: &ast.Ident{Name: "users"},
							Body: &ast.Block{
								Stmts: []ast.Stmt{
									&ast.ExprStmt{Expr: &ast.Ident{Name: "user"}},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestListAndForExpressionTypeInference(t *testing.T) {
	result := analyze(t, `
api transform(items: [Int]): [Int] {
  val literal = [1, 2, 3]
  val mapped = for item in items { item * 2 }
  mapped
}
`)
	expectNoErrors(t, result)

	body := result.Files[0].APIs[0].Body
	literal := body.Stmts[0].(*ast.ValStmt).Value
	if literal.GetTypeTag() != "Int" || !literal.IsListType() {
		t.Fatalf("list literal type = %s list=%v, want [Int]", literal.GetTypeTag(), literal.IsListType())
	}
	mapped := body.Stmts[1].(*ast.ValStmt).Value
	if mapped.GetTypeTag() != "Int" || !mapped.IsListType() || mapped.IsNullable() {
		t.Fatalf("mapped for type = %s list=%v nullable=%v, want [Int]", mapped.GetTypeTag(), mapped.IsListType(), mapped.IsNullable())
	}
}

func TestYieldForExpressionInfersNullableElementType(t *testing.T) {
	result := analyze(t, `
api findPositive(items: [Int]): Int? {
  val found = for item in items {
    if item > 0 { yield item }
  }
  found
}
`)
	expectNoErrors(t, result)

	forExpr := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ValStmt).Value
	if forExpr.GetTypeTag() != "Int" || forExpr.IsListType() || !forExpr.IsNullable() {
		t.Fatalf("yield for type = %s list=%v nullable=%v, want Int?", forExpr.GetTypeTag(), forExpr.IsListType(), forExpr.IsNullable())
	}
}

func TestListLiteralRejectsMixedElementTypes(t *testing.T) {
	result := analyze(t, `
api invalid(): [Int] {
  val values = [1, "two"]
  values
}
`)
	expectError(t, result, "list element type mismatch")
}

func TestTypedEmptyListUsesDeclaredType(t *testing.T) {
	result := analyze(t, `
api empty(): [String] {
  val values: [String] = []
  values
}
`)
	expectNoErrors(t, result)

	value := result.Files[0].APIs[0].Body.Stmts[0].(*ast.ValStmt).Value
	if value.GetTypeTag() != "String" || !value.IsListType() {
		t.Fatalf("typed empty list = %s list=%v, want [String]", value.GetTypeTag(), value.IsListType())
	}
}

func TestLoadUsesDeclaredPrimaryKeyFieldType(t *testing.T) {
	result := analyze(t, `
model Product {
  sku: String @id
  name: String
}
api invalid(): Product? {
  Product.load(42)
}
`)
	expectError(t, result, "load key expects 'String', got 'Int'")
}

func TestPrimaryKeyRequiresStableScalarType(t *testing.T) {
	for _, source := range []string{
		`model Product { sku: String? @id }`,
		`model Product { sku: [String] @id }`,
		`model Product { ratio: Float @id }`,
	} {
		result := analyze(t, source)
		expectError(t, result, "@id field")
	}
}

func TestIfStmtScoping(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  if true {
    val inner = 42
  }
  val x = inner
  x
}
`)
	// 'inner' is defined inside if block, should not be visible outside
	expectError(t, result, "undefined: 'inner'")
}

func TestIfStmtWithNilBlock(t *testing.T) {
	// Exercise checkBlock with nil Then/Else blocks
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.IfStmt{
							Condition: &ast.Literal{Kind: token.True, Value: "true"},
							Then:      nil, // nil Then block
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestReturnStmt(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  return 42
}
`)
	expectNoErrors(t, result)
}

func TestReturnStmtWithoutValue(t *testing.T) {
	// return with a value via AST (ReturnStmt with nil Value to test nil path)
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Void"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ReturnStmt{Value: nil},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestThrowStmt(t *testing.T) {
	result := analyze(t, `
error NotFound { message: String }
api test(): String {
  throw NotFound
}
`)
	// Should not crash; NotFound is an error type
	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			if !strings.Contains(err.Message, "undefined") {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}
}

func TestThrowStmtInBody(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  throw "something went wrong"
}
`)
	expectNoErrors(t, result)
}

func TestEmitStmtCheck(t *testing.T) {
	result := analyze(t, `
event UserCreated(userId: Int)
api test(): Int {
  emit UserCreated(userId: 42)
  42
}
`)
	expectNoErrors(t, result)
}

// ========== Direct AST Tests for Unreachable Parser Paths ==========

func TestEmitStmtDirect(t *testing.T) {
	// EmitStmt is not currently produced by the parser, but the analyzer supports it.
	// Test it via direct AST construction.
	file := &ast.File{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.EmitStmt{
							Args: []*ast.NamedArg{
								{Value: &ast.Literal{Kind: token.String, Value: "event"}},
								{Name: "data", Value: &ast.Literal{Kind: token.Int, Value: "42"}},
							},
						},
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "0"}},
					},
				},
			},
		},
	}

	a := New()
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestLambdaItVariable(t *testing.T) {
	// Lambda with trailing block syntax: items.filter { it }
	result := analyze(t, `
model Post { title: String }
model User { posts: [Post] }
api test(): Int {
  val items = User.find(id: 1)
  items.filter { val x = it }
  42
}
`)
	// 'it' should be defined inside the lambda scope, no "undefined: 'it'" error
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "undefined: 'it'") {
			t.Error("'it' should be defined inside lambda scope")
		}
	}
}

func TestListExpr(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val items = [1, 2, 3]
  items.size
}
`)
	expectNoErrors(t, result)
}

func TestListExprCheck(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val xs = [1, 2, 3]
  42
}
`)
	expectNoErrors(t, result)
}

func TestRangeExpr(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val r = 1..10
  0
}
`)
	expectNoErrors(t, result)
}

func TestRangeExprCheck(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val r = 1..10
  42
}
`)
	expectNoErrors(t, result)
}

func TestObjectExpr(t *testing.T) {
	result := analyze(t, `
type ObjResult { value: String }
api test(): ObjResult {
  ObjResult { value: "hello" }
}
`)
	expectNoErrors(t, result)
}

func TestObjectExprUnknownType(t *testing.T) {
	result := analyze(t, `
api test(): String {
  MissingResult { value: "hello" }
  "done"
}
`)
	expectError(t, result, "unknown type 'MissingResult'")
}

func TestObjectExprRejectsUnknownField(t *testing.T) {
	result := analyze(t, `
type ObjResult { value: String }
api test(): ObjResult {
  ObjResult { value: "hello", extra: 1 }
}
`)
	expectError(t, result, "has no field 'extra'")
}

func TestObjectExprRejectsDuplicateField(t *testing.T) {
	result := analyze(t, `
type ObjResult { value: String }
api test(): ObjResult {
  ObjResult { value: "hello", value: "again" }
}
`)
	expectError(t, result, "duplicate field 'value'")
}

func TestObjectExprRejectsFieldTypeMismatch(t *testing.T) {
	result := analyze(t, `
type ObjResult { value: String }
api test(): ObjResult {
  ObjResult { value: 42 }
}
`)
	expectError(t, result, "field 'value' expects 'String', got 'Int'")
}

func TestObjectExprRequiresNonNullableTypeFields(t *testing.T) {
	result := analyze(t, `
type ObjResult {
  value: String
  optional: String?
}
api test(): ObjResult {
  ObjResult {}
}
`)
	expectError(t, result, "missing required field 'value'")
}

func TestObjectExprChecksModuleVisibility(t *testing.T) {
	owner := parseFileWithName(t, `model User { id: Int }`, "origin/user.luxo")
	caller := parseFileWithName(t, `
api test(): String {
  User { id: 1 }
  "done"
}
`, "origin/post.luxo")
	result := New().AnalyzeWithModules([]*ast.File{owner, caller})
	expectError(t, result, "type 'User' is from module 'user'")
}

func TestUntypedObjectExprChecksValues(t *testing.T) {
	analyzer := New()
	expr := &ast.ObjectExpr{Fields: []*ast.NamedArg{{
		Name:  "value",
		Value: &ast.Literal{Kind: token.String, Value: "ok"},
	}}}
	if got := analyzer.checkObjectExpr(expr, NewScope()); got != nil {
		t.Fatalf("untyped object resolved to %v", got)
	}
}

func TestTypeAssignability(t *testing.T) {
	stringType := &ResolvedType{Kind: TypeString, Name: "String"}
	intType := &ResolvedType{Kind: TypeInt, Name: "Int"}
	base := &ResolvedType{Kind: TypeInterface, Name: "Base"}
	child := &ResolvedType{Kind: TypeModel, Name: "Child", Parents: []*ResolvedType{base}}

	tests := []struct {
		name     string
		expected *ResolvedType
		actual   *ResolvedType
		want     bool
	}{
		{name: "nil expected", actual: stringType, want: true},
		{name: "nil actual", expected: stringType, want: true},
		{name: "unresolved actual", expected: stringType, actual: &ResolvedType{Kind: TypeUnknown, Name: "Missing"}, want: true},
		{name: "null into nullable", expected: stringType.AsNullable(), actual: &ResolvedType{Kind: TypeUnknown, Name: "null", Nullable: true}, want: true},
		{name: "null into required", expected: stringType, actual: &ResolvedType{Kind: TypeUnknown, Name: "null", Nullable: true}},
		{name: "unresolved expected", expected: &ResolvedType{Kind: TypeUnknown, Name: "T"}, actual: stringType, want: true},
		{name: "nullable into required", expected: stringType, actual: stringType.AsNullable()},
		{name: "list mismatch", expected: stringType.AsList(), actual: stringType},
		{name: "same type", expected: stringType, actual: stringType, want: true},
		{name: "inherited type", expected: base, actual: child, want: true},
		{name: "unrelated type", expected: stringType, actual: intType},
		{
			name:     "matching generic args",
			expected: &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{stringType}},
			actual:   &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{stringType}},
			want:     true,
		},
		{
			name:     "generic arg count mismatch",
			expected: &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{stringType}},
			actual:   &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{stringType, intType}},
		},
		{
			name:     "generic arg type mismatch",
			expected: &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{stringType}},
			actual:   &ResolvedType{Kind: TypeGeneric, Name: "Page", TypeArgs: []*ResolvedType{intType}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTypeAssignable(tt.expected, tt.actual); got != tt.want {
				t.Fatalf("isTypeAssignable() = %v, want %v", got, tt.want)
			}
		})
	}

	if got := formatResolvedType((&ResolvedType{Name: "String", IsList: true, Nullable: true})); got != "[String]?" {
		t.Fatalf("formatResolvedType() = %q", got)
	}
	cycle := &ResolvedType{Name: "Cycle"}
	cycle.Parents = []*ResolvedType{nil, cycle}
	if cycle.inheritsFrom("Missing") {
		t.Fatal("cyclic inheritance reported an unrelated parent")
	}
}

func TestTransactionExprCheck(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): Int {
  val result = transaction {
    User.create(name: "test")
  }
  42
}
`)
	expectNoErrors(t, result)
}

func TestTransactionExprDirect(t *testing.T) {
	// TransactionExpr is not currently produced by the parser.
	file := &ast.File{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.TransactionExpr{
								Body: &ast.Block{
									Stmts: []ast.Stmt{
										&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "1"}},
									},
								},
							},
						},
						&ast.ExprStmt{Expr: &ast.Literal{Kind: token.Int, Value: "0"}},
					},
				},
			},
		},
	}

	a := New()
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTemplateStringExpr(t *testing.T) {
	// Template strings aren't parsed from source in this lexer,
	// so we construct the AST directly and analyze it.
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.TemplateString{
								Parts: []ast.Expr{
									&ast.Literal{Kind: token.String, Value: "hello "},
									&ast.Literal{Kind: token.Int, Value: "42"},
								},
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestTemplateStringDirect(t *testing.T) {
	// TemplateString is not currently produced by the parser.
	file := &ast.File{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.TemplateString{
								Parts: []ast.Expr{
									&ast.Literal{Kind: token.String, Value: "hello "},
									&ast.Literal{Kind: token.String, Value: "world"},
								},
							},
						},
					},
				},
			},
		},
	}

	a := New()
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestNilStmtInBlock(t *testing.T) {
	// Test that nil stmts (from comments parsed as nil) don't panic.
	// We construct AST directly with a nil statement in a block.
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						nil, // simulate comment parsed as nil
						&ast.ExprStmt{
							Expr: &ast.Literal{Kind: token.Int, Value: "1"},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestNilBlockCheckBlock(t *testing.T) {
	// Call checkBlock with a nil block to hit the nil guard
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body:       nil, // nil body should not panic
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCheckBlockNil(t *testing.T) {
	// Exercise the nil block guard directly
	a := New()
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body:       nil, // nil body
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCheckExprNil(t *testing.T) {
	// ExprStmt with nil Expr
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: nil},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCheckExprNilExpr(t *testing.T) {
	// Exercise checkExpr with nil expr directly
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ThrowStmt{Error: nil},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestLiteralFloatType(t *testing.T) {
	result := analyze(t, `
api test(): Float {
  val x = 3.14
  x
}
`)
	expectNoErrors(t, result)
}

func TestDurationLiteral(t *testing.T) {
	result := analyze(t, `
api test(): Duration {
  val d = 7d
  d
}
`)
	expectNoErrors(t, result)
}

func TestNullLiteral(t *testing.T) {
	result := analyze(t, `
api test(): String {
  val x = null
  "hello"
}
`)
	expectNoErrors(t, result)
}

func TestLiteralUnknownKind(t *testing.T) {
	// Exercise the default return nil in literalType
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.Literal{Kind: token.Illegal, Value: "???"},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestTupleTypeResolution(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
model Video { url: String }
api feed(): (Post, Video)
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("feed")
	if sym == nil {
		t.Fatal("expected 'feed' symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil return type")
	}
	if sym.Type.Kind != TypeTuple {
		t.Errorf("expected TypeTuple, got %v", sym.Type.Kind)
	}
	if len(sym.Type.Tuple) != 2 {
		t.Errorf("expected 2 tuple elements, got %d", len(sym.Type.Tuple))
	}
}

func TestStreamTypeResolution(t *testing.T) {
	result := analyze(t, `
model Comment { text: String }
api comments(): Comment @stream @native
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("comments")
	if sym == nil {
		t.Fatal("expected 'comments' symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil return type for @stream api")
	}
	if sym.Type.Name != "Comment" {
		t.Errorf("expected return type name 'Comment', got %q", sym.Type.Name)
	}
}

func TestBuiltinPageType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api listUsers(): Page<User>
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("listUsers")
	if sym == nil {
		t.Fatal("expected 'listUsers' symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil return type")
	}
	if sym.Type.Name != "Page" {
		t.Errorf("expected type name 'Page', got %q", sym.Type.Name)
	}
	if len(sym.Type.TypeArgs) != 1 {
		t.Errorf("expected 1 type arg, got %d", len(sym.Type.TypeArgs))
	}
}

func TestBuiltinCursorType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api listUsers(): Cursor<User>
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("listUsers")
	if sym == nil {
		t.Fatal("expected 'listUsers' symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil return type")
	}
	if sym.Type.Name != "Cursor" {
		t.Errorf("expected type name 'Cursor', got %q", sym.Type.Name)
	}
}

func TestGenericTypeResolution(t *testing.T) {
	result := analyze(t, `
model User { name: String }
type Paginated {
  items: [User]
  total: Int
}
api listUsers(): Paginated<User>
`)
	expectNoErrors(t, result)

	sym := result.Scope.Lookup("listUsers")
	if sym == nil {
		t.Fatal("expected 'listUsers' symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil return type")
	}
	if len(sym.Type.TypeArgs) != 1 {
		t.Errorf("expected 1 type arg, got %d", len(sym.Type.TypeArgs))
	}
}

func TestGenericTypeParam(t *testing.T) {
	result := analyze(t, `
type Paginated<T> {
  items: [T]
  total: Int
  page: Int
}
`)
	expectNoErrors(t, result)

	// T should be registered as a generic type
	tType, ok := result.Types["T"]
	if !ok {
		t.Fatal("expected type T to be registered")
	}
	if tType.Kind != TypeGeneric {
		t.Errorf("expected TypeGeneric, got %v", tType.Kind)
	}
}

func TestAddWarningPath(t *testing.T) {
	// Directly exercise addWarning through safe call on non-null type
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	userType := a.types["User"]
	userType.Fields["name"] = &FieldInfo{
		Name: "name",
		Type: &ResolvedType{Kind: TypeString, Name: "String"},
	}
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.MemberExpr{
								Object:   &ast.Ident{Name: "User"},
								Field:    "name",
								SafeCall: true,
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "unnecessary safe call") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'unnecessary safe call' warning")
	}
}

func TestCheckFieldDirectivesNilType(t *testing.T) {
	// Exercise the nil type guard in checkFieldDirectives
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name: "Test",
				Fields: []*ast.FieldDecl{
					{
						Name: "x",
						Type: nil,
						Directives: []*ast.Directive{
							{Name: "hash"},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestForStmt(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): String {
  val users = User.find(where: name == "x")
  for u in users {
    u.name
  }
  "done"
}
`)
	expectNoErrors(t, result)
}

func TestVarAfterCreate(t *testing.T) {
	result := analyze(t, `
model User { name: String }
type AuthResult {
  token: String
  user: User
}
api test(): AuthResult {
  val user = User.create(name: "test")
  AuthResult { token: "abc", user: user }
}
`)
	for _, e := range result.Errors {
		t.Logf("error: %v", e)
	}
	expectNoErrors(t, result)
}

func TestFnParamTypeResolution(t *testing.T) {
	result := analyze(t, `
model User { name: String }
fn process(user: User, count: Int): String {
  val n = user.name
  return n
}
`)
	expectNoErrors(t, result)
}

func TestApiParamTypeResolution(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api getUser(id: Int, name: String): User {
  val u = User.find(id: id)
  u
}
`)
	expectNoErrors(t, result)
}

// ========== New AST Node Type Checks ==========

func TestAssignStmt(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var x = 1
  x = 2
  x += 3
  x
}
`)
	expectNoErrors(t, result)
}

func TestAssignUndefinedVar(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  y = 2
  y
}
`)
	expectError(t, result, "undefined")
}

func TestBreakStmt(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var i = 0
  for {
    i += 1
    if i >= 10 { break }
  }
  i
}
`)
	expectNoErrors(t, result)
}

func TestContinueStmt(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var sum = 0
  var i = 0
  for i < 10 {
    i += 1
    if i % 2 == 0 { continue }
    sum += i
  }
  sum
}
`)
	expectNoErrors(t, result)
}

func TestContinueUnreachable(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  for {
    continue
    val x = 1
  }
  0
}
`)
	// code after continue should trigger unreachable warning
	if len(result.Warnings) == 0 {
		t.Error("expected unreachable code warning after continue")
	}
}

func TestYieldExpr(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): User {
  val users = User.find(where: name == "test")
  val found = for user in users {
    if user.name == "test" { yield user }
  }
  found
}
`)
	// should not panic, yield creates a value
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestAsyncExpr(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  async {
    val x = 1
  }
  0
}
`)
	expectNoErrors(t, result)
}

func TestAwaitExpr(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  val result = await {
    User.find(id: 1)
  }
  0
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestForConditionLoop(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var count = 0
  for count < 10 {
    count += 1
  }
  count
}
`)
	expectNoErrors(t, result)
}

func TestForInfiniteLoop(t *testing.T) {
	result := analyze(t, `
api test(): Int {
  var i = 0
  for {
    i += 1
    if i >= 5 { break }
  }
  i
}
`)
	expectNoErrors(t, result)
}

func TestDebugChainMethods(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): String {
  val user = User.find(id: id)
  val u1 = user.d
  val u2 = user.i
  val u3 = user.w
  val u4 = user.e
  val chained = user.d.i.w.e
  user.name
}
`)
	expectNoErrors(t, result)
}

func TestDebugChainOnList(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
api test(): [Post] {
  val posts = Post.find(where: title == "x")
  posts.d.filter { it.title == "y" }
}
`)
	expectNoErrors(t, result)
}

func TestMyMethodCall(t *testing.T) {
	result := analyze(t, `
api test(): String @auth {
  val user = my.load(name, role)
  "done"
}
`)
	expectNoErrors(t, result)
}

func TestMyIdAccess(t *testing.T) {
	result := analyze(t, `
api test(): Int @auth {
  my.id
}
`)
	expectNoErrors(t, result)
}

func TestLetScopeFunction(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(id: Int): String {
  val user = User.find(id: id)
  val result = user?.let { "found" }
  result ?: "not found"
}
`)
	expectNoErrors(t, result)
}

// ========== TypeKind.String() Tests ==========

func TestTypeKindString(t *testing.T) {
	tests := []struct {
		kind TypeKind
		want string
	}{
		{TypeModel, "model"},
		{TypeInterface, "interface"},
		{TypeEnum, "enum"},
		{TypeSealed, "sealed"},
		{TypeCustom, "type"},
		{TypeGeneric, "generic"},
		{TypeTuple, "tuple"},
		{TypeQueryBuilder, "query builder"},
		{TypeUnknown, "type"},
		{TypeInt, "type"},
	}
	for _, tt := range tests {
		got := tt.kind.String()
		if got != tt.want {
			t.Errorf("TypeKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
