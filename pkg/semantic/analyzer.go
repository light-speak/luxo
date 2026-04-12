package semantic

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Error represents a semantic error.
type Error struct {
	Pos        token.Position
	Message    string
	Suggestion string // "did you mean ...?"
}

func (e Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Pos, e.Message)
	if e.Suggestion != "" {
		s += " (" + e.Suggestion + ")"
	}
	return s
}

// Warning represents a semantic warning.
type Warning struct {
	Pos     token.Position
	NameLen int // length of the identifier for diagnostic range
	Message string
}

// Result is the output of semantic analysis.
type Result struct {
	Scope    *Scope
	Types    map[string]*ResolvedType // all resolved types by name
	Files    []*ast.File              // original AST files (for codegen access to directives, bodies, etc.)
	Errors   []Error
	Warnings []Warning
}

// Analyzer performs semantic analysis on AST nodes.
type Analyzer struct {
	scope         *Scope
	types         map[string]*ResolvedType
	errors        []Error
	warnings      []Warning
	inLambda      bool // true when checking inside a lambda body
	inTransaction bool // true when checking inside a transaction block
	inAwait       bool // true when checking inside an await block
	inCallFunc    bool // true when checking the Func part of a CallExpr
	files         []*ast.File

	// module isolation
	moduleMap     FileModuleMap              // file → module info
	typeOwners    *typeOwnership             // type name → owning module
	extendVisible map[string]map[string]bool // module → set of type names visible via extend
}

// New creates a new Analyzer.
func New() *Analyzer {
	a := &Analyzer{
		scope:         NewScope(),
		types:         make(map[string]*ResolvedType),
		typeOwners:    newTypeOwnership(),
		extendVisible: make(map[string]map[string]bool),
	}
	// register built-in types
	for name, typ := range BuiltinTypes() {
		a.types[name] = typ
		a.scope.Define(&Symbol{
			Name: name,
			Kind: SymType,
			Type: typ,
		})
	}
	return a
}

// Analyze performs semantic analysis on one or more parsed files.
// When module isolation is not needed (e.g., single-file analysis), use this method.
func (a *Analyzer) Analyze(files []*ast.File) *Result {
	return a.analyzeInternal(files)
}

// AnalyzeWithModules performs semantic analysis with module scope isolation.
// Files are grouped into modules based on their path under origin/.
// Cross-module type references are only allowed via extend declarations.
func (a *Analyzer) AnalyzeWithModules(files []*ast.File) *Result {
	a.moduleMap = BuildFileModuleMap(files)
	return a.analyzeInternal(files)
}

func (a *Analyzer) analyzeInternal(files []*ast.File) *Result {
	a.files = files

	// Pass 1: collect all top-level declarations (types, models, enums, etc.)
	for _, file := range files {
		a.collectDeclarations(file)
	}

	// Pass 1.5: collect extend visibility (before type resolution)
	if a.moduleMap != nil {
		a.collectExtendVisibility()
		a.checkCrossModuleDuplicates()
	}

	// Pass 2: resolve inheritance, interface implementation
	for _, file := range files {
		a.resolveInheritance(file)
	}

	// Pass 3: resolve field types, check directives
	for _, file := range files {
		a.resolveFields(file)
	}

	// Pass 3.5: check cross-module type visibility
	if a.moduleMap != nil {
		a.checkModuleVisibility()
	}

	// Pass 4: check api/fn bodies (expressions, statements)
	for _, file := range files {
		a.checkBodies(file)
	}

	// Pass 5: check circular event dependencies
	a.checkEventCycles()

	// Pass 6: query optimization analysis
	for _, file := range files {
		a.analyzeQueries(file)
	}

	return &Result{
		Scope:    a.scope,
		Types:    a.types,
		Files:    files,
		Errors:   a.errors,
		Warnings: a.warnings,
	}
}

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
// which types are made visible in each module via extend.
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

// ========== Pass 2: Resolve Inheritance ==========

func (a *Analyzer) resolveInheritance(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		for _, parentName := range m.Parents {
			parent, ok := a.types[parentName]
			if !ok {
				a.addErrorWithSuggestion(m.Pos, parentName, "type '%s' not found / 类型 '%s' 未找到", parentName, parentName)
				continue
			}
			typ.Parents = append(typ.Parents, parent)
		}
	}
}

// ========== Pass 3: Resolve Fields ==========

func (a *Analyzer) resolveFields(file *ast.File) {
	a.resolveModelFields(file)
	a.resolveInterfaceFields(file)
	a.resolveTypeFields(file)
	a.resolveApiTypes(file)
	a.resolveFnTypes(file)
	a.resolveExtendFields(file)
}

func (a *Analyzer) resolveModelFields(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range m.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				if _, exists := typ.Fields[fi.Name]; exists {
					a.addError(f.Pos, "duplicate field '%s' in model '%s' / 模型 '%s' 中字段 '%s' 重复", fi.Name, m.Name, m.Name, fi.Name)
					continue
				}
				typ.Fields[fi.Name] = fi
			}
		}
		a.checkDirectives(m.Directives, OnModel)
		// @withAuth: inject .createToken(), .verify(), .refreshToken() methods
		if hasModelDirective(m.Directives, "withAuth") {
			a.injectWithAuthMethods(typ, m.Directives)
		}
	}
}

