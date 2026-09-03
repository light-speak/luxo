package semantic

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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
	currentFile   *ast.File // file being analyzed in Pass 4

	// module isolation
	moduleMap          FileModuleMap                         // file → module info
	typeOwners         *typeOwnership                        // type name → owning module
	extendVisible      map[string]map[string]bool            // module → set of type names visible via extend
	extendFieldVisible map[string]map[string]map[string]bool // module → modelName → set of field names visible via extend
}

// New creates a new Analyzer.
func New() *Analyzer {
	a := &Analyzer{
		scope:              NewScope(),
		types:              make(map[string]*ResolvedType),
		typeOwners:         newTypeOwnership(),
		extendVisible:      make(map[string]map[string]bool),
		extendFieldVisible: make(map[string]map[string]map[string]bool),
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
	a.validateComputedFields(files)
	if a.moduleMap != nil {
		a.validateFederationExtendFields()
	}

	// Pass 3.5: check cross-module type visibility
	if a.moduleMap != nil {
		a.checkModuleVisibility()
	}

	// Pass 3.7: validate bare API names (after ALL model fields are resolved)
	for _, file := range files {
		for _, api := range file.APIs {
			a.validateBareAPI(api)
		}
	}

	// Pass 4: check api/fn bodies (expressions, statements)
	for _, file := range files {
		a.currentFile = file
		a.checkBodies(file)
	}
	a.currentFile = nil

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
	a.resolveEventTypes(file)
	a.resolveApiTypes(file)
	a.resolveFnTypes(file)
	a.resolveExtendFields(file)
}

func (a *Analyzer) resolveEventTypes(file *ast.File) {
	for _, event := range file.Events {
		seen := make(map[string]bool, len(event.Params))
		for _, param := range event.Params {
			if seen[param.Name] {
				a.addError(param.Pos, "duplicate parameter '%s' in event '%s' / 事件 '%s' 中参数 '%s' 重复", param.Name, event.Name, event.Name, param.Name)
				continue
			}
			seen[param.Name] = true
			resolved := a.resolveTypeRef(param.Type, param.Pos)
			if resolved == nil || resolved.Kind == TypeUnknown {
				continue
			}
			if param.Type.IsList && param.Type.Nullable {
				a.addError(param.Pos, "event parameter '%s.%s' cannot be a nullable list / 事件参数 '%s.%s' 不能是可空列表", event.Name, param.Name, event.Name, param.Name)
			}
			switch resolved.Kind {
			case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeBytes, TypeJSON, TypeModel, TypeCustom, TypeEnum:
			default:
				a.addError(param.Pos, "event parameter '%s.%s' has unsupported wire type '%s' / 事件参数 '%s.%s' 使用了不支持的传输类型 '%s'",
					event.Name, param.Name, formatTypeRef(param.Type), event.Name, param.Name, formatTypeRef(param.Type))
			}
		}
	}
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
			// @visible requires nullable field — non-nullable fields can't be conditionally hidden
			if hasModelDirective(f.Directives, "visible") && f.Type != nil && !f.Type.Nullable {
				a.addError(f.Pos, "@visible requires nullable field (use %s? instead of %s) / @visible 字段必须可空", f.Type.Name, f.Type.Name)
			}
		}
		a.validatePrimaryKeyFields(m)
		a.checkDirectives(m.Directives, OnModel)
		// @withAuth: inject .createToken(), .verify(), .refreshToken() methods
		if hasModelDirective(m.Directives, "withAuth") {
			a.injectWithAuthMethods(typ, m.Directives)
		}
		// @hash: inject .verifyPassword() method on the model
		for _, f := range m.Fields {
			if hasModelDirective(f.Directives, "hash") {
				if existing, exists := typ.Fields["verifyPassword"]; exists && !existing.IsMethod {
					a.addError(f.Pos, "field 'verifyPassword' conflicts with @hash injected method / 字段 'verifyPassword' 与 @hash 注入方法冲突")
					break
				}
				typ.Fields["verifyPassword"] = &FieldInfo{
					Name:     "verifyPassword",
					Type:     a.types["Boolean"],
					IsMethod: true,
					Doc:      ".verifyPassword(plain: String): Boolean — Verify plaintext against @hash field / 校验明文与 @hash 字段",
				}
				break
			}
		}
	}
}

func (a *Analyzer) validatePrimaryKeyFields(model *ast.ModelDecl) {
	var first *ast.FieldDecl
	for _, field := range model.Fields {
		if !hasModelDirective(field.Directives, "id") {
			continue
		}
		if field.Type == nil || field.Type.Nullable || field.Type.IsList || !isPrimaryKeyType(field.Type.Name) {
			typeName := "unknown"
			if field.Type != nil {
				typeName = field.Type.Name
				if field.Type.IsList {
					typeName = "[" + typeName + "]"
				}
				if field.Type.Nullable {
					typeName += "?"
				}
			}
			a.addError(field.Pos, "@id field '%s.%s' must be a non-null Int, String, or UUID, got '%s' / @id 字段 '%s.%s' 必须是非空 Int、String 或 UUID，实际为 '%s'",
				model.Name, field.Name, typeName, model.Name, field.Name, typeName)
		}
		if first == nil {
			first = field
			continue
		}
		a.addError(field.Pos, "model '%s' has multiple @id fields ('%s' and '%s') / 模型 '%s' 有多个 @id 字段（'%s' 和 '%s'）",
			model.Name, first.Name, field.Name, model.Name, first.Name, field.Name)
	}
}

func isPrimaryKeyType(name string) bool {
	return name == "Int" || name == "String" || name == "UUID"
}

func (a *Analyzer) validateComputedFields(files []*ast.File) {
	models := make(map[string]*ast.ModelDecl)
	modelModules := make(map[string]string)
	for _, file := range files {
		for _, model := range file.Models {
			models[model.Name] = model
			modelModules[model.Name] = a.fileModule(file.Name)
		}
	}
	for _, model := range models {
		for _, field := range model.Fields {
			if field.Computed != nil {
				a.validateComputedField(model, field, models, modelModules)
			}
		}
	}
}

