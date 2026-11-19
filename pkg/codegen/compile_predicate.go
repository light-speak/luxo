package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
	"strings"
)

func (c *compiler) modelTerminalFunctionModel(call *ast.CallExpr) (*ast.Ident, string) {
	name, builtin := call.Func.(*ast.Ident)
	if !builtin {
		return nil, ""
	}
	resultType := inferred.ModelTerminalType(name.Name)
	if resultType == "" || c.functions[name.Name] != nil {
		return nil, ""
	}
	if len(call.Args) > 0 && call.Args[0] != nil && call.Args[0].Name == "" {
		if model, ok := call.Args[0].Value.(*ast.Ident); ok && c.models[model.Name] != nil {
			if _, local := c.vars[model.Name]; !local {
				return model, resultType
			}
		}
	}
	position := call.Pos
	panic(queryCompilerFailure(position, name.Name, " requires a positional model argument"))
}

func (c *compiler) validateBulkUpdate(model *ast.ModelDecl, arguments []*ast.NamedArg, position token.Position) {
	for _, argument := range arguments {
		var field inferred.MutationField
		if model != nil && argument != nil && argument.Name != "where" {
			if declaration := modelField(model, argument.Name); declaration != nil && declaration.Type != nil {
				field = inferred.MutationField{TypeName: declaration.Type.Name, List: declaration.Type.IsList,
					Nullable: declaration.Type.Nullable, Computed: declaration.Computed != nil,
					Immutable: hasDirective(declaration.Directives, "immutable"), Relation: c.models[declaration.Type.Name] != nil}
			}
		}
		_, message := inferred.MutationArgument(argument, field)
		if message != "" {
			if argument != nil && argument.Value != nil {
				position = argument.Value.GetPos()
			}
			panic(queryCompilerFailure(position, "updateMany", message))
		}
	}
}

func (c *compiler) compileQueryWhere(b *strings.Builder, link chainLink) ast.Expr {
	where, index, message := inferred.QueryWhere(link.args)
	if message != "" {
		position := token.Position{}
		if link.expr != nil {
			position = link.expr.GetPos()
		}
		if argument := link.args[index]; argument != nil && argument.Value != nil {
			position = argument.Value.GetPos()
		}
		panic(queryCompilerFailure(position, link.method, message))
	}
	c.captureQueryReceiver(b, link.args)
	return where
}

func (c *compiler) compileDeleteMany(b *strings.Builder, model string, link chainLink) {
	where := c.compileQueryWhere(b, link)
	c.compileBulkMutationBase(b, model, where)
	if declaration := c.models[model]; declaration != nil && isSoftDelete(declaration) {
		b.WriteString(".SoftDelete(ctx)")
	} else {
		b.WriteString(".Delete(ctx)")
	}
}

func queryCompilerFailure(position token.Position, context, message string) *InternalCompilerError {
	return &InternalCompilerError{Detail: fmt.Sprintf("%s:%d:%d: %s%s", position.File, position.Line, position.Col, context, message)}
}

func (c *compiler) checkModelQueryReceiver(call *ast.CallExpr) {
	for {
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok {
			return
		}
		if !member.DeclaredField && inferred.RequiresModelQueryReceiver(member.Field) && c.isInstanceQueryReceiver(member.Object) {
			message := fmt.Sprintf("query method %q requires a model type or query builder receiver", member.Field)
			panic(queryCompilerFailure(member.Pos, "", message))
		}
		call, ok = member.Object.(*ast.CallExpr)
		if !ok {
			return
		}
	}
}

func (c *compiler) isInstanceQueryReceiver(expression ast.Expr) bool {
	if name, ok := expression.(*ast.Ident); ok {
		if value, local := c.vars[name.Name]; local {
			if c.queryAliasModel(name.Name) != "" {
				return false
			}
			return value.isModel || c.models[name.Name] != nil
		}
		return false
	}
	return expression.IsModelType() && !expression.IsListType() && c.models[expression.GetTypeTag()] != nil
}

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
		c.captureQueryReceiver(b, []*ast.NamedArg{argument})
		switch argument.Name {
		case "where":
			appendFindCondition(b, model, c.compileWhereArg(model, argument.Value))
		case "order":
			seedFindBuilder(b, model)
			c.compileOrderByChain(b, model, []*ast.NamedArg{argument})
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
		start, end := c.compileRangeBounds(bounds)
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