func (a *Analyzer) resolveInterfaceFields(file *ast.File) {
	for _, i := range file.Interfaces {
		typ := a.types[i.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range i.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveTypeFields(file *ast.File) {
	for _, t := range file.Types {
		typ := a.types[t.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range t.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveApiTypes(file *ast.File) {
	for _, api := range file.APIs {
		sym := a.scope.Lookup(api.Name)
		if sym == nil {
			continue
		}
		retType := a.resolveTypeRef(api.ReturnType, api.Pos)
		sym.Type = retType

		for _, p := range api.Params {
			a.resolveTypeRef(p.Type, api.Pos)
		}
		a.checkDirectives(api.Directives, OnApi)
		a.validateScopeDirective(api)
	}
}

// validateScopeDirective checks @scope usage on an API:
// 1. The return type must be a model (not a custom type, enum, etc.)
// 2. Each scope name must exist on the target model
func (a *Analyzer) validateScopeDirective(api *ast.ApiDecl) {
	for _, d := range api.Directives {
		if d.Name != "scope" {
			continue
		}
		// resolve the base return type name (strip list wrapper)
		baseTypeName := ""
		if api.ReturnType != nil {
			baseTypeName = api.ReturnType.Name
		}
		if baseTypeName == "" {
			a.addWarning(d.Pos, "@scope can only be used on APIs returning a model type / @scope 只能用在返回模型类型的 API 上")
			return
		}
		typ, ok := a.types[baseTypeName]
		if !ok {
			return // type not found — already reported by resolveTypeRef
		}
		if typ.Kind != TypeModel {
			a.addWarning(d.Pos, "@scope can only be used on APIs returning a model type, '%s' is %s / @scope 只能用在返回模型类型的 API 上，'%s' 是 %s",
				baseTypeName, typ.Kind, baseTypeName, typ.Kind)
			return
		}
		// validate each scope name arg exists on the model
		modelScopes := a.findModelScopes(baseTypeName)
		for _, arg := range d.Args {
			scopeName := ""
			if ident, ok := arg.Value.(*ast.Ident); ok {
				scopeName = ident.Name
			}
			if scopeName == "" {
				continue
			}
			if !stringInSlice(scopeName, modelScopes) {
				closest := findClosestString(scopeName, modelScopes)
				if closest != "" {
					a.addError(d.Pos, "scope '%s' not found on model '%s', did you mean '%s'? / 模型 '%s' 上未找到 scope '%s'，你是不是想写 '%s'？",
						scopeName, baseTypeName, closest, baseTypeName, scopeName, closest)
				} else {
					a.addError(d.Pos, "scope '%s' not found on model '%s' / 模型 '%s' 上未找到 scope '%s'",
						scopeName, baseTypeName, baseTypeName, scopeName)
				}
			}
		}
	}
}

// findModelScopes returns all scope names defined on a model across all files.
func (a *Analyzer) findModelScopes(modelName string) []string {
	var scopes []string
	for _, file := range a.files {
		for _, m := range file.Models {
			if m.Name == modelName {
				for _, sc := range m.Scopes {
					scopes = append(scopes, sc.Name)
				}
			}
		}
	}
	return scopes
}

func (a *Analyzer) resolveFnTypes(file *ast.File) {
	for _, fn := range file.Functions {
		sym := a.scope.Lookup(fn.Name)
		if sym == nil {
			continue
		}
		if fn.ReturnType != nil {
			sym.Type = a.resolveTypeRef(fn.ReturnType, fn.Pos)
		}
		for _, p := range fn.Params {
			a.resolveTypeRef(p.Type, fn.Pos)
		}
		a.checkDirectives(fn.Directives, OnFn)
	}
}

func (a *Analyzer) resolveExtendFields(file *ast.File) {
	// detect extend field conflicts: same model + same field name
	extendFields := map[string]map[string]token.Position{} // model → field → first pos
	for _, ext := range file.Extends {
		if _, ok := a.types[ext.Name]; !ok {
			// extend target might be in another module (resolved at gateway)
		}
		if extendFields[ext.Name] == nil {
			extendFields[ext.Name] = map[string]token.Position{}
		}
		for _, f := range ext.Fields {
			if firstPos, exists := extendFields[ext.Name][f.Name]; exists {
				a.addError(f.Pos,
					"extend field '%s.%s' conflicts with previous declaration at line %d / extend 字段 '%s.%s' 与第 %d 行的声明冲突",
					ext.Name, f.Name, firstPos.Line, ext.Name, f.Name, firstPos.Line)
			} else {
				extendFields[ext.Name][f.Name] = f.Pos
			}
			a.resolveFieldDecl(f)
		}
	}
}

func (a *Analyzer) resolveFieldDecl(f *ast.FieldDecl) *FieldInfo {
	fi := &FieldInfo{
		Name:     f.Name,
		Pos:      f.Pos,
		Doc:      f.Doc,
		Computed: f.Computed != nil,
	}

	if f.Type != nil {
		fi.Type = a.resolveTypeRef(f.Type, f.Pos)
		fi.Nullable = f.Type.Nullable
	}

	if f.Default != nil {
		fi.HasDefault = true
	}

	for _, d := range f.Directives {
		fi.Directives = append(fi.Directives, d.Name)
	}

	a.checkFieldDirectives(f)
	return fi
}

func (a *Analyzer) resolveTypeRef(ref *ast.TypeRef, pos token.Position) *ResolvedType {
	if ref == nil {
		return &ResolvedType{Kind: TypeVoid, Name: "Void"}
	}

	// Tuple type: (Post, Video, Product)
	if len(ref.Tuple) > 0 {
		result := &ResolvedType{
			Kind:     TypeTuple,
			Nullable: ref.Nullable,
		}
		for _, t := range ref.Tuple {
			resolved := a.resolveTypeRef(t, pos)
			result.Tuple = append(result.Tuple, resolved)
		}
		return result
	}

	name := ref.Name

	// Look up the type
	typ, ok := a.types[name]
	if !ok {
		a.addErrorWithSuggestion(pos, name, "unknown type '%s' / 未知类型 '%s'", name, name)
		return &ResolvedType{Kind: TypeUnknown, Name: name, Nullable: ref.Nullable}
	}

	result := &ResolvedType{
		Kind:       typ.Kind,
		Name:       typ.Name,
		Nullable:   ref.Nullable,
		IsList:     ref.IsList,
		Fields:     typ.Fields,
		Parents:    typ.Parents,
		Pos:        typ.Pos,
		Variants:   typ.Variants,
		EnumValues: typ.EnumValues,
	}

	// Generic args
	for _, arg := range ref.TypeArgs {
		result.TypeArgs = append(result.TypeArgs, a.resolveTypeRef(arg, pos))
	}

	return result
}

// ========== Pass 4: Check Bodies ==========

func (a *Analyzer) checkBodies(file *ast.File) {
	// global val/var declarations — var is forbidden at global scope
	for _, g := range file.Globals {
		if g.Mutable {
			a.addError(g.Pos, "global 'var' is not allowed, use 'val' for global constants or 'cache' for mutable state / 全局不允许 'var'，使用 'val' 定义全局常量或使用 'cache' 管理可变状态")
		}
		a.checkValStmt(g, a.scope)
	}

	for _, api := range file.APIs {
		if api.Body != nil {
			scope := a.scope.Child()
			// add params to scope
			for _, p := range api.Params {
				pType := a.resolveTypeRef(p.Type, api.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  api.Pos,
				})
			}
			// add my (current authenticated user) to scope
			scope.Define(&Symbol{
				Name: "my",
				Kind: SymVariable,
				Type: a.types["Identity"],
			})
			a.checkBlock(api.Body, scope)
			a.checkUnusedVariables(scope)
		}
	}

	for _, fn := range file.Functions {
		if fn.Body != nil {
			scope := a.scope.Child()
			for _, p := range fn.Params {
				pType := a.resolveTypeRef(p.Type, fn.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  fn.Pos,
				})
			}
			a.checkBlock(fn.Body, scope)
			a.checkUnusedVariables(scope)
		}
	}

	for _, mw := range file.Middlewares {
		a.checkDirectives(mw.Directives, OnMiddleware)
	}

	for _, on := range file.Listeners {
		if on.Body != nil {
			scope := a.scope.Child()
			// add lambda-style params to scope
			for _, p := range on.Params {
				scope.Define(&Symbol{
					Name: p,
					Kind: SymParam,
				})
			}
			a.checkBlock(on.Body, scope)
		}
	}
}

func (a *Analyzer) checkBlock(block *ast.Block, scope *Scope) {
	if block == nil {
		return
	}
	terminated := false
	for _, stmt := range block.Stmts {
		if terminated {
			a.addWarning(stmt.GetPos(), "unreachable code / 不可达代码")
			break
		}
		a.checkStmt(stmt, scope)
		if isTerminating(stmt) {
			terminated = true
		}
	}
}

func isTerminating(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BreakStmt:
		return true
	case *ast.ContinueStmt:
		return true
	case *ast.ThrowStmt:
		return true
	}
	return false
}

func (a *Analyzer) checkStmt(stmt ast.Stmt, scope *Scope) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		a.checkValStmt(s, scope)

	case *ast.IfStmt:
		a.checkExpr(s.Condition, scope)
		childScope := scope.Child()
		a.checkBlock(s.Then, childScope)

	case *ast.ForStmt:
		a.checkForStmt(s, scope)

	case *ast.ReturnStmt:
		if s.Value != nil {
			a.checkExpr(s.Value, scope)
		}

	case *ast.ThrowStmt:
		a.checkExpr(s.Error, scope)

	case *ast.EmitStmt:
		for _, arg := range s.Args {
			a.checkExpr(arg.Value, scope)
		}

	case *ast.AssignStmt:
		a.checkAssignStmt(s, scope)

	case *ast.BreakStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ContinueStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ExprStmt:
		if s.Expr != nil {
			a.checkExpr(s.Expr, scope)
		}
	}
}

func (a *Analyzer) checkValStmt(s *ast.ValStmt, scope *Scope) {
	exprType := a.checkExpr(s.Value, scope)
	if len(s.Names) > 0 {
		a.defineDestructured(s, exprType, scope)
	} else {
		pos := s.NamePos
		if pos.Line == 0 {
			pos = s.Pos // fallback for older AST without NamePos
		}
		scope.Define(&Symbol{
			Name:    s.Name,
			Kind:    SymVariable,
			Type:    exprType,
			Pos:     pos,
			Mutable: s.Mutable,
		})
	}
}

// defineDestructured unpacks a tuple type into individual variables.
// val (a, b, c) = await { expr1; expr2; expr3 }
// a gets type of expr1, b gets type of expr2, c gets type of expr3.
func (a *Analyzer) defineDestructured(s *ast.ValStmt, exprType *ResolvedType, scope *Scope) {
	for i, name := range s.Names {
		var varType *ResolvedType
		if exprType != nil && exprType.Kind == TypeTuple && i < len(exprType.Tuple) {
			varType = exprType.Tuple[i]
		} else if exprType != nil && exprType.Kind != TypeTuple {
			// non-tuple assigned to destructuring — all get same type
			varType = exprType
		}
		scope.Define(&Symbol{
			Name:    name,
			Kind:    SymVariable,
			Type:    varType,
			Pos:     s.Pos,
			Mutable: s.Mutable,
		})
	}
	// warn if count mismatch
	if exprType != nil && exprType.Kind == TypeTuple && len(s.Names) != len(exprType.Tuple) {
		a.addError(s.Pos,
			"destructuring count mismatch: %d variables but tuple has %d elements / 解构数量不匹配：%d 个变量但元组有 %d 个元素",
			len(s.Names), len(exprType.Tuple), len(s.Names), len(exprType.Tuple))
	}
}

func (a *Analyzer) checkAssignStmt(s *ast.AssignStmt, scope *Scope) {
	a.checkExpr(s.Target, scope)
	a.checkExpr(s.Value, scope)
	if ident, ok := s.Target.(*ast.Ident); ok {
		if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable && !sym.Mutable {
			a.addError(s.Pos, "cannot assign to immutable variable '%s' (declared with val) / 不能给不可变变量 '%s' 赋值（使用 val 声明）", ident.Name, ident.Name)
		}
	}
	// 1.4: mark compound assignment on model member as atomic
	if s.Op == "+=" || s.Op == "-=" {
		if member, ok := s.Target.(*ast.MemberExpr); ok {
			if objType := a.checkExpr(member.Object, scope); objType != nil && objType.Kind == TypeModel {
				s.Atomic = true
			}
		}
	}
}

func (a *Analyzer) checkForStmt(s *ast.ForStmt, scope *Scope) {
	childScope := scope.Child()
	if s.Collection != nil {
		collType := a.checkExpr(s.Collection, childScope)
		if s.VarName != "" {
			var itemType *ResolvedType
			if collType != nil && collType.IsList {
				itemType = &ResolvedType{
					Kind:   collType.Kind,
					Name:   collType.Name,
					Fields: collType.Fields,
				}
			}
			childScope.Define(&Symbol{
				Name: s.VarName,
				Kind: SymVariable,
				Type: itemType,
				Pos:  s.Pos,
			})
		}
	}
	a.checkBlock(s.Body, childScope)
}

// checkAwaitExpr collects the type of each expression statement in the await
// block and returns a tuple type so that destructuring can assign each variable
// its own type: val (user, posts) = await { getUser(id); getPosts(uid) }
func (a *Analyzer) checkAwaitExpr(e *ast.AwaitExpr, scope *Scope) *ResolvedType {
	if a.inAwait {
		a.addError(e.Pos, "nested await is not allowed / 不允许嵌套 await")
		return nil
	}
	prevAwait := a.inAwait
	a.inAwait = true
	defer func() { a.inAwait = prevAwait }()
	childScope := scope.Child()
	var elements []*ResolvedType
	if e.Body != nil {
		for _, stmt := range e.Body.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok && es.Expr != nil {
				t := a.checkExpr(es.Expr, childScope)
				elements = append(elements, t)
			} else {
				a.checkStmt(stmt, childScope)
			}
		}
	}
	// single expression — return its type directly, not a tuple
	if len(elements) == 1 {
		return elements[0]
	}
	if len(elements) > 1 {
		return &ResolvedType{
			Kind:  TypeTuple,
			Name:  "Tuple",
			Tuple: elements,
		}
	}
	return nil
}

