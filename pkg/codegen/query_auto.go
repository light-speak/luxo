package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func automaticValueName(field *ast.FieldDecl) string {
	return "_auto" + str.Capitalize(field.Name)
}

func isAutomaticUUID(field *ast.FieldDecl) bool {
	return field.Type != nil && field.Type.Name == "UUID" && hasDirective(field.Directives, "auto")
}

func automaticValueReference(field *ast.FieldDecl, expression string) string {
	if preparedMutationField(field) {
		return automaticValueName(field)
	}
	if isAutomaticUUID(field) {
		return automaticValueName(field) + "Raw"
	}
	return expression
}

// Automatic values share the typed preparation used by explicit setters.
// A nested scope keeps the shared preparation temporaries local to each field.
func writeAutomaticValue(b *strings.Builder, model string, field *ast.FieldDecl, expression, indent, onError string) string {
	if isAutomaticUUID(field) {
		raw := automaticValueName(field) + "Raw"
		fmt.Fprintf(b, "%s%s, err := %s\n%sif err != nil { %s }\n", indent, raw, expression, indent, onError)
		expression = raw
	}
	if !preparedMutationField(field) {
		return expression
	}
	value := automaticValueName(field)
	if field.Type.Nullable && !field.Type.IsList {
		fmt.Fprintf(b, "%s%sStorage := %s\n%s%s := &%sStorage\n", indent, value, expression, indent, value, value)
	} else {
		fmt.Fprintf(b, "%s%s := %s\n", indent, value, expression)
	}
	fmt.Fprintf(b, "%s{\n", indent)
	writeMutationPreparation(b, model, field, value, indent+"\t", onError)
	fmt.Fprintf(b, "%s}\n", indent)
	return value
}

func automaticUpdateField(fields []*ast.FieldDecl, directives ...[]*ast.Directive) *ast.FieldDecl {
	for _, field := range fields {
		if isAutoOnUpdate(field, directives...) {
			return field
		}
	}
	return nil
}

func (g *GeneratorContext) writeAutomaticSet(b *strings.Builder, model string, field *ast.FieldDecl, expression, receiver, indent, onError string) {
	value := writeAutomaticValue(b, model, field, expression, indent, onError)
	fmt.Fprintf(b, "%s%s.Set(%q, %s)\n", indent, receiver, g.sqlColumn(field.Name), value)
}

// Both DSL writes and the public query builder enter this method exactly once.
func (g *GeneratorContext) generatePreparedQueryUpdate(b *strings.Builder, model string, fields []*ast.FieldDecl, directives ...[]*ast.Directive) {
	field := automaticUpdateField(fields, directives...)
	if field == nil {
		fmt.Fprintf(b, "func (q *%sQuery) updatePrepared(ctx context.Context, sets ...lux.SetField) (int64,error) {\n\treturn (*pg.Query[%s])(q).Update(ctx,sets...)\n}\n", model, model)
		return
	}
	fmt.Fprintf(b, "func (q *%sQuery) updatePrepared(ctx context.Context, sets ...lux.SetField) (int64,error) {\n", model)
	value := writeAutomaticValue(b, model, field, "time.Now()", "\t", "return 0, err")
	fmt.Fprintf(b, "\tsets = append(sets, lux.SetField{Col: %q, Val: %s})\n", g.sqlColumn(field.Name), value)
	fmt.Fprintf(b, "\treturn (*pg.Query[%s])(q).Update(ctx,sets...)\n}\n", model)
}
