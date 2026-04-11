package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateErrorFile produces error.gen.go containing typed error constructors
// for each ErrorDecl in the Luxo source.
// Returns nil if there are no error declarations.
func generateErrorFile(result *semantic.Result, packageName string) []byte {
	var errs []*ast.ErrorDecl
	for _, file := range result.Files {
		errs = append(errs, file.Errors...)
	}
	if len(errs) == 0 {
		return nil
	}

	var b strings.Builder
	writeHeader(&b, packageName, "error.gen.go")

	b.WriteString("import (\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/errors\"\n")
	b.WriteString(")\n\n")

	for _, e := range errs {
		generateErrorConstructor(&b, e)
	}

	return []byte(b.String())
}

// generateErrorConstructor generates a typed constructor for an error declaration.
func generateErrorConstructor(b *strings.Builder, e *ast.ErrorDecl) {
	name := e.Name
	fmt.Fprintf(b, "// New%s creates a %s error.\n", name, name)
	fmt.Fprintf(b, "func New%s(", name)
	for i, p := range e.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		goType := resolveGoType(p.Type)
		fmt.Fprintf(b, "%s %s", p.Name, goType)
	}
	b.WriteString(") *errors.AppError {\n")

	fmt.Fprintf(b, "\treturn errors.New(%q, %d, %q)", name, e.Code, e.Message)

	if len(e.Fields) > 0 {
		b.WriteString(".WithData(map[string]any{\n")
		for _, p := range e.Fields {
			fmt.Fprintf(b, "\t\t%q: %s,\n", p.Name, p.Name)
		}
		b.WriteString("\t})")
	}

	b.WriteString("\n}\n\n")
}
