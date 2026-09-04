package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Pass 1: Collect Declarations ==========

func (a *Analyzer) collectDeclarations(file *ast.File) {
	modName := a.fileModule(file.Name)

	for _, m := range file.Models {
		a.declareType(m.Name, TypeModel, m.Pos, m.Doc)
		a.typeOwners.register(m.Name, modName)
	}
	for _, i := range file.Interfaces {
		a.declareType(i.Name, TypeInterface, i.Pos, i.Doc)
		a.typeOwners.register(i.Name, modName)
	}
	for _, e := range file.Enums {
		typ := a.declareType(e.Name, TypeEnum, e.Pos, e.Doc)
		if typ != nil {
			typ.EnumValues = e.Values
		}
		a.typeOwners.register(e.Name, modName)
	}
	for _, s := range file.Sealeds {
		typ := a.declareType(s.Name, TypeSealed, s.Pos, s.Doc)
		if typ != nil {
			a.collectSealedVariants(typ, s.Variants)
		}
		a.typeOwners.register(s.Name, modName)
	}
	for _, t := range file.Types {
		a.declareType(t.Name, TypeCustom, t.Pos, t.Doc)
		a.registerTypeParams(t.TypeParams)
		a.typeOwners.register(t.Name, modName)
	}
	for _, api := range file.APIs {
		a.scope.Define(&Symbol{
			Name: api.Name,
			Kind: SymApi,
			Pos:  api.Pos,
			Doc:  api.Doc,
		})
	}
	for _, fn := range file.Functions {
		a.scope.Define(&Symbol{
			Name: fn.Name,
			Kind: SymFn,
			Pos:  fn.Pos,
			Doc:  fn.Doc,
		})
	}
	for _, e := range file.Errors {
		a.scope.Define(&Symbol{
			Name: e.Name,
			Kind: SymError,
			Pos:  e.Pos,
			Doc:  e.Doc,
		})
	}
	for _, mw := range file.Middlewares {
		a.scope.Define(&Symbol{
			Name: mw.Name,
			Kind: SymMiddleware,
			Pos:  mw.Pos,
			Doc:  mw.Doc,
		})
	}
	for _, ev := range file.Events {
		a.scope.Define(&Symbol{
			Name: ev.Name,
			Kind: SymEvent,
			Pos:  ev.Pos,
		})
	}
}

// fileModule returns the module name for a file. Returns "" when module isolation is disabled.
func (a *Analyzer) fileModule(filename string) string {
	if a.moduleMap == nil {
		return ""
	}
	if mi, ok := a.moduleMap[filename]; ok {
		return mi.Name
	}
	return ""
}

// collectExtendVisibility scans all files for extend declarations and records
// which types and fields are made visible in each module via extend.
func (a *Analyzer) collectExtendVisibility() {
	for _, file := range a.files {
		modName := a.fileModule(file.Name)
		if modName == "" {
			continue
		}
		for _, ext := range file.Extends {
			if a.extendVisible[modName] == nil {
				a.extendVisible[modName] = make(map[string]bool)
			}
			a.extendVisible[modName][ext.Name] = true

			// Track which fields are visible per extend model
			if a.extendFieldVisible[modName] == nil {
				a.extendFieldVisible[modName] = make(map[string]map[string]bool)
			}
			if a.extendFieldVisible[modName][ext.Name] == nil {
				a.extendFieldVisible[modName][ext.Name] = make(map[string]bool)
			}
			for _, f := range ext.Fields {
				a.extendFieldVisible[modName][ext.Name][f.Name] = true
			}
		}
	}
}

// checkCrossModuleDuplicates reports errors when different (non-common) modules
// declare types with the same name. The "already declared" error from declareType
// catches in-module duplicates; this catches cross-module name collisions.
func (a *Analyzer) checkCrossModuleDuplicates() {
	// type name → first declaring module + position
	type firstDecl struct {
		module string
		pos    token.Position
	}
	seen := make(map[string]firstDecl)

	for _, file := range a.files {
		mi := a.moduleMap[file.Name]
		if mi == nil || mi.IsCommon {
			continue
		}
		for _, decls := range a.fileTypeDecls(file) {
			name, pos := decls.name, decls.pos
			if prev, ok := seen[name]; ok {
				if prev.module != mi.Name {
					a.addError(pos,
						"type '%s' already declared in module '%s', different modules cannot define same-named types / 类型 '%s' 已在模块 '%s' 中声明，不同模块不能定义同名类型",
						name, prev.module, name, prev.module)
				}
			} else {
				seen[name] = firstDecl{module: mi.Name, pos: pos}
			}
		}
	}
}

// typeDecl is a helper pair for iteration.
type typeDecl struct {
	name string
	pos  token.Position
}

// fileTypeDecls returns all type declarations in a file (model, enum, sealed, type, interface).
func (a *Analyzer) fileTypeDecls(file *ast.File) []typeDecl {
	var decls []typeDecl
	for _, m := range file.Models {
		decls = append(decls, typeDecl{m.Name, m.Pos})
	}
	for _, i := range file.Interfaces {
		decls = append(decls, typeDecl{i.Name, i.Pos})
	}
	for _, e := range file.Enums {
		decls = append(decls, typeDecl{e.Name, e.Pos})
	}
	for _, s := range file.Sealeds {
		decls = append(decls, typeDecl{s.Name, s.Pos})
	}
	for _, t := range file.Types {
		decls = append(decls, typeDecl{t.Name, t.Pos})
	}
	return decls
}

