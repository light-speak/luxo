package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestScanBodyForBuiltins_StringLiteralLog(t *testing.T) {
	// "msg".i → hasLog = true
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.MemberExpr{
					Object: &ast.Literal{Kind: token.String, Value: "hello"},
					Field:  "i",
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if !f.hasLog {
		t.Error("string literal .i should set hasLog = true")
	}
}

func TestScanBodyForBuiltins_TemplateLiteralLog(t *testing.T) {
	// `${expr}`.d → hasLog = true
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.MemberExpr{
					Object: &ast.TemplateString{
						Parts: []ast.Expr{&ast.Literal{Kind: token.String, Value: "msg"}},
					},
					Field: "d",
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if !f.hasLog {
		t.Error("template string .d should set hasLog = true")
	}
}

func TestScanBodyForBuiltins_NonStringLog(t *testing.T) {
	// obj.i where obj is an identifier (not string literal) → hasLog = false
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.MemberExpr{
					Object: &ast.Ident{Name: "obj"},
					Field:  "i",
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if f.hasLog {
		t.Error("identifier .i should NOT set hasLog")
	}
}

func TestScanBodyForBuiltins_CryptoDetection(t *testing.T) {
	// crypto.hash → hasCrypto = true
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.MemberExpr{
					Object: &ast.Ident{Name: "crypto"},
					Field:  "hash",
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if !f.hasCrypto {
		t.Error("crypto.hash should set hasCrypto = true")
	}
}

func TestScanBodyForBuiltins_TimeFunc(t *testing.T) {
	// something.hours → hasTimeFunc = true
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.MemberExpr{
					Object: &ast.Literal{Kind: token.Int, Value: "5"},
					Field:  "hours",
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if !f.hasTimeFunc {
		t.Error("X.hours should set hasTimeFunc = true")
	}
}

func TestScanBodyForBuiltins_NowCall(t *testing.T) {
	// now() → hasTimeFunc = true
	block := &ast.Block{
		Stmts: []ast.Stmt{
			&ast.ExprStmt{
				Expr: &ast.CallExpr{
					Func: &ast.Ident{Name: "now"},
				},
			},
		},
	}

	var f handlerFeatures
	scanBodyForBuiltins(block, &f)

	if !f.hasTimeFunc {
		t.Error("now() call should set hasTimeFunc = true")
	}
}

func TestScanBodyForBuiltins_NilBlock(t *testing.T) {
	// nil block should not panic
	var f handlerFeatures
	scanBodyForBuiltins(nil, &f)

	if f.hasLog || f.hasCrypto || f.hasTimeFunc {
		t.Error("nil block should set nothing")
	}
}

func TestScanBodyForBuiltins_LogFields(t *testing.T) {
	// Test all log field names: i, d, w, e
	for _, field := range []string{"i", "d", "w", "e"} {
		block := &ast.Block{
			Stmts: []ast.Stmt{
				&ast.ExprStmt{
					Expr: &ast.MemberExpr{
						Object: &ast.Literal{Kind: token.String, Value: "msg"},
						Field:  field,
					},
				},
			},
		}

		var f handlerFeatures
		scanBodyForBuiltins(block, &f)

		if !f.hasLog {
			t.Errorf("string.%s should set hasLog = true", field)
		}
	}
}