func (a *Analyzer) validateComputedField(model *ast.ModelDecl, field *ast.FieldDecl, models map[string]*ast.ModelDecl, modelModules map[string]string) {
	var aggregate *ast.Directive
	for _, directive := range field.Computed.Directives {
		if !isComputedAggregateDirective(directive.Name) {
			continue
		}
		if aggregate != nil {
			a.addError(directive.Pos,
				"computed field '%s.%s' must declare exactly one aggregate / 计算字段 '%s.%s' 只能声明一个聚合",
				model.Name, field.Name, model.Name, field.Name)
			return
		}
		aggregate = directive
	}
	if aggregate == nil {
		a.addError(field.Pos,
			"computed field '%s.%s' must declare exactly one aggregate / 计算字段 '%s.%s' 必须声明且只能声明一个聚合",
			model.Name, field.Name, model.Name, field.Name)
		return
	}
	if field.Computed.Body != nil {
		a.addError(field.Pos,
			"aggregate computed field '%s.%s' cannot also declare a body / 聚合计算字段 '%s.%s' 不能同时声明函数体",
			model.Name, field.Name, model.Name, field.Name)
		return
	}
	relationName, targetName, ok := computedAggregateTarget(aggregate)
	if !ok {
		a.addError(aggregate.Pos,
			"@%s requires a list relation%s / @%s 需要一个列表关系%s",
			aggregate.Name, computedAggregateTargetSuffix(aggregate.Name),
			aggregate.Name, computedAggregateTargetSuffix(aggregate.Name))
		return
	}
	relation := findModelField(model, relationName)
	if relation == nil || relation.Computed != nil || relation.Type == nil || !relation.Type.IsList || models[relation.Type.Name] == nil {
		a.addError(aggregate.Pos,
			"@%s target '%s' must be a list relation / @%s 目标 '%s' 必须是列表关系",
			aggregate.Name, relationName, aggregate.Name, relationName)
		return
	}
	targetModel := models[relation.Type.Name]
	if modelModules[model.Name] != modelModules[targetModel.Name] {
		a.addError(aggregate.Pos,
			"computed aggregate relation '%s.%s' must be stored in the same module / 计算聚合关系 '%s.%s' 必须存储在同一模块",
			model.Name, relationName, model.Name, relationName)
		return
	}
	if !a.validateComputedRelationKeys(model, relation, targetModel) {
		return
	}
	if aggregate.Name == "count" {
		a.validateComputedAggregateResult(model, field, aggregate.Name, "Int", "")
		return
	}
	targetField := findModelField(targetModel, targetName)
	if targetField == nil || targetField.Type == nil || targetField.Computed != nil {
		a.addError(aggregate.Pos,
			"aggregate target field '%s.%s' does not exist / 聚合目标字段 '%s.%s' 不存在",
			targetModel.Name, targetName, targetModel.Name, targetName)
		return
	}
	if targetField.Type.IsList || !isAggregateNumericType(targetField.Type.Name) {
		a.addError(aggregate.Pos,
			"@%s target field '%s.%s' must be numeric / @%s 目标字段 '%s.%s' 必须是数值类型",
			aggregate.Name, targetModel.Name, targetName, aggregate.Name, targetModel.Name, targetName)
		return
	}
	want := targetField.Type.Name
	if aggregate.Name == "avg" {
		want = "Float"
	}
	a.validateComputedAggregateResult(model, field, aggregate.Name, want, targetField.Type.Name)
}

func (a *Analyzer) validateComputedRelationKeys(model *ast.ModelDecl, relation *ast.FieldDecl, target *ast.ModelDecl) bool {
	localName, remoteName := computedRelationKeyNames(model, relation)
	local := findModelField(model, localName)
	if !validComputedKey(local, false) {
		a.addError(relation.Pos,
			"computed relation '%s.%s' local key '%s.%s' must be a non-null Int, String, or UUID / 计算关系 '%s.%s' 的本地键 '%s.%s' 必须是非空 Int、String 或 UUID",
			model.Name, relation.Name, model.Name, localName, model.Name, relation.Name, model.Name, localName)
		return false
	}
	remote := findModelField(target, remoteName)
	if remote == nil {
		a.addError(relation.Pos,
			"computed relation '%s.%s' remote key '%s.%s' does not exist / 计算关系 '%s.%s' 的远端键 '%s.%s' 不存在",
			model.Name, relation.Name, target.Name, remoteName, model.Name, relation.Name, target.Name, remoteName)
		return false
	}
	if !validComputedKey(remote, true) || local.Type.Name != remote.Type.Name {
		a.addError(relation.Pos,
			"computed relation '%s.%s' key types must match (%s.%s: %s, %s.%s: %s) / 计算关系 '%s.%s' 的键类型必须一致",
			model.Name, relation.Name, model.Name, localName, local.Type.Name, target.Name, remoteName, computedFieldTypeName(remote), model.Name, relation.Name)
		return false
	}
	return true
}

func computedRelationKeyNames(model *ast.ModelDecl, relation *ast.FieldDecl) (local, remote string) {
	local = computedModelPrimaryKeyName(model)
	for _, directive := range relation.Directives {
		if directive.Name != "by" {
			continue
		}
		if len(directive.Args) > 0 {
			if ident, ok := directive.Args[0].Value.(*ast.Ident); ok {
				remote = ident.Name
			}
		}
		if len(directive.Args) > 1 {
			if ident, ok := directive.Args[1].Value.(*ast.Ident); ok {
				local = ident.Name
			}
		}
		return local, remote
	}
	return local, lowerFirstIdentifier(model.Name) + upperFirstIdentifier(local)
}

func computedModelPrimaryKeyName(model *ast.ModelDecl) string {
	for _, field := range model.Fields {
		if hasModelDirective(field.Directives, "id") {
			return field.Name
		}
	}
	if findModelField(model, "id") != nil {
		return "id"
	}
	return "id"
}

func validComputedKey(field *ast.FieldDecl, nullableAllowed bool) bool {
	return field != nil && field.Type != nil && field.Computed == nil && !field.Type.IsList &&
		(nullableAllowed || !field.Type.Nullable) && isPrimaryKeyType(field.Type.Name)
}

func computedFieldTypeName(field *ast.FieldDecl) string {
	if field == nil || field.Type == nil {
		return "unknown"
	}
	return field.Type.Name
}

func (a *Analyzer) validateComputedAggregateResult(model *ast.ModelDecl, field *ast.FieldDecl, function, want, targetType string) {
	if field.Type != nil && !field.Type.Nullable && !field.Type.IsList && field.Type.Name == want {
		return
	}
	if function == "sum" || function == "min" || function == "max" {
		a.addError(field.Pos,
			"@%s computed field '%s.%s' must match target type %s / @%s 计算字段 '%s.%s' 必须与目标类型 %s 一致",
			function, model.Name, field.Name, targetType, function, model.Name, field.Name, targetType)
		return
	}
	a.addError(field.Pos,
		"@%s computed field '%s.%s' must have type %s / @%s 计算字段 '%s.%s' 必须是 %s 类型",
		function, model.Name, field.Name, want, function, model.Name, field.Name, want)
}

func isComputedAggregateDirective(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max":
		return true
	default:
		return false
	}
}

func computedAggregateTarget(directive *ast.Directive) (relation, field string, ok bool) {
	if len(directive.Args) != 1 {
		return "", "", false
	}
	switch target := directive.Args[0].Value.(type) {
	case *ast.Ident:
		return target.Name, "", directive.Name == "count"
	case *ast.MemberExpr:
		owner, ownerOK := target.Object.(*ast.Ident)
		if !ownerOK || directive.Name == "count" {
			return "", "", false
		}
		return owner.Name, target.Field, true
	default:
		return "", "", false
	}
}

func computedAggregateTargetSuffix(function string) string {
	if function == "count" {
		return " such as @count(posts) / 例如 @count(posts)"
	}
	return " and numeric field such as @" + function + "(posts.amount) / 以及数值字段，例如 @" + function + "(posts.amount)"
}

func findModelField(model *ast.ModelDecl, name string) *ast.FieldDecl {
	for _, field := range model.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func isAggregateNumericType(name string) bool {
	return name == "Int" || name == "Float" || name == "Decimal"
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
		a.validateStreamAPI(api)
		a.validateNativeReturnType(api)
		a.validateScopeDirective(api)
	}
}

