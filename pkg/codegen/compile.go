package codegen

import (
	"fmt"
	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/internal/builtinerror"
	"github.com/light-speak/luxo/internal/inferred"
	"sort"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
)

// compileAPIBody generates Go handler code from a .luxo API body.
// The generated function reads params from req, executes the body, and writes the result.
// compileDefaultValue converts an AST default value expression to Go code.
func compileDefaultValue(expr ast.Expr, goType string, enums map[string]bool) string {
	switch e := expr.(type) {
	case *ast.Literal:
		switch e.Kind {
		case token.Int, token.Float:
			return compileNumericLiteral(e)
		case token.String:
			return fmt.Sprintf("%q", e.Value)
		case token.True:
			return "true"
		case token.False:
			return "false"
		case token.Null:
			return "nil"
		case token.Duration:
			if value, ok := compileDurationLiteral(e.Value); ok {
				return value
			}
		}
	case *ast.UnaryExpr:
		if e.Op == "+" || e.Op == "-" {
			if literal, ok := e.Value.(*ast.Literal); ok {
				value := compileDefaultValue(literal, goType, enums)
				if literal.Kind == token.Duration {
					value = "(" + value + ")"
				}
				return compileUnaryOperation(e, value)
			}
		}
	case *ast.Ident:
		if e.Name == "true" {
			return "true"
		}
		if e.Name == "false" {
			return "false"
		}
		if enums[goType] {
			return goType + e.Name
		}
	case *ast.MemberExpr:
		// Enum.VALUE → EnumVALUE
		if ident, ok := e.Object.(*ast.Ident); ok {
			if enums[ident.Name] {
				return ident.Name + e.Field
			}
		}
	}
	// Fallback: zero value
	switch goType {
	case "int64":
		return "0"
	case "float64":
		return "0"
	case "string":
		return `""`
	case "bool":
		return "false"
	default:
		return goType + "{}"
	}
}

func compileDurationLiteral(value string) (string, bool) {
	units := []struct {
		suffix string
		goUnit string
	}{
		{suffix: "ms", goUnit: "time.Millisecond"},
		{suffix: "d", goUnit: "24 * time.Hour"},
		{suffix: "h", goUnit: "time.Hour"},
		{suffix: "m", goUnit: "time.Minute"},
		{suffix: "s", goUnit: "time.Second"},
	}
	for _, unit := range units {
		if !strings.HasSuffix(value, unit.suffix) {
			continue
		}
		amount := decimalIntegerLiteral(strings.TrimSuffix(value, unit.suffix))
		if amount == "1" {
			return unit.goUnit, true
		}
		return amount + " * " + unit.goUnit, true
	}
	return "", false
}

// Luxo integer tokens are decimal; Go interprets a leading zero as octal.
func decimalIntegerLiteral(value string) string {
	if len(value) < 2 || value[0] != '0' {
		return value
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	return value
}

func compileNumericLiteral(literal *ast.Literal) string {
	if literal.Kind == token.Int {
		return decimalIntegerLiteral(literal.Value)
	}
	if literal.Kind == token.Float {
		return "float64(" + literal.Value + ")"
	}
	return literal.Value
}

func compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().compileAPIBody(b, api, models, enums, nil, nil, nil)
}

func (g *GeneratorContext) compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool) {
	g.compileNamedAPIBody(b, api, models, enums, nativeFunctions, functions, selectionFunctions, "handle"+str.Capitalize(api.Name))
}

func (g *GeneratorContext) compileNamedAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool, handler string) {
	fmt.Fprintf(b, "func %s(app *App) api.HandlerFunc {\n", handler)
	loadSelections := analyzeLoadProjections(api.Body, models, api.ReturnType)
	writeLoadProjectionBindings(b, api.Body, loadSelections)
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// @auth — require authenticated identity
	if d := findDirective(api.Directives, "auth"); d != nil {
		writeAuthCheck(b, "\t\t", d)
	}

	// Parse params (with default value support)
	bindings := g.parameterBindings(api.Params, api.Body, models, enums)
	for index, p := range api.Params {
		writeCompiledParamBinding(b, p, bindings.name(p.Name), enums, models, index)
		// Validation is required even when the body does not reference a parameter.
		fmt.Fprintf(b, "\t\t_ = %s\n", bindings.name(p.Name))
	}
	cached := writeCachedBodyStart(b, api.Directives)
	scopeFunctions := g.scopeFunctions
	if scopeFunctions == nil && len(functions) > 0 {
		scopeFunctions = collectScopeAwareFunctions(functions)
	}
	if len(loadSelections) > 0 || bodyCallsScopedFunction(api.Body, scopeFunctions) {
		writeRequestScope(b, "\t\t", "ctx")
	}

	// Compile body statements
	c := &compiler{
		generator:          g,
		b:                  b,
		indent:             "\t\t",
		models:             models,
		enums:              enums,
		api:                api,
		bindings:           bindings,
		vars:               make(map[string]valType),
		paginationTotals:   make(map[string]string),
		nativeFunctions:    nativeFunctions,
		functions:          functions,
		selectionFunctions: selectionFunctions,
		clientSelection:    "req.Select",
		paginate:           hasDirective(api.Directives, "paginate"),
		loadSelections:     loadSelections,
		querySelections:    analyzeSelectedQueryProjections(api.Body, models, api.ReturnType, functions),
		queryRelations:     analyzeQueryRelations(api.Body, api.ReturnType, models, functions),
		explicitSelections: g.explicitCallableSelections(functions),
	}
	registerCallableParams(c, api.Params)
	c.initializePagination(g.paginationPlans[api])
	c.compileHandlerBody(api.Body.Stmts)
	writeCachedBodyEnd(b, cached)

	fmt.Fprintf(b, "\t}\n}\n\n")
}

// writeCachedBodyStart must follow authorization and all typed parameter reads.
func writeCachedBodyStart(b *strings.Builder, directives []*ast.Directive) bool {
	directive := findDirective(directives, "cache")
	if directive == nil {
		return false
	}
	if len(directive.Args) == 1 && directive.Args[0].Name == "policy" {
		b.WriteString("\t\treturn api.CacheResponsePolicy(ctx, req, luvia.BearerToken(ctx), func() error {\n")
		return true
	}
	return false
}

func writeCachedBodyEnd(b *strings.Builder, cached bool) {
	if cached {
		b.WriteString("\t\t})\n")
	}
}

func writeCallableParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	writeCallableParamBinding(b, param, param.Name, enums, indent)
}

// The wire key stays unchanged when a local binding needs a hygienic Go name.
func writeCallableParamBinding(b *strings.Builder, param *ast.ParamDecl, name string, enums map[string]bool, indent string, modelSets ...map[string]*ast.ModelDecl) {
	local := *param
	local.Name = name
	if writeBuiltinParamExtraction(b, &local, enums, indent, param.Name) {
		return
	}
	if param.Type != nil && enums[param.Type.Name] {
		writeEnumParamExtraction(b, &local, enums, indent, param.Name)
		return
	}
	if param.Type != nil && param.Type.IsList && len(modelSets) > 0 && modelSets[0][param.Type.Name] != nil {
		writeModelListParamExtraction(b, &local, enums, indent, param.Name)
		return
	}
	// Parser and generation entry validation require an explicit type.
	writeStructuredParamExtraction(b, &local, enums, indent, param.Name)
}

func paramWireKey(param *ast.ParamDecl, keys []string) string {
	if len(keys) != 0 {
		return keys[0]
	}
	return param.Name
}

func writeOptionalParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, method, indent string, keys ...string) {
	key := paramWireKey(param, keys)
	writeDefaultParamDeclaration(b, param, enums, indent)
	fmt.Fprintf(b, "%sif req.HasParam(%q) {\n", indent, key)
	fmt.Fprintf(b, "%s\t_value%s, err := req.Param%s(%q)\n", indent, str.Capitalize(param.Name), method, key)
	fmt.Fprintf(b, "%s\tif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%s\t%s = _value%s\n", indent, param.Name, str.Capitalize(param.Name))
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeStructuredParamDeclaration(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	goType := resolveGoType(param.Type)
	if param.Default == nil {
		fmt.Fprintf(b, "%svar %s %s\n", indent, param.Name, goType)
		return
	}
	writeDefaultParamDeclaration(b, param, enums, indent)
}

func writeDefaultParamDeclaration(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	goType := resolveGoType(param.Type)
	valueType := param.Type
	if valueType != nil && valueType.Nullable {
		copy := *valueType
		copy.Nullable = false
		valueType = &copy
	}
	defaultValue := compileDefaultValue(param.Default, resolveGoType(valueType), enums)
	if valueType != param.Type && !param.Type.IsList && !isNullLiteral(param.Default) {
		defaultName := "_default" + str.Capitalize(param.Name)
		fmt.Fprintf(b, "%svar %s %s = %s\n", indent, defaultName, resolveGoType(valueType), defaultValue)
		fmt.Fprintf(b, "%svar %s %s = &%s\n", indent, param.Name, goType, defaultName)
		return
	}
	fmt.Fprintf(b, "%svar %s %s = %s\n", indent, param.Name, goType, defaultValue)
}

func isNullLiteral(expr ast.Expr) bool {
	literal, ok := expr.(*ast.Literal)
	return ok && literal.Kind == token.Null
}

func (c *compiler) compileHandlerBody(statements []ast.Stmt) {
	for index, statement := range statements {
		if index == len(statements)-1 && c.api.ReturnType != nil {
			if expression, ok := statement.(*ast.ExprStmt); ok {
				c.compileReturn(&ast.ReturnStmt{Pos: expression.Pos, Value: expression.Expr})
				return
			}
		}
		c.compileStmt(statement)
	}
	if len(statements) == 0 || !isHandlerTerminating(statements[len(statements)-1]) {
		c.write("return nil")
	}
}

func (c *compiler) compileCallableBody(statements []ast.Stmt) {
	for index, statement := range statements {
		if index == len(statements)-1 && c.functionResult != nil {
			if expression, ok := statement.(*ast.ExprStmt); ok {
				c.compileReturn(&ast.ReturnStmt{Pos: expression.Pos, Value: expression.Expr})
				return
			}
		}
		c.compileStmt(statement)
	}
	if len(statements) == 0 || !isHandlerTerminating(statements[len(statements)-1]) {
		c.write("return nil")
	}
}

func isHandlerTerminating(statement ast.Stmt) bool {
	switch statement.(type) {
	case *ast.ReturnStmt, *ast.ThrowStmt:
		return true
	default:
		return false
	}
}

// compileFnBody generates Go handler code from a .luxo fn body.
// Reuses the same compilation pipeline as API bodies.
func compileFnBody(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().compileFnBody(b, fn, models, enums)
}

func (g *GeneratorContext) compileFnBody(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	g.compileFnBodyWithFunctions(b, fn, models, enums, nil, nil, nil)
}

func (g *GeneratorContext) compileFnBodyWithFunctions(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool) {
	// Convert FnDecl to ApiDecl for code reuse — they share the same structure
	api := &ast.ApiDecl{
		Pos:        fn.Pos,
		Name:       fn.Name,
		Params:     fn.Params,
		ReturnType: fn.ReturnType,
		Directives: fn.Directives,
		Body:       fn.Body,
	}
	handler := "handle" + str.Capitalize(fn.Name)
	if hasDirective(fn.Directives, "service") {
		handler = serviceHandlerGoName(fn.Name)
	}
	g.compileNamedAPIBody(b, api, models, enums, nativeFunctions, functions, selectionFunctions, handler)
}

func (g *GeneratorContext) compileLocalFunction(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool) {
	g.compileFunction(b, fn, models, enums, nativeFunctions, functions, selectionFunctions, nil, "")
}

func (g *GeneratorContext) compileStaticFunction(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool, functions map[string]*ast.FnDecl, targets map[string]string, emittedName string) {
	g.compileFunction(b, fn, models, enums, nil, functions, nil, targets, emittedName)
}

// Lexical selection metadata survives value/error closures with a different return ABI.
const functionSelectionParameter = "_select"

func (g *GeneratorContext) compileFunction(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool, staticFunctions map[string]string, emittedName string) {
	bindings := g.parameterBindings(fn.Params, fn.Body, models, enums)
	signature := *fn
	if emittedName == "" {
		signature.Name = g.functionGoName(fn.Name)
	}
	writeCompiledFunctionSignature(b, &signature, models, selectionFunctions[fn.Name], bindings, emittedName)
	api := &ast.ApiDecl{Pos: fn.Pos, Name: fn.Name, Params: fn.Params, ReturnType: fn.ReturnType, Body: fn.Body}
	c := &compiler{
		generator:          g,
		b:                  b,
		indent:             "\t",
		models:             models,
		enums:              enums,
		api:                api,
		vars:               make(map[string]valType),
		paginationTotals:   make(map[string]string),
		nativeFunctions:    nativeFunctions,
		functions:          functions,
		staticFunctions:    staticFunctions,
		selectionFunctions: selectionFunctions,
		functionResult:     fn.ReturnType,
		bindings:           bindings,
		inFunction:         true,
		loadSelections:     analyzeLoadProjections(fn.Body, models, nil),
		querySelections:    analyzeSelectedQueryProjections(fn.Body, models, fn.ReturnType, functions),
		queryRelations:     analyzeQueryRelations(fn.Body, fn.ReturnType, models, functions),
		explicitSelections: g.explicitCallableSelections(functions),
	}
	if selectionFunctions[fn.Name] {
		c.clientSelection = functionSelectionParameter
	}
	registerCallableParams(c, fn.Params)
	c.compileCallableBody(fn.Body.Stmts)
	b.WriteString("}\n\n")
}

func writeCompiledFunctionSignature(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, usesSelection bool, bindings localBindings, staticTarget string) {
	separator := ", "
	if staticTarget == "" {
		fmt.Fprintf(b, "func (app *App) %s(ctx context.Context", fn.Name)
	} else {
		fmt.Fprintf(b, "func %s(", staticTarget)
		separator = ""
	}
	if usesSelection {
		b.WriteString(", " + functionSelectionParameter + " []*selection.Field")
	}
	for _, param := range fn.Params {
		goType := compiledFunctionGoType(param.Type, models)
		if param.Spread {
			fmt.Fprintf(b, "%s%s ...%s", separator, bindings.name(param.Name), goType)
			separator = ", "
			continue
		}
		fmt.Fprintf(b, "%s%s %s", separator, bindings.name(param.Name), goType)
		separator = ", "
	}
	if fn.ReturnType == nil {
		b.WriteString(") error {\n")
		return
	}
	fmt.Fprintf(b, ") (_value %s, err error) {\n", compiledFunctionGoType(fn.ReturnType, models))
}

func compiledFunctionGoType(ref *ast.TypeRef, models map[string]*ast.ModelDecl) string {
	if ref != nil && ref.Name == "Channel" && len(ref.TypeArgs) == 1 {
		return "chan " + compiledFunctionGoType(ref.TypeArgs[0], models)
	}
	if ref == nil || models[ref.Name] == nil {
		return resolveGoType(ref)
	}
	if ref.IsList {
		return "[]*" + ref.Name
	}
	return "*" + ref.Name
}

// Model parameters expose references regardless of their input storage.
func registerCallableParams(c *compiler, params []*ast.ParamDecl) {
	for _, param := range params {
		valueType := valType{name: "string"}
		if param.Type != nil {
			valueType.name = param.Type.Name
			valueType.typeRef = param.Type
			valueType.isChan = param.Type.Name == "Channel" && !param.Type.IsList && !param.Type.Nullable
			valueType.isList = param.Type.IsList
			valueType.nullable = param.Type.Nullable
			_, valueType.isModel = c.models[param.Type.Name]
		}
		c.vars[param.Name] = valueType
	}
}

// valType tracks the resolved type of a val variable.
type valType struct {
	typeRef   *ast.TypeRef // Explicit generic arguments, including Channel element representation.
	isModel   bool         // true for model values, pointers and lists
	isValue   bool         // true for an addressable model value rather than a model pointer
	isList    bool         // true if this is a list (e.g., []*Model)
	isChan    bool         // true if this is a channel (Channel<T>)
	nullable  bool         // true if this is a pointer type (nullable param/field)
	sealedTag uint16       // Active case proven by the nearest sealed when branch.
	name      string       // model name or luxo type name (Int/String/Boolean/Float)
}

func (c *compiler) valTypeFromExpr(expr ast.Expr) (valType, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		if value, exists := c.vars[ident.Name]; exists {
			if ident.NarrowedTag && c.isNarrowedScalar(ident, value) {
				value.nullable = false
			}
			return value, true
		}
	}
	if expr == nil || expr.GetTypeTag() == "" {
		return valType{}, false
	}
	_, isModel := c.models[expr.GetTypeTag()]
	_, isObject := expr.(*ast.ObjectExpr)
	return valType{
		typeRef:  c.genericTypeRef(expr),
		isChan:   expr.GetTypeTag() == "Channel" && !expr.IsNullable() && !expr.IsListType(),
		isModel:  isModel,
		isValue:  isModel && isObject,
		isList:   expr.IsListType(),
		nullable: expr.IsNullable(),
		name:     expr.GetTypeTag(),
	}, true
}

func (c *compiler) goTypeForExpr(expr ast.Expr) string {
	value, ok := c.valTypeFromExpr(expr)
	if !ok {
		return ""
	}
	return goTypeForValType(value)
}

func (c *compiler) yieldNeedsAddress(expr ast.Expr) bool {
	if expr == nil || !expr.IsNullable() || expr.IsListType() {
		return false
	}
	name := expr.GetTypeTag()
	if _, isModel := c.models[name]; isModel {
		return false
	}
	return true
}

