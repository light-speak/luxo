package codegen

import (
	gotoken "go/token"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// Bindings distinguish source names from implementation-only Go identifiers.
type localBindings map[string]string

func (names localBindings) name(source string) string {
	if name, ok := names[source]; ok {
		return name
	}
	return source
}

// Local functions share App's member namespace; their source names remain stable.
func (g *GeneratorContext) planFunctionBindings(result *semantic.Result, enums map[string]bool) localBindings {
	var candidates []string
	for _, file := range result.Files {
		for _, fn := range file.Functions {
			if isCompiledLocalFunction(fn) && (fn.Name == "_" || gotoken.Lookup(fn.Name).IsKeyword() || g.isModelReference(fn.Name) || g.conflictsWithAppMember(fn.Name, result, enums)) {
				candidates = append(candidates, fn.Name)
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	var used map[string]bool
	for _, file := range result.Files {
		for _, fn := range file.Functions {
			if strings.HasPrefix(fn.Name, "_luxoFn") {
				if used == nil {
					used = make(map[string]bool)
				}
				used[fn.Name] = true
			}
		}
	}
	names := make(localBindings, len(candidates))
	index := 0
	for _, source := range candidates {
		for {
			name := "_luxoFn" + strconv.Itoa(index)
			index++
			if !used[name] && !g.isModelReference(name) {
				names[source] = name
				break
			}
		}
	}
	return names
}

func (g *GeneratorContext) functionGoName(source string) string {
	if g == nil {
		return source
	}
	return g.functionNames.name(source)
}

func parameterBindings(params []*ast.ParamDecl, body *ast.Block, reserved ...string) localBindings {
	return planParameterBindings(params, body, bindingTypes{}, reserved)
}

// Borrow the compilation's type indexes; ordinary bindings require no new catalog.
type bindingTypes struct {
	models  map[string]*ast.ModelDecl
	types   map[string]*ast.TypeDecl
	enums   map[string]bool
	symbols enumSymbols
	plan    *bindingSymbolPlan
}

func (types bindingTypes) contains(name string) bool {
	if types.plan != nil {
		return types.plan.contains(name)
	}
	_, model := types.models[name]
	_, object := types.types[name]
	if model || object {
		return true
	}
	if types.symbols != nil {
		if _, exists := types.symbols[name]; exists {
			return true
		}
	} else if types.enums[name] {
		return true
	}
	owner, helper := strings.CutSuffix(name, "Where")
	if !helper {
		return false
	}
	declaration := types.models[owner]
	return declaration != nil
}

func (g *GeneratorContext) parameterBindings(params []*ast.ParamDecl, body *ast.Block, models map[string]*ast.ModelDecl, enums map[string]bool, reserved ...string) localBindings {

	if g.bindingPlan != nil {
		return planParameterBindings(params, body, bindingTypes{plan: g.bindingPlan}, reserved)
	}
	declarations := g.modelDeclarations
	if declarations == nil {
		declarations = models
	}
	return planParameterBindings(params, body, bindingTypes{models: declarations, types: g.typeDeclarations, enums: enums, symbols: g.enumSymbols}, reserved)
}

func planParameterBindings(params []*ast.ParamDecl, body *ast.Block, types bindingTypes, reserved []string) localBindings {
	var candidates []string
	hasTypes := len(types.models) != 0 || len(types.types) != 0 || len(types.enums) != 0 || len(types.symbols) != 0 || types.plan != nil
	consider := func(name string) {
		if runtimeLocalName(name) || hasTypes && types.contains(name) || slices.Contains(reserved, name) {
			candidates = append(candidates, name)
		}
	}
	for _, param := range params {
		consider(param.Name)
	}
	walkSourceNames(body, consider, false)
	if len(candidates) == 0 {
		return nil
	}
	used := make(map[string]bool)
	for _, param := range params {
		used[param.Name] = true
	}
	walkSourceNames(body, func(name string) { used[name] = true }, true)
	names := make(localBindings)
	index := 0
	for _, source := range candidates {
		if _, exists := names[source]; exists {
			continue
		}
		for {
			name := "_luxoParam" + strconv.Itoa(index)
			index++
			if !used[name] && (!hasTypes || !types.contains(name)) {
				names[source], used[name] = name, true
				break
			}
		}
	}
	return names
}

func walkSourceNames(body *ast.Block, visit func(string), references bool) {
	ast.Walk(body, ast.Visitor{
		Stmt: func(stmt ast.Stmt) {
			switch value := stmt.(type) {
			case *ast.ValStmt:
				visit(value.Name)
				for _, name := range value.Names {
					visit(name)
				}
			case *ast.ForStmt:
				visit(value.VarName)
			}
		},
		Expr: func(expr ast.Expr) {
			switch value := expr.(type) {
			case *ast.Ident:
				if references {
					visit(value.Name)
				}
			case *ast.LambdaExpr:
				for _, name := range value.Params {
					visit(name)
				}
			case *ast.ForStmt:
				visit(value.VarName)
			}
		},
	})
}

func runtimeLocalName(name string) bool {
	if strings.HasPrefix(name, "_") || gotoken.Lookup(name).IsKeyword() {
		return true
	}
	if generatedPackageBit(name) != 0 {
		return true
	}
	switch name {
	case "ctx", "req", "err", "app", "result", "snapshot", "identity", "stream", "router", "resolver", "params", "g", "gctx":
		return true
	case "api", "sync":
		return true
	// Source names must not hide Go's types, constants, or generated built-in calls.
	case "any", "bool", "byte", "comparable", "complex64", "complex128", "error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make", "max", "min",
		"new", "panic", "print", "println", "real", "recover":
		return true
	case "false", "iota", "nil", "true":
		return true
	}
	return false
}

func hasDiscardedBindings(statement *ast.ValStmt) bool {
	return statement.Name == "_" || slices.Contains(statement.Names, "_")
}

// Lower discards through the same typed/error-aware declaration path. Only the
// private copy receives fresh Go names; source bindings remain unchanged.
func (c *compiler) compileDiscardedBindings(statement *ast.ValStmt) {
	copy := *statement
	var discarded []string
	rename := func(name string) string {
		if name != "_" {
			return name
		}
		fresh := c.discardedBindingName()
		discarded = append(discarded, fresh)
		return fresh
	}
	copy.Name = rename(copy.Name)
	copy.Names = append([]string(nil), statement.Names...)
	for index, name := range copy.Names {
		copy.Names[index] = rename(name)
	}
	c.compileVal(&copy)
	c.retainLocalInitialization(&ast.ValStmt{Names: discarded})
}

func (c *compiler) discardedBindingName() string {
	for {
		c.resultTmp++
		name := "_luxoDiscard" + strconv.Itoa(c.resultTmp)
		if _, mapped := c.bindings[name]; mapped {
			continue
		}
		if _, declared := c.vars[name]; !declared {
			return name
		}
	}
}

// A compilation borrows the existing source directories and analysis plans.
// Only non-invertible names (load keys, snapshot types, imports) need exact keys.
type bindingSymbolPlan struct {
	source       *semantic.Result
	models       map[string]*ast.ModelDecl
	types        map[string]*ast.TypeDecl
	enums        enumSymbols
	declarations map[string]bindingDeclaration
	exact        map[string]bool
	relations    map[string]bool
	computed     map[string]bool
	selectors    bool
	loaders      *dataLoaderInputs
	streams      []streamInfo
	owners       *streamInputOwnerPlan
}

type bindingDeclaration uint8

const (
	bindingEvent bindingDeclaration = 1 << iota
	bindingError
	bindingErrorData
)

func bindingOwner(name, prefix, suffix string) (string, bool) {
	owner, found := strings.CutPrefix(name, prefix)
	if !found {
		return "", false
	}
	return strings.CutSuffix(owner, suffix)
}

func (plan *bindingSymbolPlan) model(name string) bool {
	_, found := plan.models[name]
	return found
}

func (plan *bindingSymbolPlan) object(name string) bool {
	_, found := plan.types[name]
	return found || plan.model(name)
}

func (plan *bindingSymbolPlan) contains(name string) bool {
	if name == "" {
		return false
	}
	if _, found := plan.enums[name]; found {
		return true
	}
	if plan.object(name) || plan.exact[name] {
		return true
	}
	if owner, found := strings.CutSuffix(name, "Where"); found && plan.models[owner] != nil {
		return true
	}
	if plan.declarationSymbol(name) {
		return true
	}
	switch name[0] {
	case 'W':
		owner, found := bindingOwner(name, "WriteColumnar", "")
		return found && plan.object(owner)
	case 'R':
		owner, found := bindingOwner(name, "ReadJSONInput", "List")
		return found && plan.object(owner)
	case 'r':
		return plan.resolverSymbol(name)
	case 's':
		owner, found := bindingOwner(name, "select", "SQLColumns")
		return found && plan.selectors && plan.model(owner)
	case 'h':
		owner, found := bindingOwner(name, "has", "SelectedRelations")
		return found && plan.relations[owner]
	case 'o':
		owner, found := bindingOwner(name, "ownStreamInput", "")
		return found && plan.owners != nil && plan.owners.seen[owner]
	}
	return false
}

func (plan *bindingSymbolPlan) resolverSymbol(name string) bool {
	if owner, found := bindingOwner(name, "reference", "List"); found && plan.model(owner) {
		return true
	}
	if owner, found := bindingOwner(name, "resolve", "ListRelations"); found && plan.relations[owner] {
		return true
	}
	if owner, found := bindingOwner(name, "resolve", "Relations"); found && plan.relations[owner] {
		return true
	}
	owner, found := bindingOwner(name, "resolve", "Computed")
	return found && plan.computed[owner]
}

func (plan *bindingSymbolPlan) declarationSymbol(name string) bool {
	if len(plan.declarations) == 0 {
		return false
	}
	for _, shape := range [...]struct {
		prefix, suffix string
		kind           bindingDeclaration
	}{
		{"Emit", "", bindingEvent}, {"", "Event", bindingEvent},
		{"New", "", bindingError}, {"", "ErrorData", bindingErrorData},
	} {
		if owner, found := bindingOwner(name, shape.prefix, shape.suffix); found && plan.declarations[owner]&shape.kind != 0 {
			return true
		}
	}
	return false
}

func (plan *bindingSymbolPlan) addExact(name string) {
	if plan.exact == nil {
		plan.exact = make(map[string]bool)
	}
	plan.exact[name] = true
}

func (g *GeneratorContext) bindingContext(result *semantic.Result, enums map[string]bool) *GeneratorContext {
	if g.bindingSource == result {
		return g
	}
	bound := g.withModelReferences(result)
	bound.enumSymbols = nil
	if enums == nil {
		enums = CollectEnumsFromResult(result)
	}
	bound.prepareBindingSymbols(result, enums, nil)
	return bound
}

func (g *GeneratorContext) needsBindingSymbols(result *semantic.Result) bool {
	if len(g.modelDeclarations) != 0 || len(g.typeDeclarations) != 0 || len(g.enumSymbols) != 0 || g.events != nil {
		return true
	}
	for _, file := range result.Files {
		if len(file.Events) != 0 || len(file.Errors) != 0 || len(file.Enums) != 0 || len(file.Sealeds) != 0 {
			return true
		}
		for _, api := range file.APIs {
			if hasDirective(api.Directives, "stream") {
				return true
			}
		}
	}
	return false
}

func (g *GeneratorContext) prepareBindingSymbols(result *semantic.Result, enums map[string]bool, softModels map[string]bool) {
	g.bindingSource, g.bindingPlan = result, nil
	if !g.needsBindingSymbols(result) {
		return
	}
	plan := &bindingSymbolPlan{source: result, models: g.modelDeclarations, types: g.typeDeclarations, enums: g.enumSymbols}
	if len(plan.models) != 0 {
		loaders := g.collectDataLoaderInputs(result, enums, softModels)
		plan.loaders = &loaders
	}
	plan.streams = g.collectStreams(result)
	owners := collectStreamInputOwners(result, plan.streams)
	if len(owners.names) != 0 {
		plan.owners = &owners
	}
	g.bindingPlan = plan
	plan.addDeclarations(result)
	plan.addLoadAndStreamSymbols()
}

func (plan *bindingSymbolPlan) addDeclarations(result *semantic.Result) {
	for _, file := range result.Files {
		for _, declaration := range file.Sealeds {
			plan.addExact(declaration.Name)
			plan.addExact("ReadJSONInput" + declaration.Name + "List")
			plan.addExact("WriteColumnar" + declaration.Name)
			plan.addExact("ReadColumnar" + declaration.Name)
		}
		if plan.enums == nil && len(file.Enums) != 0 {
			plan.enums = make(enumSymbols)
		}
		for _, declaration := range file.Enums {
			if _, present := plan.enums[declaration.Name]; present {
				continue
			}
			plan.enums[declaration.Name] = declaration.Name
			for _, member := range declaration.Values {
				plan.enums[declaration.Name+member] = declaration.Name
			}
		}
		if len(file.Events)+len(file.Errors) != 0 && plan.declarations == nil {
			plan.declarations = make(map[string]bindingDeclaration)
		}
		for _, event := range file.Events {
			plan.declarations[event.Name] |= bindingEvent
		}
		for _, declaration := range file.Errors {
			plan.declarations[declaration.Name] |= bindingError
			if len(declaration.Fields) != 0 {
				plan.declarations[declaration.Name] |= bindingErrorData
			}
		}
	}
}

func (plan *bindingSymbolPlan) addLoadAndStreamSymbols() {
	if plan.loaders != nil {
		for _, call := range plan.loaders.loadCalls {
			if len(call.argNames) > 1 {
				plan.addExact(loaderNameFromLoadCall(call) + "Key")
			}
		}
	}
	for _, stream := range plan.streams {
		plan.addExact(str.Capitalize(stream.apiName) + "StreamParams")
	}
}

func (g *GeneratorContext) addHandlerBindingSymbols(models []*ast.ModelDecl) {
	if g.bindingPlan == nil {
		return
	}
	g.bindingPlan.selectors = len(models) != 0
	if g.bindingPlan.loaders == nil || len(g.bindingPlan.loaders.relations) == 0 {
		return
	}
	g.bindingPlan.relations = make(map[string]bool, len(g.bindingPlan.loaders.relations))
	for _, model := range g.bindingPlan.loaders.relations {
		g.bindingPlan.relations[model.modelName] = true
	}
}

func (g *GeneratorContext) addComputedBindingSymbol(name string) {
	if g.bindingPlan == nil {
		return
	}
	if g.bindingPlan.computed == nil {
		g.bindingPlan.computed = make(map[string]bool)
	}
	g.bindingPlan.computed[name] = true
}

func (g *GeneratorContext) addImportBindingSymbols(imports map[string]string) {
	if g.bindingPlan == nil {
		return
	}
	for _, alias := range imports {
		g.bindingPlan.addExact(alias)
	}
}