func (a *Analyzer) validateStreamAPI(api *ast.ApiDecl) {
	stream, count := findAPIDirective(api.Directives, "stream")
	if stream == nil {
		return
	}
	if count > 1 {
		a.addError(stream.Pos, "@stream may only be declared once / @stream 只能声明一次")
	}
	if api.ReturnType == nil {
		a.addError(api.Pos, "@stream API must declare a return type / @stream API 必须声明返回类型")
	} else if api.ReturnType.Nullable {
		a.addError(api.ReturnType.Pos, "@stream return type must be non-nullable / @stream 返回类型必须非空")
	}
	for _, incompatible := range []string{"cache", "paginate", "scope"} {
		if directive, _ := findAPIDirective(api.Directives, incompatible); directive != nil {
			a.addError(directive.Pos, "@stream cannot be combined with @%s / @stream 不能与 @%s 组合使用", incompatible, incompatible)
		}
	}

	native, _ := findAPIDirective(api.Directives, "native")
	if native != nil && api.Body != nil {
		a.addError(api.Body.Pos, "@native @stream API cannot declare a Luxo matcher body / @native @stream API 不能声明 Luxo 匹配器主体")
	}
	if len(stream.Args) == 0 {
		if native == nil {
			a.addError(stream.Pos, "@stream without an event source requires @native / 没有事件源的 @stream 必须使用 @native")
		}
		return
	}

	ident, ok := stream.Args[0].Value.(*ast.Ident)
	if !ok || ident.Name == "" {
		a.addError(stream.Pos, "@stream event source must be an event identifier / @stream 事件源必须是事件标识符")
		return
	}
	event := a.findEvent(ident.Name)
	if event == nil {
		a.addError(stream.Pos, "@stream event '%s' does not exist / @stream 事件 '%s' 不存在", ident.Name, ident.Name)
		return
	}
	if api.ReturnType != nil {
		matches := 0
		for _, param := range event.Params {
			if sameTypeRefExact(param.Type, api.ReturnType) {
				matches++
			}
		}
		if matches != 1 {
			a.addError(stream.Pos, "@stream event '%s' must contain exactly one payload parameter of type '%s', got %d / @stream 事件 '%s' 必须包含且仅包含一个 '%s' 类型的载荷参数，实际为 %d 个",
				ident.Name, formatTypeRef(api.ReturnType), matches, ident.Name, formatTypeRef(api.ReturnType), matches)
		}
	}
	if native == nil {
		a.validateGeneratedStreamMatcher(api, event)
	}
}

func findAPIDirective(directives []*ast.Directive, name string) (*ast.Directive, int) {
	var found *ast.Directive
	count := 0
	for _, directive := range directives {
		if directive.Name == name {
			if found == nil {
				found = directive
			}
			count++
		}
	}
	return found, count
}

func (a *Analyzer) findEvent(name string) *ast.EventDecl {
	for _, file := range a.files {
		for _, event := range file.Events {
			if event.Name == name {
				return event
			}
		}
	}
	return nil
}

func sameTypeRefExact(left, right *ast.TypeRef) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Name != right.Name || left.Nullable != right.Nullable || left.IsList != right.IsList || len(left.TypeArgs) != len(right.TypeArgs) || len(left.Tuple) != len(right.Tuple) {
		return false
	}
	for i := range left.TypeArgs {
		if !sameTypeRefExact(left.TypeArgs[i], right.TypeArgs[i]) {
			return false
		}
	}
	for i := range left.Tuple {
		if !sameTypeRefExact(left.Tuple[i], right.Tuple[i]) {
			return false
		}
	}
	return true
}

func formatTypeRef(ref *ast.TypeRef) string {
	if ref == nil {
		return ""
	}
	name := ref.Name
	if ref.IsList {
		name = "[" + name + "]"
	}
	if ref.Nullable {
		name += "?"
	}
	return name
}

func (a *Analyzer) validateGeneratedStreamMatcher(api *ast.ApiDecl, event *ast.EventDecl) {
	if api.Body == nil {
		return
	}
	if len(api.Body.Stmts) != 1 {
		a.addError(api.Body.Pos, "@stream matcher body must contain exactly one boolean expression / @stream 匹配器主体必须且只能包含一个布尔表达式")
		return
	}
	exprStmt, ok := api.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || exprStmt.Expr == nil {
		a.addError(api.Body.Pos, "@stream matcher body must contain exactly one boolean expression / @stream 匹配器主体必须且只能包含一个布尔表达式")
		return
	}

	eventParams := make(map[string]*ast.ParamDecl, len(event.Params))
	for _, param := range event.Params {
		eventParams[param.Name] = param
	}
	apiParams := make(map[string]*ast.ParamDecl, len(api.Params))
	for _, param := range api.Params {
		apiParams[param.Name] = param
	}
	reported := make(map[string]bool)
	ast.WalkExprs(api.Body, func(expr ast.Expr) {
		a.validateStreamMatcherExpr(expr, eventParams, apiParams, reported)
	})
}

func (a *Analyzer) validateStreamMatcherExpr(expr ast.Expr, eventParams, apiParams map[string]*ast.ParamDecl, reported map[string]bool) {
	switch value := expr.(type) {
	case *ast.Literal, *ast.UnaryExpr:
		return
	case *ast.BinaryExpr:
		if value.Op == "in" || value.Op == "is" {
			a.addError(value.Pos, "operator '%s' is not supported by generated @stream matchers; use @native / 生成的 @stream 匹配器不支持运算符 '%s'，请使用 @native", value.Op, value.Op)
		}
	case *ast.Ident:
		if param := apiParams[value.Name]; param != nil {
			a.validateStreamMatcherType(value.Pos, "parameter", value.Name, param.Type, reported)
		}
	case *ast.MemberExpr:
		ident, ok := value.Object.(*ast.Ident)
		if !ok {
			a.addError(value.Pos, "generated @stream matchers only support direct it.field, my.field, and enum member access / 生成的 @stream 匹配器仅支持直接访问 it.field、my.field 和枚举成员")
			return
		}
		if ident.Name == "it" {
			if param := eventParams[value.Field]; param != nil {
				a.validateStreamMatcherType(value.Pos, "event field", value.Field, param.Type, reported)
			}
			return
		}
		if ident.Name == "my" {
			return
		}
		if typ := a.types[ident.Name]; typ != nil && typ.Kind == TypeEnum {
			return
		}
		a.addError(value.Pos, "generated @stream matchers do not support member access on '%s'; use @native / 生成的 @stream 匹配器不支持访问 '%s' 的成员，请使用 @native", ident.Name, ident.Name)
	case *ast.CallExpr, *ast.ElvisExpr, *ast.BangElvisExpr, *ast.WhenExpr, *ast.LambdaExpr, *ast.ListExpr, *ast.ObjectExpr, *ast.RangeExpr, *ast.TransactionExpr, *ast.YieldExpr, *ast.AsyncExpr, *ast.AwaitExpr:
		a.addError(expr.GetPos(), "expression is not supported by generated @stream matchers; use @native / 生成的 @stream 匹配器不支持该表达式，请使用 @native")
	}
}

func (a *Analyzer) validateStreamMatcherType(pos token.Position, source, name string, ref *ast.TypeRef, reported map[string]bool) {
	key := source + ":" + name
	if reported[key] {
		return
	}
	supported := ref != nil && !ref.IsList && !ref.Nullable
	if supported {
		switch ref.Name {
		case "Int", "Float", "String", "Boolean", "Duration", "UUID":
			return
		default:
			typ := a.types[ref.Name]
			if typ != nil && typ.Kind == TypeEnum {
				return
			}
		}
	}
	reported[key] = true
	typeName := formatTypeRef(ref)
	a.addError(pos, "%s '%s' of type '%s' cannot be used in a generated @stream matcher; use @native / %s '%s' 的类型 '%s' 不能用于生成的 @stream 匹配器，请使用 @native",
		source, name, typeName, source, name, typeName)
}

