package codegen

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Select the expected list representation before compiling any element side effects.
func (c *compiler) compileExpectedExpr(expr ast.Expr, expected *ast.TypeRef) string {
	if list, ok := expr.(*ast.ListExpr); ok && expected != nil && expected.IsList {
		return c.compileTypedList(list, expected)
	}
	return c.compileExpr(expr)
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
	for _, item := range e.Items {
		value := c.compileExpr(item)
		if modelElements {
			value = c.modelReference(item, value)
		}
		items = append(items, value)
	}
	return goType + "{" + strings.Join(items, ", ") + "}"
}