// compiler holds state during body compilation.
type compiler struct {
	bindings           localBindings
	generator          *GeneratorContext
	b                  *strings.Builder
	indent             string
	models             map[string]*ast.ModelDecl
	types              map[string]bool // type declaration names (AuthPayload, etc.)
	enums              map[string]bool // enum type names
	api                *ast.ApiDecl
	vars               map[string]valType // variable name → resolved type
	inAsync            bool               // true inside async { } — no return err
	inForExpr          bool               // true inside for-as-expression with yield — yield compiles to return
	yieldAddr          bool               // true when a yielded value must be wrapped in a pointer
	yieldTmp           int                // unique temporary counter for nullable primitive yields
	paginate           bool               // true when API has @paginate
	paginationTotals   map[string]string  // result variable → exact query total variable
	paginationTmp      int                // unique pagination temporary counter
	pagePlan           *inferred.PaginationPlan
	pageQueries        map[ast.Expr]bool
	pageSlots          map[ast.Expr]string
	pageResult         string
	pagingQuery        bool
	ptrTmpCount        int                              // counter for hoisted pointer temp vars (nullable create args)
	resultTmp          int                              // counter for Result<T> values lowered from Go's (T, error)
	aggregateTmp       int                              // counter for fused await aggregate result slices
	nativeFunctions    map[string]bool                  // @native fn names available through app.Resolver
	functions          map[string]*ast.FnDecl           // function declarations available for static call lowering
	staticFunctions    map[string]string                // standalone generated call targets
	selectionFunctions map[string]bool                  // local functions that consume client field selection
	clientSelection    string                           // generated expression for the current request selection
	functionResult     *ast.TypeRef                     // non-nil while compiling a local fn body
	valueResult        *ast.TypeRef                     // declared nullable list ABI of a value closure
	inFunction         bool                             // true while compiling a local fn rather than a transport handler
	valueClosure       bool                             // Go value boundary; never writes a transport response
	loopScope          *controlScope                    // Nearest explicit loop in the current Go function.
	whenScope          *controlScope                    // Statement-when branch return boundary in this Go function.
	valueModel         bool                             // value closure returns a model pointer, not a local model value
	closureError       string                           // local failure slot for an immediately invoked value closure
	loadSelections     map[*ast.NamedArg]loadProjection // exact load expression → compiled demand
	querySelections    map[ast.Expr]loadProjection
	queryRelations     map[ast.Expr]loadProjection
	explicitSelections map[string]bool
	expressionBindings map[ast.Expr]string // Specialized condition leaves retain their static Go representation.
}

func (c *compiler) write(format string, args ...any) {
	c.b.WriteString(c.indent)
	fmt.Fprintf(c.b, format, args...)
	c.b.WriteByte('\n')
}

// compileStmt compiles a single statement to Go.
func (c *compiler) compileStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		c.compileVal(s)
		c.retainLocalInitialization(s)
	case *ast.ReturnStmt:
		c.compileReturn(s)
	case *ast.ThrowStmt:
		c.compileThrow(s)
	case *ast.IfStmt:
		c.compileIf(s)
	case *ast.ExprStmt:
		c.compileExprStmt(s)
	case *ast.EmitStmt:
		c.compileEmit(s)
	case *ast.ForStmt:
		c.compileFor(s)
	case *ast.AssignStmt:
		c.compileAssign(s)
	case *ast.BreakStmt:
		c.compileLoopJump("break")
	case *ast.ContinueStmt:
		c.compileLoopJump("continue")
	default:
		// Unreachable: every ast.Stmt implementation has a case above. A new
		// statement type added to the AST without a case here is a compiler bug —
		// fail loud at gen time rather than silently emitting broken Go.
		panic(&InternalCompilerError{Detail: fmt.Sprintf("codegen: unhandled statement type %T", stmt)})
	}
}

// compileVal: val x = expr
// Luxo permits unused locals. Retain initialization and its effects while making
// the generated declaration legal Go; blank assignments require no conversion.
func (c *compiler) retainLocalInitialization(statement *ast.ValStmt) {
	use := func(name string) {
		if name == "" || name == "_" {
			return
		}
		c.write("_ = %s", c.bindings.name(name))
		if total := c.paginationTotals[name]; total != "" {
			c.write("_ = %s", total)
		}
	}
	if len(statement.Names) == 0 {
		use(statement.Name)
		return
	}
	for _, name := range statement.Names {
		use(name)
	}
}

func (c *compiler) compileVal(s *ast.ValStmt) {
	if hasDiscardedBindings(s) {
		c.compileDiscardedBindings(s)
		return
	}
	if await, ok := s.Value.(*ast.AwaitExpr); ok {
		names := s.Names
		if len(names) == 0 {
			names = []string{s.Name}
		}
		c.compileAwaitBindings(await, names, s.Type)
		return
	}
	if len(s.Names) != 0 {
		c.compileDestructuredLocal(s)
		return
	}
	if c.compileChannelValue(s) {
		return
	}
	var expected *ast.TypeRef
	if s.Value.GetTypeTag() == "" && c.api != nil {
		expected = c.api.ReturnType
	}
	if c.compileDeclaredLocal(s) {
		return
	}
	expr := c.compileExpectedExpr(s.Value, expected)
	if c.isModelQuery(s.Value) {
		c.compileQueryLocal(s, expr)
		return
	}
	if _, ok := s.Value.(*ast.ListExpr); ok && expected != nil && expected.IsList {
		_, isModel := c.models[expected.Name]
		c.vars[s.Name] = valType{name: expected.Name, isList: true, isModel: isModel}
	}
	// Wrap bare integer literals in int64() to match Luxo's Int = int64
	if lit, ok := s.Value.(*ast.Literal); ok && lit.Kind == token.Int {
		expr = fmt.Sprintf("int64(%s)", expr)
	}
	c.write("%s := %s", c.bindings.name(s.Name), expr)
	if vt, ok := c.valTypeFromExpr(s.Value); ok {
		if vt.isChan {
			vt.typeRef = c.channelTypeRef(s.Value)
		}
		c.vars[s.Name] = vt
	} else if _, ok := s.Value.(*ast.WhenExpr); ok && c.api != nil && c.api.ReturnType != nil {
		// Only untyped source fragments may inherit the enclosing API result.
		c.vars[s.Name] = valType{name: c.api.ReturnType.Name}
	}
	c.propagateLocalSource(s)
}

func (c *compiler) compileDestructuredLocal(statement *ast.ValStmt) {
	initializer := *statement
	initializer.Name = c.discardedBindingName()
	initializer.Names = nil
	c.compileVal(&initializer)
	for _, name := range statement.Names {
		c.compileLocalAlias(name, initializer.Name)
	}
}

// Each binding gets its own slot. Keep the initialized owner's type and any
// query metadata, including when the new binding shadows an existing name.
func (c *compiler) compileLocalAlias(name, source string) {
	c.write("%s := %s", c.bindings.name(name), c.bindings.name(source))
	if value, ok := c.vars[source]; ok {
		c.vars[name] = value
	} else {
		delete(c.vars, name)
	}
	if total := c.paginationTotals[source]; total != "" {
		c.paginationTotals[name] = total
	} else {
		delete(c.paginationTotals, name)
	}
}

func (c *compiler) compileQueryLocal(s *ast.ValStmt, expr string) {
	qt := c.resolveQueryType(s.Value)
	name := c.bindings.name(s.Name)
	result := name
	lift := s.Type != nil && s.Type.Nullable && !s.Value.IsNullable()
	if lift && !qt.isList && !qt.isModel {
		result = c.discardedBindingName()
	}
	// @paginate + all → AllWithCount returns (results, total, error)
	if c.isPagedQuery(s.Value) {
		totalName := "_luxoTotal" + str.Capitalize(s.Name)
		c.write("%s, %s, err := %s", result, totalName, expr)
		if c.paginationTotals == nil {
			c.paginationTotals = make(map[string]string)
		}
		c.paginationTotals[s.Name] = totalName
	} else {
		c.write("%s, err := %s", result, expr)
	}
	c.writeErrorGuard()
	if lift {
		if qt.isList {
			c.ensureListPresent(name, s.Type)
		} else if !qt.isModel {
			c.write("%s := &%s", name, result)
		}
		qt.nullable = true
	}
	if total := c.paginationTotals[s.Name]; total != "" {
		c.paginationTotals[s.Name] = c.captureQueryTotal(s.Value, total)
	}
	c.vars[s.Name] = qt
}

// isChannelVar checks if expr is a variable known to be a channel.
func (c *compiler) isChannelVar(expr ast.Expr) bool {
	if ref := c.channelTypeRef(expr); ref != nil && ref.Name == "Channel" && !ref.IsList {
		return true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	vt, exists := c.vars[ident.Name]
	return exists && vt.isChan
}

// isChannelConstructor checks if expr is `Channel(n)`.
func (c *compiler) isChannelConstructor(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Func.(*ast.Ident)
	return ok && ident.Name == "Channel"
}

// unwrapQuestion checks if expr is `inner?` (UnaryExpr with "?" operator).
func unwrapQuestion(expr ast.Expr) (ast.Expr, bool) {
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == "?" {
		return u.Value, true
	}
	return expr, false
}

// compileReturn: return expr
func (c *compiler) compileReturn(s *ast.ReturnStmt) {
	if c.whenScope != nil {
		if s.Value != nil {
			c.compileExprStmt(&ast.ExprStmt{Expr: s.Value})
		}
		c.write("break %s", c.controlLabel(c.whenScope))
		return
	}
	if c.valueClosure {
		if s.Value == nil {
			c.write("return nil")
		} else {
			expression := c.compileAssignedValue(s.Value, c.valueResult)
			expression = c.pageBranchValue(s.Value, expression)
			if c.valueModel {
				expression = c.modelReference(s.Value, expression)
			}
			c.write("return %s", expression)
		}
		return
	}
	if c.inAsync {
		if s.Value != nil {
			c.compileExprStmt(&ast.ExprStmt{Expr: s.Value})
		}
		c.write("return")
		return
	}
	if c.inFunction {
		c.compileFunctionReturn(s)
		return
	}
	if s.Value == nil {
		c.write("return nil")
		return
	}
	var expected *ast.TypeRef
	if c.api != nil {
		expected = c.api.ReturnType
	}
	if expected != nil && expected.Nullable {
		c.compileNullableReturn(s.Value, expected)
		return
	}
	expr := c.compileExpectedExpr(s.Value, expected)

	// 1. Direct model query chain — extract to temp var, then WriteLuxo
	if c.isModelQuery(s.Value) {
		qt := c.resolveQueryType(s.Value)
		// Extract query result to temp variable (query returns (result, error)).
		totalName := ""
		if c.isPagedQuery(s.Value) {
			totalName = c.nextPaginationName("Total")
			c.write("_result, %s, err := %s", totalName, expr)
		} else {
			c.write("_result, err := %s", expr)
		}
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
		totalName = c.captureQueryTotal(s.Value, totalName)
		if qt.isModel && qt.isList {
			c.writeModelListReturn("_result", qt.name, totalName)
		} else {
			c.writeReturnByType("_result", qt)
		}
		c.write("return nil")
		return
	}

	// 2. Variable with tracked type
	if ident, ok := s.Value.(*ast.Ident); ok {
		if vt, exists := c.vars[ident.Name]; exists {
			c.writeReturnByType(expr, vt, c.pageTotal(s.Value))
			c.write("return nil")
			return
		}
	}

	// 3. Fallback: use API return type declaration
	c.writeScalarReturn(expr, c.pageTotal(s.Value))
	c.write("return nil")
}

func (c *compiler) compileFunctionReturn(statement *ast.ReturnStmt) {
	if statement.Value == nil {
		c.write("return nil")
		return
	}
	if c.functionResult == nil {
		c.compileExprStmt(&ast.ExprStmt{Expr: statement.Value})
		c.write("return nil")
		return
	}
	if c.isModelQuery(statement.Value) {
		expression := c.compileFunctionArgument(statement.Value, c.functionResult)
		c.write("return %s", expression)
		return
	}
	expression := c.compileAssignedValue(statement.Value, c.functionResult)
	c.write("return %s, nil", expression)
}

// writeReturnByType emits binary output code based on tracked variable type.
// Always writes Luxo binary — Luvia converts to JSON if needed.
func (c *compiler) writeReturnByType(expr string, vt valType, totals ...string) {
	if vt.isModel {
		if vt.isList {
			total := c.paginationTotals[expr]
			if len(totals) != 0 {
				total = totals[0]
			}
			c.writeModelListReturn(expr, vt.name, total)
		} else {
			c.writeComputedResolve(vt.name, expr, false)
			c.writeBinaryCall("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
		}
		return
	}
	if vt.isList {
		if c.writePrimitiveList(expr, vt.name) {
			return
		}
		if c.isTypeDecl(vt.name) {
			c.writeBinaryCall("WriteColumnar%s(req.Buf, %s, req.FieldMask)", vt.name, expr)
			return
		}
	}
	if appendExpr, ok := binaryScalarAppend(vt.name, "req.Buf.B", expr, c.enums[vt.name]); ok {
		c.writeBinaryAppend(appendExpr)
	} else if c.isTypeDecl(vt.name) {
		c.writeBinaryCall("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
	} else {
		c.write("_ = %s // unsupported return type for binary encoding", expr)
	}
}

// writeScalarReturn emits binary output for non-model return values.
func (c *compiler) writeScalarReturn(expr string, totals ...string) {
	if c.api != nil && c.api.ReturnType != nil {
		typeName := c.api.ReturnType.Name
		if model := c.models[typeName]; model != nil {
			if c.api.ReturnType.IsList {
				if c.paginate {
					resultName := c.nextPaginationName("Result")
					c.write("%s := %s", resultName, expr)
					total := ""
					if len(totals) != 0 {
						total = totals[0]
					}
					c.writeModelListReturn(resultName, typeName, total)
				} else {
					c.writeComputedResolve(typeName, expr, true)
					c.writeBinaryCall("WriteColumnar%s(req.Buf, %s, req.FieldMask)", typeName, expr)
				}
			} else {
				c.write("_result := %s", expr)
				c.writeComputedResolve(typeName, "&_result", false)
				c.writeBinaryCall("_result.WriteLuxo(req.Buf, req.FieldMask)")
			}
			return
		}
		if c.api.ReturnType.IsList {
			if c.writePrimitiveList(expr, typeName) {
				return
			}
			if c.isTypeDecl(typeName) {
				c.writeBinaryCall("WriteColumnar%s(req.Buf, %s, req.FieldMask)", typeName, expr)
				return
			}
		}
		if appendExpr, ok := binaryScalarAppend(typeName, "req.Buf.B", expr, c.enums[typeName]); ok {
			c.writeBinaryAppend(appendExpr)
			return
		}
		// Try as type with WriteLuxo — value receiver
		if c.isTypeDecl(typeName) {
			c.writeBinaryCall("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
			return
		}
	}
	c.write("_ = %s // unsupported return type for binary encoding", expr)
}

func (c *compiler) writeModelListReturn(expr, modelName, totalName string) {
	if c.paginate && totalName == "" {
		totalName = c.writeInMemoryPagination(expr)
	}
	c.writeComputedResolve(modelName, expr, true)
	c.writeBinaryCall("WriteColumnar%s(req.Buf, %s, req.FieldMask)", modelName, expr)
	if c.paginate {
		c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, %s)", totalName)
		c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))")
		c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))")
	}
}

func (c *compiler) writeInMemoryPagination(expr string) string {
	totalName := c.nextPaginationName("Total")
	suffix := strings.TrimPrefix(totalName, "_luxoTotal")
	startName := "_luxoStart" + suffix
	endName := "_luxoEnd" + suffix
	pageName := "_luxoPage" + suffix
	c.write("%s := int64(len(%s))", totalName, expr)
	c.write("if req.PageSize > 0 {")
	c.indent += "\t"
	c.write("%s := req.Page - 1", pageName)
	c.write("%s := len(%s)", startName, expr)
	c.write("if %s <= len(%s)/req.PageSize { %s = %s * req.PageSize }", pageName, expr, startName, pageName)
	c.write("%s := len(%s)", endName, expr)
	c.write("if req.PageSize < %s-%s { %s = %s + req.PageSize }", endName, startName, endName, startName)
	c.write("%s = %s[%s:%s]", expr, expr, startName, endName)
	c.indent = strings.TrimSuffix(c.indent, "\t")
	c.write("}")
	return totalName
}

func (c *compiler) nextPaginationName(kind string) string {
	c.paginationTmp++
	return fmt.Sprintf("_luxo%s%d", kind, c.paginationTmp)
}

func (c *compiler) writeComputedResolve(modelName, expr string, list bool) {
	if !modelHasComputedAggregates(c.models[modelName]) {
		return
	}
	items := expr
	if !list {
		items = "[]*" + modelName + "{" + expr + "}"
	}
	c.write("if err := resolve%sComputed(ctx, app, %s, req.FieldMask); err != nil { return err }", modelName, items)
}

func (c *compiler) writePrimitiveList(expr, typeName string) bool {
	appendExpr, ok := binaryScalarAppend(typeName, "req.Buf.B", "v", c.enums[typeName])
	if !ok {
		return false
	}
	c.writeBinaryAppend(fmt.Sprintf("codec.AppendArrayHeader(req.Buf.B, len(%s))", expr))
	c.write("for _, v := range %s {", expr)
	oldIndent := c.indent
	c.indent += "\t"
	c.writeBinaryAppend(appendExpr)
	c.indent = oldIndent
	c.write("}")
	return true
}

