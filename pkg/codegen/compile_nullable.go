package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Evaluate a nullable value once and keep its fallback lazy, including failures.
func (c *compiler) compileDefaultExpr(expr *ast.ElvisExpr) string {
	result := &ast.TypeRef{Name: expr.GetTypeTag(), Nullable: expr.IsNullable(), IsList: expr.IsListType()}
	if isNullLiteral(expr.Left) {
		return c.compileDefaultFallback(expr, result)
	}
	thrown, throws := expr.Right.(*ast.UnaryExpr)
	throws = throws && thrown.Op == "throw"
	if await, ok := expr.Left.(*ast.AwaitExpr); ok && await.GetTypeTag() == "null" {
		c.compileAwaitValue(await, nil)
		if throws {
			c.writeConditionalThrow("true", thrown.Value)
			return "nil"
		}
		return c.compileDefaultFallback(expr, result)
	}
	booleanGuard := throws && isBoolExpr(expr.Left)
	if !expr.Left.IsNullable() && !booleanGuard {
		return c.compileMaterializedValue(expr.Left, result)
	}
	if throws {
		return c.compileThrowDefault(expr, thrown.Value, booleanGuard)
	}
	sub := c.expressionCompiler(expr)
	page := c.pageBranch(expr)
	if sub.inlineValue == nil {
		sub.pageResult = page
	}
	sub.indent = c.indent + "\t"
	left := sub.compileMaterializedValue(expr.Left, result)
	sub.resultTmp++
	name := fmt.Sprintf("_default%d", sub.resultTmp)
	sub.write("%s := %s", name, left)
	sub.write("if %s != nil {", name)
	if !expr.IsNullable() && !expr.Left.IsListType() && c.models[expr.Left.GetTypeTag()] == nil {
		sub.writeValueCompletion("\t", "*"+name)
	} else {
		sub.indent += "\t"
		value := sub.pageBranchValue(page, expr.Left, name)
		sub.writeValueCompletion("", value)
		sub.indent = c.indent + "\t"
	}
	sub.write("}")
	right := sub.compileMaterializedValue(expr.Right, result)
	right = sub.pageBranchValue(page, expr.Right, right)
	sub.writeValueCompletion("", right)
	return c.finishValueClosure(sub, compiledFunctionGoType(result, c.models), sub.b.String())
}

func (c *compiler) compileDefaultFallback(expr *ast.ElvisExpr, result *ast.TypeRef) string {
	value := c.compileMaterializedValue(expr.Right, result)
	return c.pageBranchValue(c.pageBranch(expr), expr.Right, value)
}

// A throwing fallback has no value to merge, so it needs no value closure.
// Its guard still belongs to the current short-circuit/lambda execution scope.
func (c *compiler) compileThrowDefault(expr *ast.ElvisExpr, failure ast.Expr, boolean bool) string {
	c.resultTmp++
	name := fmt.Sprintf("_default%d", c.resultTmp)
	value := c.compileMaterializedValue(expr.Left, nil)
	c.write("%s := %s", name, value)
	condition := name + " == nil"
	if boolean {
		condition = "!" + name
	}
	c.writeConditionalThrow(condition, failure)
	if !boolean && !expr.IsNullable() && !expr.Left.IsListType() && c.models[expr.Left.GetTypeTag()] == nil {
		return "(*" + name + ")"
	}
	return name
}

