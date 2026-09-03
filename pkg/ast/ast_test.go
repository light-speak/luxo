package ast

import (
	"testing"

	"github.com/light-speak/luxo/pkg/token"
)

func TestExprGetPos(t *testing.T) {
	pos := token.Position{File: "test.luxo", Line: 10, Col: 5, Offset: 100}

	exprs := []Expr{
		&Literal{Pos: pos},
		&Ident{Pos: pos},
		&MemberExpr{Pos: pos},
		&CallExpr{Pos: pos},
		&BinaryExpr{Pos: pos},
		&UnaryExpr{Pos: pos},
		&ElvisExpr{Pos: pos},
		&WhenExpr{Pos: pos},
		&LambdaExpr{Pos: pos},
		&ListExpr{Pos: pos},
		&ObjectExpr{Pos: pos},
		&TemplateString{Pos: pos},
		&RangeExpr{Pos: pos},
		&TransactionExpr{Pos: pos},
		&YieldExpr{Pos: pos},
		&AsyncExpr{Pos: pos},
		&AwaitExpr{Pos: pos},
		&ForStmt{Pos: pos},
	}

	for _, e := range exprs {
		got := e.GetPos()
		if got != pos {
			t.Errorf("%T.GetPos() = %v, want %v", e, got, pos)
		}
	}
}

func TestStmtGetPos(t *testing.T) {
	pos := token.Position{File: "test.luxo", Line: 3, Col: 1, Offset: 20}

	stmts := []Stmt{
		&ValStmt{Pos: pos},
		&IfStmt{Pos: pos},
		&ForStmt{Pos: pos},
		&ReturnStmt{Pos: pos},
		&ThrowStmt{Pos: pos},
		&EmitStmt{Pos: pos},
		&ExprStmt{Pos: pos},
		&AssignStmt{Pos: pos},
		&BreakStmt{Pos: pos},
		&ContinueStmt{Pos: pos},
		&OnDecl{Pos: pos},
	}

	for _, s := range stmts {
		got := s.GetPos()
		if got != pos {
			t.Errorf("%T.GetPos() = %v, want %v", s, got, pos)
		}
	}
}

func TestExprNodeInterface(t *testing.T) {
	// Verify all Expr types implement the interface marker method.
	exprs := []Expr{
		&Literal{}, &Ident{}, &MemberExpr{}, &CallExpr{},
		&BinaryExpr{}, &UnaryExpr{}, &ElvisExpr{}, &WhenExpr{},
		&LambdaExpr{}, &ListExpr{}, &ObjectExpr{}, &TemplateString{},
		&RangeExpr{}, &TransactionExpr{},
		&YieldExpr{}, &AsyncExpr{}, &AwaitExpr{}, &ForStmt{},
	}
	for _, e := range exprs {
		e.exprNode() // just ensure it compiles and doesn't panic
	}
}

func TestStmtNodeInterface(t *testing.T) {
	stmts := []Stmt{
		&ValStmt{}, &IfStmt{}, &ForStmt{},
		&ReturnStmt{}, &ThrowStmt{}, &EmitStmt{}, &ExprStmt{},
		&AssignStmt{}, &BreakStmt{}, &OnDecl{},
	}
	for _, s := range stmts {
		s.stmtNode()
	}
}

func TestWalkExprsTraversesAllNestedExpressionBlocks(t *testing.T) {
	blockWith := func(name string) *Block {
		return &Block{Stmts: []Stmt{&ExprStmt{Expr: &Ident{Name: name}}}}
	}
	root := &Block{Stmts: []Stmt{
		&ExprStmt{Expr: &RangeExpr{Start: &Ident{Name: "rangeStart"}, End: &Ident{Name: "rangeEnd"}}},
		&ExprStmt{Expr: &TransactionExpr{Body: blockWith("transaction")}},
		&ExprStmt{Expr: &YieldExpr{Value: &Ident{Name: "yield"}}},
		&ExprStmt{Expr: &AsyncExpr{Body: blockWith("async")}},
		&ExprStmt{Expr: &AwaitExpr{Body: blockWith("await")}},
		&ExprStmt{Expr: &ForStmt{
			Collection: &Ident{Name: "collection"},
			Body:       blockWith("forBody"),
		}},
	}}
	seen := make(map[string]bool)
	WalkExprs(root, func(expr Expr) {
		if ident, ok := expr.(*Ident); ok {
			seen[ident.Name] = true
		}
	})
	for _, name := range []string{"rangeStart", "rangeEnd", "transaction", "yield", "async", "await", "collection", "forBody"} {
		if !seen[name] {
			t.Errorf("WalkExprs did not visit %q", name)
		}
	}
}

func TestStructFieldAccessDecls(t *testing.T) {
	m := &ModelDecl{Name: "User", Parents: []string{"Base"}}
	if m.Name != "User" || len(m.Parents) != 1 {
		t.Error("ModelDecl fields not accessible")
	}

	f := &FieldDecl{Name: "email", Doc: "email field"}
	if f.Name != "email" || f.Doc != "email field" {
		t.Error("FieldDecl fields not accessible")
	}

	tr := &TypeRef{Name: "String", Nullable: true, IsList: false}
	if !tr.Nullable || tr.IsList {
		t.Error("TypeRef fields not accessible")
	}

	file := &File{Name: "test.luxo"}
	if file.Name != "test.luxo" {
		t.Error("File.Name not accessible")
	}

	d := &Directive{Name: "cache", Args: []*NamedArg{{Name: "ttl", Value: &Literal{Value: "60"}}}}
	if d.Name != "cache" || len(d.Args) != 1 {
		t.Error("Directive fields not accessible")
	}
}

func TestStructFieldAccessNodes(t *testing.T) {
	wb := &WhenBranch{IsType: "User", Body: &Ident{Name: "x"}}
	if wb.IsType != "User" {
		t.Error("WhenBranch fields not accessible")
	}

	sv := &SealedVariant{Name: "Success"}
	if sv.Name != "Success" {
		t.Error("SealedVariant fields not accessible")
	}

	cf := &ComputedField{}
	if cf.Body != nil {
		t.Error("ComputedField.Body should be nil")
	}

	pd := &ParamDecl{Name: "id"}
	if pd.Name != "id" {
		t.Error("ParamDecl fields not accessible")
	}

	ev := &EventDecl{Name: "UserCreated", Pos: token.Position{Line: 1, Col: 1}}
	if ev.Name != "UserCreated" || ev.GetPos().Line != 1 {
		t.Error("EventDecl fields not accessible")
	}

	on := &OnDecl{EventName: "UserCreated", Native: true, Pos: token.Position{Line: 2, Col: 1}}
	if on.EventName != "UserCreated" || !on.Native || on.GetPos().Line != 2 {
		t.Error("OnDecl fields not accessible")
	}

	es := &EmitStmt{EventName: "UserCreated"}
	if es.EventName != "UserCreated" {
		t.Error("EmitStmt.EventName not accessible")
	}
}