func (a *Analyzer) checkExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *ast.Literal:
		return a.checkLiteralExpr(e)
	case *ast.Ident:
		return a.checkIdentExpr(e, scope)
	case *ast.MemberExpr:
		return a.checkMemberExpr(e, scope)
	case *ast.CallExpr:
		return a.checkCallExpr(e, scope)
	case *ast.BinaryExpr:
		return a.checkBinaryExpr(e, scope)
	case *ast.UnaryExpr:
		return a.checkUnaryExpr(e, scope)
	case *ast.ElvisExpr:
		return a.checkElvisExpr(e, scope)
	case *ast.WhenExpr:
		return a.checkWhenExpr(e, scope)
	case *ast.LambdaExpr:
		return a.checkLambdaExpr(e, scope)
	default:
		return a.checkCompositeExpr(expr, scope)
	}
}

// checkCompositeExpr handles composite expression types (list, object, range, transaction, template).
func (a *Analyzer) checkCompositeExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	switch e := expr.(type) {
	case *ast.ListExpr:
		return a.checkListExpr(e, scope)
	case *ast.ObjectExpr:
		return a.checkObjectExpr(e, scope)
	case *ast.RangeExpr:
		return a.checkRangeExpr(e, scope)
	case *ast.TransactionExpr:
		return a.checkTransactionExpr(e, scope)
	case *ast.TemplateString:
		return a.checkTemplateString(e, scope)
	case *ast.YieldExpr:
		return a.checkExpr(e.Value, scope)
	case *ast.AsyncExpr:
		childScope := scope.Child()
		a.checkBlock(e.Body, childScope)
		return nil
	case *ast.AwaitExpr:
		return a.checkAwaitExpr(e, scope)
	case *ast.ForStmt:
		// for as expression — check like statement but return type
		a.checkStmt(e, scope)
		return nil // yield type inference is complex, return nil for now
	}
	// unreachable: all known ast.Expr types are covered by checkExpr + checkCompositeExpr switches;
	// kept as defensive fallback required by Go's type system.
	return nil
}

func (a *Analyzer) checkListExpr(e *ast.ListExpr, scope *Scope) *ResolvedType {
	for _, item := range e.Items {
		a.checkExpr(item, scope)
	}
	return nil
}

func (a *Analyzer) checkObjectExpr(e *ast.ObjectExpr, scope *Scope) *ResolvedType {
	for _, f := range e.Fields {
		a.checkExpr(f.Value, scope)
	}
	return nil
}

func (a *Analyzer) checkRangeExpr(e *ast.RangeExpr, scope *Scope) *ResolvedType {
	a.checkExpr(e.Start, scope)
	a.checkExpr(e.End, scope)
	return nil
}

func (a *Analyzer) checkTransactionExpr(e *ast.TransactionExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	prev := a.inTransaction
	a.inTransaction = true
	a.checkBlock(e.Body, childScope)
	a.inTransaction = prev
	return nil
}

func (a *Analyzer) checkTemplateString(e *ast.TemplateString, scope *Scope) *ResolvedType {
	for _, part := range e.Parts {
		a.checkExpr(part, scope)
	}
	return &ResolvedType{Kind: TypeString, Name: "String"}
}

func (a *Analyzer) checkLiteralExpr(e *ast.Literal) *ResolvedType {
	return a.literalType(e)
}

func (a *Analyzer) checkIdentExpr(e *ast.Ident, scope *Scope) *ResolvedType {
	// check if it's a type name
	if typ, ok := a.types[e.Name]; ok {
		return typ
	}
	// check local scope (includes parent scopes)
	resolved := scope.ResolvedTypeOf(e.Name)
	if resolved != nil {
		return resolved
	}
	sym := scope.Lookup(e.Name)
	if sym != nil {
		return sym.Type
	}
	// don't error on built-in operations used as expressions
	if isBuiltinOp(e.Name) {
		return nil
	}
	a.addErrorWithSuggestion(e.Pos, e.Name, "undefined: '%s' / 未定义: '%s'", e.Name, e.Name)
	return nil
}

