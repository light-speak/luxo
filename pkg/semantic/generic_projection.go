package semantic

import (
	"slices"
	"sort"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// An expected argument type is synthesized at its use site, while its field
// positions can still belong to a common generic template.
func (p *valueTypeLowering) argumentOwners(nodes *ast.CloneMap) map[*ast.TypeRef]int {
	var owners map[*ast.TypeRef]int
	for argument := range nodes.Arguments {
		if argument.ExpectedType == nil || argument.Value == nil {
			continue
		}
		if owners == nil {
			owners = make(map[*ast.TypeRef]int)
		}
		assignReferenceOwner(owners, argument.ExpectedType, p.files[argument.Value.GetPos().File])
	}
	return owners
}

// The caller filters missing values/expected types; normal recursive type components stay non-nil.
// Retain the nil-reference guard for incomplete metadata.
func assignReferenceOwner(owners map[*ast.TypeRef]int, reference *ast.TypeRef, owner int) {
	if reference == nil {
		return
	}
	if _, exists := owners[reference]; exists {
		return
	}
	owners[reference] = owner
	for _, argument := range reference.TypeArgs {
		assignReferenceOwner(owners, argument, owner)
	}
	for _, item := range reference.Tuple {
		assignReferenceOwner(owners, item, owner)
	}
}

func (p *valueTypeLowering) recordUse(owner int, name string) {
	file := p.source.Files[owner].Name
	if p.uses[file] == nil {
		p.uses[file] = make(map[string]bool)
	}
	p.uses[file][name] = true
}

func (p *valueTypeLowering) compilationCatalog() *valueTypeCompilation {
	catalog := &valueTypeCompilation{compiled: true, sealed: p.source.valueTypes.sealed, origins: p.origins, uses: p.uses}
	if len(p.instances) == 0 && !catalog.sealed {
		return catalog
	}
	catalog.declarations = make(map[string]CompiledValueDeclaration)
	for _, file := range p.view.Files {
		for _, declaration := range file.Sealeds {
			if catalog.sealeds == nil {
				catalog.sealeds = make(map[string]CompiledSealedDeclaration)
			}
			catalog.sealeds[declaration.Name] = CompiledSealedDeclaration{declaration, file.Name}
		}
		for _, declaration := range file.Types {
			catalog.declarations[declaration.Name] = CompiledValueDeclaration{Declaration: declaration, File: file.Name}
		}
		for _, declaration := range file.Events {
			if catalog.events == nil {
				catalog.events = make(map[string]*ast.EventDecl)
			}
			catalog.events[declaration.Name] = declaration
		}
	}
	return catalog
}

// A service needs its own static Go definitions, even when another service uses
// the same instantiation. Canonical source ownership and wire IDs stay global.
func projectCompiledValueTypes(source *Result) *Result {
	if len(source.valueTypes.origins) == 0 && len(source.valueTypes.sealeds) == 0 {
		return source
	}
	needed := requiredValueDeclarations(source)
	present := make(map[string]bool)
	files := slices.Clone(source.Files)
	for _, file := range source.Files {
		for _, declaration := range file.Types {
			present[declaration.Name] = true
		}
	}
	files, injected := injectValueDeclarations(files, source.valueTypes.declarations, needed, present)
	files, sealedInjected := injectSealedDeclarations(files, source.valueTypes.sealeds, needed)
	injected = injected || sealedInjected
	if !injected {
		return source
	}
	result := *source
	result.Files = files
	metadata := *source.valueTypes
	metadata.injected = make(map[*ast.File]bool, len(files))
	for _, file := range source.Files {
		if source.valueTypes.injected[file] {
			metadata.injected[file] = true
		}
	}
	for _, file := range files[len(source.Files):] {
		metadata.injected[file] = true
	}
	result.valueTypes = &metadata
	return &result
}

func requiredValueDeclarations(source *Result) map[string]bool {
	needed := make(map[string]bool)
	var pending []string
	for _, file := range source.Files {
		for _, declaration := range file.Sealeds {
			pending = append(pending, declaration.Name)
		}
		for _, listener := range file.Listeners {
			if event := source.valueTypes.events[listener.EventName]; event != nil {
				for _, field := range event.Params {
					pending = appendValueReferences(pending, field.Type)
				}
			}
		}
		if !source.valueTypes.injected[file] {
			for name := range source.valueTypes.uses[file.Name] {
				pending = append(pending, name)
			}
		}
		for _, declaration := range file.Types {
			if source.valueTypes.origins[declaration.Name] != "" {
				pending = append(pending, declaration.Name)
			}
		}
	}
	for len(pending) != 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if needed[name] {
			continue
		}
		needed[name] = true
		if declaration := source.valueTypes.declarations[name].Declaration; declaration != nil {
			for _, field := range declaration.Fields {
				pending = appendValueReferences(pending, field.Type)
			}
		}
		if declaration := source.valueTypes.sealeds[name].Declaration; declaration != nil {
			for _, variant := range declaration.Variants {
				for _, field := range variant.Fields {
					pending = appendValueReferences(pending, field.Type)
				}
			}
		}
	}
	return needed
}

