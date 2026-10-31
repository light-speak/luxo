package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) checkQueryArgument(call *ast.CallExpr, argument *ast.NamedArg, scope, outer *Scope, callType *ResolvedType) *ResolvedType {
	member, ok := call.Func.(*ast.MemberExpr)
	if ok && member.Field == "where" && callType != nil && callType.Kind == TypeQueryBuilder {
		if argument.Name != "" {
			return a.checkExpr(argument.Value, outer)
		}
		return a.checkWhereArgument(argument.Value, scope, outer, callType.ModelType)
	}
	if argument.Name == "where" && isCRUDIdent(call) && scope != outer {
		if row := scope.LookupLocal("it"); row != nil && row.Type != nil && row.Type.Kind == TypeModel {
			return a.checkWhereArgument(argument.Value, scope, outer, row.Type)
		}
	}
	return a.checkExpr(argument.Value, scope)
}

func (a *Analyzer) checkWhereArgument(expression ast.Expr, fields, outer *Scope, model *ResolvedType) *ResolvedType {
	parameter := "it"
	if lambda, ok := expression.(*ast.LambdaExpr); ok {
		expression, parameter = lambda.SingleExpression()
		if expression == nil {
			a.addError(lambda.Pos, "where requires a single predicate expression / where 需要单个条件表达式")
			return nil
		}
		// The injected query scope already owns the implicit row binding.
		// Reuse it without changing the caller's symbols or duplicating a map.
		row := fields.LookupLocal(parameter)
		if fields == outer || row == nil || row.Kind != SymVariable || row.Type != model {
			fields = fields.Child()
			fields.Define(&Symbol{Name: parameter, Kind: SymVariable, Type: model})
		}
		a.rejectPredicateQueries(lambda.Body, fields)
	}
	values := queryValueScope{parent: outer, parameter: parameter, model: model}
	previous := a.inLambda
	a.inLambda = true
	result := a.checkWhereExpression(expression, fields, &values)
	a.inLambda = previous
	a.checkWhereMembership(expression, parameter, model)
	if result != nil && !result.IsNonNullBoolean() {
		a.addError(expression.GetPos(), "where predicate must return non-null Boolean / where 条件必须返回非空 Boolean")
	}
	return result
}

// Simple constants and other identifiers cannot reference the query row. Only
// complex values need the guarded child; never mutate caller-owned bindings.
type queryValueScope struct {
	parent    *Scope
	parameter string
	model     *ResolvedType
	guarded   *Scope
}

func (s *queryValueScope) forExpr(expr ast.Expr) *Scope {
	switch value := expr.(type) {
	case *ast.Literal:
		return s.parent
	case *ast.Ident:
		if value.Name != s.parameter {
			return s.parent
		}
	}
	if s.guarded == nil {
		referencesRow := false
		ast.WalkExpr(expr, func(expression ast.Expr) {
			if identifier, ok := expression.(*ast.Ident); ok && identifier.Name == s.parameter {
				referencesRow = true
			}
		})
		if !referencesRow {
			return s.parent
		}
		s.guarded = s.parent.Child()
		s.guarded.Define(&Symbol{Name: s.parameter, Kind: SymQueryRow, Type: s.model})
	}
	return s.guarded
}

func (a *Analyzer) checkWhereExpression(expression ast.Expr, fields *Scope, values *queryValueScope) *ResolvedType {
	if unary, ok := expression.(*ast.UnaryExpr); ok && unary.Op == "!" {
		resolved := a.checkBooleanNegation(a.checkWhereExpression(unary.Value, fields, values), unary.Pos)
		setExprResolvedType(unary, resolved)
		return resolved
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		if result, handled := a.checkWhereStringCall(call, fields, values); handled {
			return result
		}
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		return a.checkWhereValue(expression, fields, values)
	}
	var left, right *ResolvedType
	if binary.Op == "&&" || binary.Op == "||" {
		left = a.checkWhereExpression(binary.Left, fields, values)
		right = a.checkWhereExpression(binary.Right, fields, values)
	} else {
		left = a.checkExpr(binary.Left, fields)
		right = a.checkExpr(binary.Right, values.forExpr(binary.Right))
	}
	resolved := a.checkBinaryOp(binary.Op, left, right, binary.Pos)
	if resolved != nil {
		setExprResolvedType(expression, resolved)
	}
	return resolved
}

func (a *Analyzer) checkWhereValue(expression ast.Expr, fields *Scope, values *queryValueScope) *ResolvedType {
	if field := membershipField(expression, values.parameter, values.model); field != nil {
		if !groupScalarField(field) {
			a.addError(expression.GetPos(), "predicate requires a stored scalar field / 查询条件需要已存储的标量字段")
		}
		return a.checkExpr(expression, fields)
	}
	return a.checkExpr(expression, values.forExpr(expression))
}

func (a *Analyzer) checkWhereStringCall(call *ast.CallExpr, fields *Scope, values *queryValueScope) (*ResolvedType, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "contains" && member.Field != "startsWith" && member.Field != "endsWith" {
		return nil, false
	}
	previous := a.callTarget
	a.callTarget = call.Func
	result := a.checkExpr(call.Func, fields)
	a.callTarget = previous
	field := membershipField(member.Object, values.parameter, values.model)
	if field == nil || !groupScalarField(field) || field.Type.Kind != TypeString {
		a.addError(call.Pos, "string predicate requires a stored String field / 字符串条件需要已存储的 String 字段")
		return nil, true
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" {
		a.addError(call.Pos, "string predicate requires one unnamed String argument / 字符串条件需要一个未命名 String 参数")
		return nil, true
	}
	argument := call.Args[0].Value
	actual := a.checkExpr(argument, values.forExpr(argument))
	if actual == nil || actual.Kind != TypeString || actual.IsList || actual.Nullable {
		a.addError(argument.GetPos(), "string predicate requires a non-null String value / 字符串条件需要非空 String 值")
	}
	setExprResolvedType(call, result)
	return result, true
}

// Aggregate methods are valid inside group projections but nested database
// queries cannot be used as a row predicate's expression.
func (a *Analyzer) rejectPredicateQueries(body *ast.Block, scope *Scope) {
	ast.WalkExprs(body, func(expression ast.Expr) {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			return
		}
		if member, ok := call.Func.(*ast.MemberExpr); ok && isQueryMethod(member.Field) {
			if a.resolveModelFromExpr(member.Object, scope) != nil {
				a.addError(call.Pos, "database query inside query predicate is forbidden / 查询条件内禁止数据库查询")
			}
		}
	})
}