func (a *Analyzer) checkMemberExpr(e *ast.MemberExpr, scope *Scope) *ResolvedType {
	objType := a.checkExpr(e.Object, scope)
	if objType == nil {
		return nil
	}

	if e.SafeCall && !objType.Nullable {
		a.addWarning(e.Pos, "unnecessary safe call (?.) on non-null type '%s' / 对非空类型 '%s' 使用了不必要的安全调用 (?.)", objType.Name, objType.Name)
	}

	a.warnStringContains(e, objType)

	if result, ok := a.resolveMemberMethod(e, objType); ok {
		return result
	}

	return a.resolveFieldAccess(e, objType)
}

// warnStringContains warns about .contains() on String (non-list) without @search.
func (a *Analyzer) warnStringContains(e *ast.MemberExpr, objType *ResolvedType) {
	if e.Field == "contains" && objType.Kind == TypeString && !objType.IsList {
		if !a.hasSearchDirective(e.Object) {
			a.addWarning(e.Pos, "'contains' generates LIKE '%%%%...%%%%' which causes full table scan, consider @search for full-text index / 'contains' 会生成 LIKE 模糊查询导致全表扫描，建议使用 @search 全文索引")
		}
	}
}

// resolveMemberMethod attempts to resolve the member access as a built-in method call.
// Returns (result, true) if resolved, (nil, false) if it should fall through to field access.
func (a *Analyzer) resolveMemberMethod(e *ast.MemberExpr, objType *ResolvedType) (*ResolvedType, bool) {
	// debug chain methods — valid on all types, return self
	if isDebugMethod(e.Field) {
		return objType, true
	}
	// query builder chain methods — Model.where(...).sum(...) etc.
	if objType.Kind == TypeModel && isQueryMethod(e.Field) {
		return a.resolveQueryMethod(e.Field, objType), true
	}
	if objType.Kind == TypeQueryBuilder && isQueryMethod(e.Field) {
		return a.resolveQueryMethod(e.Field, objType.ModelType), true
	}
	// collection methods on list types
	if objType.IsList && isCollectionMethod(e.Field) {
		return a.resolveCollectionMethod(e.Field, objType), true
	}
	// .let scope function — works on any type
	if e.Field == "let" {
		return nil, true
	}
	return nil, false
}

// resolveFieldAccess resolves a field or enum value access on a type.
func (a *Analyzer) resolveFieldAccess(e *ast.MemberExpr, objType *ResolvedType) *ResolvedType {
	field := objType.LookupField(e.Field)
	if field == nil {
		if objType.Kind == TypeEnum {
			for _, v := range objType.EnumValues {
				if v == e.Field {
					return objType
				}
			}
		}
		a.addFieldError(e.Pos, objType, e.Field)
		return nil
	}

	// method fields require () to call
	if field.IsMethod && !a.inCallFunc {
		a.addError(e.Pos, "'%s' is a method, use %s() / '%s' 是方法，请使用 %s()", e.Field, e.Field, e.Field, e.Field)
		return field.Type
	}

	result := field.Type
	if result != nil && e.SafeCall {
		result = result.AsNullable()
	}
	return result
}

func (a *Analyzer) checkCallExpr(e *ast.CallExpr, scope *Scope) *ResolvedType {
	prev := a.inCallFunc
	a.inCallFunc = true
	a.checkExpr(e.Func, scope)
	a.inCallFunc = prev

	// check CRUD inside lambda (function-style and chain-style)
	if a.inLambda && isCRUDCall(e) {
		a.addError(e.Pos, "database query inside collection lambda is forbidden, use batch query instead / 集合 lambda 内禁止数据库查询，请使用批量查询")
	}

	// transaction { ... } — parsed as CallExpr(Ident("transaction"), [LambdaExpr])
	// Set inTransaction for the lambda body; also suppress inLambda since
	// transaction blocks are NOT collection lambdas.
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "transaction" {
		return a.checkTransactionCall(e, scope)
	}

	// my.load(field, ...) — field names are not variables, skip checking args
	if a.isMyMethodCall(e) {
		return nil
	}

	callScope := a.injectCRUDScope(e, scope)
	isCRUD := isCRUDIdent(e)

	// orderBy(field.desc) — sort expressions are not normal expressions, skip checking
	if isOrderByMethod(e) {
		return a.inferCallReturnType(e)
	}

	for _, arg := range e.Args {
		// skip order/select/include/distinct — these use query modifier syntax (field.desc)
		if isCRUD && isQueryModifierArg(arg.Name) {
			continue
		}
		a.checkExpr(arg.Value, callScope)
	}

	// 1.3: check create required fields (function-style and chain-style)
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "create" {
		a.checkCreateRequiredFields(e)
	}
	if method, _ := chainCRUDInfo(e); method == "create" {
		a.checkChainCreateRequiredFields(e)
	}

	return a.inferCallReturnType(e)
}

// checkTransactionCall handles transaction { ... } which is parsed as
// CallExpr(Ident("transaction"), [LambdaExpr]). It sets inTransaction
// and suppresses inLambda for the body.
func (a *Analyzer) checkTransactionCall(e *ast.CallExpr, scope *Scope) *ResolvedType {
	prevTx := a.inTransaction
	prevLambda := a.inLambda
	a.inTransaction = true
	a.inLambda = false
	for _, arg := range e.Args {
		// If the arg is a LambdaExpr, check its body directly without
		// entering checkLambdaExpr (which would set inLambda=true).
		if lambda, ok := arg.Value.(*ast.LambdaExpr); ok {
			childScope := scope.Child()
			childScope.Define(&Symbol{
				Name: "it",
				Kind: SymVariable,
			})
			a.checkBlock(lambda.Body, childScope)
		} else {
			a.checkExpr(arg.Value, scope)
		}
	}
	a.inTransaction = prevTx
	a.inLambda = prevLambda
	return nil
}

// isMyMethodCall returns true if the call is my.load(...) or similar my.xxx() builtin.
func (a *Analyzer) isMyMethodCall(e *ast.CallExpr) bool {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "my"
}

// injectCRUDScope creates a child scope with model fields injected for CRUD operations.
// Handles both function-style find(User, where: ...) and chain-style User.find(where: ...).
func (a *Analyzer) injectCRUDScope(e *ast.CallExpr, scope *Scope) *Scope {
	var modelType *ResolvedType

	// function-style: find(User, where: ...)
	if ident, ok := e.Func.(*ast.Ident); ok && isCRUDOp(ident.Name) && len(e.Args) > 0 {
		if modelIdent, ok := e.Args[0].Value.(*ast.Ident); ok {
			modelType = a.types[modelIdent.Name]
		}
	}

	// chain-style: User.find(where: ...), User.where(email == ...)
	if _, modelName := chainCRUDInfo(e); modelName != "" {
		modelType = a.types[modelName]
	}

	// chain on query builder variable: orders.sum(total) where orders is TypeQueryBuilder
	if modelType == nil {
		modelType = a.resolveChainModelType(e, scope)
	}

	if modelType == nil {
		return scope
	}

	callScope := scope.Child()
	a.defineFieldsInScope(callScope, modelType)
	for _, parent := range modelType.Parents {
		a.defineFieldsInScope(callScope, parent)
	}
	// inject 'it' as a reference to the model type for disambiguation
	callScope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
		Type: modelType,
	})
	// warn about ambiguous identifiers: field name shadows an outer scope symbol
	a.warnAmbiguousCRUDFields(e, callScope, scope)
	return callScope
}

// warnAmbiguousCRUDFields warns when a CRUD-injected field name shadows a symbol from the outer scope.
// Only checks "where:" args — other named args (id:, title:, etc.) have clear semantics.
func (a *Analyzer) warnAmbiguousCRUDFields(e *ast.CallExpr, callScope *Scope, outerScope *Scope) {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	}
}