func binaryScalarAppend(typeName, dst, value string, isEnum bool) (string, bool) {
	if isEnum {
		return enumAppendExpression(typeName, dst, value), true
	}
	switch typeName {
	case "Int":
		return fmt.Sprintf("codec.AppendSvarint(%s, %s)", dst, value), true
	case "Float":
		return fmt.Sprintf("codec.AppendFixed64(%s, %s)", dst, value), true
	case "String":
		return fmt.Sprintf("codec.AppendString(%s, %s)", dst, value), true
	case "Boolean":
		return fmt.Sprintf("codec.AppendBool(%s, %s)", dst, value), true
	case "DateTime":
		return fmt.Sprintf("codec.AppendDateTime(%s, %s)", dst, value), true
	case "Duration":
		return fmt.Sprintf("codec.AppendSvarint(%s, int64(%s))", dst, value), true
	case "UUID":
		return fmt.Sprintf("codec.AppendUUID(%s, %s)", dst, value), true
	case "Decimal":
		return fmt.Sprintf("codec.AppendString(%s, lux.FormatDecimal(%s))", dst, value), true
	case "Bytes":
		return fmt.Sprintf("codec.AppendBytes(%s, %s)", dst, value), true
	case "JSON":
		return fmt.Sprintf("codec.AppendJSON(%s, %s)", dst, value), true
	default:
		return "", false
	}
}

// isTypeDecl recognizes non-model value codecs, including closed sums.
func (c *compiler) isTypeDecl(name string) bool {
	if c.generator != nil && c.generator.sealedDeclaration(name) != nil {
		return true
	}
	if c.types != nil {
		return c.types[name]
	}
	if c.generator != nil && c.generator.asyncSource != nil {
		return c.generator.typeDeclarations[name] != nil
	}
	// Fallback: if not a model, not a primitive, not an enum — assume it's a type
	if _, isModel := c.models[name]; isModel {
		return false
	}
	if c.enums[name] {
		return false
	}
	switch name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "":
		return false
	}
	// PascalCase and not recognized — likely a type declaration
	return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
}

// compileThrow: throw ErrorName(args)
func (c *compiler) compileThrow(s *ast.ThrowStmt) {
	expr := c.compileThrowExpr(s.Error)
	c.writeErrorReturn("", expr)
}

// compileExprStmt: standalone expression (including elvis guard and ? propagation)
func (c *compiler) compileExprStmt(s *ast.ExprStmt) {
	if c.compileForEachStatement(s.Expr) {
		return
	}
	if branch, ok := s.Expr.(*ast.WhenExpr); ok {
		if c.inForExpr && containsYieldExpr(branch) {
			c.compileYieldWhenStatement(branch)
		} else {
			c.compileWhenStatement(branch)
		}
		return
	}
	if elvis, ok := s.Expr.(*ast.ElvisExpr); ok {
		if thrown, ok := elvis.Right.(*ast.UnaryExpr); ok && thrown.Op == "throw" {
			c.compileElvisGuard(elvis)
		} else {
			value := c.compileDefaultExpr(elvis)
			c.write("_ = %s", value)
		}
		return
	}
	if bang, ok := s.Expr.(*ast.BangElvisExpr); ok {
		c.compileBangElvisGuard(bang)
		return
	}
	// Standalone Result propagation still evaluates and discards the value.
	if _, hasQ := unwrapQuestion(s.Expr); hasQ {
		expr := c.compileExpr(s.Expr)
		c.write("_ = %s", expr)
		return
	}
	// Model queries as standalone statements need error checking
	if c.isModelQuery(s.Expr) {
		expr := c.compileExpr(s.Expr)
		c.write("if _, err := %s; err != nil {", expr)
		c.writeErrorReturn("\t", "err")
		c.write("}")
		return
	}
	if c.compileTransactionStatement(s.Expr) {
		return
	}
	if c.compileDiscardedFunctionCall(s.Expr) {
		return
	}
	if member, ok := s.Expr.(*ast.MemberExpr); ok && builtin.IsDebugProperty(member) && !isLogTarget(member.Object) {
		c.write("_ = %s", c.compileExpr(member))
		return
	}
	expr := c.compileExpr(s.Expr)
	if expr == "" {
		return
	}
	if s.Expr.GetTypeTag() != "" && s.Expr.GetTypeTag() != "Void" {
		if expr != "nil" {
			c.write("_ = %s", expr)
		}
		return
	}
	c.write("%s", expr)
}

func (c *compiler) compileTransactionStatement(expression ast.Expr) bool {
	var compiled string
	switch expression := expression.(type) {
	case *ast.TransactionExpr:
		compiled = c.compileTransaction(expression)
	case *ast.CallExpr:
		var ok bool
		compiled, ok = c.compileTransactionCall(expression)
		if !ok {
			return false
		}
	default:
		return false
	}
	c.write("if err := %s; err != nil {", compiled)
	c.writeErrorReturn("\t", "err")
	c.write("}")
	return true
}

func (c *compiler) compileDiscardedFunctionCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	if c.authMethod(call) != nil || isValueCollectionCall(call) {
		result := c.compileExpr(call)
		c.write("_ = %s", result)
		return true
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		return false
	}
	fn := c.functions[ident.Name]
	if isCompiledLocalFunction(fn) {
		if result := c.compileExpr(call); result != "" {
			c.write("_ = %s", result)
		}
		return true
	}
	if fn == nil || fn.ReturnType != nil || !hasDirective(fn.Directives, "native") {
		return false
	}
	result := c.compileExpr(call)
	c.write("if err := %s; err != nil {", result)
	c.writeErrorReturn("\t", "err")
	c.write("}")
	return true
}

// compileIf: if condition { stmts }
func (c *compiler) compileIf(s *ast.IfStmt) {
	cond := c.compileBooleanValue(s.Condition)
	c.write("if %s {", cond)
	c.compileBlock(s.Then)
	c.write("}")
}

// compileElvisGuard: x ?: throw Error
// - nullable: x == nil → throw
// - bool: !x → throw
// - (bool, error): val, err := x; if err → return err; if !val → throw
func (c *compiler) compileElvisGuard(e *ast.ElvisExpr) {
	if c.compileQueryBoolGuard(e.Left, e.Right, false) {
		return
	}
	// !expr ?: throw → if expr { return ... }
	if unary, ok := e.Left.(*ast.UnaryExpr); ok && unary.Op == "!" {
		inner := c.compileExpr(unary.Value)
		c.writeConditionalThrow(inner, e.Right)
		return
	}

	// Default: pointer nil check — x ?: throw → if x == nil { return ... }
	left := c.compileExpr(e.Left)
	// Bool (non-error): if !val { throw }
	if isBoolExpr(e.Left) {
		if _, binary := e.Left.(*ast.BinaryExpr); binary {
			left = "(" + left + ")"
		}
		c.writeConditionalThrow("!"+left, e.Right)
	} else {
		c.writeConditionalThrow(left+" == nil", e.Right)
	}
}

// compileBangElvisGuard: x !: throw Error
// - bool: if x → throw
// - (bool, error): val, err := x; if err → return err; if val → throw
func (c *compiler) compileBangElvisGuard(e *ast.BangElvisExpr) {
	if c.compileQueryBoolGuard(e.Left, e.Right, true) {
		return
	}

	left := c.compileExpr(e.Left)
	c.writeConditionalThrow(left, e.Right)
}

// Boolean negation changes the rejection condition, never the query error channel.
func (c *compiler) compileQueryBoolGuard(value, failure ast.Expr, rejectTrue bool) bool {
	for {
		unary, ok := value.(*ast.UnaryExpr)
		if !ok || unary.Op != "!" {
			break
		}
		value = unary.Value
		rejectTrue = !rejectTrue
	}
	if !isErrorReturningBool(value) {
		return false
	}
	condition := "!_ok"
	if rejectTrue {
		condition = "_ok"
	}
	c.compileErrorReturningBoolGuard(value, failure, condition)
	return true
}

// Keep query result temporaries local to each guard without changing execution order.
func (c *compiler) compileErrorReturningBoolGuard(value, failure ast.Expr, condition string) {
	c.write("{")
	c.indent += "\t"
	left := c.compileExpr(value)
	c.write("_ok, _err := %s", left)
	c.write("if _err != nil {")
	c.writeErrorReturn("\t", "_err")
	c.write("}")
	c.writeConditionalThrow(condition, failure)
	c.indent = strings.TrimSuffix(c.indent, "\t")
	c.write("}")
}

// isErrorReturningBool checks if an expression is a method call that returns (bool, error).
// Detects: Model.exists(), Model.where(...).exists(), query chain ending in exists/count.
func isErrorReturningBool(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if member, ok := call.Func.(*ast.MemberExpr); ok {
		switch member.Field {
		case "exists":
			return true
		}
	}
	return false
}

// isBoolExpr checks if an expression returns a plain bool (not nullable, not error).
// Detects: member.verifyPassword(), it.contains(), etc.
func isBoolExpr(expr ast.Expr) bool {
	if expr != nil && expr.GetTypeTag() == "Boolean" && !expr.IsListType() && !expr.IsNullable() {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if member, ok := call.Func.(*ast.MemberExpr); ok {
		switch member.Field {
		case "verifyPassword", "contains", "startsWith", "endsWith", "isEmpty", "matches":
			return true
		}
	}
	return false
}

// compileEmit: emit EventName(args) — handles cross-module events
func (c *compiler) compileEmit(s *ast.EmitStmt) {
	args := c.compileObjectFields(s.Args, false)

	// Check if event is cross-module
	prefix := ""
	if c.generator != nil && c.generator.events != nil && c.api != nil {
		currentMod := moduleNameFromFile(c.api.Pos.File)
		evModule := c.generator.events.EventModule[s.EventName]
		if evModule != "" && evModule != currentMod {
			prefix = evModule + "_luxo."
		}
	}

	c.write("if err := %sEmit%s(ctx, app.EventBus, %s%sEvent{%s}); err != nil {",
		prefix, s.EventName, prefix, s.EventName, strings.Join(args, ", "))
	c.writeErrorReturn("\t", "err")
	c.write("}")
}

// compileExprValue lowers an expression before query-origin relation hydration.
func (c *compiler) compileExprValue(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if _, local := c.vars[e.Name]; !local && c.generator != nil {
			if global, exists := c.generator.globalValues[e.Name]; exists {
				return global.reference
			}
		}
		if !e.NarrowedTag {
			return c.bindings.name(e.Name)
		}
		if c.isNarrowedScalar(e, c.vars[e.Name]) {
			return "(*" + c.bindings.name(e.Name) + ")"
		}
		return c.bindings.name(e.Name)
	case *ast.Literal:
		return c.compileLiteral(e)
	case *ast.MemberExpr:
		return c.compileMember(e)
	case *ast.CallExpr:
		return c.compileCall(e)
	case *ast.BinaryExpr:
		return c.compileBinary(e)
	case *ast.ElvisExpr:
		return c.compileDefaultExpr(e)
	case *ast.BangElvisExpr:
		return fmt.Sprintf("/* !: */ %s", c.compileExpr(e.Left))
	case *ast.UnaryExpr:
		return c.compileUnary(e)
	case *ast.ListExpr:
		return c.compileList(e)
	case *ast.TemplateString:
		return c.compileTemplate(e)
	case *ast.RangeExpr:
		return c.compileRange(e)
	case *ast.ObjectExpr:
		return c.compileObject(e)
	case *ast.WhenExpr:
		return c.compileWhen(e)
	default:
		return c.compileBlockExpr(expr)
	}
}

// compileBlockExpr compiles block-level expressions (lambda, transaction, async, await).
func (c *compiler) compileBlockExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.LambdaExpr:
		panic(&InternalCompilerError{Detail: "untyped lambda reached generic expression lowering; a static consumer is required"})
	case *ast.TransactionExpr:
		return c.compileTransaction(e)
	case *ast.AsyncExpr:
		return c.compileAsync(e)
	case *ast.AwaitExpr:
		c.compileAwaitStmt(e)
		return ""
	case *ast.ForStmt:
		return c.compileForExpr(e)
	case *ast.YieldExpr:
		val := c.compileExpr(e.Value)
		if c.inForExpr {
			// Inside for-as-expression closure: yield → return value from closure
			if c.yieldAddr && !e.Value.IsNullable() && !isNullLiteral(e.Value) {
				name := fmt.Sprintf("_yield%d", c.yieldTmp)
				c.yieldTmp++
				if goType := c.goTypeForExpr(e.Value); goType != "" {
					c.write("var %s %s = %s", name, goType, val)
				} else {
					c.write("%s := %s", name, val)
				}
				c.write("return &%s", name)
			} else {
				c.write("return %s", val)
			}
		} else {
			c.write("_yieldResult = %s", val)
			c.write("break")
		}
		return ""
	default:
		// Unreachable: compileExpr + compileBlockExpr cover every ast.Expr
		// implementation. A new expression type without a case here is a
		// compiler bug — fail loud at gen time rather than emit broken Go.
		panic(&InternalCompilerError{Detail: fmt.Sprintf("codegen: unhandled expression type %T", expr)})
	}
}

func (c *compiler) compileLiteral(e *ast.Literal) string {
	switch e.Kind {
	case token.Int, token.Float:
		return compileNumericLiteral(e)
	case token.String:
		return fmt.Sprintf("%q", e.Value)
	case token.True:
		return "true"
	case token.False:
		return "false"
	case token.Null:
		return "nil"
	case token.Duration:
		if value, ok := compileDurationLiteral(e.Value); ok {
			return value
		}
		return e.Value
	default:
		return e.Value
	}
}

func compileBuiltinError(name string) string {
	resolved := builtinerror.GoName(name)
	if resolved == "" {
		panic(&InternalCompilerError{Detail: "unvalidated built-in error: " + name})
	}
	return "errors." + resolved
}

// compileMember: obj.field
func (c *compiler) compileMember(e *ast.MemberExpr) string {
	if isNullDebugChain(e) {
		return "nil"
	}
	if e.SafeCall {
		return c.compileSafeAccess(e, nil)
	}
	if value, handled := c.compileSealedMember(e); handled {
		return value
	}
	if value := c.compileLengthMember(e); value != "" {
		return value
	}
	if ident, ok := e.Object.(*ast.Ident); ok {
		// error.NotFound → errors.NotFound
		if _, local := c.vars[ident.Name]; ident.Name == "error" && !local {
			return compileBuiltinError(e.Field)
		}
		if ident.Name == "my" {
			return c.compileIdentityField(e)
		}
		// Enum.VALUE → EnumVALUE (Go enum constant)
		if _, local := c.vars[ident.Name]; !local && c.enums[ident.Name] {
			return ident.Name + e.Field
		}
	}
	if value, handled := c.compileDebugMember(e, nil); handled {
		return value
	}
	if e.Object.IsListType() {
		panic(&InternalCompilerError{Detail: "unsupported list member: " + e.Field})
	}
	// Duration properties: n.days, n.hours, n.minutes, n.seconds, n.milliseconds
	// Uses TypeTag set by semantic analyzer — only triggers when object is numeric
	if e.GetTypeTag() == "Duration" {
		if unit, ok := durationUnits[e.Field]; ok && isIntScalar(e.Object) {
			return c.checkedIntegerCall("DurationFromUnits", "Duration."+e.Field, c.compileMaterializedValue(e.Object, nil), unit)
		}
	}
	obj := c.compileMaterializedValue(e.Object, nil)
	return c.structuredMemberValue(e, fmt.Sprintf("%s.%s", obj, str.Capitalize(e.Field)))
}

// compileInstanceMethod compiles model instance methods like variable.delete(), variable.update(...).
// Returns empty string if not a model instance method.
func (c *compiler) compileInstanceMethod(e *ast.CallExpr) string {
	if plan := c.instanceSaveCall(e); plan != nil {
		member := e.Func.(*ast.MemberExpr)
		value, literal := c.compileLiteralModelReceiver(member.Object)
		if !literal {
			value = c.compileMaterializedValue(member.Object, &ast.TypeRef{Name: plan.model.Name})
		}
		return fmt.Sprintf("app.%s.save(ctx, %s)", plan.model.Name, value)
	}
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return ""
	}
	// Check if variable is a model instance via vars map
	vt, known := c.vars[ident.Name]
	if !known || !vt.isModel || vt.isList {
		return ""
	}
	modelName := vt.name
	varName := c.bindings.name(ident.Name)
	model := c.models[modelName]
	idGoName := primaryKeyGoName(model)
	switch member.Field {
	case "delete":
		if model != nil && isSoftDelete(model) {
			return fmt.Sprintf("app.%s.Where(%sWhere.%s.Eq(%s.%s)).SoftDelete(ctx)", modelName, modelName, idGoName, varName, idGoName)
		}
		return fmt.Sprintf("app.%s.Where(%sWhere.%s.Eq(%s.%s)).Delete(ctx)", modelName, modelName, idGoName, varName, idGoName)
	case "update":
		var sets []string
		lastEffect := lastEffectfulArgument(e.Args)
		for index, arg := range e.Args {
			if arg.Name != "" {
				val, _, _ := c.compileMutationArgument(model, arg, index < lastEffect)
				sets = append(sets, fmt.Sprintf("lux.SetField{Col: %q, Val: %s}", c.generator.sqlColumn(arg.Name), val))
			}
		}
		return fmt.Sprintf("app.%s.Where(%sWhere.%s.Eq(%s.%s)).updatePrepared(ctx, %s)",
			modelName, modelName, idGoName, varName, idGoName, strings.Join(sets, ", "))
	}
	return ""
}

