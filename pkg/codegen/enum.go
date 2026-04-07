package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// generateEnum generates Go type + constants for a Luxo enum declaration.
//
// Input:
//
//	enum Role { USER, ADMIN, MODERATOR }
//
// Output:
//
//	type Role string
//	const (
//	    RoleUSER      Role = "USER"
//	    RoleADMIN     Role = "ADMIN"
//	    RoleMODERATOR Role = "MODERATOR"
//	)
func generateEnum(b *strings.Builder, e *ast.EnumDecl) {
	fmt.Fprintf(b, "type %s string\n\n", e.Name)

	// Calculate max constant name length for alignment.
	maxLen := 0
	for _, v := range e.Values {
		name := e.Name + v
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	b.WriteString("const (\n")
	for _, v := range e.Values {
		name := e.Name + v
		fmt.Fprintf(b, "\t%-*s %s = %q\n", maxLen, name, e.Name, v)
	}
	b.WriteString(")\n")
}