// collectAmbiguousIdents walks an expression tree and warns about identifiers
// that match both a CRUD-injected field and an outer scope symbol.
func (a *Analyzer) collectAmbiguousIdents(expr ast.Expr, callScope *Scope, outerScope *Scope) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		// bare ident is not ambiguous on its own — only same-name comparison (x == x) is.
		// No executable code here; Go coverage considers this switch branch uncoverable.
	case *ast.BinaryExpr:
		a.checkBinaryAmbiguity(e, callScope, outerScope)
	case *ast.UnaryExpr:
		a.collectAmbiguousIdents(e.Value, callScope, outerScope)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	case *ast.MemberExpr:
		a.collectAmbiguousIdents(e.Object, callScope, outerScope)
	}
}

func (a *Analyzer) checkBinaryAmbiguity(e *ast.BinaryExpr, callScope *Scope, outerScope *Scope) {
	if itMemberField(e.Left) != "" || itMemberField(e.Right) != "" {
		return
	}
	leftIdent, leftOk := e.Left.(*ast.Ident)
	rightIdent, rightOk := e.Right.(*ast.Ident)
	if leftOk && rightOk && leftIdent.Name == rightIdent.Name {
		fieldSym := callScope.LookupLocal(leftIdent.Name)
		outerSym := outerScope.Lookup(leftIdent.Name)
		if fieldSym != nil && fieldSym.Kind == SymField && outerSym != nil {
			a.addWarning(leftIdent.Pos,
				"ambiguous: '%s' == '%s' — both refer to same name, use 'it.%s' for field / 歧义：'%s' == '%s'，请用 'it.%s' 指定字段",
				leftIdent.Name, rightIdent.Name, leftIdent.Name, leftIdent.Name, rightIdent.Name, leftIdent.Name)
		}
		return
	}
	a.collectAmbiguousIdents(e.Left, callScope, outerScope)
	a.collectAmbiguousIdents(e.Right, callScope, outerScope)
}

// itMemberField returns the field name if expr is `it.fieldName`, empty string otherwise.
func itMemberField(expr ast.Expr) string {
	m, ok := expr.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	ident, ok := m.Object.(*ast.Ident)
	if !ok || ident.Name != "it" {
		return ""
	}
	return m.Field
}

// defineFieldsInScope defines all fields from a type into the given scope.
func (a *Analyzer) defineFieldsInScope(scope *Scope, typ *ResolvedType) {
	for fieldName, field := range typ.Fields {
		scope.Define(&Symbol{
			Name: fieldName,
			Kind: SymField,
			Type: field.Type,
		})
	}
}

// inferCallReturnType infers the return type of a call expression.
func (a *Analyzer) inferCallReturnType(e *ast.CallExpr) *ResolvedType {
	// function-style CRUD: find(User, ...)
	if ident, ok := e.Func.(*ast.Ident); ok {
		if isCRUDOp(ident.Name) && len(e.Args) > 0 {
			return a.inferCRUDReturnType(e, ident.Name)
		}
		sym := a.scope.Lookup(ident.Name)
		if sym != nil {
			return sym.Type
		}
		return nil
	}
	// chain-style CRUD: User.find(...)
	if method, modelName := chainCRUDInfo(e); method != "" {
		return a.inferChainCRUDReturnType(e, method, modelName)
	}
	return nil
}

// inferCRUDReturnType infers the return type for CRUD operations.
func (a *Analyzer) inferCRUDReturnType(e *ast.CallExpr, opName string) *ResolvedType {
	// aggregate/groupBy/raw/paginate have dynamic return types
	if isDynamicReturnCRUD(opName) {
		return nil
	}
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return nil
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok {
		return nil
	}
	switch opName {
	case "find":
		return a.inferFindReturnType(e, modelType)
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany", "count":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	}
	// unreachable: all known CRUD ops are handled above or filtered by isDynamicReturnCRUD;
	// kept as defensive fallback.
	return modelType
}

// isDynamicReturnCRUD returns true for CRUD ops with dynamic return types.
func isDynamicReturnCRUD(opName string) bool {
	switch opName {
	case "aggregate", "groupBy", "raw", "paginate":
		return true
	}
	return false
}

// inferFindReturnType returns list type for where queries, non-nullable for id queries
// (find auto-throws NotFound when record is missing; use first() for nullable).
func (a *Analyzer) inferFindReturnType(e *ast.CallExpr, modelType *ResolvedType) *ResolvedType {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			return modelType.AsList()
		}
	}
	return modelType
}

// inferChainCRUDReturnType infers the return type for chain-style CRUD: Model.find(...), Model.create(...), etc.
func (a *Analyzer) inferChainCRUDReturnType(e *ast.CallExpr, method string, modelName string) *ResolvedType {
	if isDynamicReturnCRUD(method) {
		return nil
	}
	modelType, ok := a.types[modelName]
	if !ok {
		return nil
	}
	switch method {
	case "find":
		return a.inferFindReturnType(e, modelType)
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany", "count":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	}
	// unreachable: all known CRUD ops are handled above or filtered by isDynamicReturnCRUD;
	// kept as defensive fallback.
	return modelType
}

// checkChainCreateRequiredFields checks required fields for chain-style User.create(...).
func (a *Analyzer) checkChainCreateRequiredFields(e *ast.CallExpr) {
	_, modelName := chainCRUDInfo(e)
	modelType, ok := a.types[modelName]
	if !ok || modelType.Fields == nil {
		return
	}
	provided := collectProvidedFields(e.Args)
	for _, field := range modelType.Fields {
		if isAutoManagedField(field) || provided[field.Name] {
			continue
		}
		a.addWarning(e.Pos, "missing required field '%s' in %s.create(...) / %s.create(...) 缺少必填字段 '%s'",
			field.Name, modelName, modelName, field.Name)
	}
}

// checkCreateRequiredFields checks that all required fields are provided in create().
func (a *Analyzer) checkCreateRequiredFields(e *ast.CallExpr) {
	if len(e.Args) == 0 {
		return
	}
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok || modelType.Fields == nil {
		return
	}
	provided := collectProvidedFields(e.Args[1:])
	for _, field := range modelType.Fields {
		if isAutoManagedField(field) || provided[field.Name] {
			continue
		}
		a.addWarning(e.Pos, "missing required field '%s' in create(%s, ...) / create(%s, ...) 缺少必填字段 '%s'",
			field.Name, modelIdent.Name, modelIdent.Name, field.Name)
	}
}

func collectProvidedFields(args []*ast.NamedArg) map[string]bool {
	provided := map[string]bool{}
	for _, arg := range args {
		if arg.Name != "" {
			provided[arg.Name] = true
		}
	}
	return provided
}

// isAutoManagedField returns true for fields that don't need explicit values in create().
func isAutoManagedField(field *FieldInfo) bool {
	if field.Nullable || field.HasDefault || field.Computed || field.IsMethod {
		return true
	}
	if hasDirective(field.Directives, "auto") || hasDirective(field.Directives, "id") {
		return true
	}
	return field.Name == "createdAt" || field.Name == "updatedAt" || field.Name == "deletedAt" || field.Name == "id"
}

func hasDirective(directives []string, name string) bool {
	for _, d := range directives {
		if d == name {
			return true
		}
	}
	return false
}

// isCRUDIdent returns true if the call is a CRUD operation (function-style or chain-style).
// isCRUDCall returns true if the call is a CRUD operation (function-style or chain-style).
func isCRUDCall(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok && isDBQueryOp(ident.Name) {
		return true
	}
	if member, ok := e.Func.(*ast.MemberExpr); ok && isDBQueryOp(member.Field) {
		return true
	}
	return false
}

func isCRUDIdent(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok {
		return isCRUDOp(ident.Name)
	}
	// chain-style: Model.find(...), User.create(...)
	if member, ok := e.Func.(*ast.MemberExpr); ok {
		return isCRUDOp(member.Field)
	}
	return false
}

// chainCRUDInfo extracts the CRUD method name and model name from a chain-style call.
// Supports both direct chain (User.find(...)) and query chain (User.where(...)).
// Returns ("", "") if not a chain-style CRUD/query call.
func chainCRUDInfo(e *ast.CallExpr) (method string, modelName string) {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return "", ""
	}
	if !isCRUDOp(member.Field) && !isQueryMethod(member.Field) {
		return "", ""
	}
	// direct: User.where(...)
	if ident, ok := member.Object.(*ast.Ident); ok {
		return member.Field, ident.Name
	}
	return member.Field, ""
}

