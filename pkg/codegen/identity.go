package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// compileMyField compiles my.field access to Go identity method call.
// identityExpr is the Go expression for the identity object (e.g., "identity", "buf.Identity").
func compileMyField(field, identityExpr string) string {
	if field == "id" {
		return fmt.Sprintf("api.IdentityID(%s)", identityExpr)
	}
	return fmt.Sprintf("api.IdentityInt(%s, %q)", identityExpr, field)
}

// inferMyFieldType checks if a compiled my.field expression should use String instead of Int,
// based on the comparison context (other side of a BinaryExpr).
// Returns the updated compiled string.
func inferMyFieldType(compiled string, self ast.Expr, other ast.Expr, identityExpr string) string {
	me, ok := self.(*ast.MemberExpr)
	if !ok {
		return compiled
	}
	ident, ok := me.Object.(*ast.Ident)
	if !ok || ident.Name != "my" || me.Field == "id" {
		return compiled
	}
	if lit, ok := other.(*ast.Literal); ok {
		if lit.Kind == token.String {
			return fmt.Sprintf("api.IdentityString(%s, %q)", identityExpr, me.Field)
		}
		if lit.Kind == token.Int || lit.Kind == token.Float {
			return fmt.Sprintf("api.IdentityInt(%s, %q)", identityExpr, me.Field)
		}
	}
	return compiled
}