func injectSealedDeclarations(files []*ast.File, catalog map[string]CompiledSealedDeclaration, needed map[string]bool) ([]*ast.File, bool) {
	present := make(map[string]bool)
	for _, file := range files {
		for _, declaration := range file.Sealeds {
			present[declaration.Name] = true
		}
	}
	var names []string
	for name := range needed {
		if !present[name] && catalog[name].Declaration != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return files, false
	}
	sort.Strings(names)
	owners := make(map[string]*ast.File)
	for _, name := range names {
		entry := catalog[name]
		owner := owners[entry.File]
		if owner == nil {
			owner = &ast.File{Name: entry.File}
			owners[entry.File] = owner
			files = append(files, owner)
		}
		owner.Sealeds = append(owner.Sealeds, entry.Declaration)
	}
	return files, true
}

// Published readonly catalog fields/parameters have non-nil, fully materialized type references.
// Their generic TypeArgs are cleared; ordinary Tuple traversal is a separate supported path.
// Retain the nil and TypeArgs fallbacks for incomplete catalog metadata.
func appendValueReferences(names []string, reference *ast.TypeRef) []string {
	if reference == nil {
		return names
	}
	names = append(names, reference.Name)
	for _, argument := range reference.TypeArgs {
		names = appendValueReferences(names, argument)
	}
	for _, item := range reference.Tuple {
		names = appendValueReferences(names, item)
	}
	return names
}

func injectValueDeclarations(files []*ast.File, catalog map[string]CompiledValueDeclaration, needed, present map[string]bool) ([]*ast.File, bool) {
	var names []string
	for name := range needed {
		if !present[name] && catalog[name].Declaration != nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return files, false
	}
	sort.Strings(names)
	owners := make(map[string]*ast.File)
	for _, name := range names {
		entry := catalog[name]
		owner := owners[entry.File]
		if owner == nil {
			owner = &ast.File{Name: entry.File}
			owners[entry.File] = owner
			files = append(files, owner)
		}
		owner.Types = append(owner.Types, entry.Declaration)
	}
	return files, true
}

func (p *valueTypeLowering) declarationOwner(reference *ast.TypeRef, useOwner int) int {
	if owner := p.privateArgumentOwner(reference); owner >= 0 {
		return owner
	}
	if owner, exists := p.owners[reference.Name]; exists {
		return owner
	}
	for index, file := range p.view.Files {
		if FileToModule(file.Name).IsCommon {
			return index
		}
	}
	name := p.source.Files[useOwner].Name
	prefix := ""
	if index := strings.LastIndex(name, "origin/"); index >= 0 {
		prefix = name[:index]
	}
	p.view.Files = append(p.view.Files, &ast.File{Name: prefix + "origin/common/luxo_value_types.luxo"})
	return len(p.view.Files) - 1
}

// Normal instantiation recursively rejects Tuple generic arguments before owner resolution.
// Keep tuple descent for incomplete metadata; ordinary Tuple fields do not use this path.
func (p *valueTypeLowering) privateArgumentOwner(reference *ast.TypeRef) int {
	if owner, exists := p.owners[reference.Name]; exists && !FileToModule(p.source.Files[owner].Name).IsCommon {
		return owner
	}
	for _, argument := range reference.TypeArgs {
		if owner := p.privateArgumentOwner(argument); owner >= 0 {
			return owner
		}
	}
	for _, item := range reference.Tuple {
		if owner := p.privateArgumentOwner(item); owner >= 0 {
			return owner
		}
	}
	return -1
}