// resolveChainModelType resolves the underlying model type from a chain call on a variable.
// e.g., orders.sum(total) where orders: TypeQueryBuilder → returns the model type.
func (a *Analyzer) resolveChainModelType(e *ast.CallExpr, scope *Scope) *ResolvedType {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return nil
	}
	return a.resolveModelFromExpr(member.Object, scope)
}

// resolveModelFromExpr extracts the underlying model type from an expression.
// Handles: Ident (variable), MemberExpr (Model.where), CallExpr (Order.where(...).sum).
func (a *Analyzer) resolveModelFromExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	switch obj := expr.(type) {
	case *ast.Ident:
		// variable: orders.sum(total) — look up variable type
		sym := scope.Lookup(obj.Name)
		if sym != nil && sym.Type != nil {
			if sym.Type.Kind == TypeQueryBuilder && sym.Type.ModelType != nil {
				return sym.Type.ModelType
			}
			if sym.Type.Kind == TypeModel {
				return sym.Type
			}
		}
		// model name directly: Order.sum(total)
		if t := a.types[obj.Name]; t != nil && t.Kind == TypeModel {
			return t
		}
	case *ast.CallExpr:
		// chained call: Order.where(...).sum(total) — recurse into the call's func
		if m, ok := obj.Func.(*ast.MemberExpr); ok {
			return a.resolveModelFromExpr(m.Object, scope)
		}
	}
	return nil
}

// isQueryModifierArg returns true for CRUD named args that use query modifier syntax.
// isOrderByMethod returns true if the call is an orderBy chain method
// whose args are sort expressions (field.desc, field.asc), not normal expressions.
func isOrderByMethod(e *ast.CallExpr) bool {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	return member.Field == "orderBy"
}

func isQueryModifierArg(name string) bool {
	switch name {
	case "order", "select", "include", "distinct",
		"by", "sum", "avg", "min", "max",
		"having", "page", "pageSize", "cursor", "limit",
		"params":
		return true
	}
	return false
}

func isCRUDOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "count", "exists",
		"aggregate", "groupBy", "raw", "paginate",
		"save":
		return true
	}
	return false
}

// isDBQueryOp returns true for operations that trigger a database query.
// Used to forbid DB queries inside collection lambdas.
// Excludes aggregate methods (sum, avg, count, etc.) which are valid in groupBy lambdas.
func isDBQueryOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "raw":
		return true
	}
	return false
}

// isAggregateFieldRefCall returns true for aggregate/groupBy calls whose args
// are field name references (not variables) and should not be expression-checked.
func isAggregateFieldRefCall(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok {
		return ident.Name == "aggregate" || ident.Name == "groupBy"
	}
	if member, ok := e.Func.(*ast.MemberExpr); ok {
		return member.Field == "aggregate" || member.Field == "groupBy"
	}
	return false
}

func (a *Analyzer) checkBinaryExpr(e *ast.BinaryExpr, scope *Scope) *ResolvedType {
	left := a.checkExpr(e.Left, scope)
	right := a.checkExpr(e.Right, scope)
	return a.checkBinaryOp(e.Op, left, right, e.Pos)
}

func (a *Analyzer) checkUnaryExpr(e *ast.UnaryExpr, scope *Scope) *ResolvedType {
	inner := a.checkExpr(e.Value, scope)
	if e.Op == "throw" {
		return nil // throw never returns
	}
	if e.Op == "!" && inner != nil && inner.Kind != TypeBool {
		a.addError(e.Pos, "operator '!' requires Boolean, got '%s' / 运算符 '!' 需要 Boolean 类型，得到 '%s'", inner.Name, inner.Name)
	}
	return inner
}

func (a *Analyzer) checkElvisExpr(e *ast.ElvisExpr, scope *Scope) *ResolvedType {
	leftType := a.checkExpr(e.Left, scope)
	a.checkExpr(e.Right, scope)
	if leftType == nil {
		return nil
	}
	// after ?:, the left side is narrowed to non-null
	if ident, ok := e.Left.(*ast.Ident); ok {
		scope.Narrow(ident.Name, leftType.AsNonNull())
	}
	return leftType.AsNonNull()
}

func (a *Analyzer) checkWhenExpr(e *ast.WhenExpr, scope *Scope) *ResolvedType {
	var subjectType *ResolvedType
	if e.Subject != nil {
		subjectType = a.checkExpr(e.Subject, scope)
	}
	// check duplicate branches
	seenBranches := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			if seenBranches[b.IsType] {
				a.addError(e.Pos, "duplicate when branch 'is %s' / 重复的 when 分支 'is %s'", b.IsType, b.IsType)
			}
			seenBranches[b.IsType] = true
		}
		if b.Condition != nil {
			a.checkExpr(b.Condition, scope)
		}
		if b.Body != nil {
			// sealed variant narrowing: inject variant fields into subject
			branchScope := scope
			if b.IsType != "" && subjectType != nil && subjectType.Kind == TypeSealed {
				branchScope = a.injectSealedVariantFields(scope, subjectType, b.IsType, e.Subject)
			}
			a.checkExpr(b.Body, branchScope)
		}
	}
	if e.Else != nil {
		a.checkExpr(e.Else, scope)
	}
	// sealed exhaustiveness check
	a.checkWhenExhaustive(e, subjectType)
	return nil
}

// checkWhenExhaustive verifies when exhaustiveness for sealed/enum types and else requirement.
func (a *Analyzer) checkWhenExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	// when without subject (no sealed/enum) — must have else
	if subjectType == nil {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// enum exhaustiveness
	if subjectType.Kind == TypeEnum {
		a.checkEnumExhaustive(e, subjectType)
		return
	}
	if subjectType.Kind != TypeSealed {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// if there's an else branch, exhaustiveness is guaranteed
	if e.Else != nil {
		return
	}
	// collect matched variant names
	matched := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			matched[b.IsType] = true
		}
	}
	// check for missing variants
	for _, v := range subjectType.Variants {
		if !matched[v.Name] {
			a.addError(e.Pos,
				"when on sealed type '%s' is not exhaustive, missing variant '%s' / when 未穷举密封类型 '%s'，缺少变体 '%s'",
				subjectType.Name, v.Name, subjectType.Name, v.Name)
		}
	}
}

// checkEnumExhaustive verifies that when on an enum type covers all values.
func (a *Analyzer) checkEnumExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	if e.Else != nil {
		return
	}
	// collect matched enum values from branch conditions (e.g. Role.ADMIN -> ...)
	matched := map[string]bool{}
	for _, b := range e.Branches {
		a.collectEnumValues(b.Condition, matched)
	}
	for _, v := range subjectType.EnumValues {
		if !matched[v] {
			a.addError(e.Pos,
				"when on enum '%s' is not exhaustive, missing value '%s' / when 未穷举枚举 '%s'，缺少值 '%s'",
				subjectType.Name, v, subjectType.Name, v)
		}
	}
}

func (a *Analyzer) collectEnumValues(expr ast.Expr, matched map[string]bool) {
	if expr == nil {
		return
	}
	if member, ok := expr.(*ast.MemberExpr); ok {
		matched[member.Field] = true
	}
}

