package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func preparedCreateDefaults(fields []*ast.FieldDecl, modelDirectives ...[]*ast.Directive) []*ast.FieldDecl {
	var defaults []*ast.FieldDecl
	for _, field := range fields {
		if hasObjectDefault(field) && preparedMutationField(field) && !isAutoManaged(field, modelDirectives...) {
			defaults = append(defaults, field)
		}
	}
	return defaults
}

// Presence comes from actual bound columns, including trusted raw Set calls.
// The ordinal bitmap is local to this create, independent of sparse wire IDs.
func (g *GeneratorContext) writeCreateDefaultPresence(b *strings.Builder, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, "\tvar _defaultSeen [%d]uint64\n", (len(fields)+63)/64)
	b.WriteString("\tfor _, column := range create.Cols {\n\t\tswitch column {\n")
	for index, field := range fields {
		fmt.Fprintf(b, "\t\tcase %q, %q: _defaultSeen[%d] |= uint64(1) << %d\n", str.ToSnakeCase(field.Name), g.sqlColumn(field.Name), index/64, index%64)
	}
	b.WriteString("\t\t}\n\t}\n")
}

func (g *GeneratorContext) generatePreparedCreateDefaults(b *strings.Builder, model string, fields []*ast.FieldDecl, enums map[string]bool) {
	g.writeCreateDefaultPresence(b, fields)
	if len(fields) > 1 {
		writeCreateDefaultReserve(b, len(fields))
	}
	for index, field := range fields {
		fmt.Fprintf(b, "\tif _defaultSeen[%d] & (uint64(1) << %d) == 0 {\n", index/64, index%64)
		fmt.Fprintf(b, "\t\tvar value %s\n", resolveGoType(field.Type))
		writeObjectDefaultAssignment(b, field, "value", "\t\t", enums)
		writeMutationPreparation(b, model, field, "value", "\t\t", "return nil, err")
		fmt.Fprintf(b, "\t\tcreate.Set(%q, value)\n\t}\n", g.sqlColumn(field.Name))
	}
}

// Reserve both driver-bound slices once when several defaults may be appended.
func writeCreateDefaultReserve(b *strings.Builder, count int) {
	fmt.Fprintf(b, "\t_missingDefaults := %d\n", count)
	b.WriteString("\tfor _, seen := range _defaultSeen { _missingDefaults -= bits.OnesCount64(seen) }\n")
	b.WriteString("\tif _missingDefaults > 0 {\n\t\tcreate.Cols = slices.Grow(create.Cols, _missingDefaults)\n\t\tcreate.Vals = slices.Grow(create.Vals, _missingDefaults)\n\t}\n")
}
