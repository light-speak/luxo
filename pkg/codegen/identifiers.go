package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/semantic"
)

func validateStableIDs(ids StableIDs) error {
	if _, err := lockfile.ValidateAssignments("apis", ids.APIs, nil, codec.MaxAPIID); err != nil {
		return err
	}
	for _, group := range []struct {
		name  string
		ids   map[string]map[string]int
		limit int
	}{
		{"models", ids.ModelFields, codec.MaxSelectionFieldID},
		{"events", ids.EventFields, codec.MaxEventFieldID},
		{"params", ids.APIParams, lockfile.MaxParameterID},
	} {
		for name, assigned := range group.ids {
			if name == "" {
				return fmt.Errorf("stable IDs %s has an empty declaration name", group.name)
			}
			if _, err := lockfile.ValidateAssignments(group.name+"."+name, assigned, nil, group.limit); err != nil {
				return err
			}
		}
	}
	return nil
}

// Formal generation requires IDs for every emitted API and active parameter.
func (g *GeneratorContext) validateRegisteredParameters(result *semantic.Result, enums map[string]bool) error {
	for _, api := range g.collectSchemaAPIs(result, enums) {
		if g.apiID(api.name) == 0 {
			return fmt.Errorf("API %s has no stable ID", api.name)
		}
		for _, param := range api.params {
			if g.apiParamID(api.name, param.Name) == 0 {
				return fmt.Errorf("registered API parameter %s.%s has no stable ID", api.name, param.Name)
			}
		}
	}
	return nil
}

func (g *GeneratorContext) validateFieldIdentifiers(result *semantic.Result) error {
	hasExtensions := false
	for _, file := range result.Files {
		hasExtensions = hasExtensions || len(file.Extends) > 0
		if err := g.validateFileFieldIdentifiers(file); err != nil {
			return err
		}
	}
	if hasExtensions {
		return g.validateExternalKeyIdentifiers(result)
	}
	return nil
}

// External stubs synthesize the owner's primary key even when extend only lists
// other fields. That key must keep its owner-assigned wire ID, never zero.
func (g *GeneratorContext) validateExternalKeyIdentifiers(result *semantic.Result) error {
	localModels := make(map[string]bool)
	for _, file := range result.Files {
		for _, model := range file.Models {
			localModels[model.Name] = true
		}
	}
	for _, file := range result.Files {
		for _, extension := range file.Extends {
			if localModels[extension.Name] {
				continue
			}
			key := g.externalModelIDFieldName(extension.Name)
			if g.modelFieldID(extension.Name, key) == 0 {
				return fmt.Errorf("external model key %s.%s has no stable ID", extension.Name, key)
			}
		}
	}
	return nil
}

func (g *GeneratorContext) validateFileFieldIdentifiers(file *ast.File) error {
	for _, model := range file.Models {
		if err := g.validateDeclaredFields(model.Name, model.Fields); err != nil {
			return err
		}
	}
	for _, declaration := range file.Types {
		if err := g.validateDeclaredFields(declaration.Name, declaration.Fields); err != nil {
			return err
		}
	}
	for _, extension := range file.Extends {
		if err := g.validateDeclaredFields(extension.Name, extension.Fields); err != nil {
			return err
		}
	}
	for _, event := range file.Events {
		for _, param := range event.Params {
			if g.eventFieldID(event.Name, param.Name) == 0 {
				return fmt.Errorf("event field %s.%s has no stable ID", event.Name, param.Name)
			}
		}
	}
	return nil
}

func (g *GeneratorContext) validateDeclaredFields(owner string, fields []*ast.FieldDecl) error {
	for _, field := range fields {
		if g.modelFieldID(owner, field.Name) == 0 {
			return fmt.Errorf("model/type field %s.%s has no stable ID", owner, field.Name)
		}
	}
	return nil
}