func (a *Analyzer) checkLambdaExpr(e *ast.LambdaExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	if len(e.Params) > 0 {
		// named params: { x -> ... } or { a, b -> ... }
		for _, name := range e.Params {
			childScope.Define(&Symbol{Name: name, Kind: SymVariable})
		}
	} else {
		// implicit 'it' parameter
		childScope.Define(&Symbol{Name: "it", Kind: SymVariable})
	}
	prev := a.inLambda
	a.inLambda = true
	a.checkBlock(e.Body, childScope)
	a.inLambda = prev
	return nil
}

func (a *Analyzer) literalType(lit *ast.Literal) *ResolvedType {
	switch lit.Kind {
	case token.Int:
		return a.types["Int"]
	case token.Float:
		return a.types["Float"]
	case token.String:
		return a.types["String"]
	case token.Duration:
		return a.types["Duration"]
	case token.True, token.False:
		return a.types["Boolean"]
	case token.Null:
		return &ResolvedType{Kind: TypeUnknown, Name: "null", Nullable: true}
	}
	return nil
}

func (a *Analyzer) checkBinaryOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	if left == nil || right == nil {
		return nil
	}

	switch op {
	case "+", "-", "*", "/", "%":
		return a.checkArithmeticOp(op, left, right, pos)

	case "==", "!=":
		// 1.1: enum cannot compare with String
		if left.Kind == TypeEnum && right.Kind == TypeString {
			a.addError(pos, "cannot compare enum '%s' with String, use %s.VALUE / 不能用 String 和枚举 '%s' 比较，请使用 %s.VALUE", left.Name, left.Name, left.Name, left.Name)
		}
		if left.Kind == TypeString && right.Kind == TypeEnum {
			a.addError(pos, "cannot compare String with enum '%s', use %s.VALUE / 不能用 String 和枚举 '%s' 比较，请使用 %s.VALUE", right.Name, right.Name, right.Name, right.Name)
		}
		return a.types["Boolean"]

	case ">", ">=", "<", "<=":
		if !left.IsOrderable() {
			a.addError(pos, "operator '%s' requires orderable type, got '%s' / 运算符 '%s' 需要可排序类型，得到 '%s'", op, left.Name, op, left.Name)
		}
		return a.types["Boolean"]

	case "&&", "||":
		return a.types["Boolean"]

	case "in":
		return a.types["Boolean"]

	case "is":
		return a.types["Boolean"]
	}

	return nil
}

func (a *Analyzer) checkArithmeticOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	// 1.2: nullable cannot be used in arithmetic
	if left.Nullable {
		a.addError(pos, "cannot use '%s' on nullable '%s?', unwrap first with ?: / 不能对可空类型 '%s?' 使用 '%s'，请先用 ?: 解包", op, left.Name, left.Name, op)
	}
	if right.Nullable {
		a.addError(pos, "cannot use '%s' on nullable '%s?', unwrap first with ?: / 不能对可空类型 '%s?' 使用 '%s'，请先用 ?: 解包", op, right.Name, right.Name, op)
	}
	if !left.IsNumeric() || !right.IsNumeric() {
		if op == "+" && left.Kind == TypeString {
			return left // string concatenation
		}
		a.addError(pos, "operator '%s' requires numeric types, got '%s' and '%s' / 运算符 '%s' 需要数字类型，得到 '%s' 和 '%s'", op, left.Name, right.Name, op, left.Name, right.Name)
	}
	if left.Kind == TypeFloat || right.Kind == TypeFloat {
		return a.types["Float"]
	}
	return a.types["Int"]
}

// ========== Sealed Variant Field Injection ==========

// injectSealedVariantFields creates a child scope where the subject variable
// has the variant's fields accessible (e.g., result.transactionId after is Success).
func (a *Analyzer) injectSealedVariantFields(scope *Scope, sealedType *ResolvedType, variantName string, subject ast.Expr) *Scope {
	ident, ok := subject.(*ast.Ident)
	if !ok {
		return scope
	}
	// find the variant
	for _, v := range sealedType.Variants {
		if v.Name == variantName {
			// create a narrowed type with the variant's fields
			narrowed := &ResolvedType{
				Kind:   TypeModel,
				Name:   sealedType.Name,
				Fields: make(map[string]*FieldInfo),
			}
			for _, f := range v.Fields {
				narrowed.Fields[f.Name] = f
			}
			child := scope.Child()
			child.Define(&Symbol{
				Name: ident.Name,
				Kind: SymVariable,
				Type: narrowed,
			})
			return child
		}
	}
	return scope
}

// ========== @withAuth Method Injection ==========

func hasModelDirective(directives []*ast.Directive, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

func (a *Analyzer) injectWithAuthMethods(typ *ResolvedType, directives []*ast.Directive) {
	typ.Fields["createToken"] = &FieldInfo{
		Name:     "createToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".createToken(expires?: Duration): String — Generate JWT token / 生成 JWT 令牌",
	}
	typ.Fields["verify"] = &FieldInfo{
		Name:     "verify",
		Type:     a.types["Boolean"],
		IsMethod: true,
		Doc:      ".verify(plain: String): Boolean — Verify password against @hash field / 校验 @hash 字段的密码",
	}
	// refreshToken always injected — whether refresh is enabled is controlled
	// by JWT_REFRESH env var at runtime, not at compile time.
	typ.Fields["refreshToken"] = &FieldInfo{
		Name:     "refreshToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".refreshToken(token: String): String — Refresh JWT token / 刷新 JWT 令牌",
	}
}

// ========== Unused Variable Warning ==========

// checkUnusedVariables warns about variables declared but never used.
func (a *Analyzer) checkUnusedVariables(scope *Scope) {
	for _, sym := range scope.AllSymbols() {
		if sym.Kind != SymVariable {
			continue
		}
		if sym.Used {
			continue
		}
		// skip _ prefixed variables and auto-injected symbols
		if len(sym.Name) > 0 && sym.Name[0] == '_' {
			continue
		}
		if sym.Name == "my" || sym.Name == "it" || sym.Name == "request" {
			continue
		}
		// Use name length so the diagnostic range highlights the full identifier
		a.addWarningWithLen(sym.Pos, len(sym.Name), "variable '%s' is declared but never used / 变量 '%s' 已声明但未使用", sym.Name, sym.Name)
	}
}

// ========== Event Cycle Detection ==========

// checkEventCycles detects circular event dependencies among on-listeners.
// For each on EventA { ... emit EventB(...) ... }, edge A → B is added.
// DFS detects cycles and reports them as errors.
func (a *Analyzer) checkEventCycles() {
	graph := a.buildEventGraph()
	if len(graph) == 0 {
		return
	}
	a.detectCycles(graph)
}

// buildEventGraph walks all OnDecl bodies looking for EmitStmt nodes
// and returns an adjacency map: event → list of emitted events.
func (a *Analyzer) buildEventGraph() map[string][]string {
	graph := map[string][]string{}
	for _, file := range a.files {
		for _, on := range file.Listeners {
			if on.Body == nil {
				continue
			}
			emitted := collectEmits(on.Body)
			if len(emitted) > 0 {
				graph[on.EventName] = append(graph[on.EventName], emitted...)
			}
		}
	}
	return graph
}

// collectEmits recursively collects all event names from EmitStmt in a block.
func collectEmits(block *ast.Block) []string {
	var result []string
	for _, stmt := range block.Stmts {
		if emit, ok := stmt.(*ast.EmitStmt); ok {
			result = append(result, emit.EventName)
		}
	}
	return result
}

// detectCycles runs DFS on the event graph to find and report cycles.
func (a *Analyzer) detectCycles(graph map[string][]string) {
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	color := map[string]int{}
	parent := map[string]string{}

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		for _, next := range graph[node] {
			if color[next] == gray {
				// cycle found — reconstruct path
				path := a.buildCyclePath(next, node, parent)
				a.addError(token.Position{},
					"circular event dependency detected: %s / 检测到循环事件依赖: %s",
					path, path)
				return
			}
			if color[next] == white {
				parent[next] = node
				dfs(next)
			}
		}
		color[node] = black
	}

	for node := range graph {
		if color[node] == white {
			dfs(node)
		}
	}
}