// compileCall: handles Model.where(...).first(), Model.create(...), etc.
func (c *compiler) compileCall(e *ast.CallExpr) string {
	if value, handled := c.compileSealedConstructor(e); handled {
		return value
	}
	if e.ConstructorType != nil {
		if _, named := e.Func.(*ast.Ident); named {
			return c.compileObject(&ast.ObjectExpr{TypeTagged: e.TypeTagged, Pos: e.Pos, TypeName: e.ConstructorType.Name, Fields: e.Args})
		}
	}
	c.checkCollectionCallBoundary(e)
	c.checkModelQueryReceiver(e)
	if member, ok := e.Func.(*ast.MemberExpr); ok && member.SafeCall {
		return c.compileSafeAccess(member, e)
	}
	if e.Group != nil {
		return c.compileTypedGroup(e.Group)
	}
	if result, ok := c.compileJoinToString(e); ok {
		return result
	}
	if result, ok := c.compileSumOf(e); ok {
		return result
	}
	if result, ok := c.compileSortBy(e); ok {
		return result
	}
	if result, ok := c.compileCollectionCall(e); ok {
		return result
	}
	if result, ok := c.compileAuthMethod(e); ok {
		return result
	}
	// Built-in functions: now(), crypto.randomHex(), etc.
	if result := c.compileBuiltinCall(e); result != "" {
		return result
	}

	// Model instance methods: variable.delete() → app.Model.Where(Id.Eq(variable.Id)).Delete(ctx)
	if result := c.compileInstanceMethod(e); result != "" {
		return result
	}

	// String methods: obj.lowercase() → strings.ToLower(obj)
	if result := c.compileStringMethod(e); result != "" {
		return result
	}

	c.rejectUntypedChannelConstructor(e)

	if result, ok := c.compileTransactionCall(e); ok {
		return result
	}

	if result, ok := c.compileChannelClose(e); ok {
		return result
	}

	if result, ok := c.compileModelCallChain(e); ok {
		return result
	}
	return c.compileGenericCall(e)
}

func (c *compiler) rejectUntypedChannelConstructor(e *ast.CallExpr) {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || ident.Name != "Channel" {
		return
	}
	panic(&InternalCompilerError{Detail: "Channel constructor requires a typed variable declaration"})
}

func (c *compiler) compileTransactionCall(e *ast.CallExpr) (string, bool) {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || ident.Name != "transaction" || len(e.Args) != 1 {
		return "", false
	}
	lambda, ok := e.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return "", false
	}
	return c.compileTransactionBlock(lambda.Body), true
}

func (c *compiler) compileChannelClose(e *ast.CallExpr) (string, bool) {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok || len(e.Args) != 0 || member.Field != "close" {
		return "", false
	}
	if ref := c.channelTypeRef(member.Object); ref != nil && ref.Name == "Channel" {
		return "close(" + c.compileExpr(member.Object) + ")", true
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	valueType, exists := c.vars[ident.Name]
	if !exists || !valueType.isChan {
		return "", false
	}
	return fmt.Sprintf("close(%s)", c.bindings.name(ident.Name)), true
}

func (c *compiler) compileModelCallChain(e *ast.CallExpr) (string, bool) {
	if model, _ := c.modelTerminalFunctionModel(e); model != nil {
		method := e.Func.(*ast.Ident).Name
		return c.compileModelChain(model.Name, []chainLink{{method: method, args: e.Args[1:], expr: e}}), true
	}
	chain := flattenChain(e)
	if len(chain) < 2 {
		return "", false
	}
	ident, ok := chain[0].expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	if model := c.queryAliasModel(ident.Name); model != "" {
		return c.compileQueryAlias(ident.Name, model, chain[1:]), true
	}
	if _, local := c.vars[ident.Name]; local {
		return "", false
	}
	if _, isModel := c.models[ident.Name]; !isModel {
		return "", false
	}
	return c.compileModelChain(ident.Name, chain[1:]), true
}

// Literal receivers need an address before selecting generated pointer methods.
func (c *compiler) compileCallTarget(target ast.Expr) string {
	if member, ok := target.(*ast.MemberExpr); ok {
		if receiver, literal := c.compileLiteralModelReceiver(member.Object); literal {
			return fmt.Sprintf("%s.%s", receiver, str.Capitalize(member.Field))
		}
	}
	return c.compileExpr(target)
}

func (c *compiler) compileGenericCall(e *ast.CallExpr) string {
	funcExpr := c.compileCallTarget(e.Func)
	ident, isIdent := e.Func.(*ast.Ident)
	var declaration *ast.FnDecl
	if isIdent {
		declaration = c.functions[ident.Name]
		if isCompiledLocalFunction(declaration) {
			return c.compileLocalFunctionCall(e, declaration)
		}
	}
	isNative := isIdent && c.nativeFunctions[ident.Name]
	if isNative {
		funcExpr = "app.Resolver." + str.Capitalize(ident.Name)
	}
	var args []string
	if isNative {
		args = append(args, "ctx")
	}
	if isNative && declaration != nil {
		args = append(args, c.compileDeclaredFunctionArguments(e, declaration)...)
		return fmt.Sprintf("%s(%s)", funcExpr, strings.Join(args, ", "))
	}
	for _, a := range e.Args {
		args = append(args, c.compileExpr(a.Value))
	}
	return fmt.Sprintf("%s(%s)", funcExpr, strings.Join(args, ", "))
}

func isCompiledLocalFunction(fn *ast.FnDecl) bool {
	return fn != nil && fn.Body != nil && !hasDirective(fn.Directives, "native") && !hasDirective(fn.Directives, "service")
}

func (c *compiler) compileLocalFunctionCall(call *ast.CallExpr, fn *ast.FnDecl) string {
	target := c.staticFunctions[fn.Name]
	var args []string
	if target == "" {
		target = "app." + c.generator.functionGoName(fn.Name)
		args = append(args, "ctx")
	}
	if c.selectionFunctions[fn.Name] {
		args = append(args, c.functionSelection(call, fn.Name))
	}
	args = append(args, c.compileDeclaredFunctionArguments(call, fn)...)
	expression := fmt.Sprintf("%s(%s)", target, strings.Join(args, ", "))
	if fn.ReturnType == nil {
		c.write("if err := %s; err != nil {", expression)
		c.writeErrorReturn("\t", "err")
		c.write("}")
		return ""
	}
	c.resultTmp++
	result := fmt.Sprintf("_result%d", c.resultTmp)
	c.write("%s, err := %s", result, expression)
	c.write("if err != nil {")
	c.writeErrorReturn("\t", "err")
	c.write("}")
	return result
}

func (c *compiler) compileDeclaredFunctionArguments(call *ast.CallExpr, fn *ast.FnDecl) []string {
	positions := make(map[string]int, len(fn.Params))
	for index, param := range fn.Params {
		positions[param.Name] = index
	}
	args := make([]string, len(fn.Params), len(fn.Params)+len(call.Args))
	nextPosition := 0
	// Capture explicit arguments in source order before arranging the call ABI.
	for _, argument := range call.Args {
		position := nextPosition
		if argument.Name != "" {
			position = positions[argument.Name]
		} else if position < len(fn.Params) && !fn.Params[position].Spread {
			nextPosition++
		}
		param := fn.Params[position]
		value, goType := c.compileCallableArgument(argument.Value, param.Type, hasDirective(fn.Directives, "native"))
		if _, literal := argument.Value.(*ast.Literal); !literal {
			c.resultTmp++
			name := fmt.Sprintf("_argument%d", c.resultTmp)
			c.write("var %s %s = %s", name, goType, value)
			value = name
		}
		if param.Spread {
			args = append(args, value)
		} else {
			args[position] = value
		}
	}
	for index, param := range fn.Params {
		if param.Spread {
			return append(args[:index], args[len(fn.Params):]...)
		}
		if args[index] == "" {
			args[index] = c.compileFunctionDefault(param)
		}
	}
	return args
}

func (c *compiler) compileFunctionDefault(param *ast.ParamDecl) string {
	valueType := param.Type
	if valueType == nil || !valueType.Nullable || isNullLiteral(param.Default) {
		return compileDefaultValue(param.Default, compiledFunctionGoType(valueType, c.models), c.enums)
	}
	copy := *valueType
	copy.Nullable = false
	value := compileDefaultValue(param.Default, compiledFunctionGoType(&copy, c.models), c.enums)
	c.ptrTmpCount++
	name := fmt.Sprintf("_default%d", c.ptrTmpCount)
	c.write("var %s %s = %s", name, compiledFunctionGoType(&copy, c.models), value)
	return "&" + name
}

// Write directly to the output buffer without allocating an intermediate statement.
func (c *compiler) writeErrorReturn(indent, expression string) {
	if c.valueClosure {
		c.writeClosureError(indent, expression)
		return
	}
	if c.inAsync {
		c.write("%sluxolog.Error((%s).Error())", indent, expression)
		c.write("%sreturn", indent)
		return
	}
	c.b.WriteString(c.indent)
	c.b.WriteString(indent)
	if c.inFunction && c.functionResult != nil {
		c.b.WriteString("return _value, ")
	} else {
		c.b.WriteString("return ")
	}
	c.b.WriteString(expression)
	c.b.WriteByte('\n')
}

func (c *compiler) writeErrorGuard() {
	c.write("if err != nil {")
	c.writeErrorReturn("\t", "err")
	c.write("}")
}

func (c *compiler) dbPackage() string {
	if c.generator == nil {
		return DriverPG.DriverPkg()
	}
	return c.generator.dbPkg
}

// compileScopeExpr compiles a scope declaration's expression into Where conditions.
// scope published = where(status == "PUBLISHED") → .Where(ModelWhere.Status.Eq("PUBLISHED"))
func (c *compiler) compileScopeExpr(b *strings.Builder, modelName string, scope *ast.ScopeDecl) {
	chain := flattenChain(scope.Expr)
	for _, link := range chain {
		// Resolve method name: from link.method (chain) or link.expr (standalone call)
		method := link.method
		if method == "" {
			if ident, ok := link.expr.(*ast.Ident); ok {
				method = ident.Name
			}
		}
		switch method {
		case "where":
			c.compileWhereChain(b, modelName, link.args, b.Len() == 0)
		case "orderBy":
			seedFindBuilder(b, modelName)
			c.compileOrderByChain(b, modelName, link.args)
		case "limit":
			if len(link.args) > 0 {
				val := c.compileExpr(link.args[0].Value)
				fmt.Fprintf(b, ".Limit(%s)", val)
			}
		}
	}
}

// chainLink represents one link in a method chain.
type chainLink struct {
	method string
	args   []*ast.NamedArg
	expr   ast.Expr // for the root node
}

// flattenChain extracts Model.where(...).first() into [Ident(Model), {where, args}, {first, []}]
func flattenChain(expr ast.Expr) []chainLink {
	var chain []chainLink
	for {
		switch e := expr.(type) {
		case *ast.CallExpr:
			if member, ok := e.Func.(*ast.MemberExpr); ok {
				link := chainLink{method: member.Field, args: e.Args}
				if member.Field == "select" || inferred.ModelTerminalType(member.Field) != "" {
					link.expr = e
				}
				chain = append([]chainLink{link}, chain...)
				expr = member.Object
				continue
			}
			if model, method := functionFindModel(e); model != nil {
				return append([]chainLink{{expr: model}, {method: method, args: e.Args[1:]}}, chain...)
			}
			// Direct call like ErrorName(args)
			chain = append([]chainLink{{expr: e.Func, args: e.Args}}, chain...)
			return chain
		case *ast.MemberExpr:
			chain = append([]chainLink{{method: e.Field}}, chain...)
			expr = e.Object
			continue
		default:
			chain = append([]chainLink{{expr: expr}}, chain...)
			return chain
		}
	}
}

// compileModelChain compiles Model.method() chains to Go code.
func (c *compiler) compileModelChain(modelName string, links []chainLink) string {
	var b strings.Builder

	// @scope injection: prepend scope where conditions before user's chain
	scopeInjected := false
	if c.api != nil {
		for _, d := range c.api.Directives {
			if d.Name == "scope" {
				for _, arg := range d.Args {
					scopeName := ""
					if arg.Name != "" {
						scopeName = arg.Name
					} else if ident, ok := arg.Value.(*ast.Ident); ok {
						scopeName = ident.Name
					}
					if scopeName == "" {
						continue
					}
					if m, ok := c.models[modelName]; ok {
						for _, s := range m.Scopes {
							if s.Name == scopeName {
								c.compileScopeExpr(&b, modelName, s)
								scopeInjected = true
							}
						}
					}
				}
			}
		}
	}

	for i, link := range links {
		if inferred.ModelTerminalType(link.method) == "" {
			c.captureQueryReceiver(&b, link.args)
		}
		// Terminal methods — return immediately
		if done := c.compileTerminalMethod(&b, modelName, link); done {
			return b.String()
		}
		// Modifier methods — append to chain and continue
		c.compileModifierMethod(&b, modelName, link, i, len(links), scopeInjected)
	}
	return b.String()
}

// Evaluate the preceding query before lowering side effects in the next call.
func (c *compiler) captureQueryReceiver(b *strings.Builder, args []*ast.NamedArg) {
	if b.Len() == 0 || lastEffectfulArgument(args) < 0 {
		return
	}
	c.resultTmp++
	name := fmt.Sprintf("_query%d", c.resultTmp)
	c.write("%s := %s", name, b.String())
	b.Reset()
	b.WriteString(name)
}

// compileModifierMethod handles non-terminal method links in a model chain.
func (c *compiler) compileModifierMethod(b *strings.Builder, modelName string, link chainLink, i, totalLinks int, scopeInjected bool) {
	if b.Len() == 0 {
		switch link.method {
		case "select", "orderBy", "limit", "offset":
			fmt.Fprintf(b, "app.%s.Where()", modelName)
		}
	}
	switch link.method {
	case "where":
		c.compileWhereChain(b, modelName, link.args, i == 0 && !scopeInjected)
	case "create":
		c.compileCreateLink(b, modelName, link, i == totalLinks-1)
	case "select":
		selector := "select" + modelName + "SQLColumns"
		selection := c.clientSelection
		if selection == "" {
			selection = "req.Select"
		}
		if projection, ok := c.querySelections[link.expr]; ok {
			if len(projection.columns) != 0 {
				fmt.Fprintf(b, ".Select(%s...)", c.compiledQueryColumns(modelName, projection, selection))
				return
			}
			selection = c.compiledProjectionSelection(projection)
			if projection.private || c.clientSelection == functionSelectionParameter && projection.client && hasPrivateSQLFields(c.models[modelName]) {
				selector = "(*" + modelName + ")(nil).loadSQLColumns"
			}
		}
		fmt.Fprintf(b, ".Select(%s(%s)...)", selector, selection)
	case "orderBy":
		c.compileOrderByChain(b, modelName, link.args)
	case "limit", "offset":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
			// Cast to int if the value is a variable (int64 from ParamInt)
			if _, isLit := link.args[0].Value.(*ast.Literal); !isLit {
				val = "int(" + val + ")"
			}
			fmt.Fprintf(b, ".%s(%s)", str.Capitalize(link.method), val)
		}
	default:
		// Unknown method — ensure model client is seeded
		if i == 0 {
			fmt.Fprintf(b, "app.%s", modelName)
		}
		fmt.Fprintf(b, ".%s(", str.Capitalize(link.method))
		var args []string
		for _, a := range link.args {
			args = append(args, c.compileExpr(a.Value))
		}
		b.WriteString(strings.Join(args, ", "))
		b.WriteString(")")
	}
}

// compileCreateLink compiles a create() link with @hash and nullable field handling.
func (c *compiler) compileCreateLink(b *strings.Builder, modelName string, link chainLink, isLast bool) {
	m := c.models[modelName]
	fmt.Fprintf(b, "app.%s.Create()", modelName)
	lastEffect := lastEffectfulArgument(link.args)
	for index, arg := range link.args {
		val, prepared, hashed := c.compileMutationArgument(m, arg, index < lastEffect)
		// Wrap with & if the model field is nullable (SetXxx expects pointer)
		// but skip if the value is already a pointer (tracked via vars nullable flag)
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && f.Type != nil && f.Type.Nullable {
					// Check if value expression is already a pointer (nullable)
					// Uses NullableTag from semantic analyzer — zero guessing
					alreadyPtr := prepared || hashed || arg.Value.IsNullable()
					if !alreadyPtr {
						// Also check vars map for API params
						if ident, ok := arg.Value.(*ast.Ident); ok {
							if vt, ok := c.vars[ident.Name]; ok && vt.nullable {
								alreadyPtr = true
							}
						}
					}
					if !alreadyPtr {
						// Plain identifiers (and hashed temp vars) are
						// addressable; any other expression (literal, call
						// like now(), binary op) must be hoisted into a temp
						// var first — Go rejects taking the address of a
						// non-addressable value.
						_, isIdent := arg.Value.(*ast.Ident)
						if isIdent || hashed {
							val = "&" + val
						} else {
							tmp := fmt.Sprintf("%sPtr%d", arg.Name, c.ptrTmpCount)
							c.ptrTmpCount++
							c.write("%s := %s", tmp, val)
							val = "&" + tmp
						}
					}
					break
				}
			}
		}
		setter := "Set"
		if prepared || hashed {
			setter = "setPrepared"
		}
		fmt.Fprintf(b, ".%s%s(%s)", setter, str.Capitalize(arg.Name), val)
	}
	if isLast {
		b.WriteString(".Exec(ctx)")
	}
}

// compileWhereChain compiles a where() link into the query builder.
// Supports both named args (id: userId → ModelWhere.Id.Eq(userId))
// and expression args (it.email == email → ModelWhere.Email.Eq(email)).
func (c *compiler) compileWhereChain(b *strings.Builder, modelName string, args []*ast.NamedArg, isFirst bool) {
	lastEffect := lastEffectfulArgument(args)
	if isFirst {
		fmt.Fprintf(b, "app.%s.Where(", modelName)
	} else {
		b.WriteString(".Where(")
	}
	for i, arg := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		var condition string
		if arg.Name != "" {
			// Named arg: where(id: userId) → ModelWhere.Id.Eq(userId)
			condition = c.compileWhereComparison(modelName, arg.Name, "Eq", arg.Value)
		} else {
			condition = c.compileWhereArg(modelName, arg.Value)
		}
		if i < lastEffect {
			condition = c.captureExpressionValue(arg.Value, condition, "")
		}
		b.WriteString(condition)
	}
	b.WriteByte(')')
}

