package semantic

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Query Builder — Model chain query syntax ==========

// helper: build a File with an api body containing a single expression statement.
func queryBuilderFile(expr ast.Expr) *ast.File {
	return &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name: "Order",
				Pos:  token.Position{File: "test.luxo", Line: 1},
				Fields: []*ast.FieldDecl{
					{Name: "total", Type: &ast.TypeRef{Name: "Float"}},
					{Name: "status", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
		APIs: []*ast.ApiDecl{
			{
				Name:       "testApi",
				ReturnType: &ast.TypeRef{Name: "Int"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{Expr: expr},
					},
				},
			},
		},
	}
}

func TestQueryBuilderWhereOnModel(t *testing.T) {
	// Order.where(...) should not produce errors
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderChainWhereSum(t *testing.T) {
	// Order.where(...).sum(total) — chain: where returns query builder, sum returns Float
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "sum",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderGroupBySelect(t *testing.T) {
	// Order.groupBy(status).select { ... }
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "groupBy",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "select",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderFirst(t *testing.T) {
	// Order.where(...).first() returns Order?
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "first",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderAll(t *testing.T) {
	// Order.all() returns [Order]
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "all",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderCount(t *testing.T) {
	// Order.count() returns Int
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "count",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderAvg(t *testing.T) {
	// Order.avg(total) returns Float
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "avg",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderMinMax(t *testing.T) {
	// Order.min(total) and Order.max(total) return Float
	for _, method := range []string{"min", "max"} {
		t.Run(method, func(t *testing.T) {
			expr := &ast.MemberExpr{
				Object: &ast.Ident{Name: "Order"},
				Field:  method,
			}
			a := New()
			file := queryBuilderFile(expr)
			result := a.Analyze([]*ast.File{file})
			expectNoErrors(t, result)
		})
	}
}

func TestQueryBuilderOrderByLimit(t *testing.T) {
	// Order.orderBy(total).limit(10) — chaining
	inner := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "orderBy",
	}
	expr := &ast.MemberExpr{
		Object: inner,
		Field:  "limit",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderOffset(t *testing.T) {
	// Order.offset(10) — returns query builder
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "offset",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderInvalidMethod(t *testing.T) {
	// Order.foo() — should produce an error (foo is not a query method or field)
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "foo",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "foo")
}

func TestQueryBuilderTripleChain(t *testing.T) {
	// Order.where(...).orderBy(total).first() — triple chain
	step1 := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "where",
	}
	step2 := &ast.MemberExpr{
		Object: step1,
		Field:  "orderBy",
	}
	expr := &ast.MemberExpr{
		Object: step2,
		Field:  "first",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}

func TestQueryBuilderReturnTypes(t *testing.T) {
	// Verify return types of query methods
	a := New()
	a.declareType("Order", TypeModel, token.Position{}, "")
	orderType := a.types["Order"]
	orderType.Fields["total"] = &FieldInfo{Name: "total", Type: a.types["Float"]}

	tests := []struct {
		method   string
		wantKind TypeKind
		wantList bool
		wantNull bool
	}{
		{"where", TypeQueryBuilder, false, false},
		{"groupBy", TypeQueryBuilder, false, false},
		{"orderBy", TypeQueryBuilder, false, false},
		{"limit", TypeQueryBuilder, false, false},
		{"offset", TypeQueryBuilder, false, false},
		{"sum", TypeFloat, false, false},
		{"avg", TypeFloat, false, false},
		{"count", TypeInt, false, false},
		{"min", TypeFloat, false, false},
		{"max", TypeFloat, false, false},
		{"first", TypeModel, false, true},
		{"all", TypeModel, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := a.resolveQueryMethod(tt.method, orderType)
			if got == nil {
				t.Fatalf("resolveQueryMethod(%q) returned nil", tt.method)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("resolveQueryMethod(%q).Kind = %v, want %v", tt.method, got.Kind, tt.wantKind)
			}
			if got.IsList != tt.wantList {
				t.Errorf("resolveQueryMethod(%q).IsList = %v, want %v", tt.method, got.IsList, tt.wantList)
			}
			if got.Nullable != tt.wantNull {
				t.Errorf("resolveQueryMethod(%q).Nullable = %v, want %v", tt.method, got.Nullable, tt.wantNull)
			}
		})
	}

	// select returns nil (depends on lambda)
	if got := a.resolveQueryMethod("select", orderType); got != nil {
		t.Errorf("resolveQueryMethod(select) = %v, want nil", got)
	}

	// unknown method returns nil
	if got := a.resolveQueryMethod("unknown", orderType); got != nil {
		t.Errorf("resolveQueryMethod(unknown) = %v, want nil", got)
	}
}

func TestIsQueryMethod(t *testing.T) {
	valid := []string{"where", "groupBy", "orderBy", "sum", "avg", "count", "min", "max", "select", "first", "all", "limit", "offset", "save"}
	for _, m := range valid {
		if !isQueryMethod(m) {
			t.Errorf("isQueryMethod(%q) = false, want true", m)
		}
	}
	invalid := []string{"foo", "bar", "map", "filter", "toString"}
	for _, m := range invalid {
		if isQueryMethod(m) {
			t.Errorf("isQueryMethod(%q) = true, want false", m)
		}
	}
}

func TestQueryBuilderFieldAccessStillWorks(t *testing.T) {
	// Order.total should still resolve as a field access, not a query method
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "Order"},
		Field:  "total",
	}
	a := New()
	file := queryBuilderFile(expr)
	result := a.Analyze([]*ast.File{file})
	expectNoErrors(t, result)
}