// buildCyclePath reconstructs the cycle path string from parent map.
func (a *Analyzer) buildCyclePath(cycleStart, cycleEnd string, parent map[string]string) string {
	var path []string
	cur := cycleEnd
	for i := 0; cur != cycleStart && i < len(parent)+1; i++ {
		path = append([]string{cur}, path...)
		next, ok := parent[cur]
		if !ok {
			break
		}
		cur = next
	}
	path = append([]string{cycleStart}, path...)
	path = append(path, cycleStart)
	return strings.Join(path, " → ")
}

// ========== Directive Validation ==========

func (a *Analyzer) checkDirectives(directives []*ast.Directive, ctx DirectiveContext) {
	for _, d := range directives {
		a.validateDirective(d, ctx, "")
	}
}

func (a *Analyzer) checkFieldDirectives(f *ast.FieldDecl) {
	if f.Type == nil {
		return
	}
	typeName := f.Type.Name
	for _, d := range f.Directives {
		a.validateDirective(d, OnField, typeName)
	}
	// computed field directives (@count, @sum, @avg, etc.)
	if f.Computed != nil {
		for _, d := range f.Computed.Directives {
			a.validateDirective(d, OnComputed, typeName)
		}
	}
}

// ========== Error Helpers ==========

func (a *Analyzer) addError(pos token.Position, format string, args ...any) {
	a.errors = append(a.errors, Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addWarning(pos token.Position, format string, args ...any) {
	a.warnings = append(a.warnings, Warning{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addWarningWithLen(pos token.Position, nameLen int, format string, args ...any) {
	a.warnings = append(a.warnings, Warning{
		Pos:     pos,
		NameLen: nameLen,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addErrorWithSuggestion(pos token.Position, name string, format string, args ...any) {
	err := Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	}
	// find closest match
	if suggestion := a.findClosest(name); suggestion != "" {
		err.Suggestion = fmt.Sprintf("did you mean '%s'?", suggestion)
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) addFieldError(pos token.Position, objType *ResolvedType, fieldName string) {
	msg := fmt.Sprintf("'%s' has no field '%s' / '%s' 没有字段 '%s'", objType.Name, fieldName, objType.Name, fieldName)
	err := Error{Pos: pos, Message: msg}

	// suggest closest field name
	if objType.Fields != nil {
		closest := findClosestString(fieldName, fieldNames(objType))
		if closest != "" {
			err.Suggestion = fmt.Sprintf("did you mean '%s'?", closest)
		}
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) findClosest(name string) string {
	var candidates []string
	for typeName := range a.types {
		candidates = append(candidates, typeName)
	}
	for _, sym := range a.scope.AllSymbols() {
		candidates = append(candidates, sym.Name)
	}
	return findClosestString(name, candidates)
}

func fieldNames(t *ResolvedType) []string {
	var names []string
	for name := range t.Fields {
		names = append(names, name)
	}
	return names
}

func findClosestString(target string, candidates []string) string {
	best := ""
	bestDist := 3 // max edit distance to suggest
	for _, c := range candidates {
		d := editDistance(target, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// hasSearchDirective checks if the expression's underlying field has @search directive.
func (a *Analyzer) hasSearchDirective(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	objType := a.checkExpr(member.Object, a.scope)
	if objType == nil {
		return false
	}
	field := objType.LookupField(member.Field)
	if field == nil {
		return false
	}
	for _, d := range field.Directives {
		if d == "search" {
			return true
		}
	}
	return false
}

func isDebugMethod(name string) bool {
	switch name {
	case "d", "i", "w", "e":
		return true
	}
	return false
}

// isQueryMethod returns true if the name is a chain query method on models.
func isQueryMethod(name string) bool {
	switch name {
	case "where", "groupBy", "orderBy",
		"sum", "avg", "count", "min", "max",
		"select", "first", "all",
		"limit", "offset",
		"aggregate", "paginate", "exists",
		"deleteMany", "save":
		return true
	}
	// CRUD ops are also valid chain methods on models (e.g. User.find(...))
	return isCRUDOp(name)
}

func isCollectionMethod(name string) bool {
	switch name {
	case "map", "filter", "sumOf", "count", "any", "firstOrNull",
		"sortBy", "groupBy", "forEach", "flatMap", "joinToString",
		"take", "takeLast", "size", "isEmpty", "contains",
		"first", "last", "lastOrNull", "reversed", "shuffled",
		"distinct", "distinctBy", "drop", "dropLast",
		"indexOf", "zip", "chunked", "windowed", "all", "none",
		"minOf", "maxOf", "sortByDesc", "isNotEmpty", "let":
		return true
	}
	return false
}

func (a *Analyzer) resolveCollectionMethod(method string, listType *ResolvedType) *ResolvedType {
	intType := a.types["Int"]
	boolType := a.types["Boolean"]
	stringType := a.types["String"]

	switch method {
	case "size", "count":
		return intType
	case "isEmpty", "any", "contains":
		return boolType
	case "joinToString":
		return stringType
	case "filter", "sortBy", "take", "takeLast":
		return listType // returns same list type
	case "map", "flatMap", "groupBy":
		return nil // type depends on lambda, can't infer here
	case "forEach":
		return nil // void
	case "firstOrNull":
		// returns single element, nullable
		return &ResolvedType{
			Kind:     listType.Kind,
			Name:     listType.Name,
			Nullable: true,
			Fields:   listType.Fields,
			Parents:  listType.Parents,
		}
	case "sumOf":
		return a.types["Float"]
	default:
		// distinct, reversed, shuffled, drop, dropLast, zip, chunked, windowed, indexOf, etc.
		return nil
	}
}

// resolveQueryMethod resolves the return type for a chain query method on a model.
// modelType is the underlying model type (not the query builder).
func (a *Analyzer) resolveQueryMethod(method string, modelType *ResolvedType) *ResolvedType {
	qb := &ResolvedType{
		Kind:      TypeQueryBuilder,
		Name:      modelType.Name + "QueryBuilder",
		Fields:    modelType.Fields,
		ModelType: modelType,
	}

	switch method {
	case "where", "groupBy", "orderBy", "limit", "offset":
		// chaining methods — return a query builder
		return qb
	case "sum", "avg":
		return a.types["Float"]
	case "count":
		return a.types["Int"]
	case "min", "max":
		// could be any numeric type; Float is a safe approximation
		return a.types["Float"]
	case "select":
		// select { ... } — return type depends on the lambda; can't infer here
		return nil
	case "first":
		return modelType.AsNullable()
	case "all":
		return modelType.AsList()
	// chain-style CRUD operations: Model.find(...), Model.create(...), etc.
	case "find":
		// return type depends on args (where → list, id → non-nullable); resolved in checkCallExpr
		return nil
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	case "aggregate", "raw", "paginate":
		return nil
	default:
		return nil
	}
}

func editDistance(a, b string) int {
	m, n := len(a), len(b)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}
	// Two-row DP: O(n) space instead of O(m*n).
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func isBuiltinOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "count", "exists",
		"aggregate", "groupBy", "raw", "paginate", "save",
		"throw", "transaction", "cache", "storage",
		"mail", "task", "services", "error", "request",
		"Channel", "Result", "my",
		"http", "json", "time", "math", "crypto", "convert",
		"regex", "base64", "url", "uuid",
		"print", "env", "now", "today",
		"verifyHash", "generateToken":
		return true
	}
	return false
}

func kindToSymbol(kind TypeKind) SymbolKind {
	switch kind {
	case TypeModel:
		return SymModel
	case TypeInterface:
		return SymInterface
	case TypeEnum:
		return SymEnum
	case TypeSealed:
		return SymSealed
	default:
		return SymType
	}
}