// compileWhereArg compiles a where condition:
// it.email == email → ModelWhere.Email.Eq(email)
// email == email    → ModelWhere.Email.Eq(email) (legacy, both same name)
// it.title.contains("x") → ModelWhere.Title.Match("x")  (@search field)
// it.title.contains("x") → ModelWhere.Title.Like("%" + lux.EscapeLike("x") + "%")  (non-search)
func (c *compiler) compileWhereArg(modelName string, expr ast.Expr) string {
	receiver := "it"
	if lambda, ok := expr.(*ast.LambdaExpr); ok {
		if body, parameter := lambda.SingleExpression(); body != nil {
			expr, receiver = body, parameter
		}
	}
	return c.compileWherePredicate(modelName, expr, receiver)
}

func (c *compiler) compileWherePredicate(modelName string, expr ast.Expr, receiver string) string {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == "!" {
		if field := c.whereBooleanField(modelName, unary.Value, receiver); field != "" {
			return field + ".IsFalse()"
		}
		return "lux.Not(" + c.compileWherePredicate(modelName, unary.Value, receiver) + ")"
	}
	// Handle method call conditions: it.field.contains/startsWith/endsWith(arg)
	if result := c.compileWhereMethodCall(modelName, expr, receiver); result != "" {
		return result
	}

	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return c.compileWhereBoolean(modelName, expr, receiver)
	}
	if bin.Op == "&&" || bin.Op == "||" {
		combine := "AllOf"
		if bin.Op == "||" {
			combine = "AnyOf"
		}
		left := c.compileWherePredicate(modelName, bin.Left, receiver)
		if expressionMayHaveEffects(bin.Right) {
			left = c.captureExpressionValue(bin.Left, left, "")
		}
		right := c.compileWherePredicate(modelName, bin.Right, receiver)
		return fmt.Sprintf("lux.%s(%s, %s)", combine, left, right)
	}

	field := wherePredicateField(bin.Left, receiver)
	if bin.Op == "in" {
		return c.compileWhereMembership(modelName, field, bin)
	}
	if literal, ok := bin.Right.(*ast.Literal); ok && literal.Kind == token.Null {
		switch bin.Op {
		case "==":
			return fmt.Sprintf("%sWhere.%s.IsNull()", modelName, str.Capitalize(field))
		case "!=":
			return fmt.Sprintf("%sWhere.%s.IsNotNull()", modelName, str.Capitalize(field))
		}
	}

	op := ""
	switch bin.Op {
	case "==":
		op = "Eq"
	case "!=":
		op = "Neq"
	case ">":
		op = "Gt"
	case ">=":
		op = "Gte"
	case "<":
		op = "Lt"
	case "<=":
		op = "Lte"
	default:
		return c.compileExpr(expr)
	}

	return c.compileWhereComparison(modelName, field, op, bin.Right)
}

// Enum parameters and computed values use the same SQL representation as literals.
func (c *compiler) compileWhereComparison(model, field, operator string, expr ast.Expr) string {
	value := c.compileExpr(expr)
	prefix, suffix := "", ""
	if c.isWhereEnum(expr) {
		prefix, suffix = "string(", ")"
	}
	return model + "Where." + str.Capitalize(field) + "." + operator + "(" + prefix + value + suffix + ")"
}

func (c *compiler) isWhereEnum(expr ast.Expr) bool {
	if c.enums[expr.GetTypeTag()] || c.isEnumExpr(expr) {
		return true
	}
	if member, ok := expr.(*ast.MemberExpr); ok {
		ident, ok := member.Object.(*ast.Ident)
		return ok && c.enums[ident.Name]
	}
	return false
}

// compileWhereMethodCall handles string method calls in where context:
//
//	it.title.contains("x")  → ModelWhere.Title.Match(x)   if @search
//	it.title.contains("x")  → ModelWhere.Title.Like("%" + lux.EscapeLike(x) + "%")  otherwise
//	it.name.startsWith("x") → ModelWhere.Name.Like(lux.EscapeLike(x) + "%")
//	it.name.endsWith("x")   → ModelWhere.Name.Like("%" + lux.EscapeLike(x))
//
// Returns "" if expr is not a recognized method call pattern.
func (c *compiler) compileWhereMethodCall(modelName string, expr ast.Expr, receiver string) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return ""
	}

	method := member.Field
	if method != "contains" && method != "startsWith" && method != "endsWith" {
		return ""
	}

	fieldName := wherePredicateField(member.Object, receiver)
	if fieldName == "" {
		return ""
	}
	argVal := c.compileExpr(call.Args[0].Value)

	// Check if the field has @search directive
	if method == "contains" && c.fieldHasSearch(modelName, fieldName) {
		return fmt.Sprintf("%sWhere.%s.Match(%s)", modelName, str.Capitalize(fieldName), argVal)
	}

	// Non-search: generate LIKE conditions
	switch method {
	case "contains":
		return fmt.Sprintf(`%sWhere.%s.Like("%%" + lux.EscapeLike(%s) + "%%")`, modelName, str.Capitalize(fieldName), argVal)
	case "startsWith":
		return fmt.Sprintf(`%sWhere.%s.Like(lux.EscapeLike(%s) + "%%")`, modelName, str.Capitalize(fieldName), argVal)
	case "endsWith":
		return fmt.Sprintf(`%sWhere.%s.Like("%%" + lux.EscapeLike(%s))`, modelName, str.Capitalize(fieldName), argVal)
	}
	// Unreachable: the method guard above admits only the three switch cases.
	return ""
}

// fieldHasSearch checks if a field on the given model has the @search directive.
func (c *compiler) fieldHasSearch(modelName, fieldName string) bool {
	m, ok := c.models[modelName]
	if !ok {
		return false
	}
	for _, f := range m.Fields {
		if f.Name == fieldName {
			return hasDirective(f.Directives, "search")
		}
	}
	return false
}

// compileBinary: a + b, a == b, etc.
func (c *compiler) compileBinary(e *ast.BinaryExpr) string {
	if e.Op == "is" {
		return c.compileTypeTest(e)
	}
	left := c.compileBooleanValue(e.Left)
	if e.Op == "<-" {
		return left + " <- " + c.compileChannelSendValue(e.Left, e.Right)
	}
	if e.Op == "&&" || e.Op == "||" {
		return c.compileLogicalBinary(e, left)
	}
	if expressionMayHaveEffects(e.Right) {
		leftType := c.goTypeForExpr(e.Left)
		if c.models[e.Left.GetTypeTag()] != nil && !e.Left.IsListType() {
			leftType = "*" + e.Left.GetTypeTag()
		}
		left = c.captureExpressionValue(e.Left, left, leftType)
	}
	left = groupBinaryOperand(e.Left, left)
	right := groupBinaryOperand(e.Right, c.compileBooleanValue(e.Right))
	if expression, ok := c.compileScalarComparison(e, left, right); ok {
		return expression
	}
	if expression, ok := c.compileIntegerArithmetic(e, left, right); ok {
		return expression
	}
	if e.Left.GetTypeTag() == "Decimal" && e.Right.GetTypeTag() == "Decimal" {
		if method := decimalArithmeticMethod(e.Op); method != "" {
			return c.compileDecimalArithmetic(left, right, method)
		}
	}
	// DateTime +/- Duration → time.Add(duration) / time.Add(-duration)
	// Only rewrite when left is DateTime (via TypeTag) and right is Duration
	if e.Left.GetTypeTag() == "DateTime" && isDurationExpr(e.Right) && (e.Op == "+" || e.Op == "-") {
		return compileDateOffset(left, right, e.Op)
	}
	if expression, ok := compileFloatArithmetic(e, left, right); ok {
		return expression
	}
	return fmt.Sprintf("%s %s %s", left, e.Op, right)
}

// Structured scalars compare their values rather than Go representation fields.
func (c *compiler) compileScalarComparison(expr *ast.BinaryExpr, left, right string) (string, bool) {
	if expression, ok := c.compileSealedComparison(expr, left, right); ok {
		return expression, true
	}
	if expr.Left.GetTypeTag() != expr.Right.GetTypeTag() || expr.Left.IsListType() || expr.Right.IsListType() {
		return "", false
	}
	var goType, order string
	switch expr.Left.GetTypeTag() {
	case "DateTime":
		goType, order = "time.Time", "Compare"
	case "Decimal":
		return compileDecimalComparison(expr, left, right)
	default:
		return c.compileNullablePrimitiveComparison(expr, left, right)
	}
	if expr.Left.IsNullable() || expr.Right.IsNullable() {
		if expr.Op == "==" || expr.Op == "!=" {
			return compileNullableScalarEquality(expr, left, right, goType, "Equal"), true
		}
		return "", false
	}
	switch expr.Op {
	case "==":
		return left + ".Equal(" + right + ")", true
	case "!=":
		return "!" + left + ".Equal(" + right + ")", true
	case "<", "<=", ">", ">=":
		return left + "." + order + "(" + right + ") " + expr.Op + " 0", true
	default:
		return "", false
	}
}

func (c *compiler) compileNullablePrimitiveComparison(expr *ast.BinaryExpr, left, right string) (string, bool) {
	if expr.Op != "==" && expr.Op != "!=" || !expr.Left.IsNullable() && !expr.Right.IsNullable() {
		return "", false
	}
	switch expr.Left.GetTypeTag() {
	case "Int", "Float", "String", "Boolean", "Duration", "UUID":
	default:
		if !c.enums[expr.Left.GetTypeTag()] {
			return "", false
		}
	}
	goType := strings.TrimPrefix(c.goTypeForExpr(expr.Left), "*")
	return compileNullableScalarEquality(expr, left, right, goType, ""), true
}

// Typed arguments evaluate each operand once before null checks and comparison.
func compileNullableScalarEquality(expr *ast.BinaryExpr, left, right, goType, method string) string {
	leftType, rightType, argument := goType, goType, "right"
	guard := ""
	if expr.Left.IsNullable() {
		leftType, guard = "*"+goType, "left != nil && "
	}
	if expr.Right.IsNullable() {
		rightType, argument = "*"+goType, "*right"
		guard += "right != nil && "
	}
	comparison := "left." + method + "(" + argument + ")"
	if method == "" || goType == "decimal.Decimal" {
		operand := "left"
		if expr.Left.IsNullable() {
			operand = "*left"
		}
		comparison = operand + " == " + argument
		if goType == "decimal.Decimal" {
			comparison = "lux.CompareDecimal(" + operand + ", " + argument + ") == 0"
		}
	}
	condition := guard + comparison
	if expr.Left.IsNullable() && expr.Right.IsNullable() {
		condition = "left == right || " + condition
	}
	if expr.Op == "!=" {
		condition = "!(" + condition + ")"
	}
	return "func(left " + leftType + ", right " + rightType + ") bool { return " + condition + " }(" + left + ", " + right + ")"
}

// time.Time arithmetic uses its static method rather than Go numeric operators.
func compileDateOffset(left, right, operator string) string {
	if operator == "-" {
		return "lux.SubDateTime(" + left + ", " + right + ")"
	}
	return left + ".Add(" + right + ")"
}

// isDurationExpr checks if an expression produces a time.Duration value.
// Matches: n.days, n.hours, n.minutes, n.seconds, n.milliseconds, duration literals
func isDurationExpr(e ast.Expr) bool {
	// Primary: use TypeTag from semantic analysis
	if e.GetTypeTag() == "Duration" {
		return true
	}
	// Fallback: duration literal (always Duration regardless of TypeTag)
	if lit, ok := e.(*ast.Literal); ok {
		return lit.Kind == token.Duration
	}
	return false
}

// compileBuiltinCall compiles built-in function calls: now(), crypto.randomHex(), etc.
func (c *compiler) compileBuiltinCall(e *ast.CallExpr) string {
	// now() → time.Now()
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "now" && c.functions[ident.Name] == nil && !c.nativeFunctions[ident.Name] && c.vars[ident.Name].name == "" {
		return "time.Now()"
	}
	// crypto.randomHex(n) / crypto.randomBytes(n)
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok || ident.Name != "crypto" {
		return ""
	}
	if member.Field != "randomHex" && member.Field != "randomBytes" {
		return ""
	}
	n := "32"
	if len(e.Args) > 0 {
		n = c.compileExpr(e.Args[0].Value)
	}
	return c.compileRandomBuiltin(member.Field, n)
}

// Preserve the DSL Int boundary before adapting to the supported 64-bit Go int ABI.
func (c *compiler) compileRandomBuiltin(method, size string) string {
	c.resultTmp++
	prefix := "_hex"
	function := "RandomHex"
	if method == "randomBytes" {
		prefix, function = "_randomBytes", "RandomBytes"
	}
	value := fmt.Sprintf("%s%d", prefix, c.resultTmp)
	errorName := fmt.Sprintf("%sErr%d", prefix, c.resultTmp)
	sizeName := fmt.Sprintf("_randomSize%d", c.resultTmp)
	c.write("var %s int64 = %s", sizeName, size)
	c.write("%s, %s := luxocrypto.%s(int(%s))", value, errorName, function, sizeName)
	c.write("if %s != nil {", errorName)
	c.writeErrorReturn("\t", errorName)
	c.write("}")
	return value
}

// durationUnits maps Luxo duration property names to Go time constants.
var durationUnits = map[string]string{
	"days":         "24 * time.Hour",
	"hours":        "time.Hour",
	"minutes":      "time.Minute",
	"seconds":      "time.Second",
	"milliseconds": "time.Millisecond",
}

// compileUnary: throw expr, !expr, -expr
func (c *compiler) compileUnary(e *ast.UnaryExpr) string {
	if e.Op == "throw" {
		return c.compileThrowExpr(e.Value)
	}
	if e.Op == "?" {
		if call, ok := e.Value.(*ast.CallExpr); ok {
			if member, ok := call.Func.(*ast.MemberExpr); ok && member.SafeCall {
				return c.compileSafeAccessValue(member, call, e)
			}
		}
		operand := c.compileExpr(e.Value)
		c.resultTmp++
		name := fmt.Sprintf("_result%d", c.resultTmp)
		c.write("%s, err := %s", name, operand)
		c.writeErrorGuard()
		if c.nativeValueModelResult(e.Value) {
			return "(&" + name + ")"
		}
		return name
	}
	var operand string
	if e.Op == "!" {
		operand = c.compileBooleanValue(e.Value)
	} else {
		operand = c.compileExpr(e.Value)
	}
	if _, binary := e.Value.(*ast.BinaryExpr); binary {
		operand = "(" + operand + ")"
	}
	if e.Op == "-" {
		if expression, checked := c.compileIntegerNegation(e, operand); checked {
			return expression
		}
		if e.Value.GetTypeTag() == "Decimal" {
			return operand + ".Neg()"
		}
		if nested, ok := e.Value.(*ast.UnaryExpr); ok && nested.Op == "-" {
			operand = "(" + operand + ")"
		}
	}
	return compileUnaryOperation(e, operand)
}

// compileThrowExpr compiles the expression after `throw`:
// throw error.NotFound → errors.NotFound
// throw DuplicateEmail(email: email) → NewDuplicateEmail(email)
func (c *compiler) compileThrowExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.MemberExpr:
		// error.NotFound → errors.NotFound
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "error" && c.vars[ident.Name].name == "" {
			return compileBuiltinError(e.Field)
		}
		return c.compileExpr(expr)
	case *ast.Ident:
		// AlreadySetup → NewAlreadySetup() (custom error without args, PascalCase)
		if len(e.Name) > 0 && e.Name[0] >= 'A' && e.Name[0] <= 'Z' {
			return fmt.Sprintf("New%s()", e.Name)
		}
		return c.compileExpr(expr)
	case *ast.CallExpr:
		// DuplicateEmail(email: email) → NewDuplicateEmail(email)
		if ident, ok := e.Func.(*ast.Ident); ok {
			if declaration := e.ErrorConstructor; declaration != nil {
				args := c.compileDeclaredFunctionArguments(e, &ast.FnDecl{Name: declaration.Name, Params: declaration.Fields})
				return fmt.Sprintf("New%s(%s)", declaration.Name, strings.Join(args, ", "))
			}
			var args []string
			for _, a := range e.Args {
				args = append(args, c.compileExpr(a.Value))
			}
			return fmt.Sprintf("New%s(%s)", ident.Name, strings.Join(args, ", "))
		}
		return c.compileExpr(expr)
	default:
		return c.compileExpr(expr)
	}
}

// resolveQueryType determines the return type of a model query chain.
func (c *compiler) resolveQueryType(expr ast.Expr) valType {
	if call, ok := expr.(*ast.CallExpr); ok {
		if plan := c.instanceSaveCall(call); plan != nil {
			return valType{isModel: true, name: plan.model.Name}
		}
		if call.Group != nil {
			return valType{name: call.Group.Result, isList: true}
		}
		if model, result := c.modelTerminalFunctionModel(call); model != nil {
			return valType{name: result}
		}
		// Instance mutations return affected-row counts.
		if member, ok := call.Func.(*ast.MemberExpr); ok {
			if ident, ok := member.Object.(*ast.Ident); ok {
				if vt, ok := c.vars[ident.Name]; ok && vt.isModel && !vt.isList {
					switch member.Field {
					case "update", "delete":
						return valType{name: "Int"}
					}
				}
			}
		}
	}

	chain := flattenChain(expr)
	if len(chain) < 2 {
		return valType{}
	}
	root := chain[0]
	if ident, ok := root.expr.(*ast.Ident); ok {
		if modelName := c.queryRootModel(ident.Name); modelName != "" {
			last := chain[len(chain)-1]
			switch last.method {
			case "first", "find", "findMany", "findFirst":
				return valType{isModel: true, isList: findReturnsList(last), name: modelName}
			case "load":
				return loadQueryValueType(modelName, last.args)
			case "all":
				return valType{isModel: true, isList: true, name: modelName}
			case "create", "exec":
				return valType{isModel: true, name: modelName}
			case "exists":
				return valType{name: "Boolean"}
			case "count":
				return valType{name: "Int"}
			case "update":
				return valType{name: "Int"} // rows affected
			case "delete", "deleteMany", "updateMany":
				return valType{name: "Int"} // rows affected
			case "sum", "avg", "min", "max":
				return c.aggregateValueType(expr, modelName, last)
			}
		}
	}
	return valType{}
}

