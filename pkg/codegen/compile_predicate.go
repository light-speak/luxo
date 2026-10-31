package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"strings"
)

func (c *compiler) compileWhereBoolean(model string, expr ast.Expr, receiver string) string {
	if field := c.whereBooleanField(model, expr, receiver); field != "" {
		return field + ".IsTrue()"
	}
	value := c.compileExpr(expr)
	if expr.GetTypeTag() == "Boolean" && !expr.IsListType() {
		return "lux.BoolCondition(" + value + ")"
	}
	return value
}

func (c *compiler) whereBooleanField(model string, expr ast.Expr, receiver string) string {
	field := wherePredicateField(expr, receiver)
	if declaration := c.models[model]; declaration != nil && field != "" {
		for _, candidate := range declaration.Fields {
			if candidate.Name == field && candidate.Type != nil && candidate.Type.Name == "Boolean" && !candidate.Type.IsList {
				return model + "Where." + str.Capitalize(field)
			}
		}
	}
	return ""
}

// Normalize function-style reads into the same model chain as method calls.
func functionFindModel(call *ast.CallExpr) (*ast.Ident, string) {
	name, ok := call.Func.(*ast.Ident)
	if !ok || len(call.Args) == 0 || call.Args[0].Name != "" {
		return nil, ""
	}
	switch name.Name {
	case "find", "findMany", "findFirst":
		model, _ := call.Args[0].Value.(*ast.Ident)
		return model, name.Name
	default:
		return nil, ""
	}
}

func findReturnsList(link chainLink) bool {
	if link.method == "findMany" {
		return true
	}
	if link.method == "find" {
		for _, argument := range link.args {
			if argument.Name == "where" {
				return true
			}
		}
	}
	return false
}

func (c *compiler) compileFindChain(b *strings.Builder, model string, link chainLink) {
	for _, argument := range link.args {
		switch argument.Name {
		case "where":
			appendFindCondition(b, model, c.compileWhereArg(model, argument.Value))
		case "order":
			seedFindBuilder(b, model)
			c.compileOrderByChain(b, []*ast.NamedArg{argument})
		case "limit", "offset":
			seedFindBuilder(b, model)
			c.compileModifierMethod(b, model, chainLink{method: argument.Name, args: []*ast.NamedArg{argument}}, 1, 2, false)
		default:
			field := argument.Name
			if field == "" {
				field = primaryKeyFieldName(c.models[model])
			}
			appendFindCondition(b, model, c.compileWhereComparison(model, field, "Eq", argument.Value))
		}
	}
	seedFindBuilder(b, model)
	if link.method == "find" && !findReturnsList(link) {
		b.WriteString(".FirstRequired(ctx)")
		return
	}
	method := "first"
	if findReturnsList(link) {
		method = "all"
	}
	c.compileTerminalMethod(b, model, chainLink{method: method})
}

func seedFindBuilder(b *strings.Builder, model string) {
	if b.Len() == 0 {
		seedFindQuery(b, model)
		b.WriteString(".Where()")
	}
}

func seedFindQuery(b *strings.Builder, model string) {
	if b.Len() == 0 {
		b.WriteString("app.")
		b.WriteString(model)
	}
}

func appendFindCondition(b *strings.Builder, model, condition string) {
	// Reserve the condition and ordinary terminal suffix together, avoiding a
	// second growth when First/All is appended immediately after the condition.
	capacity := len(condition) + len(".Where().FirstRequired(ctx)")
	if b.Len() == 0 {
		b.Grow(len("app.") + len(model) + capacity)
		seedFindQuery(b, model)
	} else {
		b.Grow(capacity)
	}
	b.WriteString(".Where(")
	b.WriteString(condition)
	b.WriteByte(')')
}

func wherePredicateField(expr ast.Expr, receiver string) string {
	if member, ok := expr.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == receiver {
			return member.Field
		}
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func (c *compiler) compileWhereMembership(model, field string, expr *ast.BinaryExpr) string {
	prefix := model + "Where." + str.Capitalize(field)
	if bounds, ok := expr.Right.(*ast.RangeExpr); ok {
		start := c.compileExpr(bounds.Start)
		end := c.compileExpr(bounds.End)
		return prefix + ".Between(" + start + ", " + end + ")"
	}
	name := expr.Left.GetTypeTag()
	var expected *ast.TypeRef
	if name != "" {
		expected = &ast.TypeRef{Name: name, IsList: true}
	}
	values := c.compileExpectedExpr(expr.Right, expected)
	method := "In"
	if c.enums[name] {
		method = "InEnum"
	}
	return prefix + "." + method + "(" + values + "...)"
}
