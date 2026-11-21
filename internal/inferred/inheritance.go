package inferred

import (
	"maps"
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
)

// ExpandedFiles provides the same effective model fields to all compiler outputs.
// It copies only affected file/model containers; source declarations stay intact.
func ExpandedFiles(files []*ast.File) []*ast.File {
	var expanded []*ast.File
	var models map[string]*ast.ModelDecl
	for index, file := range files {
		var copy *ast.File
		for modelIndex, model := range file.Models {
			if len(model.Parents) == 0 {
				continue
			}
			if models == nil {
				models = Models(files)
			}
			if models[model.Name] == model {
				continue
			}
			if expanded == nil {
				expanded = slices.Clone(files)
			}
			if copy == nil {
				cloned := *file
				cloned.Models = slices.Clone(file.Models)
				copy = &cloned
				expanded[index] = copy
			}
			copy.Models[modelIndex] = models[model.Name]
		}
	}
	if expanded == nil {
		return files
	}
	return expanded
}

// Keep the original AST intact. Local fields and earlier parents take precedence,
// matching semantic field lookup. Cycles are diagnosed by semantic analysis.
func resolveInheritedModels(models map[string]*ast.ModelDecl, files []*ast.File) {
	var resolved map[string]*ast.ModelDecl
	var sources map[string]*ast.ModelDecl
	for name, model := range models {
		if len(model.Parents) == 0 {
			continue
		}
		if sources == nil {
			sources = inheritedSources(models, files)
		}
		fields := inheritedFields{source: model.Fields}
		appendInheritedFields(&fields, model, sources, make(map[string]bool), make(map[string]bool))
		if fields.added == nil && fields.prefix == len(model.Fields) {
			continue
		}
		if resolved == nil {
			resolved = make(map[string]*ast.ModelDecl)
		}
		copy := *model
		copy.Fields = fields.values()
		resolved[name] = &copy
	}
	for name, model := range resolved {
		models[name] = model
	}
}

// Interfaces and value types may provide fields, but are not queryable models.
func inheritedSources(models map[string]*ast.ModelDecl, files []*ast.File) map[string]*ast.ModelDecl {
	sources := models
	copied := false
	add := func(name string, fields []*ast.FieldDecl) {
		if sources[name] != nil {
			return
		}
		if !copied {
			sources = maps.Clone(models)
			copied = true
		}
		sources[name] = &ast.ModelDecl{Name: name, Fields: fields}
	}
	for _, file := range files {
		for _, declaration := range file.Interfaces {
			add(declaration.Name, declaration.Fields)
		}
		for _, declaration := range file.Types {
			add(declaration.Name, declaration.Fields)
		}
	}
	return sources
}

// Retain an unchanged prefix without copying or writing into source capacity.
type inheritedFields struct {
	source []*ast.FieldDecl
	added  []*ast.FieldDecl
	prefix int
}

func (f *inheritedFields) append(field *ast.FieldDecl) {
	if f.added == nil {
		if f.prefix < len(f.source) && f.source[f.prefix] == field {
			f.prefix++
			return
		}
		f.added = make([]*ast.FieldDecl, f.prefix, f.prefix+1)
		copy(f.added, f.source[:f.prefix])
	}
	f.added = append(f.added, field)
}

func (f *inheritedFields) values() []*ast.FieldDecl {
	if f.added == nil {
		return f.source[:f.prefix]
	}
	return f.added
}

func appendInheritedFields(fields *inheritedFields, model *ast.ModelDecl, models map[string]*ast.ModelDecl, visited, names map[string]bool) {
	if model == nil || visited[model.Name] {
		return
	}
	visited[model.Name] = true
	for _, field := range model.Fields {
		if !names[field.Name] {
			names[field.Name] = true
			fields.append(field)
		}
	}
	for _, parent := range model.Parents {
		appendInheritedFields(fields, models[parent], models, visited, names)
	}
}