// PK loads are nullable values; named-key loads return non-null lists.
func loadQueryValueType(modelName string, arguments []*ast.NamedArg) valType {
	for _, argument := range arguments {
		if argument.Name != "" {
			return valType{isModel: true, isList: true, name: modelName}
		}
	}
	return valType{isModel: true, nullable: true, name: modelName}
}

// isModelQuery checks if an expression is a Model query chain (returns (*T, error)).
func (c *compiler) isModelQuery(expr ast.Expr) bool {
	// Non-chain values cannot produce query tuples; do not allocate chain metadata.
	switch expr.(type) {
	case *ast.CallExpr, *ast.MemberExpr:
	default:
		return false
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if c.instanceSaveCall(call) != nil {
			return true
		}
		if call.Group != nil {
			return true
		}
		if model, _ := c.modelTerminalFunctionModel(call); model != nil {
			return true
		}
		// Instance method: variable.delete(), variable.update(...)
		if member, ok := call.Func.(*ast.MemberExpr); ok {
			if member.SafeCall {
				return false
			}
			if ident, ok := member.Object.(*ast.Ident); ok {
				if vt, ok := c.vars[ident.Name]; ok && vt.isModel && !vt.isList {
					switch member.Field {
					case "delete", "update", "save":
						return true
					}
				}
			}
		}
	}

	chain := flattenChain(expr)
	if len(chain) < 2 {
		return false
	}
	root := chain[0]
	if ident, ok := root.expr.(*ast.Ident); ok {
		if c.queryRootModel(ident.Name) != "" {
			// Check terminal method
			last := chain[len(chain)-1]
			switch last.method {
			case "first", "all", "create", "exec", "find", "findMany", "findFirst", "load", "exists", "update", "updateMany", "delete", "deleteMany", "count",
				"sum", "avg", "min", "max":
				return true
			}
		}
	}
	return false
}

func isCountedPaginationQuery(expr ast.Expr) bool {
	chain := flattenChain(expr)
	return len(chain) >= 2 && (chain[len(chain)-1].method == "all" || findReturnsList(chain[len(chain)-1]))
}

// compileUpdateChain sends statically prepared values through the private query writer.
// Checks @hash fields and auto-hashes values before update.
func (c *compiler) compileUpdateChain(b *strings.Builder, modelName string, args []*ast.NamedArg) {
	model := c.models[modelName]
	var sets []string
	lastEffect := lastEffectfulArgument(args)
	for index, arg := range args {
		sets = append(sets, c.compileUpdateSet(model, arg, index < lastEffect))
	}
	appendUpdateSets(b, sets)
}

func (c *compiler) compileUpdateSet(model *ast.ModelDecl, arg *ast.NamedArg, capture bool) string {
	value, _, _ := c.compileMutationArgument(model, arg, capture)
	return fmt.Sprintf("lux.SetField{Col: %q, Val: %s}", c.generator.sqlColumn(arg.Name), value)
}

func appendUpdateSets(b *strings.Builder, sets []string) {
	b.WriteString(".updatePrepared(ctx")
	if len(sets) > 0 {
		b.WriteString(", ")
		b.WriteString(strings.Join(sets, ", "))
	}
	b.WriteByte(')')
}

// Keep predicate and assignment evaluation in source order before executing SQL.
func (c *compiler) compileBulkUpdate(b *strings.Builder, modelName string, args []*ast.NamedArg, position token.Position) {
	c.validateBulkUpdate(c.models[modelName], args, position)
	var sets []string
	lastEffect := lastEffectfulArgument(args)
	for index, arg := range args {
		c.captureQueryReceiver(b, []*ast.NamedArg{arg})
		if arg.Name == "where" {
			c.compileBulkMutationBase(b, modelName, arg.Value)
		} else {
			sets = append(sets, c.compileUpdateSet(c.models[modelName], arg, index < lastEffect))
		}
	}
	if b.Len() == 0 {
		c.compileBulkMutationBase(b, modelName, nil)
	}
	appendUpdateSets(b, sets)
}

func (c *compiler) compileBulkMutationBase(b *strings.Builder, modelName string, where ast.Expr) {
	if b.Len() > 0 && where == nil {
		return
	}
	if b.Len() == 0 {
		fmt.Fprintf(b, "app.%s", modelName)
	}
	b.WriteString(".Where(")
	if where != nil {
		b.WriteString(c.compileWhereArg(modelName, where))
	}
	b.WriteByte(')')
}

// isHashField checks if a model field has @hash directive.
func isHashField(model *ast.ModelDecl, fieldName string) bool {
	for _, f := range model.Fields {
		if f.Name == fieldName {
			for _, d := range f.Directives {
				if d.Name == "hash" {
					return true
				}
			}
		}
	}
	return false
}

// compileTerminalMethod handles chain methods that terminate the chain and return a result.
// compileLoad generates DataLoader calls for Model.load(...).
// Supports three patterns:
//   - PK load:         User.load(id)         → app.loaders.ExtendUser.Load(ctx, id, nil)
//   - FK load:         Post.load(userId: x)  → app.loaders.PostByUserId.Load(ctx, x, nil)
//   - Multi-condition: Post.load(userId: x, type: y) → app.loaders.PostByUserIdAndType.Load(ctx, PostByUserIdAndTypeKey{x, y}, nil)
func (c *compiler) compileLoad(b *strings.Builder, modelName string, args []*ast.NamedArg) {
	if len(args) == 0 {
		return
	}

	// Check if this is a PK load (no named args) or FK/multi-condition load
	hasNamedArgs := false
	for _, arg := range args {
		if arg.Name != "" {
			hasNamedArgs = true
			break
		}
	}

	if !hasNamedArgs {
		// PK load: User.load(id) → app.loaders.ExtendUser.Load(ctx, id, fields)
		val := c.compileExpr(args[0].Value)
		fmt.Fprintf(b, "app.loaders.Extend%s.Load(ctx, %s, %s)", modelName, val, c.compiledLoadSelection(args))
		return
	}

	// Collect named args
	var names []string
	var vals []string
	for _, arg := range args {
		names = append(names, arg.Name)
		vals = append(vals, c.compileExpr(arg.Value))
	}

	// Build loader name: PostByUserIdAndType
	loaderName := modelName + "By"
	for i, name := range names {
		if i > 0 {
			loaderName += "And"
		}
		loaderName += str.Capitalize(name)
	}

	if len(names) == 1 {
		// Single FK: Post.load(userId: x) → app.loaders.PostByUserId.Load(ctx, x, fields)
		fmt.Fprintf(b, "app.loaders.%s.Load(ctx, %s, %s)", loaderName, vals[0], c.compiledLoadSelection(args))
	} else {
		// Composite key: Post.load(userId: x, type: y)
		// → app.loaders.PostByUserIdAndType.Load(ctx, PostByUserIdAndTypeKey{UserId: x, Type: y}, nil)
		keyType := loaderName + "Key"
		fmt.Fprintf(b, "app.loaders.%s.Load(ctx, %s{", loaderName, keyType)
		for i, name := range names {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%s: %s", str.Capitalize(name), vals[i])
		}
		fmt.Fprintf(b, "}, %s)", c.compiledLoadSelection(args))
	}
}

func (c *compiler) compiledLoadSelection(args []*ast.NamedArg) string {
	if len(args) == 0 {
		return "nil"
	}
	projection, exists := c.loadSelections[args[0]]
	if !exists {
		return "nil"
	}
	return c.compiledProjectionSelection(projection)
}

func (c *compiler) compiledProjectionSelection(projection loadProjection) string {
	if !projection.client {
		return projection.logic
	}
	client := c.clientSelection
	if client == "" {
		client = "req.Select"
	}
	client = compiledClientSelection(projection, client)
	if projection.logic == "[]*selection.Field{}" {
		return client
	}
	if single := projection.singleColumn(); single != "" {
		return "selection.EnsureField(" + client + ", " + strconv.Quote(single) + ")"
	}
	return "selection.Merge(" + client + ", " + projection.logic + ")"
}

func (c *compiler) isRemoteModel(modelName string) bool {
	if c.generator == nil || c.generator.events == nil || c.api == nil {
		return false
	}
	owner := c.generator.events.ModelModule[modelName]
	source := moduleNameFromFile(c.api.Pos.File)
	return owner != "" && source != "" && owner != source
}

func directLoadModel(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "load" {
		return "", false
	}
	model, ok := member.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	return model.Name, true
}

func validModelSelectionPath(modelName string, path []string, models map[string]*ast.ModelDecl) bool {
	for index, name := range path {
		model := models[modelName]
		field := modelField(model, name)
		if field == nil {
			return false
		}
		if index+1 < len(path) {
			modelName = field.Type.Name
		}
	}
	return len(path) > 0
}

func modelField(model *ast.ModelDecl, name string) *ast.FieldDecl {
	if model == nil {
		return nil
	}
	for _, field := range model.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func pruneSelectionPrefixes(paths [][]string) [][]string {
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) > len(paths[j])
		}
		return strings.Join(paths[i], "\x00") < strings.Join(paths[j], "\x00")
	})
	result := make([][]string, 0, len(paths))
	for _, path := range paths {
		if !selectionPathCovered(path, result) {
			result = append(result, path)
		}
	}
	return result
}

func selectionPathCovered(path []string, selected [][]string) bool {
	for _, candidate := range selected {
		if len(candidate) < len(path) {
			continue
		}
		match := true
		for index := range path {
			if candidate[index] != path[index] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

type compiledSelectionNode struct {
	name     string
	children map[string]*compiledSelectionNode
}

func compileSelectionLiteral(paths [][]string) string {
	roots := make(map[string]*compiledSelectionNode)
	for _, path := range paths {
		insertCompiledSelection(roots, path)
	}
	return "[]*selection.Field{" + renderCompiledSelection(roots) + "}"
}

func insertCompiledSelection(nodes map[string]*compiledSelectionNode, path []string) {
	for _, name := range path {
		node := nodes[name]
		if node == nil {
			node = &compiledSelectionNode{name: name, children: make(map[string]*compiledSelectionNode)}
			nodes[name] = node
		}
		nodes = node.children
	}
}

func renderCompiledSelection(nodes map[string]*compiledSelectionNode) string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		node := nodes[name]
		part := fmt.Sprintf("{Name: %q", node.name)
		if len(node.children) > 0 {
			part += ", Children: []*selection.Field{" + renderCompiledSelection(node.children) + "}"
		}
		parts = append(parts, part+"}")
	}
	return strings.Join(parts, ", ")
}

func (c *compiler) compileTerminalMethod(b *strings.Builder, modelName string, link chainLink) bool {
	switch link.method {
	case "find", "findMany", "findFirst":
		c.compileFindChain(b, modelName, link)
		return true
	case "load":
		c.compileLoad(b, modelName, link.args)
		return true
	}
	// Check if this is a terminal method first
	isTerminal := false
	switch link.method {
	case "delete", "deleteMany", "all", "first", "exists", "exec", "count", "update", "updateMany", "upsert", "save", "sum", "avg", "min", "max":
		isTerminal = true
	}
	if !isTerminal {
		return false
	}
	if link.method == "deleteMany" {
		c.compileDeleteMany(b, modelName, link)
		return true
	}
	if link.method == "count" || link.method == "exists" {
		if where := c.compileQueryWhere(b, link); where != nil {
			c.compileBulkMutationBase(b, modelName, where)
		}
	}
	if link.method == "updateMany" {
		var position token.Position
		if link.expr != nil {
			position = link.expr.GetPos()
		}
		c.compileBulkUpdate(b, modelName, link.args, position)
		return true
	}
	// Seed builder if empty (terminal-only chain without prior modifier)
	if b.Len() == 0 {
		fmt.Fprintf(b, "app.%s", modelName)
		if link.method == "all" || link.method == "first" {
			b.WriteString(".Where()")
		}
	}
	return c.compileTerminalResult(b, modelName, link)
}

func (c *compiler) compileTerminalResult(b *strings.Builder, modelName string, link chainLink) bool {
	switch link.method {
	case "delete":
		if m, ok := c.models[modelName]; ok && isSoftDelete(m) {
			fmt.Fprintf(b, ".SoftDelete(ctx)")
		} else {
			fmt.Fprintf(b, ".Delete(ctx)")
		}
		return true
	case "all":
		if c.pagingQuery || c.pagePlan == nil && c.paginate {
			b.WriteString(".Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)")
		} else {
			b.WriteString(".All(ctx)")
		}
		return true
	case "first":
		b.WriteString(".First(ctx)")
		return true
	case "exists", "exec", "count":
		fmt.Fprintf(b, ".%s(ctx)", str.Capitalize(link.method))
		return true
	case "update", "upsert":
		c.compileUpdateChain(b, modelName, link.args)
		return true
	case "save":
		b.WriteString(".Exec(ctx)")
		return true
	case "sum", "avg", "min", "max":
		if len(link.args) > 0 {
			col := aggregateFieldName(link.args[0].Value)
			if col == "" {
				col = c.compileExpr(link.args[0].Value)
			}
			fmt.Fprintf(b, ".%s(ctx, %q)", str.Capitalize(c.aggregateMethod(modelName, link, col)), c.generator.sqlColumn(col))
		} else {
			fmt.Fprintf(b, ".%s(ctx)", str.Capitalize(link.method))
		}
		return true
	}
	// The caller validates terminals; keep a safe result for invalid internal calls.
	return false
}

// compileOrderByChain compiles .orderBy(field.desc) → .OrderBy("field DESC")
func (c *compiler) compileOrderByChain(b *strings.Builder, model string, args []*ast.NamedArg) {
	var clauses []string
	for _, arg := range args {
		field, descending, alternate := inferred.ParseOrderExpression(arg.Value)
		if alternate != "" {
			column := modelField(c.models[model], field)
			if column == nil || column.Computed != nil || column.Type == nil || isRelationField(column, c.enums) {
				field, descending = alternate, false
			}
		}
		if field == "" {
			panic(&InternalCompilerError{Detail: fmt.Sprintf("%s: unvalidated order selector reached query lowering", arg.Value.GetPos())})
		}
		dir := "ASC"
		if descending {
			dir = "DESC"
		}
		col := c.generator.sqlColumn(field)
		clauses = append(clauses, fmt.Sprintf("%q", col+" "+dir))
	}
	fmt.Fprintf(b, ".OrderBy(%s)", strings.Join(clauses, ", "))
}

// --- Phase 2 statement compilers ---

// compileFor: for item in collection { ... } or for i in 0..10 { ... }
func (c *compiler) compileFor(s *ast.ForStmt) {
	c.withLoopScope(func() { c.compileForStatement(s) })
}

func (c *compiler) compileForStatement(s *ast.ForStmt) {
	binding := c.saveBinding(s.VarName)
	c.compileLoopHeader(s)
	if s.VarName != "" {
		c.vars[s.VarName] = c.loopElementType(s.Collection)
		c.write("\t_ = %s", c.bindings.name(s.VarName))
	}
	c.compileBlock(s.Body)
	c.write("}")
	c.restoreBinding(binding)
}

// Keep continue in the post clause, including the final Int64 value.
func (c *compiler) compileRangeLoopHeader(name string, bounds *ast.RangeExpr) {
	start := c.compileExpr(bounds.Start)
	start = c.captureExpressionValue(bounds.Start, start, "int64")
	end := c.compileExpr(bounds.End)
	end = c.captureExpressionValue(bounds.End, end, "int64")
	c.markLoopHeader()
	if literal, ok := bounds.End.(*ast.Literal); ok && literal.Kind == token.Int {
		if limit, err := strconv.ParseInt(literal.Value, 10, 64); err == nil && limit < 9223372036854775807 {
			c.write("for %s := int64(%s); %s <= %s; %s++ {", name, start, name, end, name)
			return
		}
	}
	c.resultTmp++
	more := "_rangeMore" + strconv.Itoa(c.resultTmp)
	c.write("for %s, %s := int64(%s), %s <= %s; %s; %s, %s = %s + 1, %s < %s {", name, more, start, start, end, more, name, more, name, name, end)
}

// containsYield checks yields owned by the enclosing for expression.
func containsYield(block *ast.Block) bool {
	found := false
	ast.WalkYields(block, func(*ast.YieldExpr) {
		found = true
	})
	return found
}

// containsYieldExpr checks if an expression is or contains a yield.
func containsYieldExpr(expr ast.Expr) bool {
	found := false
	ast.WalkExpressionYields(expr, func(*ast.YieldExpr) {
		found = true
	})
	return found
}

// compileForExpr compiles a for-as-expression.
// Two modes:
//   - yield mode: for item in items { if cond { yield item } } → single nullable value
//   - map mode: for item in items { item.id } → collect all into a typed slice
func (c *compiler) compileForExpr(s *ast.ForStmt) string {
	if len(s.Body.Stmts) == 0 {
		return "nil"
	}

	// Detect yield mode: body contains at least one YieldExpr
	if containsYield(s.Body) {
		return c.compileForExprYield(s)
	}
	return c.compileForExprCollect(s)
}

// compileForExprYield generates a closure returning the first yielded value (or nil).
func (c *compiler) compileForExprYield(s *ast.ForStmt) string {
	return c.compileForValue(s, true)
}

// compileForExprCollect generates a closure collecting all values into a typed slice.
func (c *compiler) compileForExprCollect(s *ast.ForStmt) string {
	return c.compileForValue(s, false)
}

// compileBlock compiles a block body with indentation.
func (c *compiler) compileBlock(block *ast.Block) {
	old := c.indent
	c.indent += "\t"
	var local [8]lexicalBinding
	bindings := local[:0]
	for _, stmt := range block.Stmts {
		bindings = c.saveStatementBindings(bindings, stmt)
		c.compileStmt(stmt)
	}
	c.restoreBindings(bindings)
	c.indent = old
}

func decimalArithmeticMethod(operator string) string {
	switch operator {
	case "+":
		return "Add"
	case "-":
		return "Sub"
	case "*":
		return "Mul"
	default:
		return ""
	}
}

func (c *compiler) captureAssignmentSlot(target string) string {
	c.resultTmp++
	slot := fmt.Sprintf("_assignSlot%d", c.resultTmp)
	c.write("%s := &(%s)", slot, target)
	return slot
}

// Capture the destination and old value before evaluating the right operand.
func (c *compiler) captureAssignmentTarget(target string) (slot, old string) {
	slot = c.captureAssignmentSlot(target)
	old = fmt.Sprintf("_assignValue%d", c.resultTmp)
	c.write("%s := *%s", old, slot)
	return slot, old
}

func (c *compiler) compileDecimalAssign(s *ast.AssignStmt, target, method string) {
	slot, old := c.captureAssignmentTarget(target)
	value := c.compileExpr(s.Value)
	result := c.compileDecimalArithmetic(old, value, method)
	c.write("*%s = %s", slot, result)
}

// compileAssign: x = expr, x += expr, etc.
func (c *compiler) compileAssign(s *ast.AssignStmt) {
	c.prepareSealedAssignment(s.Target)
	target, mark := c.compileSaveAssignmentTarget(s.Target)
	if mark != "" {
		defer c.write("%s", mark)
	}
	c.compileAssignment(s, target)
}

func (c *compiler) compileAssignment(s *ast.AssignStmt, target string) {
	if c.compileIntegerAssignment(s, target) {
		return
	}
	if c.compileFloatAssignment(s, target) {
		return
	}
	if s.Target.GetTypeTag() == "Decimal" && s.Value.GetTypeTag() == "Decimal" && s.Op != "=" {
		if method := decimalArithmeticMethod(strings.TrimSuffix(s.Op, "=")); method != "" {
			c.compileDecimalAssign(s, target, method)
			return
		}
	}
	if s.Target.GetTypeTag() == "DateTime" && isDurationExpr(s.Value) && (s.Op == "+=" || s.Op == "-=") {
		slot, old := c.captureAssignmentTarget(target)
		value := c.compileExpr(s.Value)
		c.write("*%s = %s", slot, compileDateOffset(old, value, strings.TrimSuffix(s.Op, "=")))
		return
	}
	expected := &ast.TypeRef{Name: s.Target.GetTypeTag(), IsList: s.Target.IsListType(), Nullable: s.Target.IsNullable()}
	if _, member := s.Target.(*ast.MemberExpr); member && s.Op == "=" && expressionMayHaveEffects(s.Value) {
		slot := c.captureAssignmentSlot(target)
		value := c.compileAssignedValue(s.Value, expected)
		c.write("*%s = %s", slot, value)
		return
	}
	if s.Op != "=" && expressionMayHaveEffects(s.Value) {
		slot, old := c.captureAssignmentTarget(target)
		value := c.compileAssignedValue(s.Value, expected)
		c.write("*%s = %s %s (%s)", slot, old, strings.TrimSuffix(s.Op, "="), value)
		return
	}
	val := c.compileAssignedValue(s.Value, expected)
	c.write("%s %s %s", target, s.Op, val)
}

// --- Phase 2 expression compilers ---

// compileTemplate: "hello ${name}: ${count} items"
// Typed writes avoid dynamic formatting and temporary numeric strings.
func (c *compiler) compileTemplate(e *ast.TemplateString) string {
	if len(e.Parts) == 0 {
		return `""`
	}
	// Simple case: single string literal
	if len(e.Parts) == 1 {
		if lit, ok := e.Parts[0].(*ast.Literal); ok && lit.Kind == token.String {
			return fmt.Sprintf("%q", lit.Value)
		}
	}
	sub := c.valueCompiler()
	sub.indent = c.indent + "\t"
	parts := make([]compiledTemplatePart, len(e.Parts))
	sizes := make([]string, len(e.Parts))
	for index, part := range e.Parts {
		parts[index] = sub.prepareTemplatePart(part, index)
		sizes[index] = "len(" + parts[index].value + ")"
	}
	sub.write("var _sb strings.Builder")
	sub.write("_sb.Grow(%s)", strings.Join(sizes, " + "))
	for _, part := range parts {
		method := "WriteString"
		if part.numeric {
			method = "Write"
		}
		sub.write("_sb.%s(%s)", method, part.value)
	}
	sub.write("return _sb.String()")
	return c.finishValueClosure(sub, "string", sub.b.String())
}

// isLogTarget checks if an expression is a valid target for .i/.d/.w/.e log methods.
// Only string literals and template strings are valid — not arbitrary member access like obj.i.
func isLogTarget(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Literal:
		return e.Kind == token.String
	case *ast.TemplateString:
		return true
	}
	return false
}

