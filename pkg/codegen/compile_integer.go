package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func checkedIntegerMethod(operator string, left, right ast.Expr) (string, string) {
	kind := "Int"
	if !isIntScalar(left) || !isIntScalar(right) {
		if left == nil || right == nil || left.GetTypeTag() != "Duration" || right.GetTypeTag() != "Duration" || left.IsNullable() || right.IsNullable() || left.IsListType() || right.IsListType() {
			return "", ""
		}
		kind = "Duration"
	}
	method := ""
	switch operator {
	case "+":
		method = "Add"
	case "-":
		method = "Sub"
	case "*":
		if kind == "Int" {
			method = "Mul"
		}
	case "/":
		if kind == "Int" {
			method = "Div"
		}
	case "%":
		if kind == "Int" {
			method = "Mod"
		}
	}
	if method == "" {
		return "", ""
	}
	return method + kind, kind + "." + operator
}

func (c *compiler) checkedIntegerCall(method, operation string, arguments ...string) string {
	c.resultTmp++
	name := fmt.Sprintf("_integer%d", c.resultTmp)
	c.write("%s, %sErr := lux.%s(%s)", name, name, method, strings.Join(arguments, ", "))
	c.write("if %sErr != nil {", name)
	c.writeErrorReturn("\t", fmt.Sprintf(`errors.BadRequest.WithData(&errors.ParamError{Param: %q, Error: %sErr.Error()})`, operation, name))
	c.write("}")
	return name
}

func (c *compiler) compileIntegerArithmetic(expression *ast.BinaryExpr, left, right string) (string, bool) {
	method, operation := checkedIntegerMethod(expression.Op, expression.Left, expression.Right)
	if method == "" {
		return "", false
	}
	return c.checkedIntegerCall(method, operation, left, right), true
}

func checkedIntegerNegationMethod(expression *ast.UnaryExpr) (string, string) {
	if expression.Op != "-" {
		return "", ""
	}
	// Signed literals are checked by analysis, including the MinInt64 spelling.
	if literal, ok := expression.Value.(*ast.Literal); ok && (literal.Kind == token.Int || literal.Kind == token.Duration) {
		return "", ""
	}
	if isIntScalar(expression.Value) {
		return "NegInt", "Int.-"
	}
	if expression.Value.GetTypeTag() == "Duration" && !expression.Value.IsNullable() && !expression.Value.IsListType() {
		return "NegDuration", "Duration.-"
	}
	return "", ""
}

func (c *compiler) compileIntegerNegation(expression *ast.UnaryExpr, value string) (string, bool) {
	method, operation := checkedIntegerNegationMethod(expression)
	if method == "" {
		return "", false
	}
	return c.checkedIntegerCall(method, operation, value), true
}

func (c *compiler) compileIntegerAssignment(statement *ast.AssignStmt, target string) bool {
	operator := strings.TrimSuffix(statement.Op, "=")
	method, operation := checkedIntegerMethod(operator, statement.Target, statement.Value)
	if method == "" {
		return false
	}
	slot, old := c.captureAssignmentTarget(target)
	value := c.compileAssignedValue(statement.Value, &ast.TypeRef{Name: statement.Target.GetTypeTag()})
	result := c.checkedIntegerCall(method, operation, old, value)
	c.write("*%s = %s", slot, result)
	return true
}
