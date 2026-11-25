package inferred

import (
	"github.com/light-speak/luxo/pkg/ast"
)

func (p *paginationAnalysis) expression(expr ast.Expr) pageValue {
	value := p.expressionValue(expr)
	if value.object != nil && !expr.IsNullable() && p.models[expr.GetTypeTag()] == nil {
		value.ref = false
	}
	if value.owner != nil && len(value.queries) != 0 {
		if p.values == nil {
			p.values = make(map[ast.Expr]pageValue)
		}
		p.values[expr] = value
	}
	return value
}

func (p *paginationAnalysis) expressionValue(expr ast.Expr) pageValue {
	switch value := expr.(type) {
	case nil, *ast.Literal:
		return pageValue{}
	case *ast.Ident:
		return p.lookup(value.Name)
	case *ast.CallExpr:
		return p.call(value)
	case *ast.ForStmt:
		return p.mapping(value.Collection, value.Body, value.VarName, value)
	case *ast.WhenExpr:
		return p.when(value)
	case *ast.ElvisExpr:
		return p.defaultValue(value)
	case *ast.ListExpr:
		var combined pageValue
		for _, item := range value.Items {
			combined = mergePageValues(combined, p.expression(item))
		}
		if len(combined.queries) == 0 && combined.opaque == nil {
			return pageValue{}
		}
		// Literal cardinality is independent of lists stored in its elements.
		// Keep their dependencies for consumers without paging those inner lists.
		object := newPageObject(nil)
		object.set("", combined)
		return pageValue{object: object}
	case *ast.MemberExpr:
		return p.member(value)
	case *ast.ObjectExpr:
		return p.object(value)
	case *ast.TransactionExpr:
		return p.opaqueBlock(value.Body)
	case *ast.LambdaExpr:
		return p.block(value.Body, nil, true, false)
	case *ast.AwaitExpr:
		return p.opaqueAwait(value.Body)
	case *ast.YieldExpr:
		return p.expression(value.Value)
	case *ast.UnaryExpr:
		base := p.expression(value.Value)
		if value.Op == "?" {
			return base
		}
		p.consume(base, value.Value)
	default:
		p.consumeInputs(expr)
	}
	return pageValue{}
}

func (p *paginationAnalysis) consumeInput(expression ast.Expr) pageValue {
	value := p.expression(expression)
	p.consume(value, expression)
	return value
}

func (p *paginationAnalysis) consumeInputs(expression ast.Expr) {
	switch value := expression.(type) {
	case *ast.TemplateString:
		for _, part := range value.Parts {
			p.consumeInput(part)
		}
	case *ast.BinaryExpr:
		p.consumeInput(value.Left)
		p.consumeInput(value.Right)
	case *ast.RangeExpr:
		p.consumeInput(value.Start)
		p.consumeInput(value.End)
	case *ast.BangElvisExpr:
		p.consumeInput(value.Left)
		p.consumeInput(value.Right)
	case *ast.AsyncExpr:
		p.conditionalBlock(value.Body, nil, false, false)
	}
}

func (p *paginationAnalysis) defaultValue(value *ast.ElvisExpr) pageValue {
	left := p.expression(value.Left)
	if thrown, ok := value.Right.(*ast.UnaryExpr); ok && thrown.Op == "throw" {
		return left
	}
	if !value.Left.IsNullable() {
		return left
	}
	return p.branch(value, left, p.expression(value.Right))
}

func (p *paginationAnalysis) opaqueBlock(body *ast.Block) pageValue {
	result := p.block(body, nil, true, false)
	result.bad = len(result.queries) != 0
	return result
}

func (p *paginationAnalysis) opaqueAwait(body *ast.Block) pageValue {
	// A single await result can precede a trailing statement.
	if body == nil || len(body.Stmts) < 2 {
		return p.opaqueBlock(body)
	}
	result := false
	for index, stmt := range body.Stmts {
		if expression, ok := stmt.(*ast.ExprStmt); ok && expression.Expr != nil {
			if result || index == len(body.Stmts)-1 {
				return p.opaqueBlock(body)
			}
			result = true
		}
	}
	if !result {
		return p.opaqueBlock(body)
	}
	var value pageValue
	p.visitAwaitResults(body, func(expr ast.Expr) {
		value = p.awaitResult(p.expression(expr))
	})
	return value
}

func (p *paginationAnalysis) visitAwaitResults(body *ast.Block, visit func(ast.Expr)) {
	if body == nil {
		return
	}
	outer := len(p.scopes)
	defer func() { p.scopes = p.scopes[:outer] }()
	for _, stmt := range body.Stmts {
		expression, ok := stmt.(*ast.ExprStmt)
		if !ok || expression.Expr == nil {
			if len(p.scopes) == outer {
				p.scopes = append(p.scopes, nil)
			}
			p.statement(stmt, false)
			continue
		}
		visit(expression.Expr)
	}
}

