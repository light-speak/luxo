package ast

import "testing"

func TestSingleExpressionLambda(t *testing.T) {
	expression := &Ident{Name: "condition"}
	body := &Block{Stmts: []Stmt{&ExprStmt{Expr: expression}}}
	for _, parameter := range []string{"", "row"} {
		lambda := &LambdaExpr{Body: body}
		want := "it"
		if parameter != "" {
			lambda.Params = []string{parameter}
			want = parameter
		}
		if got, binding := lambda.SingleExpression(); got != expression || binding != want {
			t.Fatal(got, binding)
		}
	}
	for _, lambda := range []*LambdaExpr{
		nil, {}, {Body: &Block{}}, {Body: body, Params: []string{"a", "b"}},
		{Body: &Block{Stmts: []Stmt{&ReturnStmt{Value: expression}}}},
		{Body: &Block{Stmts: []Stmt{&ExprStmt{}}}},
		{Body: &Block{Stmts: []Stmt{&ExprStmt{Expr: expression}, &ExprStmt{Expr: expression}}}},
	} {
		if got, binding := lambda.SingleExpression(); got != nil || binding != "" {
			t.Fatal("invalid query lambda", got, binding)
		}
	}
}

func TestWalkVisitsNestedStatementsAndAssignmentTargets(t *testing.T) {
	var expressions, statements int
	emit := &EmitStmt{EventName: "Changed", Args: []*NamedArg{{Value: &Ident{Name: "value"}}}}
	block := &Block{Stmts: []Stmt{
		&AssignStmt{Target: &MemberExpr{Object: &CallExpr{Func: &Ident{Name: "target"}}, Field: "field"}, Value: &Ident{Name: "replacement"}},
		&IfStmt{Condition: &Ident{Name: "condition"}, Then: &Block{Stmts: []Stmt{emit}}},
		&ExprStmt{Expr: &LambdaExpr{Body: &Block{Stmts: []Stmt{emit}}}},
	}}
	visitor := Visitor{
		Expr: func(Expr) { expressions++ },
		Stmt: func(Stmt) { statements++ },
	}
	Walk(block, visitor)
	if expressions != 8 || statements != 5 {
		t.Fatalf("visited expressions/statements = %d/%d, want 8/5", expressions, statements)
	}
	Walk(nil, visitor)
	Walk(block, Visitor{})
}

func TestWalkExprWithoutTemporaryStatement(t *testing.T) {
	root := &ListExpr{Items: []Expr{&Ident{Name: "value"}, &LambdaExpr{Body: &Block{
		Stmts: []Stmt{&ReturnStmt{Value: &Ident{Name: "nested"}}},
	}}}}
	count := 0
	visit := func(Expr) { count++ }
	WalkExpr(root, visit)
	WalkExpr(nil, visit)
	if count != 4 {
		t.Fatalf("visited %d expressions, want 4", count)
	}
	if allocations := testing.AllocsPerRun(100, func() { WalkExpr(root, visit) }); allocations != 0 {
		t.Fatalf("expression traversal allocated %v times", allocations)
	}
}

func TestExpressionModelTag(t *testing.T) {
	var expression Expr = &Ident{}
	if expression.IsModelType() {
		t.Fatal("zero expression has a model tag")
	}
	expression.SetModelType(true)
	if !expression.IsModelType() {
		t.Fatal("model tag was not retained")
	}
}

func TestExpressionTypeTags(t *testing.T) {
	var expression Expr = &Ident{}
	if expression.GetTypeTag() != "" || expression.IsNullable() || expression.IsListType() {
		t.Fatal("zero expression has type metadata")
	}
	expression.SetTypeTag("User")
	expression.SetNullable(true)
	expression.SetListType(true)
	expression.SetModelType(true)
	if expression.GetTypeTag() != "User" || !expression.IsNullable() || !expression.IsListType() || !expression.IsModelType() {
		t.Fatal("type metadata was not retained independently")
	}
	expression.SetNullable(false)
	if expression.IsNullable() || expression.GetTypeTag() != "User" || !expression.IsListType() || !expression.IsModelType() {
		t.Fatal("clearing nullable changed unrelated metadata")
	}
	expression.SetListType(false)
	expression.SetModelType(false)
	expression.SetTypeTag("")
	if expression.GetTypeTag() != "" || expression.IsNullable() || expression.IsListType() || expression.IsModelType() {
		t.Fatal("type metadata was not reset")
	}
}

func TestWalkExpressionShapes(t *testing.T) {
	leaf := &Ident{Name: "leaf"}
	body := &Block{Stmts: []Stmt{&ExprStmt{Expr: leaf}}}
	for _, test := range []struct {
		expression Expr
		leaves     int
	}{
		{&MemberExpr{Object: leaf}, 1},
		{&CallExpr{Func: leaf, Args: []*NamedArg{{Value: leaf}}}, 2},
		{&BinaryExpr{Left: leaf, Right: leaf}, 2},
		{&UnaryExpr{Value: leaf}, 1},
		{&ElvisExpr{Left: leaf, Right: leaf}, 2},
		{&BangElvisExpr{Left: leaf, Right: leaf}, 2},
		{&LambdaExpr{Body: body}, 1},
		{&ListExpr{Items: []Expr{leaf}}, 1},
		{&ObjectExpr{Fields: []*NamedArg{{Value: leaf}}}, 1},
		{&WhenExpr{Subject: leaf, Branches: []*WhenBranch{{Condition: leaf, Body: leaf}}, Else: leaf}, 4},
		{&TemplateString{Parts: []Expr{leaf}}, 1},
		{&RangeExpr{Start: leaf, End: leaf}, 2},
		{&TransactionExpr{Body: body}, 1},
		{&YieldExpr{Value: leaf}, 1},
		{&AsyncExpr{Body: body}, 1},
		{&AwaitExpr{Body: body}, 1},
		{&ForStmt{Collection: leaf, Body: body}, 2},
		{nil, 0},
	} {
		count := 0
		WalkExprs(&Block{Stmts: []Stmt{&ExprStmt{Expr: test.expression}}}, func(expression Expr) {
			if expression == leaf {
				count++
			}
		})
		if count != test.leaves {
			t.Fatalf("%T visited %d leaves, want %d", test.expression, count, test.leaves)
		}
	}
}

func TestWalkStatementShapes(t *testing.T) {
	leaf := &Ident{}
	block := &Block{Stmts: []Stmt{
		nil, &ValStmt{Value: leaf}, &ReturnStmt{Value: leaf}, &ThrowStmt{Error: leaf},
		&ForStmt{Collection: leaf}, &BreakStmt{}, &ContinueStmt{},
	}}
	count := 0
	WalkExprs(block, func(Expr) { count++ })
	if count != 4 {
		t.Fatalf("statement values visited %d times, want 4", count)
	}
}
