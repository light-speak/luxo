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

func (c *compiler) restoreBindings(bindings []lexicalBinding) {
	for index := len(bindings) - 1; index >= 0; index-- {
		c.restoreBinding(bindings[index])
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
	right := c.compileBooleanValue(expr.Right)
	c.b, c.indent = parent, indent
	if statements.Len() == 0 {
		return logicalExpression(left, expr.Op, right)
	}
	c.resultTmp++
	result := fmt.Sprintf("_logical%d", c.resultTmp)
	writeLogicalBranch(parent, indent, result, left, right, statements.String(), expr.Op == "||")
	return result
}

// A database Boolean still has an error channel before it can enter an expression.
func (c *compiler) compileBooleanValue(expr ast.Expr) string {
	query := c.isModelQuery(expr)
	if query && !isErrorReturningBool(expr) {
		return c.compileMaterializedValue(expr, nil)
	}
	value := c.compileExpr(expr)
	if c.models[expr.GetTypeTag()] != nil && !expr.IsListType() {
		value = c.modelReference(expr, value)
		if _, literal := expr.(*ast.ObjectExpr); literal {
			// Composite literals need grouping in switch and if headers.
			value = "(" + value + ")"
		}
	}
	if !isErrorReturningBool(expr) || !query {
		return value
	}
	c.resultTmp++
	name := fmt.Sprintf("_boolean%d", c.resultTmp)
	c.write("%s, err := %s", name, value)
	c.write("if err != nil {")
	c.writeErrorReturn("\t", "err")
	c.write("}")
	return name
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

// Branches share the resolved result ABI, including nullable scalar/object values.
func (c *compiler) branchValueCompiler(expr *ast.WhenExpr) *compiler {
	sub := c.expressionCompiler(expr)
	sub.statementWhen = nil
	page := c.pageBranch(expr)
	if sub.inlineValue != nil {
		sub.inlineWhen = true
		sub.inlineValue.when = expr
		sub.inlineValue.page = page
		return sub
	}
	sub.pageResult = page
	if expr.IsNullable() {
		sub.valueResult = &ast.TypeRef{Name: expr.GetTypeTag(), IsList: expr.IsListType(), Nullable: true}
	} else {
		sub.valueModel = c.models[expr.GetTypeTag()] != nil
	}
	return sub
}

// finishValueClosure keeps infallible expressions allocation-free. Fallible
// expressions use a local error slot checked immediately after the inline call;
// no panic/recover, reflection or transport serialization crosses this boundary.
func (c *compiler) finishValueClosure(sub *compiler, goType, body string) string {
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	if sub.inlineValue != nil {
		return c.writeInlineValueScope(sub, goType, body)
	}
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
	switch value := expr.(type) {
	case *ast.LambdaExpr:
		c.compileWhenBranchBlock(value.Body)
		return
	case *ast.UnaryExpr:
		if value.Op == "throw" {
			c.writeErrorReturn("", c.compileThrowExpr(value.Value))
			return
		}
	}
	if scope := c.statementWhen; scope != nil {
		c.statementWhen = nil
		c.compileExprStmt(&ast.ExprStmt{Expr: expr})
		c.statementWhen = scope
		return
	}
	if c.inlineWhen {
		c.writeInlineWhenValue(c.inlineValue, expr)
		return
	}
	c.compileReturn(&ast.ReturnStmt{Value: expr})
}

func (c *compiler) compileWhenBranchBlock(body *ast.Block) {
	if scope := c.statementWhen; scope != nil {
		outer := c.whenScope
		c.statementWhen, c.whenScope = nil, scope
		c.compileBranchStatements(body)
		c.statementWhen, c.whenScope = scope, outer
		return
	}
	if !c.inlineWhen {
		c.compileBranchStatements(body)
		return
	}
	scope := c.inlineValue
	outer := c.whenScope
	c.whenScope = nil
	scope.branch = true
	c.compileBranchStatements(body)
	scope.branch = false
	c.whenScope = outer
}

func (c *compiler) compileValueResult(expr ast.Expr, expected *ast.TypeRef, page string, model bool) string {
	value := c.compileAssignedValue(expr, expected)
	value = c.pageBranchValue(page, expr, value)
	if model {
		value = c.modelReference(expr, value)
	}
	return value
}

func (c *compiler) compileBranchStatements(block *ast.Block) {
	var local [8]lexicalBinding
	bindings := local[:0]
	for index, statement := range block.Stmts {
		bindings = c.saveStatementBindings(bindings, statement)
		if last, ok := statement.(*ast.ExprStmt); ok && index == len(block.Stmts)-1 && c.whenScope == nil {
			c.compileReturn(&ast.ReturnStmt{Value: last.Expr})
		} else {
			c.compileStmt(statement)
		}
		switch statement.(type) {
		case *ast.ReturnStmt, *ast.ThrowStmt:
			c.restoreBindings(bindings)
			return
		}
	}
	c.restoreBindings(bindings)
}

func (c *compiler) finishWhenClosure(sub *compiler, expr *ast.WhenExpr, fallback bool) string {
	goType := c.inferWhenReturnType(expr)
	if fallback {
		sub.writeValueCompletion("", zeroValueForType(goType))
	}
	return c.finishValueClosure(sub, goType, sub.b.String())
}

// Nested else scopes preserve lazy condition evaluation, including statements
// emitted by fallible fn/native calls used as later branch conditions.
func (c *compiler) compileWhenConditions(expr *ast.WhenExpr, subject string) {
	indent := c.indent
	branches := runtimeWhenBranches(expr)
	for _, branch := range branches {
		condition := c.compileBooleanValue(branch.Condition)
		if subject != "" {
			condition = c.compileWhenEquality(expr.Subject, branch.Condition, subject, condition)
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
	for range branches {
		c.indent = strings.TrimSuffix(c.indent, "\t")
		c.write("}")
	}
	c.indent = indent
}

func whenNeedsValueEquality(expr *ast.WhenExpr) bool {
	if expr.Subject == nil || expr.Subject.IsListType() {
		return false
	}
	if expr.Subject.GetTypeTag() == "DateTime" || expr.Subject.GetTypeTag() == "Decimal" || expr.Subject.IsNullable() {
		return true
	}
	for _, branch := range expr.Branches {
		if branch.Condition != nil && branch.Condition.IsNullable() {
			return true
		}
	}
	return false
}

func (c *compiler) compileWhenEquality(subject, pattern ast.Expr, left, right string) string {
	if isStaticWhenNullSubject(subject) {
		return c.compileStaticNullMatch(pattern, right)
	}
	comparison := &ast.BinaryExpr{Left: subject, Right: pattern, Op: "=="}
	if expression, ok := c.compileScalarComparison(comparison, left, right); ok {
		return expression
	}
	return left + " == " + right
}

func isStaticWhenNullSubject(expr ast.Expr) bool {
	return isNullLiteral(expr) || isNullAwait(expr)
}

func isNullAwait(expr ast.Expr) bool {
	await, ok := expr.(*ast.AwaitExpr)
	return ok && await.GetTypeTag() == "null"
}

func (c *compiler) compileAwaitNullComparison(expr *ast.BinaryExpr, left, right string) (string, bool) {
	if left != "nil" && right != "nil" || expr.Op != "==" && expr.Op != "!=" {
		return "", false
	}
	var result string
	switch {
	case isNullAwait(expr.Left):
		result = c.compileStaticNullMatch(expr.Right, right)
	case isNullAwait(expr.Right):
		result = c.compileStaticNullMatch(expr.Left, left)
	default:
		return "", false
	}
	if expr.Op == "!=" {
		return "!(" + result + ")", true
	}
	return result, true
}

func (c *compiler) compileStaticNullMatch(pattern ast.Expr, value string) string {
	nullPattern := isNullLiteral(pattern) || pattern.GetTypeTag() == "null"
	if !nullPattern && pattern.IsNullable() {
		return value + " == nil"
	}
	// A non-null pattern cannot match, but its evaluation can still fail.
	if value != "nil" {
		c.write("_ = %s", value)
	}
	if nullPattern {
		return "true"
	}
	return "false"
}

// A statically null subject has no possible type match.
func runtimeWhenBranches(expr *ast.WhenExpr) []*ast.WhenBranch {
	if !isStaticWhenNullSubject(expr.Subject) || !hasWhenTypeBranch(expr) {
		return expr.Branches
	}
	branches := make([]*ast.WhenBranch, 0, len(expr.Branches))
	for _, branch := range expr.Branches {
		if branch.IsType == "" {
			branches = append(branches, branch)
		}
	}
	return branches
}

func whenConditionsCall(expr *ast.WhenExpr) bool {
	found := false
	for _, branch := range expr.Branches {
		ast.WalkExprs(&ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: branch.Condition}}}, func(value ast.Expr) {
			switch value.(type) {
			case *ast.CallExpr, *ast.AwaitExpr:
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
		subject = c.compileBooleanValue(expr.Subject)
		if !isStaticWhenNullSubject(expr.Subject) {
			c.resultTmp++
			name := fmt.Sprintf("_yieldSubject%d", c.resultTmp)
			c.write("%s := %s", name, subject)
			subject = name
		}
	}
	branches := runtimeWhenBranches(expr)
	for _, branch := range branches {
		condition := c.compileBooleanValue(branch.Condition)
		if subject != "" {
			condition = c.compileWhenEquality(expr.Subject, branch.Condition, subject, condition)
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
	for range branches {
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
	c.restoreBindings(bindings)
}

func (c *compiler) compileForValue(s *ast.ForStmt, yield bool) string {
	sub := c.expressionCompiler(s)
	sub.indent += "\t"
	sub.inForExpr = yield
	sub.yieldAddr = yield && c.yieldNeedsAddress(s)
	if sub.inlineValue != nil {
		sub.inlineValue.yield = yield
	}
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
	sub.withLoopScope(func() { sub.compileForValueLoop(s, yield) })
	if yield {
		sub.writeValueCompletion("", "nil")
	} else {
		sub.writeValueCompletion("", "_result")
	}
	return c.finishValueClosure(sub, goType, sub.b.String())
}

func (c *compiler) compileForValueLoop(s *ast.ForStmt, yield bool) {
	c.compileLoopHeader(s)
	if s.VarName != "" {
		c.vars[s.VarName] = c.loopElementType(s.Collection)
	}
	c.indent += "\t"
	if s.VarName != "" {
		c.write("_ = %s", c.bindings.name(s.VarName))
	}
	for index, statement := range s.Body.Stmts {
		if !yield && index == len(s.Body.Stmts)-1 {
			var expression ast.Expr
			switch last := statement.(type) {
			case *ast.ExprStmt:
				expression = last.Expr
			case *ast.ReturnStmt:
				expression = last.Value
			}
			value := c.compileMaterializedValue(expression, nil)
			if c.models[s.GetTypeTag()] != nil {
				value = c.modelReference(expression, value)
			}
			c.write("_result = append(_result, %s)", value)
		} else {
			c.compileStmt(statement)
		}
	}
	c.indent = strings.TrimSuffix(c.indent, "\t")
	c.write("}")
}
