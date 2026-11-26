package codegen

import (
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

// Select a contextual representation before lowering any expression effects.
func (c *compiler) compileExpectedExpr(expr ast.Expr, expected *ast.TypeRef) string {
	if await, ok := expr.(*ast.AwaitExpr); ok {
		return c.compileAwaitValue(await, expected)
	}
	if list, ok := expr.(*ast.ListExpr); ok && expected != nil && expected.IsList {
		return c.compileTypedList(list, expected)
	}
	if member, ok := expr.(*ast.MemberExpr); ok && expected != nil && expected.IsList && isEmptyDebugList(member) {
		value, _ := c.compileDebugMember(member, expected)
		return value
	}
	return c.compileExpr(expr)
}

// Await values use the same task join and error return as local bindings.
// Only context-dependent results borrow the consumer's concrete type.
func (c *compiler) compileAwaitValue(expr *ast.AwaitExpr, expected *ast.TypeRef) string {
	value := singleAwaitValue(expr)
	name := c.discardedBindingName()
	c.compileAwaitBindings(expr, []string{name}, awaitValueRepresentation(value, expected))
	if value.GetTypeTag() == "null" && !concreteAwaitType(expected) {
		// Keep the joined result slot valid even when the consumer needs an untyped nil.
		c.write("_ = %s", c.bindings.name(name))
		return "nil"
	}
	return c.bindings.name(name)
}

func singleAwaitValue(expr *ast.AwaitExpr) ast.Expr {
	const invalid = "await in a value position requires a single result"
	if expr.Body == nil || expr.NoAwaitResult {
		panic(&InternalCompilerError{Detail: invalid})
	}
	var result ast.Expr
	for _, statement := range expr.Body.Stmts {
		value, ok := statement.(*ast.ExprStmt)
		if !ok || value.Expr == nil {
			continue
		}
		if result != nil {
			panic(&InternalCompilerError{Detail: invalid})
		}
		result = value.Expr
	}
	if result == nil {
		panic(&InternalCompilerError{Detail: invalid})
	}
	return result
}

func awaitValueRepresentation(value ast.Expr, expected *ast.TypeRef) *ast.TypeRef {
	if value.GetTypeTag() == "null" {
		ref := ast.TypeRef{Name: "struct{}"}
		if concreteAwaitType(expected) {
			ref = *expected
		}
		ref.Nullable = true
		return &ref
	}
	if value.IsListType() && value.GetTypeTag() == "" {
		ref := ast.TypeRef{Name: "struct{}", IsList: true}
		if concreteAwaitType(expected) && expected.IsList {
			ref = *expected
		}
		ref.Nullable = false
		return &ref
	}
	return nil
}

func concreteAwaitType(ref *ast.TypeRef) bool {
	return ref != nil && ref.Name != "" && ref.Name != "null"
}

// Empty debug chains inherit the surrounding concrete list type, just like [].
func isEmptyDebugList(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.ListExpr:
		return len(value.Items) == 0
	case *ast.MemberExpr:
		return !value.SafeCall && builtin.IsDebugProperty(value) && isEmptyDebugList(value.Object)
	default:
		return false
	}
}

func (c *compiler) compileFunctionArgument(expr ast.Expr, expected *ast.TypeRef) string {
	value := c.compileExpectedExpr(expr, expected)
	if expected != nil && !expected.IsList && c.models[expected.Name] != nil {
		return c.modelReference(expr, value)
	}
	return value
}

// Model locals may be values; function and list ABIs use pointers without copying them.
func (c *compiler) modelReference(expr ast.Expr, value string) string {
	if _, ok := expr.(*ast.ObjectExpr); ok {
		return "&" + value
	}
	if ident, ok := expr.(*ast.Ident); ok && c.vars[ident.Name].isValue {
		return "&" + value
	}
	return value
}

// compileList emits a concrete slice when semantic analysis resolved the element type.
func (c *compiler) compileList(e *ast.ListExpr) string {
	goType := c.goTypeForExpr(e)
	if goType == "" {
		goType = "[]any"
	}
	return c.compileListItems(e, goType, c.models[e.GetTypeTag()] != nil)
}

func (c *compiler) compileTypedList(e *ast.ListExpr, listType *ast.TypeRef) string {
	goType := resolveGoType(listType)
	isModel := c.models[listType.Name] != nil
	if isModel {
		goType = "[]*" + listType.Name
	}
	return c.compileListItems(e, goType, isModel)
}

func (c *compiler) compileListItems(e *ast.ListExpr, goType string, modelElements bool) string {
	items := make([]string, 0, len(e.Items))
	lastEffect := -1
	for index := len(e.Items) - 1; index >= 0; index-- {
		if expressionMayHaveEffects(e.Items[index]) {
			lastEffect = index
			break
		}
	}
	for index, item := range e.Items {
		value := c.compileMaterializedValue(item, nil)
		if modelElements {
			value = c.modelReference(item, value)
		}
		if index < lastEffect {
			value = c.captureExpressionValue(item, value, strings.TrimPrefix(goType, "[]"))
		}
		items = append(items, value)
	}
	return goType + "{" + strings.Join(items, ", ") + "}"
}

// Earlier operands must be retained before this argument emits side effects.
func lastEffectfulArgument(arguments []*ast.NamedArg) int {
	for index := len(arguments) - 1; index >= 0; index-- {
		if expressionMayHaveEffects(arguments[index].Value) {
			return index
		}
	}
	return -1
}

// Calls and control-flow expressions may emit statements before their Go value.
func expressionMayHaveEffects(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Literal, *ast.Ident:
		return false
	case *ast.MemberExpr:
		return memberMayHaveEffects(value)
	case *ast.BinaryExpr:
		if value.Left.GetTypeTag() == "Decimal" && value.Right.GetTypeTag() == "Decimal" && decimalArithmeticMethod(value.Op) != "" {
			return true
		}
		return value.Op == "<-" || expressionMayHaveEffects(value.Left) || expressionMayHaveEffects(value.Right)
	case *ast.UnaryExpr:
		return value.Op != "+" && value.Op != "-" && value.Op != "!" || expressionMayHaveEffects(value.Value)
	case *ast.ListExpr:
		for _, item := range value.Items {
			if expressionMayHaveEffects(item) {
				return true
			}
		}
		return false
	case *ast.ObjectExpr:
		for _, field := range value.Fields {
			if expressionMayHaveEffects(field.Value) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func memberMayHaveEffects(value *ast.MemberExpr) bool {
	if value.SafeCall {
		return true
	}
	if ident, ok := value.Object.(*ast.Ident); ok && ident.Name == "my" {
		return true
	}
	if builtin.IsDebugProperty(value) {
		return true
	}
	return expressionMayHaveEffects(value.Object)
}

// Capture a value before later operands can mutate the storage it reads.
func (c *compiler) captureExpressionValue(expr ast.Expr, value, goType string) string {
	if _, literal := expr.(*ast.Literal); literal {
		return value
	}
	c.resultTmp++
	name := "_evaluated" + strconv.Itoa(c.resultTmp)
	if goType == "" {
		c.write("%s := %s", name, value)
	} else {
		c.write("var %s %s = %s", name, goType, value)
	}
	return name
}
