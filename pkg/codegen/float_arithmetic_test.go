package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func TestCompileFloatArithmeticBoundaries(t *testing.T) {
	floating := &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Float"}}
	integer := &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Int"}}
	for _, test := range []struct {
		name, operator string
		left, right    ast.Expr
		want           string
	}{
		{"float remainder", "%", floating, floating, "math.Mod(left, right)"},
		{"float int", "%", floating, integer, "math.Mod(left, float64(right))"},
		{"int float", "%", integer, floating, "math.Mod(float64(left), right)"},
		{"promote add", "+", integer, floating, "float64(left) + right"},
		{"promote divide", "/", floating, integer, "left / float64(right)"},
		{"int literal", "%", floating, &ast.Literal{Kind: token.Int}, "math.Mod(left, float64(right))"},
		{"int remainder unchanged", "%", integer, integer, ""},
		{"float add unchanged", "+", floating, floating, ""},
		{"comparison unchanged", "==", floating, integer, ""},
		{"absent operand", "%", nil, floating, ""},
		{"typed nil literal", "%", (*ast.Literal)(nil), floating, ""},
		{"wrong literal", "%", &ast.Literal{Kind: token.String}, floating, ""},
		{"nullable float", "%", &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Float", NullableTag: true}}, integer, ""},
		{"float list", "%", &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Float", ListTypeTag: true}}, integer, ""},
		{"nullable int", "%", floating, &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Int", NullableTag: true}}, ""},
		{"int list", "%", floating, &ast.Ident{TypeTagged: ast.TypeTagged{TypeTag: "Int", ListTypeTag: true}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			expression := &ast.BinaryExpr{Op: test.operator, Left: test.left, Right: test.right}
			got, matched := compileFloatArithmetic(expression, "left", "right")
			if got != test.want || matched != (test.want != "") {
				t.Fatal(got, matched, test.want)
			}
			if test.operator != "%" {
				if _, matched := compileFloatRemainder(expression, "left", "right"); matched {
					t.Fatal("non-remainder matched import detection")
				}
			}
		})
	}
}
