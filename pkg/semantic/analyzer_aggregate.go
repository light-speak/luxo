package semantic

import "github.com/light-speak/luxo/pkg/ast"

// Aggregate results are determined by the stored field, never by a float fallback.
func (a *Analyzer) checkScalarAggregate(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || !isScalarAggregate(member.Field) {
		return nil, false
	}
	owner := a.checkExpr(member.Object, scope)
	if owner == nil {
		return nil, false
	}
	if owner.Kind == TypeQueryBuilder {
		owner = owner.ModelType
	}
	if owner == nil || owner.Kind != TypeModel || owner.IsList {
		return nil, false
	}
	if len(call.Args) != 1 {
		a.addError(call.Pos, "aggregate requires one stored numeric field / 聚合必须指定一个已存储的数值字段")
		return nil, true
	}
	expr := call.Args[0].Value
	parameter := "it"
	if lambda, ok := expr.(*ast.LambdaExpr); ok {
		expr, parameter = lambda.SingleExpression()
	}
	field := aggregateField(owner, expr, parameter)
	if field == nil || field.Computed || field.Type == nil || field.Type.IsList || !aggregateNumeric(field.Type.Kind) {
		a.addError(call.Pos, "aggregate requires one stored numeric field / 聚合必须指定一个已存储的数值字段")
		return nil, true
	}
	result := field.Type.AsNonNull()
	if member.Field == "avg" && result.Kind == TypeInt {
		result = a.types["Float"]
	}
	setExprResolvedType(expr, field.Type)
	return result, true
}

func isScalarAggregate(method string) bool {
	return method == "sum" || method == "avg" || method == "min" || method == "max"
}

func aggregateNumeric(kind TypeKind) bool {
	return kind == TypeInt || kind == TypeFloat || kind == TypeDecimal
}

func aggregateField(model *ResolvedType, expr ast.Expr, parameter string) *FieldInfo {
	if field, ok := expr.(*ast.Ident); ok {
		return model.LookupField(field.Name)
	}
	if field, ok := expr.(*ast.MemberExpr); ok {
		owner, ok := field.Object.(*ast.Ident)
		if ok && owner.Name == parameter {
			return model.LookupField(field.Field)
		}
	}
	return nil
}
