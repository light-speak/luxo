package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// valueCompiler owns a Go value-return boundary, not the enclosing fn/API ABI.
func (c *compiler) valueCompiler() *compiler {
	sub := c.subCompiler()
	sub.inFunction = false
	sub.functionResult = nil
	sub.inAsync = false
	sub.valueClosure = true
	sub.closureError = ""
	return sub
}

// finishValueClosure keeps infallible expressions allocation-free. Fallible
// expressions use a local error slot checked immediately after the inline call;
// no panic/recover, reflection or transport serialization crosses this boundary.
func (c *compiler) finishValueClosure(sub *compiler, goType, body string) string {
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	signature := goType
	if sub.closureError != "" {
		signature = "(_value " + goType + ")"
	}
	expr := fmt.Sprintf("func() %s {\n%s%s}()", signature, body, c.indent)
	if sub.closureError == "" {
		return expr
	}
	c.write("var %s error", sub.closureError)
	c.resultTmp++
	name := fmt.Sprintf("_closureValue%d", c.resultTmp)
	c.write("%s := %s", name, expr)
	c.write("if %s != nil {", sub.closureError)
	c.writeErrorReturn("\t", sub.closureError)
	c.write("}")
	return name
}

func (c *compiler) writeClosureError(indent, expression string) {
	if c.closureError == "" {
		c.resultTmp++
		c.closureError = fmt.Sprintf("_closureErr%d", c.resultTmp)
	}
	c.write("%s%s = %s", indent, c.closureError, expression)
	c.write("%sreturn", indent)
}

func (c *compiler) errorCompiler() *compiler {
	sub := c.subCompiler()
	sub.inFunction = true
	sub.functionResult = nil
	sub.inAsync = false
	sub.valueClosure = false
	sub.closureError = ""
	return sub
}

func (c *compiler) compileValueBranch(expr ast.Expr) {
	if lambda, ok := expr.(*ast.LambdaExpr); ok {
		for index, statement := range lambda.Body.Stmts {
			if last, ok := statement.(*ast.ExprStmt); ok && index == len(lambda.Body.Stmts)-1 {
				c.compileReturn(&ast.ReturnStmt{Value: last.Expr})
			} else {
				c.compileStmt(statement)
			}
		}
		return
	}
	c.compileReturn(&ast.ReturnStmt{Value: expr})
}

// Nested else scopes preserve lazy condition evaluation, including statements
// emitted by fallible fn/native calls used as later branch conditions.
func (c *compiler) compileWhenConditions(expr *ast.WhenExpr, subject string) {
	indent := c.indent
	for _, branch := range expr.Branches {
		condition := c.compileExpr(branch.Condition)
		if subject != "" {
			condition = subject + " == " + condition
		}
		c.write("if %s {", condition)
		c.indent += "\t"
		c.compileValueBranch(branch.Body)
		c.indent = strings.TrimSuffix(c.indent, "\t")
		c.write("} else {")
		c.indent += "\t"
	}
	if expr.Else != nil {
		c.compileValueBranch(expr.Else)
	}
	for range expr.Branches {
		c.indent = strings.TrimSuffix(c.indent, "\t")
		c.write("}")
	}
	c.indent = indent
}

func whenConditionsCall(expr *ast.WhenExpr) bool {
	found := false
	for _, branch := range expr.Branches {
		ast.WalkExprs(&ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: branch.Condition}}}, func(value ast.Expr) {
			if _, ok := value.(*ast.CallExpr); ok {
				found = true
			}
		})
	}
	return found
}

func (c *compiler) compileForValue(s *ast.ForStmt, yield bool) string {
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.inForExpr = yield
	sub.yieldAddr = yield && c.yieldNeedsAddress(s)
	goType := c.goTypeForExpr(s)
	if goType == "" {
		if yield {
			goType = "any"
		} else {
			goType = "[]any"
		}
	}
	if !yield {
		sub.write("var _result %s", goType)
	}
	if bounds, ok := s.Collection.(*ast.RangeExpr); ok {
		start, end := c.compileExpr(bounds.Start), c.compileExpr(bounds.End)
		sub.write("for %s := int64(%s); %s <= %s; %s++ {", s.VarName, start, s.VarName, end, s.VarName)
	} else {
		sub.write("for _, %s := range %s {", s.VarName, c.compileExpr(s.Collection))
	}
	sub.indent += "\t"
	for index, statement := range s.Body.Stmts {
		if !yield && index == len(s.Body.Stmts)-1 {
			var expression ast.Expr
			switch last := statement.(type) {
			case *ast.ExprStmt:
				expression = last.Expr
			case *ast.ReturnStmt:
				expression = last.Value
			}
			sub.write("_result = append(_result, %s)", sub.compileExpr(expression))
		} else {
			sub.compileStmt(statement)
		}
	}
	sub.indent = strings.TrimSuffix(sub.indent, "\t")
	sub.write("}")
	if yield {
		sub.write("return nil")
	} else {
		sub.write("return _result")
	}
	return c.finishValueClosure(sub, goType, sub.b.String())
}