// validateBareAPI checks that APIs without body or @native have a valid inferred name.
// Validates: action prefix + known model + valid field segments after By.
func (a *Analyzer) validateBareAPI(api *ast.ApiDecl) {
	if api.Body != nil || len(api.Params) > 0 || api.ReturnType != nil {
		return
	}
	for _, d := range api.Directives {
		if d.Name == "native" {
			return
		}
	}

	name := api.Name
	rest, ok := extractActionPrefix(name)
	if !ok {
		a.addError(api.Pos, "inferred API '%s' must start with get/list/count/exists/delete + ModelName", name)
		return
	}

	rest = stripTopFirstPrefix(rest)

	modelName, modelType := a.findModelInName(rest)
	if modelName == "" {
		a.addError(api.Pos, "inferred API '%s' does not reference a known model", name)
		return
	}

	a.validateAfterModel(api.Pos, name, rest[len(modelName):], modelType)
}

// extractActionPrefix extracts the rest after a valid action prefix.
func extractActionPrefix(name string) (string, bool) {
	for _, p := range []string{"list", "get", "count", "exists", "delete"} {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return name[len(p):], true
		}
	}
	return "", false
}

// stripTopFirstPrefix strips Top10/First5 prefixes from list actions.
func stripTopFirstPrefix(rest string) string {
	if strings.HasPrefix(rest, "Top") || strings.HasPrefix(rest, "First") {
		i := 3
		if strings.HasPrefix(rest, "First") {
			i = 5
		}
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		return rest[i:]
	}
	return rest
}

// findModelInName finds the longest matching model name at the start of rest.
func (a *Analyzer) findModelInName(rest string) (string, *ResolvedType) {
	var bestName string
	var bestType *ResolvedType
	for mn, mt := range a.types {
		if mt.Kind != TypeModel {
			continue
		}
		plural := mn + "s"
		if strings.HasPrefix(rest, plural) && len(plural) > len(bestName) {
			bestName = plural
			bestType = mt
		} else if strings.HasPrefix(rest, mn) && len(mn) > len(bestName) {
			bestName = mn
			bestType = mt
		}
	}
	return bestName, bestType
}

// validateAfterModel validates the part after the model name (By/OrderBy + fields).
func (a *Analyzer) validateAfterModel(pos token.Position, apiName, afterModel string, modelType *ResolvedType) {
	if afterModel == "" {
		return // "listUsers" — valid
	}
	if strings.HasPrefix(afterModel, "OrderBy") {
		return // "listUsersOrderByNameDesc" — valid
	}
	if !strings.HasPrefix(afterModel, "By") {
		a.addError(pos, "inferred API '%s' — expected 'By' after model name", apiName)
		return
	}

	afterBy := afterModel[2:]

	// Strip trailing OrderBy... clause before field validation
	if idx := strings.Index(afterBy, "OrderBy"); idx > 0 {
		afterBy = afterBy[:idx]
	}

	if afterBy == "" {
		a.addError(pos, "inferred API '%s' is incomplete — missing field name after By", apiName)
		return
	}
	if strings.HasSuffix(afterBy, "And") || strings.HasSuffix(afterBy, "Or") {
		suffix := afterBy[len(afterBy)-2:]
		if strings.HasSuffix(afterBy, "And") {
			suffix = "And"
		}
		a.addError(pos, "inferred API '%s' is incomplete — missing field name after %s", apiName, suffix)
		return
	}

	if modelType.Fields != nil {
		a.validateFieldSegments(pos, apiName, afterBy, modelType)
	}
}

// validateFieldSegments checks that each field reference in a By-clause exists in the model.
func (a *Analyzer) validateFieldSegments(pos token.Position, apiName, afterBy string, model *ResolvedType) {
	// Known operator suffixes to strip before field matching
	opSuffixes := []string{
		"GreaterThanEqual", "LessThanEqual", "GreaterThan", "LessThan",
		"NotContaining", "StartingWith", "EndingWith", "Containing",
		"IsNotNull", "IsNull", "NotNull", "Between", "NotIn", "In",
		"NotLike", "Like", "IgnoreCase", "Not", "True", "False",
		"After", "Before", "OrderBy",
	}

	// Split by And/Or
	segments := splitByAndOr(afterBy)

	for _, seg := range segments {
		// Strip operator suffix
		field := seg
		for _, op := range opSuffixes {
			if strings.HasSuffix(field, op) {
				field = field[:len(field)-len(op)]
				break
			}
		}

		// Strip OrderBy tail (OrderByNameDesc → before OrderBy)
		if idx := strings.Index(field, "OrderBy"); idx >= 0 {
			field = field[:idx]
		}

		if field == "" {
			continue // operator-only segment like "True"/"IsNull"
		}

		// Lowercase first char to match field names
		fieldName := strings.ToLower(field[:1]) + field[1:]
		// Check explicit fields + implicit timestamp fields
		if _, ok := model.Fields[fieldName]; !ok {
			if fieldName != "createdAt" && fieldName != "updatedAt" && fieldName != "deletedAt" {
				a.addError(pos, "inferred API '%s' — unknown field '%s'", apiName, fieldName)
			}
		}
	}
}

// splitByAndOr splits "EmailAndNameOrAge" into ["Email", "Name", "Age"].
// splitByAndOr splits "EmailAndNameOrAge" into ["Email", "Name", "Age"].
// Only splits at PascalCase boundaries: the char before "And"/"Or" must be lowercase
// and the char after must be uppercase, to avoid splitting field names like "OrderId".
func splitByAndOr(s string) []string {
	var parts []string
	for len(s) > 0 {
		idx, sepLen := findAndOrBoundary(s)
		if idx < 0 {
			parts = append(parts, s)
			break
		}
		if idx > 0 {
			parts = append(parts, s[:idx])
		}
		s = s[idx+sepLen:]
	}
	return parts
}

// findAndOrBoundary finds the first "And"/"Or" at a PascalCase word boundary.
func findAndOrBoundary(s string) (int, int) {
	for i := 0; i < len(s); i++ {
		// Check "And" at position i
		if i+3 <= len(s) && s[i:i+3] == "And" {
			if isPascalBoundary(s, i, 3) {
				return i, 3
			}
		}
		// Check "Or" at position i
		if i+2 <= len(s) && s[i:i+2] == "Or" {
			if isPascalBoundary(s, i, 2) {
				return i, 2
			}
		}
	}
	return -1, 0
}

// isPascalBoundary checks if the separator at position idx is at a PascalCase word boundary.
func isPascalBoundary(s string, idx, sepLen int) bool {
	// Must not be at the start (need a preceding field)
	if idx == 0 {
		return false
	}
	// Char before must be lowercase (end of previous field name)
	prev := s[idx-1]
	if prev < 'a' || prev > 'z' {
		return false
	}
	// Char after separator must be uppercase (start of next field) or end of string
	after := idx + sepLen
	if after >= len(s) {
		return true // trailing And/Or
	}
	return s[after] >= 'A' && s[after] <= 'Z'
}

