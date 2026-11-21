package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func modelQueryCapabilities(model *ast.ModelDecl, enums map[string]bool) *schema.QueryCapabilities {
	query := &schema.QueryCapabilities{}
	for _, field := range exposedQueryFields(model, "filterable", enums) {
		query.Filters = append(query.Filters, schema.QueryFilter{
			Field: field.Name, Type: field.Type.Name, Operators: queryOperators(field, enums),
		})
	}
	for _, field := range exposedQueryFields(model, "sortable", enums) {
		query.Sorters = append(query.Sorters, field.Name)
	}
	return query
}

func queryOperators(field *ast.FieldDecl, enums map[string]bool) []string {
	if enums[field.Type.Name] || field.Type.Name == "Boolean" || field.Type.Name == "UUID" {
		return []string{"eq", "ne"}
	}
	if field.Type.Name == "String" {
		operators := []string{"eq", "ne", "contains", "startswith", "endswith"}
		if hasDirective(field.Directives, "search") {
			operators = append(operators, "match")
		}
		return operators
	}
	return []string{"eq", "ne", "gt", "gte", "lt", "lte"}
}

func writeQueryCapabilities(b *strings.Builder, query *schema.QueryCapabilities) {
	if query == nil {
		return
	}
	b.WriteString("\t\tQuery: &schema.QueryCapabilities{Filters: []schema.QueryFilter{\n")
	for _, field := range query.Filters {
		fmt.Fprintf(b, "{Field: %q, Type: %q, Operators: %#v},\n", field.Field, field.Type, field.Operators)
	}
	fmt.Fprintf(b, "}, Sorters: %#v},\n", query.Sorters)
}
