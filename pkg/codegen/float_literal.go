package codegen

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func isFloatExpression(expression ast.Expr) bool {
	if literal, ok := expression.(*ast.Literal); ok {
		return literal != nil && literal.Kind == token.Float
	}
	return expression != nil && expression.GetTypeTag() == "Float"
}

// Go constants cannot retain negative zero. A typed parameter preserves the
// value before negation without capturing state or constructing a runtime object.
func compileFloatNegation(operand string) string {
	return "func(value float64) float64 { return -value }(" + operand + ")"
}

func compileUnaryOperation(expression *ast.UnaryExpr, operand string) string {
	if expression.Op == "-" && isFloatExpression(expression.Value) {
		return compileFloatNegation(operand)
	}
	return expression.Op + operand
}

// Float remainder uses a static standard-library call, preserving signed zero.
func compileFloatRemainder(expression *ast.BinaryExpr, left, right string) (string, bool) {
	if expression.Op != "%" {
		return "", false
	}
	return compileFloatArithmetic(expression, left, right)
}

func isFloatScalar(expression ast.Expr) bool {
	return isFloatExpression(expression) && !expression.IsNullable() && !expression.IsListType()
}

func isIntScalar(expression ast.Expr) bool {
	if literal, ok := expression.(*ast.Literal); ok {
		return literal != nil && literal.Kind == token.Int && !literal.IsNullable() && !literal.IsListType()
	}
	return expression != nil && expression.GetTypeTag() == "Int" && !expression.IsNullable() && !expression.IsListType()
}

// Match the analyzer's existing Int/Float promotion without dynamic conversion.
func floatArithmeticNeeded(operator string, left, right ast.Expr) bool {
	switch operator {
	case "+", "-", "*", "/", "%":
	default:
		return false
	}
	leftFloat, rightFloat := isFloatScalar(left), isFloatScalar(right)
	if leftFloat && rightFloat {
		return operator == "%"
	}
	return leftFloat && isIntScalar(right) || rightFloat && isIntScalar(left)
}

func compileFloatArithmetic(expression *ast.BinaryExpr, left, right string) (string, bool) {
	if !floatArithmeticNeeded(expression.Op, expression.Left, expression.Right) {
		return "", false
	}
	if !isFloatScalar(expression.Left) {
		left = "float64(" + left + ")"
	}
	if !isFloatScalar(expression.Right) {
		right = "float64(" + right + ")"
	}
	if expression.Op == "%" {
		return "math.Mod(" + left + ", " + right + ")", true
	}
	return left + " " + expression.Op + " " + right, true
}

func (c *compiler) compileFloatAssignment(statement *ast.AssignStmt, target string) bool {
	operator := strings.TrimSuffix(statement.Op, "=")
	if !isFloatScalar(statement.Target) || !floatArithmeticNeeded(operator, statement.Target, statement.Value) {
		return false
	}
	slot, old := c.captureAssignmentTarget(target)
	value := c.compileAssignedValue(statement.Value, &ast.TypeRef{Name: "Float"})
	expression, _ := compileFloatArithmetic(&ast.BinaryExpr{Op: operator, Left: statement.Target, Right: statement.Value}, old, value)
	c.write("*%s = %s", slot, expression)
	return true
}
