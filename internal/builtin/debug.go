package builtin

import "github.com/light-speak/luxo/pkg/ast"

// IsDebugProperty excludes enum declarations whose short member names are values.
func IsDebugProperty(member *ast.MemberExpr) bool {
	return !member.EnumConstant && !member.DeclaredField && DebugMethod(member.Field) != ""
}

// DebugMethod names the self-returning diagnostic properties, not callable methods.
func DebugMethod(name string) string {
	switch name {
	case "d":
		return "Debug"
	case "i":
		return "Info"
	case "w":
		return "Warn"
	case "e":
		return "Error"
	default:
		return ""
	}
}