// isEnumExpr checks if an expression is an enum type (needs string() cast).
func (c *compiler) isEnumExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		if vt, ok := c.vars[e.Name]; ok {
			return c.enums[vt.name]
		}
	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok {
			if vt, ok := c.vars[ident.Name]; ok && vt.isModel {
				if m, ok := c.models[vt.name]; ok {
					for _, f := range m.Fields {
						if f.Name == e.Field && f.Type != nil {
							return c.enums[f.Type.Name]
						}
					}
				}
			}
		}
	}
	return false
}

// isIntExpr checks if an expression is known to be an integer.
func (c *compiler) isIntExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		if vt, ok := c.vars[e.Name]; ok {
			switch vt.name {
			case "Int", "int64":
				return true
			}
		}
	case *ast.Literal:
		return e.Kind == token.Int
	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok {
			if vt, ok := c.vars[ident.Name]; ok && vt.isModel {
				if m, ok := c.models[vt.name]; ok {
					for _, f := range m.Fields {
						if f.Name == e.Field && f.Type != nil && f.Type.Name == "Int" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// Standalone ranges materialize checked slices; direct loops use compileForRange.
func (c *compiler) compileRange(e *ast.RangeExpr) string {
	start, end := c.compileRangeBounds(e)
	c.resultTmp++
	name := fmt.Sprintf("_range%d", c.resultTmp)
	c.write("%s, err := lux.IntRange(%s, %s)", name, start, end)
	c.write("if err != nil {")
	c.writeErrorReturn("\t", `errors.BadRequest.WithData(&errors.ParamError{Param: "range", Error: err.Error()}).WithCause(err)`)
	c.write("}")
	return name
}

// Preserve the start value before evaluating a potentially effectful end.
func (c *compiler) compileRangeBounds(bounds *ast.RangeExpr) (string, string) {
	start := c.compileExpr(bounds.Start)
	if expressionMayHaveEffects(bounds.End) {
		start = c.captureExpressionValue(bounds.Start, start, "int64")
	}
	return start, c.compileExpr(bounds.End)
}

// compileObject: { name: "lin", age: 18 } → struct literal
func (c *compiler) compileObject(e *ast.ObjectExpr) string {
	prefix := ""
	if e.TypeName != "" {
		prefix = e.TypeName
	} else if c.api != nil && c.api.ReturnType != nil && c.isTypeDecl(c.api.ReturnType.Name) {
		prefix = c.api.ReturnType.Name
	}
	fields := c.compileObjectFields(e.Fields, true)
	fields = c.compileObjectDefaults(prefix, e.Fields, fields)
	fields = c.compileSaveObjectState(prefix, e.Fields, fields)
	return prefix + "{" + strings.Join(fields, ", ") + "}"
}

// Evaluate fields in source order before constructing an object or event payload.
func (c *compiler) compileObjectFields(values []*ast.NamedArg, references bool) []string {
	var fields []string
	lastEffect := lastEffectfulArgument(values)
	for index, f := range values {
		expected := objectFieldType(f.ExpectedType, references, c.enums)
		val := c.compileAssignedValue(f.Value, expected)
		if index < lastEffect {
			goType := c.goTypeForExpr(f.Value)
			if expected != nil {
				goType = compiledFunctionGoType(expected, c.models)
			}
			val = c.captureExpressionValue(f.Value, val, goType)
		}
		fields = append(fields, fmt.Sprintf("%s: %s", str.Capitalize(f.Name), val))
	}
	return fields
}

// compileWhen: when { cond -> expr, else -> expr }
func (c *compiler) compileWhen(e *ast.WhenExpr) string {
	return c.compileWhenValue(e)
}

func (c *compiler) compileWhenValue(e *ast.WhenExpr) string {
	if c.inForExpr && containsYieldExpr(e) {
		return c.compileYieldWhenValue(e)
	}
	if e.Subject == nil && c.hasChannelBranch(e) {
		return c.compileSelect(e)
	}
	if value, handled := c.compileSealedWhenValue(e); handled {
		return value
	}
	sub := c.branchValueCompiler(e)
	sub.indent += "\t"
	sub.pageResult = c.pageBranch(e)
	subject := ""
	if e.Subject != nil {
		subject = sub.compileBooleanValue(e.Subject)
	}
	hasIsType := false
	for _, branch := range e.Branches {
		hasIsType = hasIsType || branch.IsType != ""
	}
	if hasIsType && e.Subject != nil {
		subject += ".(type)"
	}
	if !hasIsType && (whenConditionsCall(e) || whenNeedsValueEquality(e)) {
		if e.Subject != nil {
			sub.write("_subject := %s", subject)
			subject = "_subject"
		}
		sub.compileWhenConditions(e, subject)
	} else {
		sub.compileWhenSwitch(e, subject)
	}
	return c.finishWhenClosure(sub, e, e.Else == nil)
}

func (c *compiler) compileWhenSwitch(e *ast.WhenExpr, subject string) {
	if subject == "" {
		c.write("switch {")
	} else {
		c.write("switch %s {", subject)
	}
	for _, branch := range e.Branches {
		condition := branch.IsType
		if condition == "" {
			condition = c.compileBooleanValue(branch.Condition)
		}
		c.write("case %s:", condition)
		c.indent += "\t"
		c.compileValueBranch(branch.Body)
		c.indent = strings.TrimSuffix(c.indent, "\t")
	}
	if e.Else != nil {
		c.write("default:")
		c.indent += "\t"
		c.compileValueBranch(e.Else)
		c.indent = strings.TrimSuffix(c.indent, "\t")
	}
	c.write("}")
}

// inferWhenReturnType infers the Go return type for a when expression.
// Uses the API return type if available, otherwise falls back to "any".
func (c *compiler) inferWhenReturnType(e *ast.WhenExpr) string {
	if goType := c.goTypeForExpr(e); goType != "" {
		return goType
	}
	if c.api != nil && c.api.ReturnType != nil {
		switch c.api.ReturnType.Name {
		case "String":
			return "string"
		case "Int":
			return "int64"
		case "Float":
			return "float64"
		case "Boolean":
			return "bool"
		}
	}
	return "any"
}

// zeroValueForType returns the Go zero value for a type string.
func zeroValueForType(t string) string {
	switch t {
	case "string":
		return `""`
	case "int64", "int", "float64":
		return "0"
	case "bool":
		return "false"
	default:
		return "nil"
	}
}

// hasChannelBranch checks if any when-branch condition is a channel receive (<-ch).
func (c *compiler) hasChannelBranch(e *ast.WhenExpr) bool {
	for _, br := range e.Branches {
		if u, ok := br.Condition.(*ast.UnaryExpr); ok && u.Op == "<-" {
			return true
		}
	}
	return false
}

// compileSelect: when { <-ch1 -> { ... }, <-ch2 -> { ... } } → Go select
func (c *compiler) compileSelect(e *ast.WhenExpr) string {
	sub := c.branchValueCompiler(e)
	sub.indent += "\t"
	sub.compileSelectBody(e)
	return c.finishWhenClosure(sub, e, true)
}

func (c *compiler) compileSelectBody(e *ast.WhenExpr) {
	var local [8]selectReceive
	receives := local[:0]
	for _, branch := range e.Branches {
		receives = append(receives, c.prepareSelectReceive(branch.Condition, len(e.Branches) > 1))
	}
	c.write("select {")
	for index, branch := range e.Branches {
		c.compileSelectBranch(branch, receives[index])
	}
	if e.Else != nil {
		c.write("default:")
		c.indent += "\t"
		c.compileValueBranch(e.Else)
		c.indent = strings.TrimSuffix(c.indent, "\t")
	}
	c.write("}")
}

// extractLambdaField extracts the field name from a simple lambda like { it.minutes }.
// Returns empty string if the lambda is not a simple it.field expression.
func extractLambdaField(expr ast.Expr) string {
	lambda, ok := expr.(*ast.LambdaExpr)
	if !ok || lambda.Body == nil || len(lambda.Body.Stmts) != 1 {
		return ""
	}
	// The single statement should be an ExprStmt with a MemberExpr
	exprStmt, ok := lambda.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		return ""
	}
	member, ok := exprStmt.Expr.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	// Check that the object is "it" (implicit param)
	parameter := "it"
	if len(lambda.Params) == 1 {
		parameter = lambda.Params[0]
	}
	if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == parameter {
		return member.Field
	}
	return ""
}

// compileTransaction binds model operations to the transaction's App.
func (c *compiler) compileTransaction(e *ast.TransactionExpr) string {
	return c.compileTransactionBlock(e.Body)
}

func (c *compiler) compileTransactionBlock(body *ast.Block) string {
	sub := c.errorCompiler()
	sub.indent = c.indent + "\t"
	for _, stmt := range body.Stmts {
		sub.compileStmt(stmt)
	}
	return fmt.Sprintf("app.Tx(ctx, func(app *App) error {\n%s%s\treturn nil\n%s})",
		sub.b.String(), c.indent, c.indent)
}

// subCompiler creates a child compiler with its own buffer and vars scope.
// Inherits parent vars (read-only copy) but mutations don't leak back.
func (c *compiler) subCompiler() *compiler {
	var b strings.Builder
	childVars := make(map[string]valType, len(c.vars))
	for k, v := range c.vars {
		childVars[k] = v
	}
	return &compiler{
		generator:          c.generator,
		b:                  &b,
		indent:             c.indent,
		models:             c.models,
		types:              c.types,
		enums:              c.enums,
		api:                c.api,
		vars:               childVars,
		bindings:           c.bindings,
		nativeFunctions:    c.nativeFunctions,
		functions:          c.functions,
		staticFunctions:    c.staticFunctions,
		selectionFunctions: c.selectionFunctions,
		clientSelection:    c.clientSelection,
		loadSelections:     c.loadSelections,
		querySelections:    c.querySelections,
		queryRelations:     c.queryRelations,
		pagePlan:           c.pagePlan,
		pageQueries:        c.pageQueries,
		pageSlots:          c.pageSlots,
		explicitSelections: c.explicitSelections,
		resultTmp:          c.resultTmp,
	}
}

// compileAsync keeps abnormal exits inside the asynchronous task boundary.
func (c *compiler) compileAsync(e *ast.AsyncExpr) string {
	prefix, params, args := c.prepareAsyncCaptures(e.Body)
	sub := c.subCompiler()
	sub.inFunction = false
	sub.functionResult = nil
	sub.indent = c.indent + "\t\t"
	sub.inAsync = true
	for _, stmt := range e.Body.Stmts {
		sub.compileStmt(stmt)
	}
	if params == "" {
		return fmt.Sprintf("go func() {\n%s\tapi.RunAsyncTask(func() {\n%s%s\t})\n%s}()",
			c.indent, sub.b.String(), c.indent, c.indent)
	}
	body := fmt.Sprintf("go func(%s) {\n%s\tapi.RunAsyncTask(func() {\n%s%s\t})\n%s}(%s)",
		params, c.indent, sub.b.String(), c.indent, c.indent, args)
	if prefix == "" {
		return body
	}
	return "{\n" + prefix + c.indent + body + "\n" + c.indent + "}"
}

type awaitTask struct {
	varName   string
	goType    string
	source    ast.Expr
	isQuery   bool
	valueType valType
	body      string
	aggregate *awaitAggregate
}

func (task awaitTask) modelLiteral() bool {
	_, literal := task.source.(*ast.ObjectExpr)
	return literal && task.body == "" && task.valueType.isModel && !task.valueType.isList
}

func (c *compiler) newAwaitTask(name string, value ast.Expr) awaitTask {
	task := awaitTask{varName: name, source: value, isQuery: c.isModelQuery(value)}
	if task.isQuery {
		task.valueType = c.resolveQueryType(value)
		task.goType = goTypeForValType(task.valueType)
	} else {
		valueType, ok := c.valTypeFromExpr(value)
		if ok {
			valueType.isValue = false // Await bindings preserve model identity.
			task.valueType = valueType
			task.goType = goTypeForValType(valueType)
		}
	}
	if task.goType == "" {
		task.goType = "any"
	}
	return task
}

func goTypeForValType(value valType) string {
	if value.isModel {
		if value.isList {
			return "[]*" + value.name
		}
		if value.isValue {
			return value.name
		}
		return "*" + value.name
	}
	ref := ast.TypeRef{Name: value.name, IsList: value.isList, Nullable: value.nullable}
	if value.typeRef != nil {
		ref.TypeArgs = value.typeRef.TypeArgs
	}
	return resolveGoType(&ref)
}

func (c *compiler) emitAwaitTasks(tasks []awaitTask) {
	for _, t := range tasks {
		if t.modelLiteral() {
			c.write("%s := new(%s)", c.bindings.name(t.varName), t.valueType.name)
		} else {
			c.write("var %s %s", c.bindings.name(t.varName), t.goType)
		}
		c.vars[t.varName] = t.valueType
	}

	groups := c.awaitAggregateGroups(tasks)
	c.write("{")
	c.indent += "\t"
	c.write("g, gctx := api.NewTaskGroup(ctx)")
	c.write("_ = gctx")
	for index := range tasks {
		group, first := awaitAggregateGroupAt(groups, index)
		if group != nil {
			if first {
				c.emitAwaitAggregateGroup(*group)
			}
			continue
		}
		c.emitAwaitTask(tasks[index])
	}

	c.write("if err := g.Wait(); err != nil {")
	c.writeErrorReturn("\t", "err")
	c.write("}")
	c.indent = strings.TrimSuffix(c.indent, "\t")
	c.write("}")
	for _, t := range tasks {
		c.write("_ = %s", c.bindings.name(t.varName))
	}
}

type awaitAggregate struct {
	taskIndex  int
	varName    string
	model      string
	function   string
	column     string
	conditions []string
}

type awaitAggregateGroup struct {
	model  string
	common []string
	tasks  []awaitAggregate
}

func (c *compiler) awaitAggregateGroups(tasks []awaitTask) []awaitAggregateGroup {
	if c.api != nil && hasDirective(c.api.Directives, "scope") {
		return nil
	}
	byModel := make(map[string]int)
	groups := make([]awaitAggregateGroup, 0, len(tasks)/2)
	for index, task := range tasks {
		aggregate, ok := c.awaitAggregate(index, task)
		if !ok {
			continue
		}
		groupIndex, exists := byModel[aggregate.model]
		if !exists {
			groupIndex = len(groups)
			byModel[aggregate.model] = groupIndex
			groups = append(groups, awaitAggregateGroup{model: aggregate.model})
		}
		groups[groupIndex].tasks = append(groups[groupIndex].tasks, aggregate)
	}
	return finalizeAwaitAggregateGroups(groups)
}

func finalizeAwaitAggregateGroups(groups []awaitAggregateGroup) []awaitAggregateGroup {
	result := groups[:0]
	for _, group := range groups {
		if len(group.tasks) < 2 {
			continue
		}
		group.common = commonAggregateConditions(group.tasks)
		for index := range group.tasks {
			group.tasks[index].conditions = subtractAggregateConditions(group.tasks[index].conditions, group.common)
		}
		result = append(result, group)
	}
	return result
}

func (c *compiler) awaitAggregate(taskIndex int, task awaitTask) (awaitAggregate, bool) {
	if task.body != "" {
		if task.aggregate == nil {
			return awaitAggregate{}, false
		}
		aggregate := *task.aggregate
		aggregate.taskIndex = taskIndex
		return aggregate, true
	}
	chain := flattenChain(task.source)
	if !task.isQuery || len(chain) < 2 {
		return awaitAggregate{}, false
	}
	root, ok := chain[0].expr.(*ast.Ident)
	if !ok || c.models[root.Name] == nil || c.isRemoteModel(root.Name) {
		return awaitAggregate{}, false
	}
	if _, local := c.vars[root.Name]; local {
		return awaitAggregate{}, false
	}
	terminal := chain[len(chain)-1]
	column, ok := aggregateTerminal(terminal)
	if !ok {
		return awaitAggregate{}, false
	}
	conditions, ok := c.awaitAggregateConditions(root.Name, chain[1:len(chain)-1])
	if !ok {
		return awaitAggregate{}, false
	}
	return awaitAggregate{taskIndex: taskIndex, varName: task.varName, model: root.Name, function: terminal.method, column: column, conditions: conditions}, true
}

func aggregateTerminal(link chainLink) (string, bool) {
	switch link.method {
	case "count":
		return "", len(link.args) == 0
	case "sum", "avg", "min", "max":
		if len(link.args) != 1 {
			return "", false
		}
		column := extractLambdaField(link.args[0].Value)
		return column, column != ""
	default:
		return "", false
	}
}

func (c *compiler) awaitAggregateConditions(model string, links []chainLink) ([]string, bool) {
	var conditions []string
	for _, link := range links {
		if link.method != "where" {
			return nil, false
		}
		for _, arg := range link.args {
			condition, ok := c.awaitAggregateCondition(model, arg)
			if !ok {
				return nil, false
			}
			conditions = append(conditions, condition)
		}
	}
	return conditions, true
}

func (c *compiler) awaitAggregateCondition(model string, arg *ast.NamedArg) (string, bool) {
	if arg == nil || !stableAggregateCondition(arg) {
		return "", false
	}
	if arg.Name != "" {
		return fmt.Sprintf("%sWhere.%s.Eq(%s)", model, str.Capitalize(arg.Name), c.compileExpr(arg.Value)), true
	}
	return c.compileWhereArg(model, arg.Value), true
}

func stableAggregateCondition(arg *ast.NamedArg) bool {
	if arg.Name != "" {
		return stableAggregateValue(arg.Value)
	}
	binary, ok := arg.Value.(*ast.BinaryExpr)
	if !ok || !aggregateComparison(binary.Op) || !aggregateFieldReference(binary.Left) {
		return false
	}
	return stableAggregateValue(binary.Right)
}

func aggregateComparison(operator string) bool {
	switch operator {
	case "==", "!=", ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func aggregateFieldReference(expr ast.Expr) bool {
	if identifier, ok := expr.(*ast.Ident); ok {
		return identifier.Name != ""
	}
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	identifier, ok := member.Object.(*ast.Ident)
	return ok && identifier.Name == "it" && member.Field != ""
}

func stableAggregateValue(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.Literal, *ast.Ident:
		return true
	case *ast.MemberExpr:
		return stableAggregateValue(value.Object)
	case *ast.UnaryExpr:
		return (value.Op == "+" || value.Op == "-") && stableAggregateValue(value.Value)
	default:
		return false
	}
}

func commonAggregateConditions(tasks []awaitAggregate) []string {
	minimums := conditionCounts(tasks[0].conditions)
	for _, task := range tasks[1:] {
		counts := conditionCounts(task.conditions)
		for condition, minimum := range minimums {
			if counts[condition] < minimum {
				minimums[condition] = counts[condition]
			}
		}
	}
	common := make([]string, 0, len(tasks[0].conditions))
	used := make(map[string]int, len(minimums))
	for _, condition := range tasks[0].conditions {
		if used[condition] < minimums[condition] {
			common = append(common, condition)
			used[condition]++
		}
	}
	return common
}

func conditionCounts(conditions []string) map[string]int {
	counts := make(map[string]int, len(conditions))
	for _, condition := range conditions {
		counts[condition]++
	}
	return counts
}

func subtractAggregateConditions(conditions, common []string) []string {
	remaining := conditionCounts(common)
	result := make([]string, 0, len(conditions)-len(common))
	for _, condition := range conditions {
		if remaining[condition] > 0 {
			remaining[condition]--
			continue
		}
		result = append(result, condition)
	}
	return result
}

func awaitAggregateGroupAt(groups []awaitAggregateGroup, taskIndex int) (*awaitAggregateGroup, bool) {
	for index := range groups {
		for taskOffset, task := range groups[index].tasks {
			if task.taskIndex == taskIndex {
				return &groups[index], taskOffset == 0
			}
		}
	}
	return nil, false
}

func (c *compiler) emitAwaitAggregateGroup(group awaitAggregateGroup) {
	c.aggregateTmp++
	result := fmt.Sprintf("_luxoAggregates%d", c.aggregateTmp)
	base := fmt.Sprintf("app.%s.Where(%s)", group.model, strings.Join(group.common, ", "))
	specs := make([]string, len(group.tasks))
	for index, task := range group.tasks {
		specs[index] = c.compileAggregateSpec(task)
	}
	if c.emitMixedAggregateGroup(group, base, specs) {
		return
	}
	c.write("g.Go(func() error {")
	c.write("\t%s, err := %s.AggregateBatch(gctx, %s)", result, base, strings.Join(specs, ", "))
	c.write("\tif err != nil { return err }")
	for index, task := range group.tasks {
		c.write("\t%s = %s[%d]", c.bindings.name(task.varName), result, index)
	}
	c.write("\treturn nil")
	c.write("})")
}

func (c *compiler) compileAggregateSpec(task awaitAggregate) string {
	fields := []string{"Function: lux.Aggregate" + str.Capitalize(task.function)}
	if task.column != "" {
		fields = append(fields, fmt.Sprintf("Column: %q", c.generator.sqlColumn(task.column)))
	}
	if len(task.conditions) > 0 {
		fields = append(fields, "Conditions: []lux.Condition{"+strings.Join(task.conditions, ", ")+"}")
	}
	return "lux.AggregateSpec{" + strings.Join(fields, ", ") + "}"
}

func (c *compiler) emitAwaitTask(task awaitTask) {
	// Fused aggregates never reach this path: compile each remaining expression
	// exactly once, inside its own error-return and cancellation boundary.
	body, expression := task.body, ""
	if body == "" {
		sub := c.errorCompiler()
		sub.indent = c.indent + "\t"
		if task.valueType.isModel && !task.valueType.isList && !task.isQuery && !task.modelLiteral() {
			expression = sub.compileFunctionArgument(task.source, &ast.TypeRef{Name: task.valueType.name, Nullable: task.valueType.nullable})
		} else {
			expression = sub.compileExpr(task.source)
		}
		body = sub.b.String()
	}
	c.write("g.Go(func() error {")
	// Absence of these bytes proves the generated body cannot reference ctx.
	// Literals/comments may conservatively retain an unused capture, never omit a needed one.
	if strings.Contains(body, "ctx") || strings.Contains(expression, "ctx") {
		c.write("\tctx := gctx")
		c.write("\t_ = ctx")
	}
	c.b.WriteString(body)
	if task.body != "" {
		c.write("\treturn nil")
	} else if task.isQuery {
		c.write("\t_awaitValue, err := %s", expression)
		c.write("\t%s = _awaitValue", c.bindings.name(task.varName))
		c.write("\treturn err")
	} else if task.modelLiteral() {
		c.write("\t*%s = %s", c.bindings.name(task.varName), expression)
		c.write("\treturn nil")
	} else {
		c.write("\t%s = %s", c.bindings.name(task.varName), expression)
		c.write("\treturn nil")
	}
	c.write("})")
}

// compileStringMethod compiles Luxo string methods to Go standard library calls.
// Returns "" if the call is not a recognized string method.
func (c *compiler) compileStringMethod(e *ast.CallExpr) string {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	if len(e.Args) == 0 {
		if value := c.compileLengthMember(member); value != "" {
			return value
		}
	}
	if receiver, arguments, named := c.compileNamedStringArguments(e, member); named {
		return c.compileStringOperation(member.Field, func() string { return receiver }, func(index int) string { return arguments[index] }, len(e.Args))
	}
	lastEffect := lastEffectfulArgument(e.Args)
	obj := func() string {
		value := c.compileExpr(member.Object)
		if lastEffect >= 0 {
			value = c.captureExpressionValue(member.Object, value, c.goTypeForExpr(member.Object))
		}
		return value
	}
	arg := func(i int) string {
		if i < len(e.Args) {
			source := e.Args[i].Value
			value := c.compileExpr(source)
			if i < lastEffect {
				value = c.captureExpressionValue(source, value, c.goTypeForExpr(source))
			}
			return value
		}
		// Defensive: string helpers check nargs before requesting a fixed argument index.
		return ""
	}
	return c.compileStringOperation(member.Field, obj, arg, len(e.Args))
}

func (c *compiler) compileStringOperation(method string, obj func() string, arg func(int) string, nargs int) string {
	if method == "repeat" && nargs == 1 {
		return c.compileStringRepeat(obj(), arg(0))
	}
	if method == "substring" && (nargs == 1 || nargs == 2) {
		value, start := obj(), arg(0)
		var call string
		if nargs == 2 {
			call = fmt.Sprintf("str.Substring(%s, int(%s), int(%s))", value, start, arg(1))
		} else {
			call = fmt.Sprintf("str.SubstringFrom(%s, int(%s))", value, start)
		}
		return c.compileCheckedSubstring(call)
	}
	if result := compileStringTransform(method, obj, arg, nargs); result != "" {
		return result
	}
	if result := compileStringQuery(method, obj, arg, nargs); result != "" {
		return result
	}
	return compileStringConvert(method, obj)
}

func (c *compiler) compileNamedStringArguments(call *ast.CallExpr, member *ast.MemberExpr) (string, [2]string, bool) {
	var values [2]string
	named := false
	for _, argument := range call.Args {
		named = named || argument.Name != ""
	}
	if !named {
		return "", values, false
	}
	signature, known := builtin.StringMethod(member.Field)
	if !known {
		return "", values, false
	}
	positions, issue := signature.Bind(call.Args)
	if issue.Kind != "" {
		panic(&InternalCompilerError{Detail: "invalid String." + member.Field + " argument binding: " + issue.Kind})
	}
	lastEffect := lastEffectfulArgument(call.Args)
	receiver := c.compileExpr(member.Object)
	if lastEffect >= 0 {
		receiver = c.captureExpressionValue(member.Object, receiver, c.goTypeForExpr(member.Object))
	}
	var written [2]string
	for index, argument := range call.Args {
		value := c.compileExpr(argument.Value)
		if index < lastEffect {
			value = c.captureExpressionValue(argument.Value, value, c.goTypeForExpr(argument.Value))
		}
		written[index] = value
	}
	for position, index := range positions {
		if index >= 0 {
			values[position] = written[index]
		}
	}
	return receiver, values, true
}

func (c *compiler) compileStringRepeat(value, count string) string {
	c.resultTmp++
	name := fmt.Sprintf("_stringRepeat%d", c.resultTmp)
	c.write("%s, %sErr := str.Repeat(%s, int(%s))", name, name, value, count)
	c.write("if %sErr != nil {", name)
	c.writeErrorReturn("\t", fmt.Sprintf(`errors.BadRequest.WithData(&errors.ParamError{Param: "String.repeat", Error: %sErr.Error()})`, name))
	c.write("}")
	return name
}

func (c *compiler) compileCheckedSubstring(call string) string {
	c.resultTmp++
	name := fmt.Sprintf("_stringSubstring%d", c.resultTmp)
	c.write("%s, %sErr := %s", name, name, call)
	c.write("if %sErr != nil {", name)
	c.writeErrorReturn("\t", fmt.Sprintf(`errors.BadRequest.WithData(&errors.ParamError{Param: "String.substring", Error: %sErr.Error()})`, name))
	c.write("}")
	return name
}

// compileStringTransform handles string transform methods (returns string).
func compileStringTransform(method string, obj func() string, arg func(int) string, nargs int) string {
	switch method {
	case "lowercase":
		return fmt.Sprintf("strings.ToLower(%s)", obj())
	case "uppercase":
		return fmt.Sprintf("strings.ToUpper(%s)", obj())
	case "trim":
		return fmt.Sprintf("strings.TrimSpace(%s)", obj())
	case "trimStart":
		if nargs > 0 {
			return fmt.Sprintf("strings.TrimLeft(%s, %s)", obj(), arg(0))
		}
		return fmt.Sprintf("strings.TrimLeft(%s, \" \")", obj())
	case "trimEnd":
		if nargs > 0 {
			return fmt.Sprintf("strings.TrimRight(%s, %s)", obj(), arg(0))
		}
		return fmt.Sprintf("strings.TrimRight(%s, \" \")", obj())
	case "reversed":
		return fmt.Sprintf("str.Reverse(%s)", obj())
	case "replace", "replaceAll":
		if nargs >= 2 {
			return fmt.Sprintf("strings.ReplaceAll(%s, %s, %s)", obj(), arg(0), arg(1))
		}
	case "padStart":
		if nargs >= 2 {
			return fmt.Sprintf("str.PadLeft(%s, int(%s), %s)", obj(), arg(0), arg(1))
		}
	case "padEnd":
		if nargs >= 2 {
			return fmt.Sprintf("str.PadRight(%s, int(%s), %s)", obj(), arg(0), arg(1))
		}
	case "split":
		if nargs > 0 {
			return fmt.Sprintf("strings.Split(%s, %s)", obj(), arg(0))
		}
	}
	return ""
}

// compileStringQuery handles string query methods (returns bool/int).
func compileStringQuery(method string, obj func() string, arg func(int) string, nargs int) string {
	switch method {
	case "contains":
		if nargs > 0 {
			return fmt.Sprintf("strings.Contains(%s, %s)", obj(), arg(0))
		}
	case "startsWith":
		if nargs > 0 {
			return fmt.Sprintf("strings.HasPrefix(%s, %s)", obj(), arg(0))
		}
	case "endsWith":
		if nargs > 0 {
			return fmt.Sprintf("strings.HasSuffix(%s, %s)", obj(), arg(0))
		}
	case "isEmpty":
		return fmt.Sprintf("(len(%s) == 0)", obj())
	case "matches":
		if nargs > 0 {
			value := obj()
			return fmt.Sprintf("str.Matches(%s, %s)", arg(0), value)
		}
	case "length", "size":
		return fmt.Sprintf("int64(len(%s))", obj())
	}
	return ""
}

// compileStringConvert handles string type conversion methods.
func compileStringConvert(method string, obj func() string) string {
	switch method {
	case "toInt":
		return fmt.Sprintf("convert.StringToInt(%s)", obj())
	case "toFloat":
		return fmt.Sprintf("convert.StringToFloat(%s)", obj())
	}
	return ""
}

func firstModelSet(sets []map[string]*ast.ModelDecl) map[string]*ast.ModelDecl {
	if len(sets) == 0 {
		return nil
	}
	return sets[0]
}

// Native scalar model results are values; compiled expressions hold references.
func (c *compiler) nativeValueModelResult(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || !c.nativeFunctions[ident.Name] {
		return false
	}
	fn := c.functions[ident.Name]
	if fn == nil {
		return false
	}
	ref := unwrapResultType(fn.ReturnType)
	return ref != nil && !ref.IsList && !ref.Nullable && c.models[ref.Name] != nil
}

// Preserve the declared Go ABI while capturing argument values in source order.
func (c *compiler) compileCallableArgument(expr ast.Expr, ref *ast.TypeRef, native bool) (string, string) {
	value := c.compileAssignedValue(expr, ref)
	if !native {
		return value, compiledFunctionGoType(ref, c.models)
	}
	if ref != nil && !ref.Nullable && !ref.IsList && c.models[ref.Name] != nil {
		value = "*(" + value + ")"
	}
	return value, nativeGoType(ref, c.models)
}
