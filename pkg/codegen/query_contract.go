package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// queryFieldExposed excludes server-only and non-stored fields before dispatch.
func queryFieldExposed(f *ast.FieldDecl, annotation string) bool {
	return f.Type != nil && f.Computed == nil && !f.Type.IsList &&
		hasDirective(f.Directives, annotation) && !hasDirective(f.Directives, "internal")
}

// generateFilterParser emits static validation before constructing a query.
func generateFilterParser(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	fields := exposedQueryFields(m, "filterable", enums)
	fmt.Fprintf(b, "// parse%sFilters validates every filter before database access.\n", m.Name)
	fmt.Fprintf(b, "func parse%sFilters(filters []api.Filter) ([]lux.Condition, error) {\n", m.Name)
	if len(fields) == 0 {
		writeNoQueryFields(b, "filters", "filtering")
		return
	}
	b.WriteString("\tvar conds []lux.Condition\n\tfor _, f := range filters {\n\t\tvar cond lux.Condition\n\t\tvar err error\n\t\tswitch f.Field {\n")
	for _, f := range fields {
		constructor := filterFieldConstructor(f, enums)
		fmt.Fprintf(b, "\t\tcase %q:\n", f.Name)
		if enums[f.Type.Name] {
			fmt.Fprintf(b, "\t\t\tif !%s(f.Value).IsValid() { return nil, errors.BadRequest.WithData(errors.ParamError{Param: f.Field, Error: \"undeclared enum filter value\"}) }\n", f.Type.Name)
			b.WriteString("\t\t\tif f.Operator != \"eq\" && f.Operator != \"ne\" { return nil, errors.BadRequest.WithData(errors.ParamError{Param: f.Field, Error: \"enum filters require eq/ne\"}) }\n")
		}
		fmt.Fprintf(b, "\t\t\tcond, err = lux.New%s(%q).FilterOp(f.Operator, f.Value)\n", constructor, str.ToSnakeCase(f.Name))
	}
	b.WriteString("\t\tdefault:\n\t\t\treturn nil, errors.BadRequest.WithData(errors.ParamError{Param: f.Field, Error: \"field does not expose filtering\"})\n\t\t}\n")
	b.WriteString("\t\tif err != nil { return nil, errors.BadRequest.WithData(errors.ParamError{Param: f.Field, Error: err.Error()}) }\n\t\tconds = append(conds, cond)\n\t}\n\treturn conds, nil\n}\n\n")
}

func exposedQueryFields(m *ast.ModelDecl, annotation string, enums map[string]bool) []*ast.FieldDecl {
	var fields []*ast.FieldDecl
	for _, field := range m.Fields {
		if !queryFieldExposed(field, annotation) || isRelationField(field, enums) {
			continue
		}
		if annotation == "filterable" && filterFieldConstructor(field, enums) == "" {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func writeNoQueryFields(b *strings.Builder, argument, capability string) {
	fmt.Fprintf(b, "\tif len(%s) > 0 { return nil, errors.BadRequest.WithData(errors.ParamError{Param: %s[0].Field, Error: %q}) }\n", argument, argument, "model does not expose "+capability)
	b.WriteString("\treturn nil, nil\n}\n\n")
}

func filterFieldConstructor(f *ast.FieldDecl, enums map[string]bool) string {
	if enums[f.Type.Name] {
		return "StringField"
	}
	switch f.Type.Name {
	case "Int", "Float", "String", "Boolean", "DateTime", "UUID", "Decimal":
		return fieldConditionTypeWithDirectives(f)
	default:
		return ""
	}
}

// generateSorterParser rejects unknown fields and invalid directions, including
// models with no sortable fields. No user-supplied SQL identifier is emitted.
func generateSorterParser(b *strings.Builder, m *ast.ModelDecl, enumSets ...map[string]bool) {
	var enums map[string]bool
	if len(enumSets) > 0 {
		enums = enumSets[0]
	}
	fields := exposedQueryFields(m, "sortable", enums)
	fmt.Fprintf(b, "// parse%sSorters validates every sorter before database access.\n", m.Name)
	fmt.Fprintf(b, "func parse%sSorters(sorters []api.Sorter) ([]string, error) {\n", m.Name)
	if len(fields) == 0 {
		writeNoQueryFields(b, "sorters", "sorting")
		return
	}
	b.WriteString("\tvar clauses []string\n\tfor _, s := range sorters {\n\t\tvar col string\n\t\tswitch s.Field {\n")
	for _, f := range fields {
		fmt.Fprintf(b, "\t\tcase %q:\n\t\t\tcol = %q\n", f.Name, str.ToSnakeCase(f.Name))
	}
	b.WriteString("\t\tdefault:\n\t\t\treturn nil, errors.BadRequest.WithData(errors.ParamError{Param: s.Field, Error: \"field does not expose sorting\"})\n\t\t}\n")
	b.WriteString("\t\tswitch strings.ToLower(s.Order) {\n\t\tcase \"desc\": clauses = append(clauses, col+\" DESC\")\n\t\tcase \"asc\": clauses = append(clauses, col+\" ASC\")\n\t\tdefault: return nil, errors.BadRequest.WithData(errors.ParamError{Param: s.Field, Error: \"sort order must be asc/desc\"})\n\t\t}\n\t}\n\treturn clauses, nil\n}\n\n")
}
