package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// A defined query type preserves the adapter layout without promoting raw writes.
// The embedded PostgreSQL reader supplies the shared read terminals.
// Multiline wrappers let the Go printer skip sizing each body for a single line.
func (g *GeneratorContext) generateQueryFacade(b *strings.Builder, name string, fields []*ast.FieldDecl, writable bool, directives ...[]*ast.Directive) {
	writeQueryFacadeParts(b, "type ", name, "Query pg.Query[", name, "]\n\n")
	for _, method := range []struct{ name, params, args string }{
		{"Where", "values ...lux.Condition", "values..."},
		{"Select", "values ...string", "values..."},
		{"OrderBy", "values ...string", "values..."},
		{"Limit", "value int", "value"}, {"Offset", "value int", "value"},
	} {
		writeQueryFacadeParts(b,
			"func (q *", name, "Query) ", method.name, "(", method.params, ") *", name, "Query {\n",
			"\t(*pg.Query[", name, "])(q).", method.name, "(", method.args, ")\n\treturn q\n}\n",
		)
	}
	generateDecimalQueryMethods(b, name, fields)
	if writable {
		for _, method := range []string{"Delete", "SoftDelete"} {
			writeQueryFacadeParts(b,
				"func (q *", name, "Query) ", method, "(ctx context.Context) (int64,error) {\n",
				"\treturn (*pg.Query[", name, "])(q).", method, "(ctx)\n}\n",
			)
		}
		g.generatePreparedQueryUpdate(b, name, fields, directives...)
	}
}

// Write one declaration at a time to keep the destination builder's growth
// tied to declarations, including when the model has a long identifier.
func writeQueryFacadeParts(b *strings.Builder, parts ...string) {
	var scratch [256]byte
	declaration := scratch[:0]
	for _, part := range parts {
		declaration = append(declaration, part...)
	}
	b.Write(declaration)
}

func generateDecimalQueryMethods(b *strings.Builder, name string, fields []*ast.FieldDecl) {
	for _, field := range fields {
		if field.Type != nil && field.Type.Name == "Decimal" {
			for _, method := range []string{"SumDecimal", "AvgDecimal", "MinDecimal", "MaxDecimal"} {
				writeQueryFacadeParts(b,
					"func (q *", name, "Query) ", method, "(ctx context.Context, column string) (decimal.Decimal,error) {\n",
					"\treturn (*pg.Query[", name, "])(q).", method, "(ctx,column)\n}\n",
				)
			}
			return
		}
	}
}

// Bulk updates reuse the existing typed setter state and preparation rules.
func generateQueryUpdateBuilder(b *strings.Builder, name string, fields []*ast.FieldDecl, directives []*ast.Directive) {
	fmt.Fprintf(b, "type %sQueryUpdateBuilder struct { state %sUpdateBuilder; query *%sQuery }\n", name, name, name)
	fmt.Fprintf(b, "func (q *%sQuery) Update() *%sQueryUpdateBuilder { return &%sQueryUpdateBuilder{query:q} }\n", name, name, name)
	primary := primaryKeyField(&ast.ModelDecl{Name: name, Fields: fields})
	setters := mutationSetterFields(fields, primary, true, directives)
	for _, field := range setters {
		method := str.Capitalize(field.Name)
		fmt.Fprintf(b, "func (b *%sQueryUpdateBuilder) Set%s(value %s) *%sQueryUpdateBuilder {\n\tb.state.Set%s(value)\n\treturn b\n}\n", name, method, resolveGoType(field.Type), name, method)
	}
	fmt.Fprintf(b, "func (b *%sQueryUpdateBuilder) Exec(ctx context.Context) (int64,error) {\n", name)
	if hasFallibleSave(setters) {
		b.WriteString("if b.state._prepareErr != nil { return 0,b.state._prepareErr }\n")
	}
	b.WriteString("update := b.state.UpdateBase\n")
	b.WriteString("return b.query.updatePrepared(ctx,update.Sets...)\n}\n\n")
}
