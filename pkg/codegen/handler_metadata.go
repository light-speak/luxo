package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func explicitAPINames(result *semantic.Result) map[string]bool {
	names := make(map[string]bool)
	for _, file := range result.Files {
		for _, api := range file.APIs {
			names[api.Name] = true
		}
	}
	return names
}

// Runtime schema, handler parameters and formal ID checks share ownership.
// A field already declared by its owner does not create an extension endpoint.
func (g *GeneratorContext) schemaModelOwnership(result *semantic.Result) (map[string]string, map[string]map[string]bool) {
	owners := make(map[string]string)
	fields := make(map[string]map[string]bool)
	if g.events != nil {
		for name, module := range g.events.ModelModule {
			owners[name] = module
		}
		for name, declared := range g.events.ModelFields {
			fields[name] = declared
		}
	}
	for _, file := range result.Files {
		module := moduleNameFromFile(file.Name)
		for _, model := range file.Models {
			owners[model.Name] = module
			declared := make(map[string]bool, len(model.Fields))
			for _, field := range model.Fields {
				declared[field.Name] = true
			}
			fields[model.Name] = declared
		}
	}
	return owners, fields
}

// withRequestDeclarations borrows immutable declarations only for this emission.
// Schema and handlers must not interpret parameter types independently.
func (g *GeneratorContext) withRequestDeclarations(result *semantic.Result, enums map[string]bool) *GeneratorContext {
	bound := *g
	bound.requestParams = make(map[string][]*ast.ParamDecl)
	for _, api := range g.collectSchemaAPIs(result, enums) {
		bound.requestParams[api.name] = api.params
	}
	return &bound
}
