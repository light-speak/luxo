package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
)

// Evaluate a nullable value once and keep its fallback lazy, including failures.
func (c *compiler) compileDefaultExpr(expr *ast.ElvisExpr) string {
	result := &ast.TypeRef{Name: expr.GetTypeTag(), Nullable: expr.IsNullable(), IsList: expr.IsListType()}
	if isNullLiteral(expr.Left) {
		return c.compileDefaultOperand(expr.Right, result)
	}
	thrown, throws := expr.Right.(*ast.UnaryExpr)
	throws = throws && thrown.Op == "throw"
	booleanGuard := throws && isBoolExpr(expr.Left)
	if !expr.Left.IsNullable() && !booleanGuard {
		return c.compileDefaultOperand(expr.Left, result)
	}
	sub := c.valueCompiler()
	sub.indent = c.indent + "\t"
	left := sub.compileDefaultOperand(expr.Left, result)
	sub.resultTmp++
	name := fmt.Sprintf("_default%d", sub.resultTmp)
	sub.write("%s := %s", name, left)
	condition := name + " != nil"
	if booleanGuard {
		condition = name
	}
	sub.write("if %s {", condition)
	if !booleanGuard && !expr.IsNullable() && !expr.Left.IsListType() && c.models[expr.Left.GetTypeTag()] == nil {
		sub.write("\treturn *%s", name)
	} else {
		sub.write("\treturn %s", name)
	}
	sub.write("}")
	if throws {
		value := sub.compileThrowExpr(thrown.Value)
		sub.writeErrorReturn("", value)
	} else {
		right := sub.compileDefaultOperand(expr.Right, result)
		sub.write("return %s", right)
	}
	return c.finishValueClosure(sub, compiledFunctionGoType(result, c.models), sub.b.String())
}

func (c *compiler) compileDefaultOperand(expr ast.Expr, expected *ast.TypeRef) string {
	value := c.compileFunctionArgument(expr, expected)
	if !c.isModelQuery(expr) {
		return value
	}
	c.resultTmp++
	name := fmt.Sprintf("_defaultQuery%d", c.resultTmp)
	c.write("%s, err := %s", name, value)
	c.writeErrorGuard()
	return name
}

// Error arguments can call fallible functions; evaluate them only after failure.
func (c *compiler) writeConditionalThrow(condition string, expression ast.Expr) {
	c.write("if %s {", condition)
	c.indent += "\t"
	value := c.compileThrowExpr(expression)
	c.writeErrorReturn("", value)
	c.indent = c.indent[:len(c.indent)-1]
	c.write("}")
}

// Nullable roots wrap the complete value, not its fields or list elements.
func (c *compiler) compileNullableReturn(value ast.Expr, expected *ast.TypeRef) {
	if isNullLiteral(value) {
		c.write("req.Buf.B = codec.AppendNull(req.Buf.B)")
		c.write("return nil")
		return
	}
	c.resultTmp++
	name := fmt.Sprintf("_nullableResult%d", c.resultTmp)
	c.compileNullableResult(name, value, expected)
	if value.IsNullable() {
		c.write("if %s == nil {", name)
		c.write("\treq.Buf.B = codec.AppendNull(req.Buf.B)")
		c.write("\treturn nil")
		c.write("}")
		if !expected.IsList && c.models[expected.Name] == nil {
			name = "(*" + name + ")"
		}
	}
	c.write("req.Buf.B = codec.AppendPresent(req.Buf.B)")
	c.writeReturnByType(name, valType{name: expected.Name, isList: expected.IsList, isModel: c.models[expected.Name] != nil})
	c.write("return nil")
}

func (c *compiler) compileNullableResult(name string, value ast.Expr, expected *ast.TypeRef) {
	expression := c.compileFunctionArgument(value, expected)
	if c.isModelQuery(value) {
		c.write("%s, err := %s", name, expression)
		c.writeErrorGuard()
		return
	}
	actual := *expected
	actual.Nullable = value.IsNullable()
	c.write("var %s %s = %s", name, compiledFunctionGoType(&actual, c.models), expression)
}
