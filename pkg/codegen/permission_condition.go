package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// One reader spans the entire predicate: negating an invalid read cannot grant
// access, while ordinary short-circuit branches still avoid unnecessary reads.
func compileTypedPermissionCondition(expression ast.Expr) string {
	if !isTypedPermissionCondition(expression) {
		return ""
	}
	if conditionHasCheckedArithmetic(expression) {
		checked := compileCheckedVisibleCondition(expression, nil, "api.NewIdentityReader(identity)")
		return "func() bool { allowed, err := " + checked + "; return err == nil && allowed }()"
	}
	compiled := compileVisibleCondition(expression, nil, "_identity")
	if compiled == "" {
		return ""
	}
	return "func() bool { _identity := api.NewIdentityReader(identity); return _identity.Allowed(" + compiled + ") }()"
}

func isTypedPermissionCondition(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.UnaryExpr:
		return (value.Op == "!" || value.Op == "-") && isTypedPermissionCondition(value.Value)
	case *ast.BinaryExpr:
		return typedPermissionOperator(value.Op) && isTypedPermissionCondition(value.Left) && isTypedPermissionCondition(value.Right)
	case *ast.MemberExpr:
		if value.EnumConstant {
			return true
		}
		name, ok := value.Object.(*ast.Ident)
		if !ok || name.Name != "my" || value.IsListType() || value.IsNullable() {
			return false
		}
		if value.IsEnumType() {
			return true
		}
		switch value.GetTypeTag() {
		case "Int", "Float", "String", "Boolean":
			return true
		}
	case *ast.Literal:
		switch value.Kind {
		case token.Int, token.Float, token.String, token.True, token.False:
			return true
		}
	}
	return false
}

func typedPermissionOperator(op string) bool {
	switch op {
	case "&&", "||", "==", "!=", "<", "<=", ">", ">=", "+", "-", "*":
		return true
	}
	return false
}