func (c *compiler) compileMaterializedValue(expr ast.Expr, expected *ast.TypeRef) string {
	value := c.compileFunctionArgument(expr, expected)
	if !c.isModelQuery(expr) {
		return value
	}
	c.resultTmp++
	name := fmt.Sprintf("_defaultQuery%d", c.resultTmp)
	total := c.queryAssignment(name, expr, value)
	c.writeErrorGuard()
	c.captureQueryTotal(expr, total)
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

func nullableNullCondition(ref *ast.TypeRef, value string) string {
	condition := value + " == nil"
	if ref.Name == "JSON" && !ref.IsList {
		condition += " || codec.IsJSONNull(*" + value + ")"
	}
	return condition
}

// Nullable roots wrap the complete value, not its fields or list elements.
func (c *compiler) compileNullableReturn(value ast.Expr, expected *ast.TypeRef) {
	if value.GetTypeTag() == "null" || isNullLiteral(value) {
		if !isNullLiteral(value) {
			if expression := c.compileExpectedExpr(value, expected); expression != "nil" {
				c.write("_ = %s", expression)
			}
		}
		c.write("req.Buf.B = codec.AppendNull(req.Buf.B)")
		c.write("return nil")
		return
	}
	c.resultTmp++
	name := fmt.Sprintf("_nullableResult%d", c.resultTmp)
	c.compileNullableResult(name, value, expected)
	if value.IsNullable() {
		c.write("if %s {", nullableNullCondition(expected, name))
		c.write("\treq.Buf.B = codec.AppendNull(req.Buf.B)")
		c.write("\treturn nil")
		c.write("}")
		if !expected.IsList && c.models[expected.Name] == nil {
			name = "(*" + name + ")"
		}
	} else if expected.Name == "JSON" && !expected.IsList {
		c.write("if codec.IsJSONNull(%s) { req.Buf.B = codec.AppendNull(req.Buf.B); return nil }", name)
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

// Preserve the declared nullable representation without evaluating a value twice.
func (c *compiler) compileAssignedValue(value ast.Expr, expected *ast.TypeRef) string {
	expression := c.compileMaterializedValue(value, expected)
	if expected == nil || !expected.Nullable || value.IsNullable() || expression == "nil" {
		return expression
	}
	if expected.IsList {
		if _, literal := value.(*ast.ListExpr); literal {
			return expression
		}
		return c.compileNullableListValue(expression, expected)
	}
	if c.models[expected.Name] != nil {
		return expression
	}
	if _, literal := value.(*ast.ObjectExpr); literal {
		return "&" + expression
	}
	goType := ""
	if _, typed := value.(*ast.Ident); !typed {
		concrete := *expected
		concrete.Nullable = false
		goType = compiledFunctionGoType(&concrete, c.models)
	}
	return newValueExpression(goType, expression)
}

// Identifiers already have their resolved Go type; constants need an explicit cast.
func newValueExpression(goType, expression string) string {
	var boxed strings.Builder
	size := len(expression) + 5
	if goType != "" {
		size += len(goType) + 2
	}
	boxed.Grow(size)
	boxed.WriteString("new(")
	if goType != "" {
		boxed.WriteString(goType)
		boxed.WriteByte('(')
	}
	boxed.WriteString(expression)
	if goType != "" {
		boxed.WriteByte(')')
	}
	boxed.WriteByte(')')
	return boxed.String()
}

// Object storage uses references; event payloads retain their declared value ABI.
func objectFieldType(ref *ast.TypeRef, references bool, enums map[string]bool) *ast.TypeRef {
	if ref == nil || ref.Nullable || ref.IsList {
		return ref
	}
	if !references || !isRelationField(&ast.FieldDecl{Type: ref}, enums) {
		return nil
	}
	pointer := *ref
	pointer.Nullable = true
	return &pointer
}

// A required type field has pointer storage but remains a value in DSL expressions.
func (c *compiler) structuredMemberValue(member *ast.MemberExpr, expression string) string {
	if member.IsNullable() || member.IsListType() || !c.isTypeDecl(member.GetTypeTag()) {
		return expression
	}
	parent := member.Object.GetTypeTag()
	if _, model := c.models[parent]; !model && !c.isTypeDecl(parent) {
		return expression
	}
	return "(*" + expression + ")"
}

// Explicit locals retain their declared ABI even when the initializer is non-null.
func (c *compiler) compileDeclaredLocal(s *ast.ValStmt) bool {
	declared := c.localDeclarationType(s)
	if declared == nil || c.isModelQuery(s.Value) {
		return false
	}
	expr := c.compileAssignedValue(s.Value, declared)
	c.write("var %s %s = %s", c.bindings.name(s.Name), compiledFunctionGoType(declared, c.models), expr)
	c.vars[s.Name] = valType{name: declared.Name, isList: declared.IsList, nullable: declared.Nullable,
		isModel: c.models[declared.Name] != nil, typeRef: declared, isChan: declared.Name == "Channel"}
	if source, ok := s.Value.(*ast.Ident); ok && c.paginationTotals[source.Name] != "" {
		c.paginationTotals[s.Name] = c.paginationTotals[source.Name]
	}
	return true
}

func (c *compiler) localDeclarationType(statement *ast.ValStmt) *ast.TypeRef {
	if statement.Type != nil {
		return statement.Type
	}
	value, known := c.valTypeFromExpr(statement.Value)
	if !known || !value.isModel || value.isList {
		return nil
	}
	return &ast.TypeRef{Name: value.name, Nullable: value.nullable}
}

// Keep request decoding in static value storage while exposing the same model
// reference ABI as functions, relations and local constructors.
func writeCompiledParamBinding(b *strings.Builder, param *ast.ParamDecl, name string, enums map[string]bool, models map[string]*ast.ModelDecl, index int) {
	ref := param.Type
	if ref == nil || ref.Nullable || ref.IsList || models[ref.Name] == nil {
		writeCallableParamBinding(b, param, name, enums, "\t\t", models)
		return
	}
	storage := "_luxoModelInput" + strconv.Itoa(index)
	writeCallableParamBinding(b, param, storage, enums, "\t\t", models)
	fmt.Fprintf(b, "\t\t%s := &%s\n", name, storage)
}

func (c *compiler) propagateLocalSource(s *ast.ValStmt) {
	source, ok := s.Value.(*ast.Ident)
	if !ok {
		return
	}
	if c.vars[source.Name].isChan {
		c.vars[s.Name] = c.vars[source.Name]
	}
	if totalName := c.paginationTotals[source.Name]; totalName != "" {
		c.paginationTotals[s.Name] = totalName
	}
}

// Safe access evaluates the receiver once and keeps method arguments behind its nil guard.
func (c *compiler) compileSafeAccess(member *ast.MemberExpr, call *ast.CallExpr) string {
	return c.compileSafeAccessValue(member, call, nil)
}

func (c *compiler) compileSafeAccessValue(member *ast.MemberExpr, call *ast.CallExpr, unwrap *ast.UnaryExpr) string {
	var source ast.Expr = member
	if call != nil {
		source = call
	}
	sub := c.expressionCompiler(source)
	sub.indent = c.indent + "\t"
	sub.resultTmp++
	name := "_safeReceiver" + strconv.Itoa(sub.resultTmp)
	receiver := &ast.TypeRef{Name: member.Object.GetTypeTag(), IsList: member.Object.IsListType(), Nullable: member.Object.IsNullable()}
	sub.compileNullableResult(name, member.Object, receiver)
	if receiver.Nullable {
		sub.writeConditionalValue(name+" == nil", "nil")
	}
	sub.vars[name] = valType{name: receiver.Name, isList: receiver.IsList, nullable: receiver.Nullable, isModel: c.models[receiver.Name] != nil}
	access := *member
	access.SafeCall = false
	access.NullableTag = member.ValueNullable
	access.Object = &ast.Ident{Name: name, TypeTagged: ast.TypeTagged{TypeTag: receiver.Name, ListTypeTag: receiver.IsList, NarrowedTag: receiver.Nullable}}
	var expression ast.Expr = &access
	if call != nil {
		invocation := *call
		invocation.Func = &access
		invocation.NullableTag = member.ValueNullable
		expression = &invocation
	}
	if unwrap != nil {
		propagation := *unwrap
		propagation.Value = expression
		propagation.NullableTag = unwrap.ResultValueNullable
		expression = &propagation
	}
	if sub.isModelQuery(expression) {
		sub.resultTmp++
		queryName := "_safeQuery" + strconv.Itoa(sub.resultTmp)
		queryType := &ast.TypeRef{Name: expression.GetTypeTag(), IsList: expression.IsListType(), Nullable: expression.IsNullable()}
		sub.compileNullableResult(queryName, expression, queryType)
		expression = &ast.Ident{Name: queryName, TypeTagged: ast.TypeTagged{TypeTag: queryType.Name, ListTypeTag: queryType.IsList, NullableTag: queryType.Nullable}}
	}
	result := &ast.TypeRef{Name: expression.GetTypeTag(), IsList: expression.IsListType(), Nullable: true}
	value := sub.compileAssignedValue(expression, result)
	sub.writeValueCompletion("", value)
	return c.finishValueClosure(sub, compiledFunctionGoType(result, c.models), sub.b.String())
}

// A non-null empty Go slice must not become null when lifted to a nullable list.
func (c *compiler) compileNullableListValue(expression string, expected *ast.TypeRef) string {
	c.resultTmp++
	name := "_nullableList" + strconv.Itoa(c.resultTmp)
	c.write("%s := %s", name, expression)
	c.ensureListPresent(name, expected)
	return name
}

func (c *compiler) ensureListPresent(name string, expected *ast.TypeRef) {
	c.write("if %s == nil { %s = make(%s, 0) }", name, name, compiledFunctionGoType(expected, c.models))
}
