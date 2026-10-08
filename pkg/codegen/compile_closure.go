package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Nullable non-list parameters are Go pointers, including Bytes/JSON/types.
// Queried models retain their pointer ABI after a lexical non-null guard.
func (c *compiler) isNarrowedScalar(expr *ast.Ident, declared valType) bool {
	if !declared.nullable || declared.isList || declared.isModel || expr.GetTypeTag() == "" || expr.IsNullable() {
		return false
	}
	if _, model := c.models[declared.name]; model {
		return false
	}
	return true
}

type lexicalBinding struct {
	name  string
	value valType
	total string
	set   bool
}

func (c *compiler) saveBinding(name string) lexicalBinding {
	value, set := c.vars[name]
	return lexicalBinding{name: name, value: value, total: c.paginationTotals[name], set: set}
}

func (c *compiler) restoreBinding(binding lexicalBinding) {
	if binding.set {
		c.vars[binding.name] = binding.value
	} else {
		delete(c.vars, binding.name)
	}
	if binding.total != "" {
		c.paginationTotals[binding.name] = binding.total
	} else {
		delete(c.paginationTotals, binding.name)
	}
}

func (c *compiler) saveStatementBindings(bindings []lexicalBinding, statement ast.Stmt) []lexicalBinding {
	declaration, ok := statement.(*ast.ValStmt)
	if !ok {
		return bindings
	}
	if len(declaration.Names) == 0 {
		return append(bindings, c.saveBinding(declaration.Name))
	}
	for _, name := range declaration.Names {
		bindings = append(bindings, c.saveBinding(name))
	}
	return bindings
}

// Keep statements produced by a fallible RHS inside the short-circuit branch.
// Use the enclosing return ABI, including its closure error slot, unchanged.
func (c *compiler) compileLogicalBinary(expr *ast.BinaryExpr, left string) string {
	switch expr.Right.(type) {
	case *ast.Ident, *ast.Literal:
		return logicalExpression(left, expr.Op, c.compileExpr(expr.Right))
	}
	var statements strings.Builder
	parent, indent := c.b, c.indent
	c.b, c.indent = &statements, indent+"\t"
	right := c.compileExpr(expr.Right)
	c.b, c.indent = parent, indent
	if statements.Len() == 0 {
		return logicalExpression(left, expr.Op, right)
	}
	c.resultTmp++
	result := fmt.Sprintf("_logical%d", c.resultTmp)
	writeLogicalBranch(parent, indent, result, left, right, statements.String(), expr.Op == "||")
	return result
}

func logicalExpression(left, op, right string) string {
	var b strings.Builder
	b.Grow(len(left) + len(op) + len(right) + 4)
	b.WriteByte('(')
	b.WriteString(left)
	b.WriteByte(' ')
	b.WriteString(op)
	b.WriteByte(' ')
	b.WriteString(right)
	b.WriteByte(')')
	return b.String()
}

func writeLogicalBranch(b *strings.Builder, indent, result, left, right, body string, negate bool) {
	size := 4*len(indent) + 3*len(result) + len(left) + len(right) + len(body) + 18
	if negate {
		size++
	}
	b.Grow(size)
	b.WriteString(indent)
	b.WriteString(result)
	b.WriteString(" := ")
	b.WriteString(left)
	b.WriteByte('\n')
	b.WriteString(indent)
	b.WriteString("if ")
	if negate {
		b.WriteByte('!')
	}
	b.WriteString(result)
	b.WriteString(" {\n")
	b.WriteString(body)
	b.WriteString(indent)
	b.WriteByte('\t')
	b.WriteString(result)
	b.WriteString(" = ")
	b.WriteString(right)
	b.WriteByte('\n')
	b.WriteString(indent)
	b.WriteString("}\n")
}

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

// Keep a yielding statement branch inside the enclosing loop return boundary.
func (c *compiler) compileYieldWhenStatement(expr *ast.WhenExpr) {
	c.compileYieldWhenBranches(expr, "", nil)
}

func (c *compiler) compileYieldWhenValue(expr *ast.WhenExpr) string {
	c.resultTmp++
	result := fmt.Sprintf("_yieldWhen%d", c.resultTmp)
	c.write("var %s %s", result, c.inferWhenReturnType(expr))
	expected := &ast.TypeRef{Name: expr.GetTypeTag(), Nullable: expr.IsNullable(), IsList: expr.IsListType()}
	c.compileYieldWhenBranches(expr, result, expected)
	return result
}

func (c *compiler) compileYieldWhenBranches(expr *ast.WhenExpr, result string, expected *ast.TypeRef) {
	indent := c.indent
	subject := ""
	if expr.Subject != nil {
		value := c.compileExpr(expr.Subject)
		c.resultTmp++
		subject = fmt.Sprintf("_yieldSubject%d", c.resultTmp)
		c.write("%s := %s", subject, value)
	}
	for _, branch := range expr.Branches {
		condition := c.compileExpr(branch.Condition)
		if subject != "" {
			condition = subject + " == " + condition
		}
		c.write("if %s {", condition)
		c.indent += "\t"
		c.compileYieldBranch(branch.Body, result, expected)
		c.indent = strings.TrimSuffix(c.indent, "\t")
		c.write("} else {")
		c.indent += "\t"
	}
	if expr.Else != nil {
		c.compileYieldBranch(expr.Else, result, expected)
	}
	for range expr.Branches {
		c.indent = strings.TrimSuffix(c.indent, "\t")
		c.write("}")
	}
	c.indent = indent
}

func (c *compiler) compileYieldBranch(expr ast.Expr, result string, expected *ast.TypeRef) {
	if block, ok := expr.(*ast.LambdaExpr); ok {
		if result == "" {
			c.compileBlock(block.Body)
		} else {
			c.compileYieldValueBlock(block.Body, result, expected)
		}
		return
	}
	if _, yield := expr.(*ast.YieldExpr); yield || result == "" && containsYieldExpr(expr) {
		c.compileExprStmt(&ast.ExprStmt{Expr: expr})
		return
	}
	value := c.compileExpectedExpr(expr, expected)
	if result != "" {
		c.write("%s = %s", result, value)
	} else if value != "" && value != "nil" {
		c.write("_ = %s", value)
	}
}

func (c *compiler) compileYieldValueBlock(block *ast.Block, result string, expected *ast.TypeRef) {
	var local [8]lexicalBinding
	bindings := local[:0]
	for i, statement := range block.Stmts {
		bindings = c.saveStatementBindings(bindings, statement)
		if last, ok := statement.(*ast.ExprStmt); ok && i == len(block.Stmts)-1 {
			c.compileYieldBranch(last.Expr, result, expected)
		} else {
			c.compileStmt(statement)
		}
	}
	for i := len(bindings) - 1; i >= 0; i-- {
		c.restoreBinding(bindings[i])
	}
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