// validateNativeReturnType checks that @native APIs declare a return type.
func (a *Analyzer) validateNativeReturnType(api *ast.ApiDecl) {
	for _, d := range api.Directives {
		if d.Name == "native" && api.ReturnType == nil {
			a.addError(api.Pos, "@native API must declare a return type")
			return
		}
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

func (a *Analyzer) validateFederationExtendFields() {
	for _, file := range a.files {
		currentModule := a.fileModule(file.Name)
		for _, ext := range file.Extends {
			target := a.types[ext.Name]
			if target == nil || currentModule == "" || a.typeOwners.ownerOf(ext.Name) == currentModule {
				continue
			}
			for _, field := range ext.Fields {
				a.validateFederationExtendField(currentModule, ext.Name, target, field)
			}
		}
	}
}

func (a *Analyzer) validateFederationExtendField(currentModule, modelName string, target *ResolvedType, field *ast.FieldDecl) {
	fieldType := a.resolveTypeRef(field.Type, field.Pos)
	if fieldType == nil {
		return
	}
	if ownerField := target.LookupField(field.Name); ownerField != nil {
		if !sameResolvedType(ownerField.Type, fieldType) {
			a.addError(field.Pos,
				"extend projection '%s.%s' must match owner type '%s', got '%s' / extend 投影 '%s.%s' 必须匹配所属模块类型 '%s'，实际为 '%s'",
				modelName, field.Name, formatResolvedType(ownerField.Type), formatResolvedType(fieldType),
				modelName, field.Name, formatResolvedType(ownerField.Type), formatResolvedType(fieldType))
		}
		return
	}
	if fieldType.Kind != TypeModel {
		a.addError(field.Pos,
			"scalar field '%s.%s' has no federation resolver; extend fields must reference a model / 标量字段 '%s.%s' 没有 Federation resolver，新增的 extend 字段必须引用模型",
			modelName, field.Name, modelName, field.Name)
		return
	}
	if owner := a.typeOwners.ownerOf(fieldType.Name); owner != currentModule {
		a.addError(field.Pos,
			"extend field '%s.%s' must resolve a model owned by module '%s' / extend 字段 '%s.%s' 必须解析当前模块 '%s' 所属的模型",
			modelName, field.Name, currentModule, modelName, field.Name, currentModule)
		return
	}
	a.validateFederationForeignKey(modelName, target, fieldType, field)
}

func (a *Analyzer) validateFederationForeignKey(modelName string, owner, related *ResolvedType, field *ast.FieldDecl) {
	primaryKey := resolvedPrimaryKeyField(owner)
	if primaryKey == nil || primaryKey.Type == nil {
		a.addError(field.Pos,
			"federation model '%s' requires a primary key / Federation 模型 '%s' 必须声明主键",
			modelName, modelName)
		return
	}
	foreignKey, localKey := federationRelationKeys(modelName, primaryKey.Name, field)
	if localKey != "" && localKey != primaryKey.Name {
		a.addError(field.Pos,
			"federation relation '%s.%s' must resolve by primary key '%s', got '%s' / Federation 关系 '%s.%s' 必须按主键 '%s' 解析，实际为 '%s'",
			modelName, field.Name, primaryKey.Name, localKey, modelName, field.Name, primaryKey.Name, localKey)
		return
	}
	foreignField := related.LookupField(foreignKey)
	if foreignField == nil || foreignField.Type == nil {
		a.addError(field.Pos,
			"federation relation '%s.%s' requires foreign-key field '%s' on model '%s' / Federation 关系 '%s.%s' 要求模型 '%s' 声明外键字段 '%s'",
			modelName, field.Name, foreignKey, related.Name, modelName, field.Name, related.Name, foreignKey)
		return
	}
	if foreignField.Type.IsList || foreignField.Type.Kind != primaryKey.Type.Kind || foreignField.Type.Name != primaryKey.Type.Name {
		a.addError(field.Pos,
			"foreign-key field '%s.%s' must match primary key '%s.%s' type '%s', got '%s' / 外键字段 '%s.%s' 必须匹配主键 '%s.%s' 的类型 '%s'，实际为 '%s'",
			related.Name, foreignKey, modelName, primaryKey.Name, formatResolvedType(primaryKey.Type), formatResolvedType(foreignField.Type),
			related.Name, foreignKey, modelName, primaryKey.Name, formatResolvedType(primaryKey.Type), formatResolvedType(foreignField.Type))
	}
}

func federationRelationKeys(modelName, primaryKey string, field *ast.FieldDecl) (string, string) {
	for _, directive := range field.Directives {
		if directive.Name != "by" {
			continue
		}
		foreignKey := directiveIdentifierArg(directive, 0)
		localKey := directiveIdentifierArg(directive, 1)
		if foreignKey != "" {
			return foreignKey, localKey
		}
	}
	return lowerFirstIdentifier(modelName) + upperFirstIdentifier(primaryKey), primaryKey
}

func directiveIdentifierArg(directive *ast.Directive, index int) string {
	if index >= len(directive.Args) {
		return ""
	}
	identifier, _ := directive.Args[index].Value.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func lowerFirstIdentifier(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToLower(r)) + value[size:]
}

func upperFirstIdentifier(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToUpper(r)) + value[size:]
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
	a.checkTransformDirective(f, fi.Type)
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
			// @stream(EventName): inject `it` with event's field types
			a.injectStreamIt(api, scope, file)
			a.checkBlock(api.Body, scope)
			a.validateStreamMatcherResult(api)
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
			// Build event type from event declaration
			var eventType *ResolvedType
			for _, f := range a.files {
				for _, ev := range f.Events {
					if ev.Name == on.EventName {
						evType := &ResolvedType{Kind: TypeModel, Name: ev.Name, Fields: make(map[string]*FieldInfo)}
						for _, p := range ev.Params {
							pType := a.resolveTypeRef(p.Type, on.Pos)
							evType.Fields[p.Name] = &FieldInfo{Name: p.Name, Type: pType}
						}
						eventType = evType
						break
					}
				}
				if eventType != nil {
					break
				}
			}
			// add lambda-style params to scope with event type
			if len(on.Params) > 0 {
				for _, p := range on.Params {
					scope.Define(&Symbol{
						Name: p,
						Kind: SymParam,
						Type: eventType,
					})
				}
			} else if eventType != nil {
				// No named params — inject `it` with event type
				scope.Define(&Symbol{
					Name: "it",
					Kind: SymVariable,
					Type: eventType,
					Used: true,
				})
			}
			a.checkBlock(on.Body, scope)
		}
	}
}

func (a *Analyzer) validateStreamMatcherResult(api *ast.ApiDecl) {
	stream, _ := findAPIDirective(api.Directives, "stream")
	native, _ := findAPIDirective(api.Directives, "native")
	if stream == nil || native != nil || api.Body == nil || len(api.Body.Stmts) != 1 {
		return
	}
	stmt, ok := api.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || stmt.Expr == nil {
		return
	}
	resolved := a.resolvedExprType(stmt.Expr)
	if resolved == nil || resolved.Kind != TypeBool || resolved.IsList || resolved.Nullable {
		actual := "unknown"
		if resolved != nil {
			actual = formatResolvedType(resolved)
		}
		a.addError(stmt.GetPos(), "@stream matcher expression must be Boolean, got '%s' / @stream 匹配器表达式必须是 Boolean，实际为 '%s'", actual, actual)
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
	if s.Type != nil {
		declaredType := a.resolveTypeRef(s.Type, s.Pos)
		if exprType != nil && !isEmptyListExpr(s.Value) && !isTypeAssignable(declaredType, exprType) {
			a.addError(s.Pos, "variable '%s' expects '%s', got '%s' / 变量 '%s' 需要 '%s'，得到 '%s'",
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType),
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType))
		}
		exprType = declaredType
		setExprResolvedType(s.Value, declaredType)
	}
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

