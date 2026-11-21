package semantic

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func groupedQueryTail(call, group *ast.CallExpr) (*ast.CallExpr, []*ast.CallExpr) {
	var conditions []*ast.CallExpr
	for call != nil {
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok {
			break
		}
		if member.Field == "select" && member.Object == group {
			slices.Reverse(conditions)
			return call, conditions
		}
		if member.Field != "having" {
			break
		}
		conditions = append(conditions, call)
		call, _ = member.Object.(*ast.CallExpr)
	}
	return nil, nil
}

func (a *Analyzer) checkGroupHaving(call *ast.CallExpr, model *ResolvedType, plan *ast.GroupProjection, scope *Scope) {
	expression, parameter := groupLambda(call)
	if expression == nil {
		a.addError(call.Pos, "having requires a single predicate expression / having 需要单个条件表达式")
		return
	}
	condition := a.groupHavingCondition(expression, parameter, model, plan.Keys, scope)
	if condition != nil {
		plan.Having = append(plan.Having, condition)
	}
}

func (a *Analyzer) groupHavingCondition(expression ast.Expr, parameter string, model *ResolvedType, keys []string, scope *Scope) *ast.GroupCondition {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		a.addError(expression.GetPos(), "having requires a group comparison or Boolean composition / having 需要分组比较或布尔组合")
		return nil
	}
	condition := &ast.GroupCondition{Operator: binary.Op}
	if binary.Op == "&&" || binary.Op == "||" {
		condition.Left = a.groupHavingCondition(binary.Left, parameter, model, keys, scope)
		condition.Right = a.groupHavingCondition(binary.Right, parameter, model, keys, scope)
		if condition.Left == nil || condition.Right == nil {
			return nil
		}
		return condition
	}
	column, resolved := a.groupColumn(binary.Left, parameter, model, keys)
	if resolved == nil || !a.checkHavingOperator(binary, resolved) {
		return nil
	}
	if !a.checkHavingValue(binary.Right, parameter, resolved, scope) {
		return nil
	}
	column.Type, column.Nullable = resolved.Name, resolved.Nullable
	condition.Column, condition.Value = column, binary.Right
	return condition
}

func (a *Analyzer) checkHavingOperator(binary *ast.BinaryExpr, resolved *ResolvedType) bool {
	op := binary.Op
	if op != "==" && op != "!=" && op != ">" && op != ">=" && op != "<" && op != "<=" {
		a.addError(binary.Pos, "unsupported having comparison '%s' / having 不支持比较 '%s'", op, op)
		return false
	}
	if literal, ok := binary.Right.(*ast.Literal); ok && literal.Kind == token.Null {
		if op != "==" && op != "!=" {
			a.addError(binary.Pos, "having null comparison requires == or != / having 空值比较只能使用 == 或 !=")
			return false
		}
		if !resolved.Nullable {
			a.addError(binary.Pos, "having cannot compare a non-null group value with null / having 非空分组值不能与 null 比较")
			return false
		}
		return true
	}
	if !resolved.IsComparable() || op != "==" && op != "!=" && !resolved.IsOrderable() {
		a.addError(binary.Pos, "unsupported having comparison for %s / having 不支持 %s 的此类比较", resolved.Name, resolved.Name)
		return false
	}
	return true
}

func (a *Analyzer) checkHavingValue(expression ast.Expr, parameter string, expected *ResolvedType, scope *Scope) bool {
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: expression}}}
	dependent := false
	ast.WalkExprs(body, func(value ast.Expr) {
		if ident, ok := value.(*ast.Ident); ok && ident.Name == parameter {
			dependent = true
		}
	})
	if dependent {
		a.addError(expression.GetPos(), "having comparison value must be row-independent / having 比较值不能引用当前分组")
		return false
	}
	a.rejectPredicateQueries(body, scope)
	actual := a.checkExpr(expression, scope)
	if actual == nil {
		return false
	}
	if literal, ok := expression.(*ast.Literal); ok && literal.Kind == token.Null {
		return true
	}
	if actual.Kind == TypeUnknown || actual.Nullable || !isTypeAssignable(expected.AsNonNull(), actual) {
		a.addError(expression.GetPos(), "having value type mismatch: expected %s, got %s / having 比较值类型不匹配", expected.Name, formatResolvedType(actual))
		return false
	}
	return true
}
