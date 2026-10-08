package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// SQL projection keys and cache source metadata use the same aggregate relation.
// Callers have already checked that the field has a computed declaration.
func computedFieldRelation(field *ast.FieldDecl, relations []Relation) (Relation, bool) {
	for _, directive := range field.Computed.Directives {
		if len(directive.Args) != 1 {
			continue
		}
		relationName, _, ok := computedAggregateTarget(directive.Name, directive.Args[0].Value)
		if !ok {
			continue
		}
		for _, relation := range relations {
			if relation.FieldName == relationName && relation.IsList {
				return relation, true
			}
		}
	}
	return Relation{}, false
}

func (g *GeneratorContext) computedModelSources(model *ast.ModelDecl, relations []Relation, module string) map[string]schema.ComputedSource {
	var sources map[string]schema.ComputedSource
	for _, field := range model.Fields {
		if field.Computed == nil || field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") || g.modelFieldID(model.Name, field.Name) == 0 {
			continue
		}
		relation, ok := computedFieldRelation(field, relations)
		if !ok {
			continue
		}
		if sources == nil {
			sources = make(map[string]schema.ComputedSource)
		}
		// Semantic validation requires aggregates to read a same-module relation.
		sources[field.Name] = schema.ComputedSource{Model: relation.TargetName, Module: module}
	}
	return sources
}

func (g *GeneratorContext) writeComputedSources(b *strings.Builder, model *ast.ModelDecl, relations []Relation, module string) {
	sources := g.computedModelSources(model, relations, module)
	if len(sources) == 0 {
		return
	}
	b.WriteString("\t\tComputedSources: map[string]schema.ComputedSource{\n")
	for _, field := range model.Fields {
		if source, ok := sources[field.Name]; ok {
			fmt.Fprintf(b, "\t\t\t%q: {Model: %q, Module: %q},\n", field.Name, source.Model, source.Module)
		}
	}
	b.WriteString("\t\t},\n")
}