func (a *Analyzer) checkForStmt(s *ast.ForStmt, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	if s.Collection != nil {
		collType := a.checkExpr(s.Collection, childScope)
		if s.VarName != "" {
			itemType := collectionElementType(collType)
			childScope.Define(&Symbol{
				Name: s.VarName,
				Kind: SymVariable,
				Type: itemType,
				Pos:  s.Pos,
			})
		}
	}
	a.checkBlock(s.Body, childScope)
	return a.inferForExprType(s.Body)
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

	var resolved *ResolvedType
	switch e := expr.(type) {
	case *ast.Literal:
		resolved = a.checkLiteralExpr(e)
	case *ast.Ident:
		resolved = a.checkIdentExpr(e, scope)
	case *ast.MemberExpr:
		resolved = a.checkMemberExpr(e, scope)
	case *ast.CallExpr:
		resolved = a.checkCallExpr(e, scope)
	case *ast.BinaryExpr:
		resolved = a.checkBinaryExpr(e, scope)
	case *ast.UnaryExpr:
		resolved = a.checkUnaryExpr(e, scope)
	case *ast.ElvisExpr:
		resolved = a.checkElvisExpr(e, scope)
	case *ast.BangElvisExpr:
		a.checkExpr(e.Left, scope)
		a.checkExpr(e.Right, scope)
	case *ast.WhenExpr:
		resolved = a.checkWhenExpr(e, scope)
	case *ast.LambdaExpr:
		resolved = a.checkLambdaExpr(e, scope)
	default:
		resolved = a.checkCompositeExpr(expr, scope)
	}

	// Write type tag into AST node for codegen to read
	if resolved != nil {
		setExprResolvedType(expr, resolved)
	}
	return resolved
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
		return a.checkForStmt(e, scope)
	}
	// unreachable: all known ast.Expr types are covered by checkExpr + checkCompositeExpr switches;
	// kept as defensive fallback required by Go's type system.
	return nil
}

func (a *Analyzer) checkListExpr(e *ast.ListExpr, scope *Scope) *ResolvedType {
	if len(e.Items) == 0 {
		return &ResolvedType{Kind: TypeUnknown, IsList: true}
	}

	var elementType *ResolvedType
	for _, item := range e.Items {
		actual := a.checkExpr(item, scope)
		if actual == nil {
			continue
		}
		if actual.Kind == TypeUnknown && actual.Name == "null" {
			a.addError(item.GetPos(), "list elements cannot be null / 列表元素不能为 null")
			continue
		}
		if elementType == nil {
			elementType = actual
			continue
		}
		if !sameResolvedType(elementType, actual) {
			a.addError(item.GetPos(), "list element type mismatch: expected '%s', got '%s' / 列表元素类型不匹配：需要 '%s'，得到 '%s'",
				formatResolvedType(elementType), formatResolvedType(actual),
				formatResolvedType(elementType), formatResolvedType(actual))
		}
	}
	if elementType == nil {
		return &ResolvedType{Kind: TypeUnknown, IsList: true}
	}
	result := cloneResolvedType(elementType)
	result.IsList = true
	result.Nullable = false
	return result
}

func setExprResolvedType(expr ast.Expr, resolved *ResolvedType) {
	if expr == nil || resolved == nil {
		return
	}
	expr.SetTypeTag(resolved.Name)
	expr.SetNullable(resolved.Nullable)
	expr.SetListType(resolved.IsList)
}

func isEmptyListExpr(expr ast.Expr) bool {
	list, ok := expr.(*ast.ListExpr)
	return ok && len(list.Items) == 0
}

func cloneResolvedType(typ *ResolvedType) *ResolvedType {
	if typ == nil {
		return nil
	}
	result := *typ
	return &result
}

func collectionElementType(collection *ResolvedType) *ResolvedType {
	if collection == nil {
		return nil
	}
	if collection.Kind == TypeGeneric && collection.Name == "Channel" && len(collection.TypeArgs) > 0 {
		return cloneResolvedType(collection.TypeArgs[0])
	}
	if !collection.IsList {
		return nil
	}
	result := cloneResolvedType(collection)
	result.IsList = false
	result.Nullable = false
	return result
}

func sameResolvedType(left, right *ResolvedType) bool {
	if left == nil || right == nil {
		return true
	}
	return left.Name == right.Name && left.Kind == right.Kind && left.IsList == right.IsList && left.Nullable == right.Nullable &&
		typeArgsAssignable(left.TypeArgs, right.TypeArgs) && typeArgsAssignable(right.TypeArgs, left.TypeArgs)
}

func (a *Analyzer) inferForExprType(body *ast.Block) *ResolvedType {
	var yieldType *ResolvedType
	hasYield := false
	ast.WalkExprs(body, func(expr ast.Expr) {
		yieldExpr, ok := expr.(*ast.YieldExpr)
		if !ok {
			return
		}
		hasYield = true
		actual := a.resolvedExprType(yieldExpr.Value)
		if actual == nil || actual.Kind == TypeUnknown && actual.Name == "null" {
			return
		}
		if yieldType == nil {
			yieldType = actual
			return
		}
		if !sameResolvedType(yieldType, actual) {
			a.addError(yieldExpr.GetPos(), "yield type mismatch: expected '%s', got '%s' / yield 类型不匹配：需要 '%s'，得到 '%s'",
				formatResolvedType(yieldType), formatResolvedType(actual),
				formatResolvedType(yieldType), formatResolvedType(actual))
		}
	})
	if hasYield {
		if yieldType == nil {
			return nil
		}
		result := cloneResolvedType(yieldType)
		result.Nullable = true
		result.IsList = false
		return result
	}

	value := blockResultExpr(body)
	result := a.resolvedExprType(value)
	if result == nil {
		return nil
	}
	result.IsList = true
	result.Nullable = false
	return result
}

func blockResultExpr(body *ast.Block) ast.Expr {
	if body == nil || len(body.Stmts) == 0 {
		return nil
	}
	switch stmt := body.Stmts[len(body.Stmts)-1].(type) {
	case *ast.ExprStmt:
		return stmt.Expr
	case *ast.ReturnStmt:
		return stmt.Value
	default:
		return nil
	}
}

func (a *Analyzer) resolvedExprType(expr ast.Expr) *ResolvedType {
	if expr == nil || expr.GetTypeTag() == "" {
		return nil
	}
	base := a.types[expr.GetTypeTag()]
	if base == nil {
		base = &ResolvedType{Kind: TypeUnknown, Name: expr.GetTypeTag()}
	}
	result := cloneResolvedType(base)
	result.Nullable = expr.IsNullable()
	result.IsList = expr.IsListType()
	return result
}

func (a *Analyzer) checkObjectExpr(e *ast.ObjectExpr, scope *Scope) *ResolvedType {
	if e.TypeName == "" {
		for _, f := range e.Fields {
			a.checkExpr(f.Value, scope)
		}
		return nil
	}

	typ, ok := a.types[e.TypeName]
	if !ok {
		for _, f := range e.Fields {
			a.checkExpr(f.Value, scope)
		}
		a.addErrorWithSuggestion(e.Pos, e.TypeName, "unknown type '%s' / 未知类型 '%s'", e.TypeName, e.TypeName)
		return nil
	}
	if a.moduleMap != nil && a.currentFile != nil {
		if mi := a.moduleMap[a.currentFile.Name]; mi != nil {
			a.checkNameVisibility(e.TypeName, mi, e.Pos)
		}
	}
	a.validateObjectExprFields(e, typ, scope)
	return typ.AsNonNull()
}

func (a *Analyzer) validateObjectExprFields(e *ast.ObjectExpr, typ *ResolvedType, scope *Scope) {
	provided := make(map[string]bool, len(e.Fields))
	for _, value := range e.Fields {
		actual := a.checkExpr(value.Value, scope)
		if provided[value.Name] {
			a.addError(value.Value.GetPos(), "duplicate field '%s' in '%s' / '%s' 中字段 '%s' 重复", value.Name, typ.Name, typ.Name, value.Name)
			continue
		}
		provided[value.Name] = true

		field := typ.LookupField(value.Name)
		if field == nil {
			a.addFieldError(value.Value.GetPos(), typ, value.Name)
			continue
		}
		if field.Type != nil && actual != nil && !isTypeAssignable(field.Type, actual) {
			a.addError(value.Value.GetPos(), "field '%s' expects '%s', got '%s' / 字段 '%s' 需要 '%s'，得到 '%s'",
				value.Name, formatResolvedType(field.Type), formatResolvedType(actual),
				value.Name, formatResolvedType(field.Type), formatResolvedType(actual))
		}
	}

	if typ.Kind == TypeCustom {
		a.checkRequiredObjectFields(e, typ, provided)
	}
}