// checkModuleVisibility checks that type references in each file only refer to
// types within the same module, in common/, or made visible via extend.
func (a *Analyzer) checkModuleVisibility() {
	for _, file := range a.files {
		mi := a.moduleMap[file.Name]
		if mi == nil {
			continue
		}
		a.checkFileTypeRefs(file, mi)
	}
}

// checkFileTypeRefs checks all type references in a file for module visibility.
func (a *Analyzer) checkFileTypeRefs(file *ast.File, mi *ModuleInfo) {
	a.checkModelTypeRefs(file, mi)
	a.checkDeclTypeRefs(file, mi)
}

func (a *Analyzer) checkModelTypeRefs(file *ast.File, mi *ModuleInfo) {
	for _, m := range file.Models {
		for _, f := range m.Fields {
			a.checkTypeRefVisibility(f.Type, mi, f.Pos)
		}
		for _, parentName := range m.Parents {
			a.checkNameVisibility(parentName, mi, m.Pos)
		}
	}
	for _, iface := range file.Interfaces {
		for _, f := range iface.Fields {
			a.checkTypeRefVisibility(f.Type, mi, f.Pos)
		}
	}
	for _, t := range file.Types {
		for _, f := range t.Fields {
			a.checkTypeRefVisibility(f.Type, mi, f.Pos)
		}
	}
}

func (a *Analyzer) checkDeclTypeRefs(file *ast.File, mi *ModuleInfo) {
	for _, api := range file.APIs {
		a.checkTypeRefVisibility(api.ReturnType, mi, api.Pos)
		for _, p := range api.Params {
			a.checkTypeRefVisibility(p.Type, mi, p.Pos)
		}
	}
	for _, fn := range file.Functions {
		a.checkTypeRefVisibility(fn.ReturnType, mi, fn.Pos)
		for _, p := range fn.Params {
			a.checkTypeRefVisibility(p.Type, mi, p.Pos)
		}
	}
	for _, ev := range file.Events {
		for _, p := range ev.Params {
			a.checkTypeRefVisibility(p.Type, mi, p.Pos)
		}
	}
	// sealed variant fields
	for _, s := range file.Sealeds {
		for _, v := range s.Variants {
			for _, f := range v.Fields {
				a.checkTypeRefVisibility(f.Type, mi, f.Pos)
			}
		}
	}
}

// checkTypeRefVisibility checks if a type reference is visible from the given module.
func (a *Analyzer) checkTypeRefVisibility(ref *ast.TypeRef, mi *ModuleInfo, pos token.Position) {
	if ref == nil {
		return
	}
	a.checkNameVisibility(ref.Name, mi, pos)
	// check generic args
	for _, arg := range ref.TypeArgs {
		a.checkTypeRefVisibility(arg, mi, pos)
	}
	// check tuple elements
	for _, t := range ref.Tuple {
		a.checkTypeRefVisibility(t, mi, pos)
	}
}

// checkNameVisibility checks if a type name is visible from the given module.
func (a *Analyzer) checkNameVisibility(name string, mi *ModuleInfo, pos token.Position) {
	if name == "" {
		return
	}
	// built-in types are always visible
	if _, isBuiltin := BuiltinTypes()[name]; isBuiltin {
		return
	}
	// check ownership
	ownerMod := a.typeOwners.ownerOf(name)
	if ownerMod == "" {
		return // unknown type — will be caught by resolveTypeRef
	}
	// same module — ok
	if ownerMod == mi.Name {
		return
	}
	// common module types are globally visible
	if a.isCommonModule(ownerMod) {
		return
	}
	// common module can see everything
	if mi.IsCommon {
		return
	}
	// check if visible via extend
	if visible, ok := a.extendVisible[mi.Name]; ok && visible[name] {
		return
	}
	a.addError(pos,
		"type '%s' is from module '%s', use 'extend %s { ... }' to access it / 类型 '%s' 来自模块 '%s'，使用 'extend %s { ... }' 来访问",
		name, ownerMod, name, name, ownerMod, name)
}

// isCommonModule checks if the given module name is the common module.
func (a *Analyzer) isCommonModule(modName string) bool {
	for _, mi := range a.moduleMap {
		if mi.Name == modName && mi.IsCommon {
			return true
		}
	}
	return false
}

// collectSealedVariants populates a sealed type with its variant information.
func (a *Analyzer) collectSealedVariants(typ *ResolvedType, variants []*ast.SealedVariant) {
	for _, v := range variants {
		vi := &SealedVariantInfo{Name: v.Name}
		for _, f := range v.Fields {
			vi.Fields = append(vi.Fields, &FieldInfo{
				Name: f.Name,
				Pos:  token.Position{},
			})
		}
		typ.Variants = append(typ.Variants, vi)
	}
}

// registerTypeParams registers generic type parameters as types if not already declared.
func (a *Analyzer) registerTypeParams(params []string) {
	for _, tp := range params {
		if _, exists := a.types[tp]; !exists {
			a.types[tp] = &ResolvedType{Kind: TypeGeneric, Name: tp, Fields: make(map[string]*FieldInfo)}
		}
	}
}

func (a *Analyzer) declareType(name string, kind TypeKind, pos token.Position, doc string) *ResolvedType {
	if _, exists := a.types[name]; exists {
		a.addError(pos, "type '%s' already declared / 类型 '%s' 已声明", name, name)
		return nil
	}
	typ := &ResolvedType{
		Kind:   kind,
		Name:   name,
		Fields: make(map[string]*FieldInfo),
		Pos:    pos,
	}
	a.types[name] = typ
	a.scope.Define(&Symbol{
		Name: name,
		Kind: kindToSymbol(kind),
		Type: typ,
		Pos:  pos,
		Doc:  doc,
	})
	return typ
}
