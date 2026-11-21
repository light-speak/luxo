package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
)

func compileDecimalComparison(expr *ast.BinaryExpr, left, right string) (string, bool) {
	if expr.Left.IsNullable() || expr.Right.IsNullable() {
		if expr.Op == "==" || expr.Op == "!=" {
			return compileNullableScalarEquality(expr, left, right, "decimal.Decimal", "Equal"), true
		}
		return "", false
	}
	switch expr.Op {
	case "==", "!=", "<", "<=", ">", ">=":
		return "lux.CompareDecimal(" + left + ", " + right + ") " + expr.Op + " 0", true
	default:
		return "", false
	}
}

// Guard the underlying exponent representation without rounding decimal values.
func (c *compiler) compileDecimalArithmetic(left, right, method string) string {
	if method == "Add" || method == "Sub" {
		return c.compileAlignedDecimal(left, right, method)
	}
	if method == "Mul" {
		c.resultTmp++
		prefix := fmt.Sprintf("_decimal%d", c.resultTmp)
		c.write("%sLeft, %sRight := %s, %s", prefix, prefix, left, right)
		left, right = prefix+"Left", prefix+"Right"
		c.write("%sExponent := int64(%s.Exponent()) + int64(%s.Exponent())", prefix, left, right)
		c.write("if %sExponent < -2147483648 || %sExponent > 2147483647 {", prefix, prefix)
		c.writeErrorReturn("\t", `errors.BadRequest.WithData(&errors.ParamError{Param: "Decimal", Error: "multiplication exponent out of range"})`)
		c.write("}")
	}
	return left + "." + method + "(" + right + ")"
}

func (c *compiler) compileAlignedDecimal(left, right, method string) string {
	c.resultTmp++
	value := fmt.Sprintf("_decimal%d", c.resultTmp)
	c.write("%s, %sErr := lux.%sDecimal(%s, %s)", value, value, method, left, right)
	c.write("if %sErr != nil {", value)
	c.writeErrorReturn("\t", fmt.Sprintf(`errors.BadRequest.WithData(&errors.ParamError{Param: "Decimal", Error: %sErr.Error()})`, value))
	c.write("}")
	return value
}
