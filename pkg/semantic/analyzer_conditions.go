package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Only facts guaranteed by the taken branch can narrow its lexical bindings.
// Allocate a short-circuit scope only when a nullable binding actually narrows.
func narrowCondition(expr ast.Expr, truth bool, parent, narrowed *Scope) *Scope {
	switch condition := expr.(type) {
	case *ast.UnaryExpr:
		if condition.Op == "!" {
			return narrowCondition(condition.Value, !truth, parent, narrowed)
		}
	case *ast.BinaryExpr:
		if condition.Op == "&&" && truth || condition.Op == "||" && !truth {
			narrowed = narrowCondition(condition.Left, truth, parent, narrowed)
			return narrowCondition(condition.Right, truth, parent, narrowed)
		}
		if condition.Op == "!=" && truth || condition.Op == "==" && !truth {
			name := nullComparedBinding(condition.Left, condition.Right)
			if name == "" {
				return narrowed
			}
			typ := parent.ResolvedTypeOf(name)
			if typ != nil && typ.Nullable {
				if narrowed == nil {
					narrowed = parent.Child()
				}
				narrowed.Narrow(name, typ.AsNonNull())
			}
		}
	}
	return narrowed
}

func nullComparedBinding(left, right ast.Expr) string {
	if literal, ok := left.(*ast.Literal); ok && literal.Kind == token.Null {
		left, right = right, left
	}
	identifier, ok := left.(*ast.Ident)
	if !ok {
		return ""
	}
	literal, ok := right.(*ast.Literal)
	if !ok || literal.Kind != token.Null {
		return ""
	}
	return identifier.Name
}
