package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) checkWhereMembership(expression ast.Expr, receiver string, model *ResolvedType) {
	if unary, ok := expression.(*ast.UnaryExpr); ok && unary.Op == "!" {
		a.checkWhereMembership(unary.Value, receiver, model)
		return
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		return
	}
	if binary.Op == "&&" || binary.Op == "||" {
		a.checkWhereMembership(binary.Left, receiver, model)
		a.checkWhereMembership(binary.Right, receiver, model)
		return
	}
	if binary.Op != "in" {
		return
	}
	field := membershipField(binary.Left, receiver, model)
	if field == nil || !groupScalarField(field) || field.Type.Kind == TypeDuration {
		a.addError(binary.Pos, "membership requires a stored scalar model field / 成员条件需要已存储的标量模型字段")
		return
	}
	right := binary.Right
	if list, ok := right.(*ast.ListExpr); ok && len(list.Items) == 0 {
		return // The generator supplies the field's element type for an empty literal.
	}
	if !right.IsListType() || right.IsNullable() || right.GetTypeTag() != field.Type.Name {
		a.addError(right.GetPos(), "membership requires a non-null [%s] value / 成员条件需要非空的同类型列表", field.Type.Name)
	}
}

func membershipField(expression ast.Expr, receiver string, model *ResolvedType) *FieldInfo {
	if model == nil {
		return nil
	}
	if member, ok := expression.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == receiver {
			return model.LookupField(member.Field)
		}
	}
	if ident, ok := expression.(*ast.Ident); ok {
		return model.LookupField(ident.Name)
	}
	return nil
}
