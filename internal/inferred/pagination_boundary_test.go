package inferred

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestPaginationPartialASTFailsClosed(t *testing.T) {
	query := func() ast.Expr {
		return &ast.CallExpr{TypeTagged: ast.TypeTagged{TypeTag: "Node", ListTypeTag: true}, Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Node"}, Field: "all"}}
	}
	for _, sample := range []struct {
		name       string
		expression ast.Expr
	}{
		{"missing_mapping_body", &ast.ForStmt{Collection: query()}},
		{"transaction_value", &ast.TransactionExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: query()}}}}},
		{"await_value", &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: query()}}}}},
		{"early_return_mapping", &ast.ForStmt{Collection: query(), Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "row"}}}}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			api := &ast.ApiDecl{Directives: []*ast.Directive{{Name: "paginate"}}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: sample.expression}}}}
			if plan, err := PlanPagination(api, map[string]*ast.ModelDecl{"Node": {Name: "Node"}}, nil); err == nil || plan != nil {
				t.Fatalf("unsafe partial AST accepted: %v %v", plan, err)
			}
		})
	}
}

func TestPaginationDestructuringWithoutAwaitBody(t *testing.T) {
	p := paginationAnalysis{scopes: []map[string]pageValue{{"outer": {builder: "Node"}}}}
	p.declaration(&ast.ValStmt{Names: []string{"_", "result"}, Value: &ast.AwaitExpr{}})
	if len(p.scopes) != 1 || len(p.scopes[0]) != 1 || p.lookup("outer").builder != "Node" {
		t.Fatalf("partial await introduced a binding: %v", p.scopes)
	}
}

func TestPaginationTransactionWithoutBodyKeepsOuterScope(t *testing.T) {
	p := paginationAnalysis{scopes: []map[string]pageValue{{"outer": {builder: "Node"}}}, publicReturn: true}
	value := p.expression(&ast.TransactionExpr{})
	if value.owner != nil || len(value.queries) != 0 {
		t.Fatalf("partial transaction acquired pagination lineage: %+v", value)
	}
	if len(p.scopes) != 1 || p.lookup("outer").builder != "Node" || !p.publicReturn {
		t.Fatalf("partial transaction changed its outer scope: %+v", p.scopes)
	}
}

func TestPaginationBuilderMetadataAndMutation(t *testing.T) {
	// Prepared type metadata must not disguise a database list as a manual list.
	root := &ast.Ident{Name: "provided", TypeTagged: ast.TypeTagged{TypeTag: "NodeQueryBuilder"}}
	query := &ast.CallExpr{TypeTagged: ast.TypeTagged{TypeTag: "Node", ListTypeTag: true}, Func: &ast.MemberExpr{Object: root, Field: "all"}}
	api := &ast.ApiDecl{Directives: []*ast.Directive{{Name: "paginate"}}, Body: &ast.Block{Stmts: []ast.Stmt{
		&ast.ForStmt{Body: &ast.Block{}},
		&ast.AssignStmt{Target: &ast.MemberExpr{Object: &ast.Ident{Name: "row"}, Field: "id"}, Value: &ast.Literal{}},
		&ast.ReturnStmt{Value: query},
	}}}
	plan, err := PlanPagination(api, map[string]*ast.ModelDecl{"Node": {Name: "Node"}}, nil)
	if err != nil || len(plan.Queries) != 1 || plan.Queries[0] != query {
		t.Fatalf("typed builder lost pagination: %v %v", plan, err)
	}
	api.Body.Stmts[2] = &ast.ReturnStmt{Value: &ast.YieldExpr{Value: query}}
	if plan, err := PlanPagination(api, map[string]*ast.ModelDecl{"Node": {Name: "Node"}}, nil); err != nil || len(plan.Queries) != 1 {
		t.Fatalf("yield lineage lost: %v %v", plan, err)
	}
}
