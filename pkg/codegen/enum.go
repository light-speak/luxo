package codegen

import (
	"fmt"
	"slices"
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
	fmt.Fprintf(b, "\n// IsValid reports whether the value is declared by this service's schema.\nfunc (v %s) IsValid() bool {\n", e.Name)
	if len(e.Values) != 0 {
		b.WriteString("\tswitch v {\n\tcase ")
		for index, value := range e.Values {
			if index != 0 {
				b.WriteString(", ")
			}
			b.WriteString(e.Name + value)
		}
		b.WriteString(":\n\t\treturn true\n\t}\n")
	}
	b.WriteString("\treturn false\n}\n")
	fmt.Fprintf(b, "\n// AppendLuxo encodes a declared enum member, leaving dst unchanged on failure.\nfunc (v %s) AppendLuxo(dst []byte) ([]byte, error) {\n", e.Name)
	b.WriteString("\tif !v.IsValid() { return dst, codec.ErrInvalidEnum }\n\treturn codec.AppendString(dst, string(v))\n}\n")
}

// generateSharedEnumFile emits one canonical definition per project enum.
func generateSharedEnumFile(declarations map[string]*ast.EnumDecl) ([]byte, error) {
	if len(declarations) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	slices.Sort(names)
	var body strings.Builder
	writeHeader(&body, "luxotypes", "enum.gen.go")
	body.WriteString("import \"github.com/light-speak/luxo/pkg/lux/codec\"\n\n")
	for _, name := range names {
		generateEnum(&body, declarations[name])
		body.WriteByte('\n')
	}
	return formatGeneratedChecked([]byte(body.String()))
}

func generateEnumAlias(body *strings.Builder, declaration *ast.EnumDecl) {
	fmt.Fprintf(body, "type %s = luxotypes.%s\n\n", declaration.Name, declaration.Name)
	if len(declaration.Values) == 0 {
		return
	}
	body.WriteString("const (\n")
	for _, value := range declaration.Values {
		name := declaration.Name + value
		fmt.Fprintf(body, "\t%s = luxotypes.%s\n", name, name)
	}
	body.WriteString(")\n\n")
}

// GenerateSharedEnums emits the project's canonical enum package.
// The caller writes it to internal/luxotypes/enum.gen.go before building services.
func (g *GeneratorContext) GenerateSharedEnums() ([]byte, error) {
	if g.events == nil || len(g.events.Enums) == 0 {
		return nil, nil
	}
	if g.events.SharedEnumPackage == "" {
		return nil, fmt.Errorf("shared enums require an explicit shared package path")
	}
	return generateSharedEnumFile(g.events.Enums)
}

func (g *GeneratorContext) usesSharedEnums() bool {
	return g.events != nil && g.events.SharedEnumPackage != "" && len(g.events.Enums) != 0
}

func enumAppendExpression(typeName, destination, value string) string {
	return fmt.Sprintf("%s(%s).AppendLuxo(%s)", typeName, value, destination)
}
