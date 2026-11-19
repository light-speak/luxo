package codegen

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func compiledEventContext(source *EventContext, result *semantic.Result, catalog map[string]semantic.CompiledValueDeclaration) *EventContext {
	if source == nil {
		return nil
	}
	context := source
	for name, declaration := range result.CompiledValueEvents() {
		if !sameCompiledEventParameters(source.Events[name], declaration) {
			if context == source {
				context = cloneEventContext(source)
			}
			if context.Events == nil {
				context.Events = make(map[string]*ast.EventDecl)
			}
			context.Events[name] = declaration
		}
	}
	for name, declaration := range catalog {
		owner := moduleNameFromFile(declaration.File)
		if source.TypeModule[name] != owner {
			if context == source {
				context = cloneEventContext(source)
			}
			if context.TypeModule == nil {
				context.TypeModule = make(map[string]string)
			}
			context.TypeModule[name] = owner
		}
	}
	return context
}

func sameCompiledEventParameters(left, right *ast.EventDecl) bool {
	if left == nil || len(left.Params) != len(right.Params) {
		return false
	}
	for index, field := range right.Params {
		previous := left.Params[index]
		if previous.Name != field.Name || !sameTypeRef(previous.Type, field.Type) {
			return false
		}
	}
	return true
}

func compiledEventValueTypes(result *semantic.Result, events *EventContext, catalog map[string]semantic.CompiledValueDeclaration) map[string]bool {
	var pending []string
	var sealed map[string]*ast.SealedDecl
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			if sealed == nil {
				sealed = make(map[string]*ast.SealedDecl)
			}
			sealed[declaration.Name] = declaration
		}
		for _, declaration := range file.Events {
			for _, field := range declaration.Params {
				if field.Type != nil {
					pending = append(pending, field.Type.Name)
				}
			}
		}
	}
	if events != nil {
		for _, declaration := range events.Events {
			for _, field := range declaration.Params {
				if field.Type != nil {
					pending = append(pending, field.Type.Name)
				}
			}
		}
	}
	if len(pending) == 0 {
		return nil
	}
	needed := make(map[string]bool)
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		declaration := catalog[name].Declaration
		if needed[name] || declaration == nil && sealed[name] == nil {
			continue
		}
		needed[name] = true
		if declaration != nil {
			for _, field := range declaration.Fields {
				if field.Type != nil {
					pending = append(pending, field.Type.Name)
				}
			}
		} else {
			for _, variant := range sealed[name].Variants {
				for _, field := range variant.Fields {
					if field.Type != nil {
						pending = append(pending, field.Type.Name)
					}
				}
			}
		}
	}
	return needed
}

func (g *GeneratorContext) generateCompiledEventValues(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	if g.valueTypes == nil || len(g.valueTypes.events) == 0 {
		return nil
	}
	var declarations []*ast.TypeDecl
	for _, file := range result.Files {
		for _, declaration := range file.Types {
			if g.valueTypes.events[declaration.Name] && g.valueTypes.aliases[declaration.Name] == "" {
				declarations = append(declarations, declaration)
			}
		}
	}
	if len(declarations) == 0 {
		return nil
	}
	var body strings.Builder
	objects := collectEventObjectTypes(result)
	for _, declaration := range declarations {
		g.writeEventObjectCodec(&body, declaration.Name, declaration.Fields, enums, objects)
	}
	var b strings.Builder
	writeHeader(&b, packageName, "value_event.gen.go")
	b.WriteString("import (\n\"fmt\"\n\"github.com/light-speak/luxo/pkg/lux/codec\"\n\"github.com/light-speak/luxo/pkg/lux/event\"\n")
	if generatedPackages(body.String()).has("api") {
		b.WriteString("\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	}
	writeEventBodyImports(&b, body.String())
	b.WriteString(")\n\n")
	b.WriteString(body.String())
	return []byte(b.String())
}