func (a *Analyzer) checkRequiredObjectFields(e *ast.ObjectExpr, typ *ResolvedType, provided map[string]bool) {
	names := make([]string, 0, len(typ.Fields))
	for name := range typ.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := typ.Fields[name]
		if provided[name] || field.Nullable || field.HasDefault || field.Computed || field.IsMethod {
			continue
		}
		a.addError(e.Pos, "missing required field '%s' in '%s' / '%s' 缺少必填字段 '%s'", name, typ.Name, typ.Name, name)
	}
}

func isTypeAssignable(expected, actual *ResolvedType) bool {
	if expected == nil || actual == nil {
		return true
	}
	if actual.Kind == TypeUnknown {
		return actual.Name != "null" || expected.Nullable
	}
	if expected.Kind == TypeUnknown {
		return true
	}
	if actual.Nullable && !expected.Nullable {
		return false
	}
	if expected.IsList != actual.IsList {
		return false
	}
	if expected.Name == actual.Name {
		return typeArgsAssignable(expected.TypeArgs, actual.TypeArgs)
	}
	return actual.inheritsFrom(expected.Name)
}

func typeArgsAssignable(expected, actual []*ResolvedType) bool {
	if len(expected) == 0 || len(actual) == 0 {
		return true
	}
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if !isTypeAssignable(expected[i], actual[i]) {
			return false
		}
	}
	return true
}

func formatResolvedType(typ *ResolvedType) string {
	name := typ.Name
	if typ.IsList {
		name = "[" + name + "]"
	}
	if typ.Nullable {
		name += "?"
	}
	return name
}