func (p *paginationAnalysis) awaitResult(value pageValue) pageValue {
	value.bad = value.bad || len(value.queries) != 0
	// Later statements can replace fields on a local value.
	return p.bindObject(value, nil)
}

func (p *paginationAnalysis) call(call *ast.CallExpr) pageValue {
	if query, found := p.query(call); found {
		return query
	}
	if name, ok := call.Func.(*ast.Ident); ok && p.functions[name.Name] != nil {
		return p.function(call, p.functions[name.Name])
	}
	if member, ok := call.Func.(*ast.MemberExpr); ok {
		if member.Field == "map" && len(call.Args) == 1 && member.Object.IsListType() {
			if lambda, ok := call.Args[0].Value.(*ast.LambdaExpr); ok {
				name := "it"
				if len(lambda.Params) == 1 {
					name = lambda.Params[0]
				}
				return p.mapping(member.Object, lambda.Body, name, call)
			}
		}
		base := flattenPageObject(p.expression(member.Object))
		if len(base.queries) != 0 {
			p.consume(base, call)
			base.bad = true
			return base
		}
	}
	var arguments pageValue
	for _, argument := range call.Args {
		value := p.consumeInput(argument.Value)
		arguments = mergePageValues(arguments, value)
	}
	if (call.IsListType() || call.GetTypeTag() == "") && len(arguments.queries) != 0 {
		arguments.bad = true
		return arguments
	}
	if value := p.builder(call); value.builder != "" {
		return value
	}
	return p.unknownObject(call)
}

func (p *paginationAnalysis) query(call *ast.CallExpr) (pageValue, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || !call.IsListType() {
		return pageValue{}, false
	}
	base := p.builder(member.Object)
	if base.builder == "" {
		return pageValue{}, false
	}
	switch member.Field {
	case "all", "find", "findMany", "load":
	default:
		return pageValue{}, false
	}
	p.queryInputs(call)
	for _, argument := range call.Args {
		base.limited = base.limited || argument.Name == "limit" || argument.Name == "offset"
	}
	value := pageValue{owner: call, queries: []ast.Expr{call}, bad: base.limited || member.Field == "load"}
	p.queries = append(p.queries, call)
	return value, true
}

func (p *paginationAnalysis) queryInputs(expression ast.Expr) {
	switch value := expression.(type) {
	case *ast.MemberExpr:
		p.queryInputs(value.Object)
	case *ast.CallExpr:
		p.queryInputs(value.Func)
		for _, argument := range value.Args {
			p.consumeInput(argument.Value)
		}
	}
}

func (p *paginationAnalysis) mapping(collection ast.Expr, body *ast.Block, name string, expression ast.Expr) pageValue {
	base := p.expression(collection)
	p.conditionalBlock(body, map[string]pageValue{name: p.iterationItem(collection)}, true, false)
	if len(base.queries) == 0 {
		return pageValue{}
	}
	if body == nil || len(body.Stmts) != 1 || !purePageResult(body.Stmts[0]) {
		base.bad = true
		p.consume(base, expression)
	}
	return base
}

func purePageResult(statement ast.Stmt) bool {
	switch value := statement.(type) {
	case *ast.ExprStmt:
		return purePageExpression(value.Expr)
	}
	return false
}

func purePageExpression(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.Ident, *ast.Literal:
		return true
	case *ast.MemberExpr:
		return purePageExpression(value.Object)
	case *ast.ObjectExpr:
		for _, field := range value.Fields {
			if !purePageExpression(field.Value) {
				return false
			}
		}
		return true
	case *ast.UnaryExpr:
		return value.Op != "?" && value.Op != "throw" && purePageExpression(value.Value)
	case *ast.BinaryExpr:
		return purePageExpression(value.Left) && purePageExpression(value.Right)
	}
	return false
}

func (p *paginationAnalysis) when(value *ast.WhenExpr) pageValue {
	p.consumeInput(value.Subject)
	var combined pageValue
	for _, branch := range value.Branches {
		p.consumeInput(branch.Condition)
		combined = mergePageValues(combined, p.branchValue(branch.Body))
	}
	combined = mergePageValues(combined, p.branchValue(value.Else))
	return p.branch(value, combined, pageValue{})
}

func (p *paginationAnalysis) branchValue(value ast.Expr) pageValue {
	if block, ok := value.(*ast.LambdaExpr); ok {
		return p.conditionalBlock(block.Body, nil, true, false)
	}
	return p.expression(value)
}

func (p *paginationAnalysis) branch(expr ast.Expr, left, right pageValue) pageValue {
	value := mergePageValues(left, right)
	if len(value.queries) != 0 {
		value.owner = expr
		if p.branches == nil {
			p.branches = make(map[ast.Expr]bool)
		}
		p.branches[expr] = true
	}
	return value
}