func (a *Analyzer) checkRangeExpr(e *ast.RangeExpr, scope *Scope) *ResolvedType {
	start := a.checkExpr(e.Start, scope)
	end := a.checkExpr(e.End, scope)
	intType := a.types["Int"]
	if start != nil && !isTypeAssignable(intType, start) {
		a.addError(e.Start.GetPos(), "range start must be Int, got '%s' / range 起点必须是 Int，得到 '%s'", formatResolvedType(start), formatResolvedType(start))
	}
	if end != nil && !isTypeAssignable(intType, end) {
		a.addError(e.End.GetPos(), "range end must be Int, got '%s' / range 终点必须是 Int，得到 '%s'", formatResolvedType(end), formatResolvedType(end))
	}
	return intType.AsList()
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
		// mark variable as used
		if sym := scope.Lookup(e.Name); sym != nil {
			sym.Used = true
		}
		return resolved
	}
	sym := scope.Lookup(e.Name)
	if sym != nil {
		sym.Used = true
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

	// Duration properties: Int.days, Int.hours, Int.minutes, Int.seconds, Int.milliseconds
	// Only Int, not Float — duration multiplier must be integer
	if objType.Kind == TypeInt {
		switch e.Field {
		case "days", "hours", "minutes", "seconds", "milliseconds":
			return a.types["Duration"]
		}
	}

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

	// Cross-module field visibility: only extend-declared fields are accessible
	a.checkExtendFieldVisibility(e, objType)

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

// checkExtendFieldVisibility checks that field access on cross-module model types
// only accesses fields declared in the extend block.
func (a *Analyzer) checkExtendFieldVisibility(e *ast.MemberExpr, objType *ResolvedType) {
	if a.moduleMap == nil || a.currentFile == nil || objType.Kind != TypeModel {
		return
	}
	ownerMod := a.typeOwners.ownerOf(objType.Name)
	if ownerMod == "" {
		return
	}
	mi := a.moduleMap[a.currentFile.Name]
	if mi == nil || ownerMod == mi.Name || a.isCommonModule(ownerMod) || mi.IsCommon {
		return
	}
	// Cross-module model: check if field is declared in extend
	fields := a.extendFieldVisible[mi.Name]
	if fields == nil || fields[objType.Name] == nil || !fields[objType.Name][e.Field] {
		a.addError(e.Pos,
			"field '%s' not declared in extend %s — add it to your extend block / 字段 '%s' 未在 extend %s 中声明",
			e.Field, objType.Name, e.Field, objType.Name)
	}
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

	// Built-in function return types: now() → DateTime, today() → DateTime
	if ident, ok := e.Func.(*ast.Ident); ok {
		switch ident.Name {
		case "now", "today":
			return a.types["DateTime"]
		}
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

	// Cross-module model restriction: only load() allowed, not find/where/create etc.
	a.checkCrossModuleCRUD(e)

	callScope := a.injectCRUDScope(e, scope)
	isCRUD := isCRUDIdent(e)

	// orderBy(field.desc) — sort expressions are not normal expressions, skip checking
	if isOrderByMethod(e) {
		return a.inferCallReturnType(e)
	}

	argTypes := make([]*ResolvedType, len(e.Args))
	for i, arg := range e.Args {
		// skip order/select/include/distinct — these use query modifier syntax (field.desc)
		if isCRUD && isQueryModifierArg(arg.Name) {
			continue
		}
		argTypes[i] = a.checkExpr(arg.Value, callScope)
		// Named args in CRUD create: also mark variable usage in outer scope
		// (callScope may shadow variables with model fields)
		if isCRUD && arg.Name != "" {
			if ident, ok := arg.Value.(*ast.Ident); ok {
				if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable {
					sym.Used = true
				}
			}
		}
	}
	a.checkLoadArguments(e, argTypes)

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

// inferLoadReturnType infers the return type for Model.load(...).
// No named args → PK load → nullable model (single result)
// Named args → FK/multi-condition load → list of models
func (a *Analyzer) inferLoadReturnType(e *ast.CallExpr, modelType *ResolvedType) *ResolvedType {
	hasNamedArgs := false
	for _, arg := range e.Args {
		if arg.Name != "" {
			hasNamedArgs = true
			break
		}
	}
	if hasNamedArgs {
		return modelType.AsList() // FK load returns list
	}
	return modelType // PK load returns single (nullable)
}

func (a *Analyzer) checkLoadArguments(e *ast.CallExpr, argTypes []*ResolvedType) {
	method, modelName := chainCRUDInfo(e)
	if method != "load" {
		return
	}
	modelType := a.types[modelName]
	if modelType == nil {
		return
	}
	if len(e.Args) == 0 {
		a.addError(e.Pos, "%s.load requires one primary key or named lookup fields / %s.load 需要主键或命名查询字段", modelName, modelName)
		return
	}
	namedCount := 0
	for _, arg := range e.Args {
		if arg.Name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		a.checkPrimaryKeyLoadArguments(e, modelType, argTypes)
		return
	}
	if namedCount != len(e.Args) {
		a.addError(e.Pos, "%s.load cannot mix positional and named arguments / %s.load 不能混用位置参数和命名参数", modelName, modelName)
		return
	}
	a.checkNamedLoadArguments(e, modelType, argTypes)
}

func (a *Analyzer) checkPrimaryKeyLoadArguments(e *ast.CallExpr, modelType *ResolvedType, argTypes []*ResolvedType) {
	if len(e.Args) != 1 {
		a.addError(e.Pos, "%s.load primary-key form expects exactly one argument / %s.load 主键形式只接受一个参数", modelType.Name, modelType.Name)
		return
	}
	idField := resolvedPrimaryKeyField(modelType)
	if idField == nil || idField.Type == nil || argTypes[0] == nil {
		return
	}
	if !isTypeAssignable(idField.Type, argTypes[0]) {
		a.addError(e.Args[0].Value.GetPos(), "load key expects '%s', got '%s' / load 主键需要 '%s'，得到 '%s'",
			formatResolvedType(idField.Type), formatResolvedType(argTypes[0]), formatResolvedType(idField.Type), formatResolvedType(argTypes[0]))
	}
}

func resolvedPrimaryKeyField(modelType *ResolvedType) *FieldInfo {
	if modelType == nil {
		return nil
	}
	for _, field := range modelType.Fields {
		for _, directive := range field.Directives {
			if directive == "id" {
				return field
			}
		}
	}
	return modelType.LookupField("id")
}

func (a *Analyzer) checkNamedLoadArguments(e *ast.CallExpr, modelType *ResolvedType, argTypes []*ResolvedType) {
	seen := make(map[string]bool, len(e.Args))
	for i, arg := range e.Args {
		if seen[arg.Name] {
			a.addError(arg.Value.GetPos(), "duplicate load field '%s' / load 字段 '%s' 重复", arg.Name, arg.Name)
			continue
		}
		seen[arg.Name] = true
		field := modelType.LookupField(arg.Name)
		if field == nil || field.Type == nil {
			a.addError(arg.Value.GetPos(), "model '%s' has no load field '%s' / 模型 '%s' 没有 load 字段 '%s'", modelType.Name, arg.Name, modelType.Name, arg.Name)
			continue
		}
		if !isLoadKeyType(field) {
			a.addError(arg.Value.GetPos(), "load field '%s' must be Int, String, or UUID / load 字段 '%s' 必须是 Int、String 或 UUID", arg.Name, arg.Name)
			continue
		}
		if !a.isLoadFieldVisible(modelType.Name, arg.Name) {
			a.addError(arg.Value.GetPos(), "load field '%s' not declared in extend %s / load 字段 '%s' 未在 extend %s 中声明", arg.Name, modelType.Name, arg.Name, modelType.Name)
			continue
		}
		if argTypes[i] != nil && !isTypeAssignable(field.Type, argTypes[i]) {
			a.addError(arg.Value.GetPos(), "load field '%s' expects '%s', got '%s' / load 字段 '%s' 需要 '%s'，得到 '%s'",
				arg.Name, formatResolvedType(field.Type), formatResolvedType(argTypes[i]), arg.Name, formatResolvedType(field.Type), formatResolvedType(argTypes[i]))
		}
	}
}

func isLoadKeyType(field *FieldInfo) bool {
	if field.Nullable || field.Computed || field.Type.Nullable || field.Type.IsList {
		return false
	}
	switch field.Type.Kind {
	case TypeInt, TypeString, TypeUUID:
		return true
	default:
		return false
	}
}

func (a *Analyzer) isLoadFieldVisible(modelName, fieldName string) bool {
	if a.moduleMap == nil || a.currentFile == nil {
		return true
	}
	ownerModule := a.typeOwners.ownerOf(modelName)
	currentModule := a.moduleMap[a.currentFile.Name]
	if currentModule == nil || ownerModule == "" || ownerModule == currentModule.Name || a.isCommonModule(ownerModule) || currentModule.IsCommon {
		return true
	}
	return a.extendFieldVisible[currentModule.Name] != nil && a.extendFieldVisible[currentModule.Name][modelName] != nil &&
		a.extendFieldVisible[currentModule.Name][modelName][fieldName]
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
	case "load":
		return a.inferLoadReturnType(e, modelType)
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
	// Relation fields (model type or list of model) are not database columns
	if field.Type != nil && (field.Type.Kind == TypeModel || field.Type.IsList) {
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

// checkCrossModuleCRUD restricts cross-module models to load() only.
// e.g., User.find(id) in post module → error; User.load(id) → ok.
func (a *Analyzer) checkCrossModuleCRUD(e *ast.CallExpr) {
	if a.moduleMap == nil || a.currentFile == nil {
		return
	}
	method, modelName := chainCRUDInfo(e)
	if modelName == "" {
		return
	}
	ownerMod := a.typeOwners.ownerOf(modelName)
	if ownerMod == "" {
		return
	}
	mi := a.moduleMap[a.currentFile.Name]
	if mi == nil {
		return
	}
	// Same module or common module — all ops allowed
	if ownerMod == mi.Name || a.isCommonModule(ownerMod) || mi.IsCommon {
		return
	}
	// Cross-module: only load is allowed
	if method != "load" {
		a.addError(e.Pos,
			"cross-module model '%s' can only use 'load', not '%s' — use %s.load(id) / 跨模块 model '%s' 只能用 load，不能用 '%s'",
			modelName, method, modelName, modelName, method)
	}
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
		"save", "load":
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
		"upsert", "raw", "load":
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
	// DateTime +/- Duration → DateTime
	if left.Kind == TypeDateTime && right.Kind == TypeDuration && (op == "+" || op == "-") {
		return a.types["DateTime"]
	}
	// Duration + Duration → Duration
	if left.Kind == TypeDuration && right.Kind == TypeDuration && (op == "+" || op == "-") {
		return a.types["Duration"]
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

// injectStreamIt injects `it` into scope for @stream API bodies.
// `it` has the event's fields so `it.projectId == projectId` works.
func (a *Analyzer) injectStreamIt(api *ast.ApiDecl, scope *Scope, file *ast.File) {
	var eventName string
	for _, d := range api.Directives {
		if d.Name == "stream" && len(d.Args) > 0 {
			if ident, ok := d.Args[0].Value.(*ast.Ident); ok {
				eventName = ident.Name
			}
		}
	}
	if eventName == "" {
		return
	}
	// Find the event declaration across all files (event may be in another file)
	var event *ast.EventDecl
	for _, f := range a.files {
		for _, ev := range f.Events {
			if ev.Name == eventName {
				event = ev
				break
			}
		}
		if event != nil {
			break
		}
	}
	if event == nil {
		return
	}
	// Build a type for `it` with event's params as fields
	itType := &ResolvedType{Kind: TypeModel, Name: eventName, Fields: make(map[string]*FieldInfo)}
	for _, p := range event.Params {
		pType := a.resolveTypeRef(p.Type, api.Pos)
		itType.Fields[p.Name] = &FieldInfo{Name: p.Name, Type: pType}
	}
	scope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
		Type: itType,
		Used: true, // implicit, don't warn unused
	})
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

	// Inject stores fields into Identity type so my.<field> works for all stored fields
	identityType := a.types["Identity"]
	for _, d := range directives {
		if d.Name != "withAuth" {
			continue
		}
		for _, arg := range d.Args {
			if arg.Name != "stores" {
				continue
			}
			list, ok := arg.Value.(*ast.ListExpr)
			if !ok {
				break
			}
			for _, elem := range list.Items {
				ident, ok := elem.(*ast.Ident)
				if !ok {
					continue
				}
				// skip fields already defined with type (id, load)
				// but override nil-typed defaults (role) with actual type
				if existing, exists := identityType.Fields[ident.Name]; exists && existing.Type != nil {
					continue
				}
				// resolve type from the model's fields, skip methods
				fieldType := typ.Fields[ident.Name]
				if fieldType != nil && !fieldType.IsMethod {
					identityType.Fields[ident.Name] = &FieldInfo{
						Name: ident.Name,
						Type: fieldType.Type,
					}
				}
			}
		}
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
