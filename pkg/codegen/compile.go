package codegen

import (
	"fmt"
	"sort"
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
		case token.Int:
			return e.Value
		case token.Float:
			return e.Value
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
					return e.Op + "(" + value + ")"
				}
				return e.Op + value
			}
		}
	case *ast.Ident:
		if e.Name == "true" {
			return "true"
		}
		if e.Name == "false" {
			return "false"
		}
	case *ast.MemberExpr:
		// Enum.VALUE → EnumVALUE
		if ident, ok := e.Object.(*ast.Ident); ok {
			if enums[ident.Name] {
				return ident.Name + strings.ToUpper(e.Field)
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
		amount := strings.TrimSuffix(value, unit.suffix)
		if amount == "1" {
			return unit.goUnit, true
		}
		return amount + " * " + unit.goUnit, true
	}
	return "", false
}

func compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().compileAPIBody(b, api, models, enums, nil, nil, nil)
}

func (g *GeneratorContext) compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool) {
	name := api.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// @auth — require authenticated identity
	if d := findDirective(api.Directives, "auth"); d != nil {
		writeAuthCheck(b, "\t\t", d)
	}

	// Parse params (with default value support)
	for _, p := range api.Params {
		writeCallableParamExtraction(b, p, enums, "\t\t")
	}

	// Compile body statements
	c := &compiler{
		generator:          g,
		b:                  b,
		indent:             "\t\t",
		models:             models,
		enums:              enums,
		api:                api,
		vars:               make(map[string]valType),
		paginationTotals:   make(map[string]string),
		nativeFunctions:    nativeFunctions,
		functions:          functions,
		selectionFunctions: selectionFunctions,
		clientSelection:    "req.Select",
		paginate:           hasDirective(api.Directives, "paginate"),
		loadSelections:     analyzeLoadSelections(api.Body, models),
	}
	// Register API params in vars with Luxo type name for type-aware compilation
	for _, p := range api.Params {
		typeName := "string"
		if p.Type != nil {
			typeName = p.Type.Name
		}
		vt := valType{name: typeName}
		if p.Type != nil && p.Type.Nullable {
			vt.nullable = true
		}
		c.vars[p.Name] = vt
	}
	c.compileHandlerBody(api.Body.Stmts)

	fmt.Fprintf(b, "\t}\n}\n\n")
}

func isStructuredParam(param *ast.ParamDecl, enums map[string]bool) bool {
	if param == nil || param.Type == nil || enums[param.Type.Name] {
		return false
	}
	switch param.Type.Name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON":
		return false
	default:
		return true
	}
}

func writeCallableParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	if isStructuredParam(param, enums) {
		writeStructuredParamExtraction(b, param, enums, indent)
		return
	}
	goType := resolveGoType(param.Type)
	method := paramMethod(goType)
	if param.Type != nil && param.Type.Nullable {
		method = ""
	}
	if param.Default != nil {
		writeOptionalParamExtraction(b, param, enums, method, indent)
		return
	}
	if method == "" {
		fmt.Fprintf(b, "%svar %s %s\n", indent, param.Name, goType)
		fmt.Fprintf(b, "%sif err := req.%s(%q, &%s); err != nil {\n", indent, paramJSONMethod(param), param.Name, param.Name)
		fmt.Fprintf(b, "%s\treturn err\n%s}\n", indent, indent)
		return
	}
	fmt.Fprintf(b, "%s%s, err := req.Param%s(%q)\n", indent, param.Name, method, param.Name)
	fmt.Fprintf(b, "%sif err != nil {\n%s\treturn err\n%s}\n", indent, indent, indent)
}

func writeOptionalParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, method, indent string) {
	writeDefaultParamDeclaration(b, param, enums, indent)
	if method == "" {
		fmt.Fprintf(b, "%sif err := req.%s(%q, &%s); err != nil {\n", indent, paramJSONMethod(param), param.Name, param.Name)
		fmt.Fprintf(b, "%s\treturn err\n%s}\n", indent, indent)
		return
	}
	fmt.Fprintf(b, "%sif req.HasParam(%q) {\n", indent, param.Name)
	fmt.Fprintf(b, "%s\t_value%s, err := req.Param%s(%q)\n", indent, str.Capitalize(param.Name), method, param.Name)
	fmt.Fprintf(b, "%s\tif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%s\t%s = _value%s\n", indent, param.Name, str.Capitalize(param.Name))
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeStructuredParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	writeStructuredParamDeclaration(b, param, enums, indent)
	fmt.Fprintf(b, "%sif req.BinaryMode {\n", indent)
	if param.Type.IsList {
		writeStructuredListParamDecode(b, param, indent+"\t")
	} else {
		writeStructuredScalarParamDecode(b, param, indent+"\t")
	}
	fmt.Fprintf(b, "%s} else {\n", indent)
	fmt.Fprintf(b, "%s\tif err := req.%s(%q, &%s); err != nil {\n", indent, paramJSONMethod(param), param.Name, param.Name)
	fmt.Fprintf(b, "%s\t\treturn err\n%s\t}\n%s}\n", indent, indent, indent)
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
	if valueType != param.Type && !isNullLiteral(param.Default) {
		defaultName := "_default" + str.Capitalize(param.Name)
		fmt.Fprintf(b, "%s%s := %s\n", indent, defaultName, defaultValue)
		fmt.Fprintf(b, "%svar %s %s = &%s\n", indent, param.Name, goType, defaultName)
		return
	}
	fmt.Fprintf(b, "%svar %s %s = %s\n", indent, param.Name, goType, defaultValue)
}

func isNullLiteral(expr ast.Expr) bool {
	literal, ok := expr.(*ast.Literal)
	return ok && literal.Kind == token.Null
}

func writeStructuredScalarParamDecode(b *strings.Builder, param *ast.ParamDecl, indent string) {
	optional := param.Default != nil
	nullable := param.Type.Nullable
	name := param.Name
	fmt.Fprintf(b, "%s_%sMessage, _%sPresent, err := req.ParamMessage(%q, %t, %t)\n", indent, name, name, name, optional, nullable)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif _%sPresent && _%sMessage != nil {\n", indent, name, name)
	fmt.Fprintf(b, "%s\t_%sDecoder := codec.NewDecoder(_%sMessage)\n", indent, name, name)
	if nullable {
		fmt.Fprintf(b, "%s\t%s = &%s{}\n", indent, name, param.Type.Name)
	}
	fmt.Fprintf(b, "%s\t%s.ReadLuxo(_%sDecoder)\n", indent, name, name)
	fmt.Fprintf(b, "%s\tif _%sDecoder.Err() != nil { return api.InvalidParam(%q, _%sDecoder.Err()) }\n", indent, name, name, name)
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeStructuredListParamDecode(b *strings.Builder, param *ast.ParamDecl, indent string) {
	optional := param.Default != nil
	nullable := param.Type.Nullable
	name := param.Name
	fmt.Fprintf(b, "%s_%sMessages, _%sPresent, err := req.ParamMessageArray(%q, %t, %t)\n", indent, name, name, name, optional, nullable)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif _%sPresent && _%sMessages != nil {\n", indent, name, name)
	fmt.Fprintf(b, "%s\t%s = make([]%s, len(_%sMessages))\n", indent, name, param.Type.Name, name)
	fmt.Fprintf(b, "%s\tfor i := range _%sMessages {\n", indent, name)
	fmt.Fprintf(b, "%s\t\t_%sDecoder := codec.NewDecoder(_%sMessages[i])\n", indent, name, name)
	fmt.Fprintf(b, "%s\t\t%s[i].ReadLuxo(_%sDecoder)\n", indent, name, name)
	fmt.Fprintf(b, "%s\t\tif _%sDecoder.Err() != nil { return api.InvalidParam(%q, _%sDecoder.Err()) }\n", indent, name, name, name)
	fmt.Fprintf(b, "%s\t}\n%s}\n", indent, indent)
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
	g.compileAPIBody(b, api, models, enums, nativeFunctions, functions, selectionFunctions)
}

func (g *GeneratorContext) compileLocalFunction(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool, nativeFunctions map[string]bool, functions map[string]*ast.FnDecl, selectionFunctions map[string]bool) {
	writeCompiledFunctionSignature(b, fn, models, selectionFunctions[fn.Name])
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
		selectionFunctions: selectionFunctions,
		functionResult:     fn.ReturnType,
		inFunction:         true,
		loadSelections:     analyzeLoadSelections(fn.Body, models),
	}
	if selectionFunctions[fn.Name] {
		c.clientSelection = "_select"
	}
	registerCompiledFunctionParams(c, fn.Params)
	c.compileCallableBody(fn.Body.Stmts)
	b.WriteString("}\n\n")
}

func writeCompiledFunctionSignature(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, usesSelection bool) {
	fmt.Fprintf(b, "func (app *App) %s(ctx context.Context", fn.Name)
	if usesSelection {
		b.WriteString(", _select []*selection.Field")
	}
	for _, param := range fn.Params {
		goType := compiledFunctionGoType(param.Type, models)
		if param.Spread {
			fmt.Fprintf(b, ", %s ...%s", param.Name, goType)
			continue
		}
		fmt.Fprintf(b, ", %s %s", param.Name, goType)
	}
	if fn.ReturnType == nil {
		b.WriteString(") error {\n")
		return
	}
	fmt.Fprintf(b, ") (_value %s, err error) {\n", compiledFunctionGoType(fn.ReturnType, models))
}

func compiledFunctionGoType(ref *ast.TypeRef, models map[string]*ast.ModelDecl) string {
	if ref == nil || models[ref.Name] == nil {
		return resolveGoType(ref)
	}
	if ref.IsList {
		return "[]*" + ref.Name
	}
	return "*" + ref.Name
}

func registerCompiledFunctionParams(c *compiler, params []*ast.ParamDecl) {
	for _, param := range params {
		valueType := valType{name: "string"}
		if param.Type != nil {
			valueType.name = param.Type.Name
			valueType.isList = param.Type.IsList
			valueType.nullable = param.Type.Nullable
			_, valueType.isModel = c.models[param.Type.Name]
		}
		c.vars[param.Name] = valueType
	}
}

// valType tracks the resolved type of a val variable.
type valType struct {
	isModel  bool   // true if this is a *Model or []*Model
	isList   bool   // true if this is a list (e.g., []*Model)
	isChan   bool   // true if this is a channel (Channel<T>)
	nullable bool   // true if this is a pointer type (nullable param/field)
	name     string // model name or luxo type name (Int/String/Boolean/Float)
}

func (c *compiler) valTypeFromExpr(expr ast.Expr) (valType, bool) {
	if expr == nil || expr.GetTypeTag() == "" {
		return valType{}, false
	}
	_, isModel := c.models[expr.GetTypeTag()]
	return valType{
		isModel:  isModel,
		isList:   expr.IsListType(),
		nullable: expr.IsNullable(),
		name:     expr.GetTypeTag(),
	}, true
}

func (c *compiler) goTypeForExpr(expr ast.Expr) string {
	if expr == nil || expr.GetTypeTag() == "" {
		return ""
	}
	name := expr.GetTypeTag()
	base := mapBaseType(name)
	if _, isModel := c.models[name]; isModel {
		base = "*" + name
	}
	if expr.IsListType() {
		return "[]" + base
	}
	if expr.IsNullable() && !isNilableGoType(base) {
		return "*" + base
	}
	return base
}

func (c *compiler) yieldNeedsAddress(expr ast.Expr) bool {
	if expr == nil || !expr.IsNullable() || expr.IsListType() {
		return false
	}
	name := expr.GetTypeTag()
	if _, isModel := c.models[name]; isModel {
		return false
	}
	return !isNilableGoType(mapBaseType(name))
}

func isNilableGoType(goType string) bool {
	return strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[]") ||
		strings.HasPrefix(goType, "map[") || strings.HasPrefix(goType, "chan ") ||
		strings.HasPrefix(goType, "func(") || goType == "any"
}

// compiler holds state during body compilation.
type compiler struct {
	generator          *GeneratorContext
	b                  *strings.Builder
	indent             string
	models             map[string]*ast.ModelDecl
	types              map[string]bool // type declaration names (AuthPayload, etc.)
	enums              map[string]bool // enum type names
	api                *ast.ApiDecl
	vars               map[string]valType     // variable name → resolved type
	inAsync            bool                   // true inside async { } — no return err
	inForExpr          bool                   // true inside for-as-expression with yield — yield compiles to return
	yieldAddr          bool                   // true when a yielded value must be wrapped in a pointer
	yieldTmp           int                    // unique temporary counter for nullable primitive yields
	paginate           bool                   // true when API has @paginate
	paginationTotals   map[string]string      // result variable → exact query total variable
	paginationTmp      int                    // unique pagination temporary counter
	ptrTmpCount        int                    // counter for hoisted pointer temp vars (nullable create args)
	resultTmp          int                    // counter for Result<T> values lowered from Go's (T, error)
	aggregateTmp       int                    // counter for fused await aggregate result slices
	nativeFunctions    map[string]bool        // @native fn names available through app.Resolver
	functions          map[string]*ast.FnDecl // function declarations available for static call lowering
	selectionFunctions map[string]bool        // local functions that consume client field selection
	clientSelection    string                 // generated expression for the current request selection
	functionResult     *ast.TypeRef           // non-nil while compiling a local fn body
	inFunction         bool                   // true while compiling a local fn rather than a transport handler
	loadSelections     map[string]string      // load variable → exact compiled selection literal
	loadSelection      string                 // selection for the load expression currently being compiled
	hasLoadSelection   bool                   // distinguishes an exact empty projection from select-all nil
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
		c.write("break")
	case *ast.ContinueStmt:
		c.write("continue")
	default:
		// Unreachable: every ast.Stmt implementation has a case above. A new
		// statement type added to the AST without a case here is a compiler bug —
		// fail loud at gen time rather than silently emitting broken Go.
		panic(fmt.Sprintf("codegen: unhandled statement type %T", stmt))
	}
}

// compileVal: val x = expr
func (c *compiler) compileVal(s *ast.ValStmt) {
	if await, ok := s.Value.(*ast.AwaitExpr); ok {
		names := s.Names
		if len(names) == 0 {
			names = []string{s.Name}
		}
		c.compileAwaitBindings(await, names)
		return
	}
	previousSelection, previousHasSelection := c.loadSelection, c.hasLoadSelection
	if modelName, ok := directLoadModel(s.Value); ok && c.isRemoteModel(modelName) {
		c.loadSelection, c.hasLoadSelection = c.loadSelections[s.Name]
	}
	expr := c.compileExpr(s.Value)
	c.loadSelection, c.hasLoadSelection = previousSelection, previousHasSelection
	if c.isModelQuery(s.Value) {
		qt := c.resolveQueryType(s.Value)
		// @paginate + all → AllWithCount returns (results, total, error)
		if c.paginate && qt.isList && isCountedPaginationQuery(s.Value) {
			totalName := "_luxoTotal" + str.Capitalize(s.Name)
			c.write("%s, %s, err := %s", s.Name, totalName, expr)
			c.paginationTotals[s.Name] = totalName
		} else {
			c.write("%s, err := %s", s.Name, expr)
		}
		c.writeErrorGuard()
		c.vars[s.Name] = qt
	} else {
		if list, ok := s.Value.(*ast.ListExpr); ok && c.api != nil && c.api.ReturnType != nil && c.api.ReturnType.IsList {
			expr = c.compileTypedList(list, c.api.ReturnType)
			_, isModel := c.models[c.api.ReturnType.Name]
			c.vars[s.Name] = valType{name: c.api.ReturnType.Name, isList: true, isModel: isModel}
		}
		// Wrap bare integer literals in int64() to match Luxo's Int = int64
		if lit, ok := s.Value.(*ast.Literal); ok && lit.Kind == token.Int {
			expr = fmt.Sprintf("int64(%s)", expr)
		}
		c.write("%s := %s", s.Name, expr)
		if vt, ok := c.valTypeFromExpr(s.Value); ok {
			c.vars[s.Name] = vt
		}
		// Track channel variables for close() and for-range codegen
		if c.isChannelConstructor(s.Value) {
			c.vars[s.Name] = valType{isChan: true}
		}
		// Track when-expression type based on API return type
		if _, ok := s.Value.(*ast.WhenExpr); ok && c.api != nil && c.api.ReturnType != nil {
			c.vars[s.Name] = valType{name: c.api.ReturnType.Name}
		}
		if source, ok := s.Value.(*ast.Ident); ok {
			if sourceType, exists := c.vars[source.Name]; exists {
				c.vars[s.Name] = sourceType
			}
			if totalName := c.paginationTotals[source.Name]; totalName != "" {
				c.paginationTotals[s.Name] = totalName
			}
		}
	}
}

// isChannelVar checks if expr is a variable known to be a channel.
func (c *compiler) isChannelVar(expr ast.Expr) bool {
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
	if c.inFunction {
		c.compileFunctionReturn(s)
		return
	}
	if s.Value == nil {
		c.write("return nil")
		return
	}
	expr := c.compileExpr(s.Value)
	if list, ok := s.Value.(*ast.ListExpr); ok && c.api != nil && c.api.ReturnType != nil && c.api.ReturnType.IsList {
		expr = c.compileTypedList(list, c.api.ReturnType)
	}

	// 1. Direct model query chain — extract to temp var, then WriteLuxo
	if c.isModelQuery(s.Value) {
		qt := c.resolveQueryType(s.Value)
		// Extract query result to temp variable (query returns (result, error)).
		totalName := ""
		if c.paginate && qt.isList && isCountedPaginationQuery(s.Value) {
			totalName = c.nextPaginationName("Total")
			c.write("_result, %s, err := %s", totalName, expr)
		} else {
			c.write("_result, err := %s", expr)
		}
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
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
			c.writeReturnByType(expr, vt)
			c.write("return nil")
			return
		}
	}

	// 3. Fallback: use API return type declaration
	c.writeScalarReturn(expr)
	c.write("return nil")
}

func (c *compiler) compileFunctionReturn(statement *ast.ReturnStmt) {
	if statement.Value == nil {
		c.write("return nil")
		return
	}
	expression := c.compileExpr(statement.Value)
	if list, ok := statement.Value.(*ast.ListExpr); ok && c.functionResult != nil && c.functionResult.IsList {
		expression = c.compileTypedList(list, c.functionResult)
	}
	if c.isModelQuery(statement.Value) {
		c.write("return %s", expression)
		return
	}
	if c.functionResult != nil && c.models[c.functionResult.Name] != nil && !c.functionResult.IsList {
		if _, ok := statement.Value.(*ast.ObjectExpr); ok {
			expression = "&" + expression
		}
	}
	c.write("return %s, nil", expression)
}

// writeReturnByType emits binary output code based on tracked variable type.
// Always writes Luxo binary — Luvia converts to JSON if needed.
func (c *compiler) writeReturnByType(expr string, vt valType) {
	if vt.isModel {
		if vt.isList {
			c.writeModelListReturn(expr, vt.name, c.paginationTotals[expr])
		} else {
			c.writeComputedResolve(vt.name, expr, false)
			c.write("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
		}
		return
	}
	if vt.isList {
		if c.writePrimitiveList(expr, vt.name) {
			return
		}
		if c.isTypeDecl(vt.name) {
			c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", vt.name, expr)
			return
		}
	}
	if appendExpr, ok := binaryScalarAppend(vt.name, "req.Buf.B", expr, c.enums[vt.name]); ok {
		c.write("req.Buf.B = %s", appendExpr)
	} else if c.isTypeDecl(vt.name) {
		c.write("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
	} else {
		c.write("_ = %s // unsupported return type for binary encoding", expr)
	}
}

// writeScalarReturn emits binary output for non-model return values.
func (c *compiler) writeScalarReturn(expr string) {
	if c.api != nil && c.api.ReturnType != nil {
		typeName := c.api.ReturnType.Name
		if model := c.models[typeName]; model != nil {
			if c.api.ReturnType.IsList {
				if c.paginate {
					resultName := c.nextPaginationName("Result")
					c.write("%s := %s", resultName, expr)
					c.writeModelListReturn(resultName, typeName, "")
				} else {
					c.writeComputedResolve(typeName, expr, true)
					c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", typeName, expr)
				}
			} else {
				c.write("_result := %s", expr)
				c.writeComputedResolve(typeName, "&_result", false)
				c.write("_result.WriteLuxo(req.Buf, req.FieldMask)")
			}
			return
		}
		if c.api.ReturnType.IsList {
			if c.writePrimitiveList(expr, typeName) {
				return
			}
			if c.isTypeDecl(typeName) {
				c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", typeName, expr)
				return
			}
		}
		if appendExpr, ok := binaryScalarAppend(typeName, "req.Buf.B", expr, c.enums[typeName]); ok {
			c.write("req.Buf.B = %s", appendExpr)
			return
		}
		// Try as type with WriteLuxo — value receiver
		if c.isTypeDecl(typeName) {
			c.write("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
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
	c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", modelName, expr)
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
	c.write("req.Buf.B = codec.AppendVarint(req.Buf.B, uint64(len(%s)))", expr)
	c.write("for _, v := range %s {", expr)
	oldIndent := c.indent
	c.indent += "\t"
	c.write("req.Buf.B = %s", appendExpr)
	c.indent = oldIndent
	c.write("}")
	return true
}

func binaryScalarAppend(typeName, dst, value string, isEnum bool) (string, bool) {
	if isEnum {
		return fmt.Sprintf("codec.AppendString(%s, string(%s))", dst, value), true
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
		return fmt.Sprintf("codec.AppendSvarint(%s, %s.Unix())", dst, value), true
	case "Duration":
		return fmt.Sprintf("codec.AppendSvarint(%s, int64(%s))", dst, value), true
	case "UUID":
		return fmt.Sprintf("codec.AppendUUID(%s, %s)", dst, value), true
	case "Decimal":
		return fmt.Sprintf("codec.AppendString(%s, %s.String())", dst, value), true
	case "Bytes", "JSON":
		return fmt.Sprintf("codec.AppendBytes(%s, %s)", dst, value), true
	default:
		return "", false
	}
}

// isTypeDecl checks if a name is a known `type` declaration (has WriteLuxo but is not a model).
func (c *compiler) isTypeDecl(name string) bool {
	if c.types != nil {
		return c.types[name]
	}
	// Fallback: if not a model, not a primitive, not an enum — assume it's a type
	if _, isModel := c.models[name]; isModel {
		return false
	}
	if c.enums[name] {
		return false
	}
	switch name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal", "Bytes", "JSON", "", "GroupResult":
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
	if elvis, ok := s.Expr.(*ast.ElvisExpr); ok {
		c.compileElvisGuard(elvis)
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
	// Transaction as standalone statement — check error
	// transaction { ... } is parsed as CallExpr(Ident("transaction"), lambda)
	if call, ok := s.Expr.(*ast.CallExpr); ok {
		if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "transaction" {
			expr := c.compileExpr(s.Expr)
			c.write("if err := %s; err != nil {", expr)
			c.writeErrorReturn("\t", "err")
			c.write("}")
			return
		}
	}
	if c.compileDiscardedFunctionCall(s.Expr) {
		return
	}
	expr := c.compileExpr(s.Expr)
	c.write("%s", expr)
}

func (c *compiler) compileDiscardedFunctionCall(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
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
	cond := c.compileExpr(s.Condition)
	// null → nil
	cond = strings.ReplaceAll(cond, " null", " nil")
	cond = strings.ReplaceAll(cond, "null ", "nil ")
	c.write("if %s {", cond)
	c.compileBlock(s.Then)
	c.write("}")
}

// compileElvisGuard: x ?: throw Error
// - nullable: x == nil → throw
// - bool: !x → throw
// - (bool, error): val, err := x; if err → return err; if !val → throw
func (c *compiler) compileElvisGuard(e *ast.ElvisExpr) {
	right := c.compileExpr(e.Right)

	// !expr ?: throw → if expr { return ... }
	if unary, ok := e.Left.(*ast.UnaryExpr); ok && unary.Op == "!" {
		inner := c.compileExpr(unary.Value)
		c.write("if %s {", inner)
		c.writeErrorReturn("\t", right)
		c.write("}")
		return
	}

	// Bool method call (returns (bool, error)): val, err := x; if !val { throw }
	if isErrorReturningBool(e.Left) {
		left := c.compileExpr(e.Left)
		c.write("_ok, _err := %s", left)
		c.write("if _err != nil {")
		c.writeErrorReturn("\t", "_err")
		c.write("}")
		c.write("if !_ok {")
		c.writeErrorReturn("\t", right)
		c.write("}")
		return
	}

	// Default: pointer nil check — x ?: throw → if x == nil { return ... }
	left := c.compileExpr(e.Left)
	// Bool (non-error): if !val { throw }
	if isBoolExpr(e.Left) {
		c.write("if !%s {", left)
	} else {
		c.write("if %s == nil {", left)
	}
	c.writeErrorReturn("\t", right)
	c.write("}")
}

// compileBangElvisGuard: x !: throw Error
// - bool: if x → throw
// - (bool, error): val, err := x; if err → return err; if val → throw
func (c *compiler) compileBangElvisGuard(e *ast.BangElvisExpr) {
	right := c.compileThrowExpr(e.Right)

	if isErrorReturningBool(e.Left) {
		left := c.compileExpr(e.Left)
		c.write("_ok, _err := %s", left)
		c.write("if _err != nil {")
		c.writeErrorReturn("\t", "_err")
		c.write("}")
		c.write("if _ok {")
		c.writeErrorReturn("\t", right)
		c.write("}")
		return
	}

	left := c.compileExpr(e.Left)
	c.write("if %s {", left)
	c.writeErrorReturn("\t", right)
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
	var args []string
	for _, a := range s.Args {
		args = append(args, fmt.Sprintf("%s: %s", str.Capitalize(a.Name), c.compileExpr(a.Value)))
	}

	// Check if event is cross-module
	prefix := ""
	if c.generator != nil && c.generator.events != nil && c.api != nil {
		currentMod := moduleNameFromFile(c.api.Pos.File)
		evModule := c.generator.events.EventModule[s.EventName]
		if evModule != "" && evModule != currentMod {
			prefix = evModule + "_luxo."
		}
	}

	if c.inAsync {
		c.write("%sEmit%s(ctx, app.EventBus, %s%sEvent{%s})",
			prefix, s.EventName, prefix, s.EventName, strings.Join(args, ", "))
	} else {
		c.write("if err := %sEmit%s(ctx, app.EventBus, %s%sEvent{%s}); err != nil {",
			prefix, s.EventName, prefix, s.EventName, strings.Join(args, ", "))
		c.writeErrorReturn("\t", "err")
		c.write("}")
	}
}

// compileExpr compiles an expression to a Go expression string.
func (c *compiler) compileExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.Literal:
		return c.compileLiteral(e)
	case *ast.MemberExpr:
		return c.compileMember(e)
	case *ast.CallExpr:
		return c.compileCall(e)
	case *ast.BinaryExpr:
		return c.compileBinary(e)
	case *ast.ElvisExpr:
		// Standalone elvis (not as guard) — not common in Phase 1
		return fmt.Sprintf("/* elvis */ %s", c.compileExpr(e.Left))
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
		return c.compileLambda(e)
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
			if c.yieldAddr {
				name := fmt.Sprintf("_yield%d", c.yieldTmp)
				c.yieldTmp++
				c.write("%s := %s", name, val)
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
		panic(fmt.Sprintf("codegen: unhandled expression type %T", expr))
	}
}

func (c *compiler) compileLiteral(e *ast.Literal) string {
	switch e.Kind {
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

// compileMember: obj.field
func (c *compiler) compileMember(e *ast.MemberExpr) string {
	if ident, ok := e.Object.(*ast.Ident); ok {
		// error.NotFound → errors.NotFound
		if ident.Name == "error" {
			return fmt.Sprintf("errors.%s", str.Capitalize(e.Field))
		}
		// my.id → identity.ID(), my.field → identity.String("field")
		// my.role (enum) → MemberRole(identity.String("role"))
		if ident.Name == "my" {
			if e.Field == "id" {
				return "identity.ID()"
			}
			accessor := fmt.Sprintf("identity.String(%q)", e.Field)
			// If the MemberExpr has a TypeTag that's an enum, wrap with type cast
			if tag := e.GetTypeTag(); tag != "" && c.enums[tag] {
				return fmt.Sprintf("%s(%s)", tag, accessor)
			}
			return accessor
		}
		// Enum.VALUE → EnumVALUE (Go enum constant)
		if c.enums[ident.Name] {
			return ident.Name + strings.ToUpper(e.Field)
		}
	}
	// Duration properties: n.days, n.hours, n.minutes, n.seconds, n.milliseconds
	// Uses TypeTag set by semantic analyzer — only triggers when object is numeric
	if e.GetTypeTag() == "Duration" {
		if unit, ok := durationUnits[e.Field]; ok {
			return fmt.Sprintf("(time.Duration(%s) * %s)", c.compileExpr(e.Object), unit)
		}
	}
	// Log methods: "message".i / .d / .w / .e — only on string literals or template strings
	if isLogTarget(e.Object) {
		switch e.Field {
		case "i":
			return fmt.Sprintf("luxolog.Info(%s)", c.compileExpr(e.Object))
		case "d":
			return fmt.Sprintf("luxolog.Debug(%s)", c.compileExpr(e.Object))
		case "w":
			return fmt.Sprintf("luxolog.Warn(%s)", c.compileExpr(e.Object))
		case "e":
			return fmt.Sprintf("luxolog.Error(%s)", c.compileExpr(e.Object))
		}
	}

	obj := c.compileExpr(e.Object)
	return fmt.Sprintf("%s.%s", obj, str.Capitalize(e.Field))
}

// compileInstanceMethod compiles model instance methods like variable.delete(), variable.update(...).
// Returns empty string if not a model instance method.
func (c *compiler) compileInstanceMethod(e *ast.CallExpr) string {
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
	varName := ident.Name
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
		for _, arg := range e.Args {
			if arg.Name != "" {
				val := c.compileExpr(arg.Value)
				if model, ok := c.models[modelName]; ok && isHashField(model, arg.Name) {
					hashedVar := "hashed" + str.Capitalize(arg.Name)
					c.write("%s, err := luxocrypto.HashPassword(%s)", hashedVar, val)
					c.writeErrorGuard()
					val = hashedVar
				}
				sets = append(sets, fmt.Sprintf("lux.SetField{Col: %q, Val: %s}", str.ToSnakeCase(arg.Name), val))
			}
		}
		return fmt.Sprintf("app.%s.Where(%sWhere.%s.Eq(%s.%s)).Update(ctx, %s)",
			modelName, modelName, idGoName, varName, idGoName, strings.Join(sets, ", "))
	}
	return ""
}

// compileCall: handles Model.where(...).first(), Model.create(...), etc.
func (c *compiler) compileCall(e *ast.CallExpr) string {
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

	if result, ok := c.compileChannelConstructor(e); ok {
		return result
	}

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

func (c *compiler) compileChannelConstructor(e *ast.CallExpr) (string, bool) {
	ident, ok := e.Func.(*ast.Ident)
	if !ok || ident.Name != "Channel" {
		return "", false
	}
	size := "0"
	if len(e.Args) > 0 {
		size = c.compileExpr(e.Args[0].Value)
	}
	return fmt.Sprintf("make(chan any, %s)", size), true
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
	sub := c.subCompiler()
	sub.indent = c.indent + "\t"
	for _, stmt := range lambda.Body.Stmts {
		sub.compileStmt(stmt)
	}
	return fmt.Sprintf("app.DB.Tx(ctx, func(tx *%s.DB) error {\n%s%s\treturn nil\n%s})",
		c.dbPackage(), sub.b.String(), c.indent, c.indent), true
}

func (c *compiler) compileChannelClose(e *ast.CallExpr) (string, bool) {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok || len(e.Args) != 0 || member.Field != "close" {
		return "", false
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	valueType, exists := c.vars[ident.Name]
	if !exists || !valueType.isChan {
		return "", false
	}
	return fmt.Sprintf("close(%s)", ident.Name), true
}

func (c *compiler) compileModelCallChain(e *ast.CallExpr) (string, bool) {
	chain := flattenChain(e)
	if len(chain) < 2 {
		return "", false
	}
	ident, ok := chain[0].expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	if _, isModel := c.models[ident.Name]; !isModel {
		return "", false
	}
	return c.compileModelChain(ident.Name, chain[1:]), true
}

func (c *compiler) compileGenericCall(e *ast.CallExpr) string {
	funcExpr := c.compileExpr(e.Func)
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
	args := []string{"ctx"}
	if c.selectionFunctions[fn.Name] {
		args = append(args, c.clientSelection)
	}
	args = append(args, c.compileDeclaredFunctionArguments(call, fn)...)
	expression := fmt.Sprintf("app.%s(%s)", fn.Name, strings.Join(args, ", "))
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
	values, variadicValues := declaredFunctionArgumentValues(call, fn)
	args := make([]string, 0, len(call.Args))
	for index, param := range fn.Params {
		if param.Spread {
			for _, value := range variadicValues {
				args = append(args, c.compileExpr(value))
			}
			continue
		}
		if values[index] != nil {
			args = append(args, c.compileExpr(values[index]))
			continue
		}
		args = append(args, c.compileFunctionDefault(param))
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
	c.write("%s := %s", name, value)
	return "&" + name
}

func declaredFunctionArgumentValues(call *ast.CallExpr, fn *ast.FnDecl) ([]ast.Expr, []ast.Expr) {
	values := make([]ast.Expr, len(fn.Params))
	variadicValues := make([]ast.Expr, 0, len(call.Args))
	positions := make(map[string]int, len(fn.Params))
	for index, param := range fn.Params {
		positions[param.Name] = index
	}
	nextPosition := 0
	for _, argument := range call.Args {
		position := nextPosition
		if argument.Name != "" {
			position = positions[argument.Name]
		} else if position < len(fn.Params) && !fn.Params[position].Spread {
			nextPosition++
		}
		if fn.Params[position].Spread {
			variadicValues = append(variadicValues, argument.Value)
			continue
		}
		values[position] = argument.Value
	}
	return values, variadicValues
}

// Write directly to the output buffer without allocating an intermediate statement.
func (c *compiler) writeErrorReturn(indent, expression string) {
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
			c.compileOrderByChain(b, link.args)
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
				chain = append([]chainLink{{method: member.Field, args: e.Args}}, chain...)
				expr = member.Object
				continue
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

	// Detect groupBy chain: Model.where(...).groupBy { it.field }.select { ... }
	if idx := findGroupByLink(links); idx >= 0 {
		c.compileGroupByChain(&b, modelName, links, idx, scopeInjected)
		return b.String()
	}

	for i, link := range links {
		// Terminal methods — return immediately
		if done := c.compileTerminalMethod(&b, modelName, link); done {
			return b.String()
		}
		// Modifier methods — append to chain and continue
		c.compileModifierMethod(&b, modelName, link, i, len(links), scopeInjected)
	}
	return b.String()
}

// compileModifierMethod handles non-terminal method links in a model chain.
func (c *compiler) compileModifierMethod(b *strings.Builder, modelName string, link chainLink, i, totalLinks int, scopeInjected bool) {
	switch link.method {
	case "where":
		c.compileWhereChain(b, modelName, link.args, i == 0 && !scopeInjected)
	case "create":
		c.compileCreateLink(b, modelName, link, i == totalLinks-1)
	case "select":
		selection := c.clientSelection
		if selection == "" {
			selection = "req.Select"
		}
		fmt.Fprintf(b, ".Select(select%sSQLColumns(%s)...)", modelName, selection)
	case "orderBy":
		c.compileOrderByChain(b, link.args)
	case "limit", "offset":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
			// Cast to int if the value is a variable (int64 from ParamInt)
			if _, isLit := link.args[0].Value.(*ast.Literal); !isLit {
				val = "int(" + val + ")"
			}
			fmt.Fprintf(b, ".%s(%s)", str.Capitalize(link.method), val)
		}
	case "groupBy":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
			fmt.Fprintf(b, ".GroupBy(%q)", str.ToSnakeCase(val))
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

// findGroupByLink returns the index of a groupBy link, or -1.
func findGroupByLink(links []chainLink) int {
	for i, l := range links {
		if l.method == "groupBy" {
			return i
		}
	}
	return -1
}

// compileGroupByChain compiles Model.where(...).groupBy { it.field }.select { aggs... }
// into a Query.GroupBy(ctx, cols, aggs) call returning []map[string]any.
func (c *compiler) compileGroupByChain(b *strings.Builder, modelName string, links []chainLink, groupByIdx int, scopeInjected bool) {
	// Compile preceding where/filter links
	for i := 0; i < groupByIdx; i++ {
		c.compileModifierMethod(b, modelName, links[i], i, len(links), scopeInjected)
	}
	if b.Len() == 0 {
		fmt.Fprintf(b, "app.%s", modelName)
	}

	// Extract group column(s) from groupBy lambda
	groupLink := links[groupByIdx]
	groupCols := c.extractGroupByCols(groupLink)

	// Extract aggregations from .select lambda (if present)
	var aggs []string
	for i := groupByIdx + 1; i < len(links); i++ {
		if links[i].method == "select" && len(links[i].args) > 0 {
			aggs = c.extractSelectAggs(links[i])
			break
		}
	}

	// Generate: .GroupBy(ctx, []string{"col"}, []lux.GroupAgg{...})
	fmt.Fprintf(b, ".GroupBy(ctx, []string{%s}, []lux.GroupAgg{%s})",
		strings.Join(groupCols, ", "),
		strings.Join(aggs, ", "))
}

// extractGroupByCols extracts column names from groupBy { it.field } or groupBy { [it.f1, it.f2] }.
func (c *compiler) extractGroupByCols(link chainLink) []string {
	if len(link.args) == 0 {
		return nil
	}
	arg := link.args[0].Value
	// Lambda wrapper: unwrap body
	if lambda, ok := arg.(*ast.LambdaExpr); ok && lambda.Body != nil && len(lambda.Body.Stmts) > 0 {
		if es, ok := lambda.Body.Stmts[0].(*ast.ExprStmt); ok {
			arg = es.Expr
		}
	}
	switch e := arg.(type) {
	case *ast.MemberExpr:
		return []string{fmt.Sprintf("%q", str.ToSnakeCase(e.Field))}
	case *ast.ListExpr:
		var cols []string
		for _, item := range e.Items {
			if m, ok := item.(*ast.MemberExpr); ok {
				cols = append(cols, fmt.Sprintf("%q", str.ToSnakeCase(m.Field)))
			}
		}
		return cols
	default:
		val := c.compileExpr(arg)
		return []string{fmt.Sprintf("%q", str.ToSnakeCase(val))}
	}
}

// extractSelectAggs extracts aggregation definitions from .select { key: it.key, count: it.count(), sum: it.sum { it.col } }.
func (c *compiler) extractSelectAggs(link chainLink) []string {
	if len(link.args) == 0 {
		return nil
	}
	arg := link.args[0].Value
	// Unwrap lambda body
	if lambda, ok := arg.(*ast.LambdaExpr); ok && lambda.Body != nil && len(lambda.Body.Stmts) > 0 {
		if es, ok := lambda.Body.Stmts[0].(*ast.ExprStmt); ok {
			arg = es.Expr
		}
	}
	obj, ok := arg.(*ast.ObjectExpr)
	if !ok {
		return nil
	}
	var aggs []string
	for _, field := range obj.Fields {
		// Skip key fields (it.key, it.key[0], etc) — those come from GROUP BY columns
		if isGroupKeyRef(field.Value) {
			continue
		}
		// Parse aggregation calls: it.count(), it.sum { it.col }, it.avg { it.col }, etc.
		if call, ok := field.Value.(*ast.CallExpr); ok {
			if member, ok := call.Func.(*ast.MemberExpr); ok {
				fn := strings.ToUpper(member.Field)
				col := ""
				// Lambda arg: it.sum { it.total } → col = "total"
				if len(call.Args) > 0 {
					col = extractLambdaField(call.Args[0].Value)
				}
				alias := str.ToSnakeCase(field.Name)
				if col == "" {
					aggs = append(aggs, fmt.Sprintf(`{Fn: %q, Alias: %q}`, fn, alias))
				} else {
					aggs = append(aggs, fmt.Sprintf(`{Fn: %q, Col: %q, Alias: %q}`, fn, str.ToSnakeCase(col), alias))
				}
			}
		}
	}
	return aggs
}

// isGroupKeyRef checks if an expression references the group key (it.key or it.key[N]).
func isGroupKeyRef(e ast.Expr) bool {
	if m, ok := e.(*ast.MemberExpr); ok {
		return m.Field == "key"
	}
	// it.key[0] — IndexExpr on MemberExpr
	return false
}

// compileCreateLink compiles a create() link with @hash and nullable field handling.
func (c *compiler) compileCreateLink(b *strings.Builder, modelName string, link chainLink, isLast bool) {
	// Pre-hash any @hash fields before building the create chain
	m := c.models[modelName]
	for _, arg := range link.args {
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && hasDirective(f.Directives, "hash") {
					val := c.compileExpr(arg.Value)
					hashed := "hashed" + str.Capitalize(arg.Name)
					c.write("%s, err := luxocrypto.HashPassword(%s)", hashed, val)
					c.writeErrorGuard()
				}
			}
		}
	}
	fmt.Fprintf(b, "app.%s.Create()", modelName)
	for _, arg := range link.args {
		val := c.compileExpr(arg.Value)
		// Use hashed value if field has @hash directive
		hashed := false
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && hasDirective(f.Directives, "hash") {
					val = "hashed" + str.Capitalize(arg.Name)
					hashed = true
				}
			}
		}
		// Wrap with & if the model field is nullable (SetXxx expects pointer)
		// but skip if the value is already a pointer (tracked via vars nullable flag)
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && f.Type != nil && f.Type.Nullable {
					// Check if value expression is already a pointer (nullable)
					// Uses NullableTag from semantic analyzer — zero guessing
					alreadyPtr := arg.Value.IsNullable()
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
		fmt.Fprintf(b, ".Set%s(%s)", str.Capitalize(arg.Name), val)
	}
	if isLast {
		b.WriteString(".Exec(ctx)")
	}
}

// compileWhereChain compiles a where() link into the query builder.
// Supports both named args (id: userId → ModelWhere.Id.Eq(userId))
// and expression args (it.email == email → ModelWhere.Email.Eq(email)).
func (c *compiler) compileWhereChain(b *strings.Builder, modelName string, args []*ast.NamedArg, isFirst bool) {
	if isFirst {
		fmt.Fprintf(b, "app.%s.Where(", modelName)
	} else {
		b.WriteString(".Where(")
	}
	for i, arg := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		if arg.Name != "" {
			// Named arg: where(id: userId) → ModelWhere.Id.Eq(userId)
			val := c.compileExpr(arg.Value)
			b.WriteString(fmt.Sprintf("%sWhere.%s.Eq(%s)", modelName, str.Capitalize(arg.Name), val))
		} else {
			b.WriteString(c.compileWhereArg(modelName, arg.Value))
		}
	}
	b.WriteByte(')')
}

// compileWhereArg compiles a where condition:
// it.email == email → ModelWhere.Email.Eq(email)
// email == email    → ModelWhere.Email.Eq(email) (legacy, both same name)
// it.title.contains("x") → ModelWhere.Title.Match("x")  (@search field)
// it.title.contains("x") → ModelWhere.Title.Like("%" + lux.EscapeLike("x") + "%")  (non-search)
func (c *compiler) compileWhereArg(modelName string, expr ast.Expr) string {
	// Handle method call conditions: it.field.contains/startsWith/endsWith(arg)
	if result := c.compileWhereMethodCall(modelName, expr); result != "" {
		return result
	}

	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return c.compileExpr(expr)
	}

	field := ""
	val := c.compileExpr(bin.Right)
	// Enum values need string() cast for Where conditions (StringField.Eq expects string)
	if member, ok := bin.Right.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && c.enums[ident.Name] {
			val = "string(" + val + ")"
		}
	}

	// it.field == value → field from member expr
	if member, ok := bin.Left.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == "it" {
			field = member.Field
		}
	}
	// field == value → field from ident (legacy/shorthand)
	if field == "" {
		if ident, ok := bin.Left.(*ast.Ident); ok {
			field = ident.Name
		}
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

	return fmt.Sprintf("%sWhere.%s.%s(%s)", modelName, str.Capitalize(field), op, val)
}

// compileWhereMethodCall handles string method calls in where context:
//
//	it.title.contains("x")  → ModelWhere.Title.Match(x)   if @search
//	it.title.contains("x")  → ModelWhere.Title.Like("%" + lux.EscapeLike(x) + "%")  otherwise
//	it.name.startsWith("x") → ModelWhere.Name.Like(lux.EscapeLike(x) + "%")
//	it.name.endsWith("x")   → ModelWhere.Name.Like("%" + lux.EscapeLike(x))
//
// Returns "" if expr is not a recognized method call pattern.
func (c *compiler) compileWhereMethodCall(modelName string, expr ast.Expr) string {
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

	// Extract field name from it.field.method(arg) pattern
	innerMember, ok := member.Object.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	if ident, ok := innerMember.Object.(*ast.Ident); !ok || ident.Name != "it" {
		return ""
	}
	fieldName := innerMember.Field
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
	left := c.compileExpr(e.Left)
	right := c.compileExpr(e.Right)
	// DateTime +/- Duration → time.Add(duration) / time.Add(-duration)
	// Only rewrite when left is DateTime (via TypeTag) and right is Duration
	if e.Left.GetTypeTag() == "DateTime" && isDurationExpr(e.Right) && (e.Op == "+" || e.Op == "-") {
		if e.Op == "-" {
			return fmt.Sprintf("%s.Add(-%s)", left, right)
		}
		return fmt.Sprintf("%s.Add(%s)", left, right)
	}
	return fmt.Sprintf("%s %s %s", left, e.Op, right)
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
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "now" {
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
	n := "32"
	if len(e.Args) > 0 {
		n = c.compileExpr(e.Args[0].Value)
	}
	switch member.Field {
	case "randomHex":
		// RandomHex returns (string, error) — assign to temp var with error check
		varName := "_hex"
		c.write("%s, _hexErr := luxocrypto.RandomHex(%s)", varName, n)
		c.write("if _hexErr != nil {\n%s\treturn _hexErr\n%s}", c.indent, c.indent)
		return varName
	case "randomBytes":
		return fmt.Sprintf("luxocrypto.RandomBytes(%s)", n)
	}
	return ""
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
		operand := c.compileExpr(e.Value)
		c.resultTmp++
		name := fmt.Sprintf("_result%d", c.resultTmp)
		c.write("%s, err := %s", name, operand)
		c.writeErrorGuard()
		return name
	}
	operand := c.compileExpr(e.Value)
	return fmt.Sprintf("%s%s", e.Op, operand)
}

// compileThrowExpr compiles the expression after `throw`:
// throw error.NotFound → errors.NotFound
// throw DuplicateEmail(email: email) → NewDuplicateEmail(email)
func (c *compiler) compileThrowExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.MemberExpr:
		// error.NotFound → errors.NotFound
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "error" {
			return fmt.Sprintf("errors.%s", str.Capitalize(e.Field))
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
	// Instance method: variable.update() → Int (rows affected), variable.delete() → Int
	if call, ok := expr.(*ast.CallExpr); ok {
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
		if _, isModel := c.models[ident.Name]; isModel {
			last := chain[len(chain)-1]
			switch last.method {
			case "first", "find":
				return valType{isModel: true, name: ident.Name}
			case "load":
				// PK load (no named args) → single model; FK load (named args) → list
				hasNamed := false
				for _, arg := range last.args {
					if arg.Name != "" {
						hasNamed = true
						break
					}
				}
				if hasNamed {
					return valType{isModel: true, isList: true, name: ident.Name}
				}
				return valType{isModel: true, name: ident.Name}
			case "all":
				return valType{isModel: true, isList: true, name: ident.Name}
			case "create", "exec":
				return valType{isModel: true, name: ident.Name}
			case "exists":
				return valType{name: "Boolean"}
			case "count":
				return valType{name: "Int"}
			case "update":
				return valType{name: "Int"} // rows affected
			case "delete", "deleteMany", "updateMany":
				return valType{name: "Int"} // rows affected
			case "sum", "avg", "min", "max":
				return valType{name: "Int"}
			}
			// groupBy chain → returns ([]map[string]any, error)
			if findGroupByLink(chain) >= 0 {
				return valType{isList: true, name: "GroupResult"}
			}
		}
	}
	return valType{}
}

// isModelQuery checks if an expression is a Model query chain (returns (*T, error)).
func (c *compiler) isModelQuery(expr ast.Expr) bool {
	// Instance method: variable.delete(), variable.update(...)
	if call, ok := expr.(*ast.CallExpr); ok {
		if member, ok := call.Func.(*ast.MemberExpr); ok {
			if ident, ok := member.Object.(*ast.Ident); ok {
				if vt, ok := c.vars[ident.Name]; ok && vt.isModel && !vt.isList {
					switch member.Field {
					case "delete", "update":
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
		if _, isModel := c.models[ident.Name]; isModel {
			// Check terminal method
			last := chain[len(chain)-1]
			switch last.method {
			case "first", "all", "create", "exec", "find", "load", "exists", "update", "updateMany", "delete", "deleteMany", "count",
				"sum", "avg", "min", "max":
				return true
			}
			// groupBy chain: check if any link is groupBy (not necessarily last)
			if findGroupByLink(chain) >= 0 {
				return true
			}
		}
	}
	return false
}

func isCountedPaginationQuery(expr ast.Expr) bool {
	chain := flattenChain(expr)
	return len(chain) >= 2 && chain[len(chain)-1].method == "all"
}

// compileUpdateChain compiles .update(field: val, ...) → .Update(ctx, SetField{...}, ...)
// Checks @hash fields and auto-hashes values before update.
func (c *compiler) compileUpdateChain(b *strings.Builder, modelName string, args []*ast.NamedArg) {
	if len(args) == 0 {
		b.WriteString(".Update(ctx)")
		return
	}
	// Pre-hash @hash fields
	model := c.models[modelName]
	for _, arg := range args {
		if model != nil && isHashField(model, arg.Name) {
			val := c.compileExpr(arg.Value)
			hashedVar := "hashed" + str.Capitalize(arg.Name)
			c.write("%s, err := luxocrypto.HashPassword(%s)", hashedVar, val)
			c.writeErrorGuard()
		}
	}
	var sets []string
	for _, arg := range args {
		col := str.ToSnakeCase(arg.Name)

		// Check @hash
		if model != nil && isHashField(model, arg.Name) {
			sets = append(sets, fmt.Sprintf("lux.SetField{Col: %q, Val: %s}", col, "hashed"+str.Capitalize(arg.Name)))
			continue
		}

		// Check for atomic pattern: obj.field + expr or obj.field - expr
		if bin, ok := arg.Value.(*ast.BinaryExpr); ok && (bin.Op == "+" || bin.Op == "-") {
			if member, ok := bin.Left.(*ast.MemberExpr); ok && member.Field == arg.Name {
				// obj.field + expr → AtomicField
				rightVal := c.compileExpr(bin.Right)
				sets = append(sets, fmt.Sprintf("lux.SetField{Col: %q, Val: %s, Atomic: %q}", col, rightVal, bin.Op))
				continue
			}
		}

		val := c.compileExpr(arg.Value)
		sets = append(sets, fmt.Sprintf("lux.SetField{Col: %q, Val: %s}", col, val))
	}
	fmt.Fprintf(b, ".Update(ctx, %s)", strings.Join(sets, ", "))
}

func splitBulkMutationArgs(args []*ast.NamedArg) (ast.Expr, []*ast.NamedArg) {
	var where ast.Expr
	sets := make([]*ast.NamedArg, 0, len(args))
	for _, arg := range args {
		if arg.Name == "where" {
			where = arg.Value
			continue
		}
		sets = append(sets, arg)
	}
	return where, sets
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
		fmt.Fprintf(b, "app.loaders.Extend%s.Load(ctx, %s, %s)", modelName, val, c.compiledLoadSelection())
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
		fmt.Fprintf(b, "app.loaders.%s.Load(ctx, %s, %s)", loaderName, vals[0], c.compiledLoadSelection())
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
		fmt.Fprintf(b, "}, %s)", c.compiledLoadSelection())
	}
}

func (c *compiler) compiledLoadSelection() string {
	if !c.hasLoadSelection {
		return "nil"
	}
	return c.loadSelection
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

func analyzeLoadSelections(body *ast.Block, models map[string]*ast.ModelDecl) map[string]string {
	bindings := make(map[string]string)
	collectLoadBindings(body, bindings)
	paths := make(map[string][][]string, len(bindings))
	ast.WalkExprs(body, func(expr ast.Expr) {
		member, ok := expr.(*ast.MemberExpr)
		if !ok {
			return
		}
		root, path := memberSelectionPath(member)
		modelName, exists := bindings[root]
		if exists && validModelSelectionPath(modelName, path, models) {
			paths[root] = append(paths[root], path)
		}
	})
	result := make(map[string]string, len(bindings))
	for variable := range bindings {
		result[variable] = compileSelectionLiteral(pruneSelectionPrefixes(paths[variable]))
	}
	return result
}

func collectLoadBindings(block *ast.Block, bindings map[string]string) {
	if block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		switch value := stmt.(type) {
		case *ast.ValStmt:
			if modelName, ok := directLoadModel(value.Value); ok {
				bindings[value.Name] = modelName
			}
		case *ast.IfStmt:
			collectLoadBindings(value.Then, bindings)
		case *ast.ForStmt:
			collectLoadBindings(value.Body, bindings)
		}
	}
}

func memberSelectionPath(member *ast.MemberExpr) (string, []string) {
	path := []string{member.Field}
	object := member.Object
	for {
		switch value := object.(type) {
		case *ast.Ident:
			return value.Name, path
		case *ast.MemberExpr:
			path = append([]string{value.Field}, path...)
			object = value.Object
		default:
			return "", nil
		}
	}
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
	case "find":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
			fmt.Fprintf(b, "app.%s.Where(%sWhere.%s.Eq(%s)).First(ctx)", modelName, modelName, primaryKeyGoName(c.models[modelName]), val)
		}
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
		where, _ := splitBulkMutationArgs(link.args)
		c.compileBulkMutationBase(b, modelName, where)
		if m, ok := c.models[modelName]; ok && isSoftDelete(m) {
			b.WriteString(".SoftDelete(ctx)")
		} else {
			b.WriteString(".Delete(ctx)")
		}
		return true
	}
	if link.method == "updateMany" {
		where, sets := splitBulkMutationArgs(link.args)
		c.compileBulkMutationBase(b, modelName, where)
		c.compileUpdateChain(b, modelName, sets)
		return true
	}
	// Seed builder if empty (terminal-only chain without prior modifier)
	if b.Len() == 0 {
		fmt.Fprintf(b, "app.%s", modelName)
	}
	switch link.method {
	case "delete":
		if m, ok := c.models[modelName]; ok && isSoftDelete(m) {
			fmt.Fprintf(b, ".SoftDelete(ctx)")
		} else {
			fmt.Fprintf(b, ".Delete(ctx)")
		}
		return true
	case "all":
		if c.paginate {
			b.WriteString(".Limit(req.PageSize).Offset((req.Page - 1) * req.PageSize).AllWithCount(ctx)")
		} else {
			b.WriteString(".All(ctx)")
		}
		return true
	case "first", "exists", "exec", "count":
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
			col := extractLambdaField(link.args[0].Value)
			if col == "" {
				col = c.compileExpr(link.args[0].Value)
			}
			fmt.Fprintf(b, ".%s(ctx, %q)", str.Capitalize(link.method), str.ToSnakeCase(col))
		} else {
			fmt.Fprintf(b, ".%s(ctx)", str.Capitalize(link.method))
		}
		return true
	}
	return false
}

// compileOrderByChain compiles .orderBy(field.desc) → .OrderBy("field DESC")
func (c *compiler) compileOrderByChain(b *strings.Builder, args []*ast.NamedArg) {
	var clauses []string
	for _, arg := range args {
		expr := arg.Value
		dir := "ASC"
		if member, ok := expr.(*ast.MemberExpr); ok && (member.Field == "asc" || member.Field == "desc") {
			expr = member.Object
			dir = strings.ToUpper(member.Field)
		}
		col := str.ToSnakeCase(c.orderByColumn(expr))
		clauses = append(clauses, fmt.Sprintf("%q", col+" "+dir))
	}
	fmt.Fprintf(b, ".OrderBy(%s)", strings.Join(clauses, ", "))
}

func (c *compiler) orderByColumn(expr ast.Expr) string {
	if member, ok := expr.(*ast.MemberExpr); ok {
		if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == "it" {
			return member.Field
		}
	}
	return c.compileExpr(expr)
}

// --- Phase 2 statement compilers ---

// compileFor: for item in collection { ... } or for i in 0..10 { ... }
func (c *compiler) compileFor(s *ast.ForStmt) {
	// Check if collection is a range expression → C-style for loop
	if rangeExpr, ok := s.Collection.(*ast.RangeExpr); ok {
		c.compileForRange(s.VarName, rangeExpr, s.Body)
		return
	}
	coll := c.compileExpr(s.Collection)
	// Channel range uses single variable: for v := range ch
	if c.isChannelVar(s.Collection) {
		c.write("for %s := range %s {", s.VarName, coll)
	} else {
		c.write("for _, %s := range %s {", s.VarName, coll)
	}
	c.compileBlock(s.Body)
	c.write("}")
}

// compileForRange: for i in start..end { ... } → for i := int64(start); i <= end; i++ { ... }
// Uses int64 cast on start to ensure type compatibility with int64 parameters.
func (c *compiler) compileForRange(varName string, r *ast.RangeExpr, body *ast.Block) {
	start := c.compileExpr(r.Start)
	end := c.compileExpr(r.End)
	c.write("for %s := int64(%s); %s <= %s; %s++ {", varName, start, varName, end, varName)
	c.compileBlock(body)
	c.write("}")
}

// containsYield walks an AST block and returns true if any YieldExpr is found.
func containsYield(block *ast.Block) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Stmts {
		if containsYieldStmt(stmt) {
			return true
		}
	}
	return false
}

// containsYieldStmt checks a single statement for yield expressions.
func containsYieldStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return containsYieldExpr(s.Expr)
	case *ast.ValStmt:
		return containsYieldExpr(s.Value)
	case *ast.IfStmt:
		if containsYield(s.Then) {
			return true
		}
	case *ast.ForStmt:
		if containsYield(s.Body) {
			return true
		}
	}
	return false
}

// containsYieldExpr checks if an expression is or contains a yield.
func containsYieldExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if _, ok := expr.(*ast.YieldExpr); ok {
		return true
	}
	return false
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
	sub := c.subCompiler()
	sub.indent = c.indent + "\t\t"
	sub.inForExpr = true
	returnType := c.goTypeForExpr(s)
	if returnType == "" {
		returnType = "any"
	}
	sub.yieldAddr = c.yieldNeedsAddress(s)

	// Compile entire body — YieldExpr will emit "return <value>"
	for _, stmt := range s.Body.Stmts {
		sub.compileStmt(stmt)
	}

	if rangeExpr, ok := s.Collection.(*ast.RangeExpr); ok {
		start := c.compileExpr(rangeExpr.Start)
		end := c.compileExpr(rangeExpr.End)
		return fmt.Sprintf("func() %s {\n%s\tfor %s := int64(%s); %s <= %s; %s++ {\n%s%s\t}\n%s\treturn nil\n%s}()",
			returnType, c.indent, s.VarName, start, s.VarName, end, s.VarName,
			sub.b.String(), c.indent, c.indent, c.indent)
	}

	coll := c.compileExpr(s.Collection)
	return fmt.Sprintf("func() %s {\n%s\tfor _, %s := range %s {\n%s%s\t}\n%s\treturn nil\n%s}()",
		returnType, c.indent, s.VarName, coll,
		sub.b.String(), c.indent, c.indent, c.indent)
}

// compileForExprCollect generates a closure collecting all values into a typed slice.
func (c *compiler) compileForExprCollect(s *ast.ForStmt) string {
	sub := c.subCompiler()
	sub.indent = c.indent + "\t\t"
	resultType := c.goTypeForExpr(s)
	if resultType == "" {
		resultType = "[]any"
	}

	// Compile all but last statement normally
	for i := 0; i < len(s.Body.Stmts)-1; i++ {
		sub.compileStmt(s.Body.Stmts[i])
	}
	// Last statement is the collected value
	lastExpr := ""
	if es, ok := s.Body.Stmts[len(s.Body.Stmts)-1].(*ast.ExprStmt); ok {
		lastExpr = sub.compileExpr(es.Expr)
	} else if rs, ok := s.Body.Stmts[len(s.Body.Stmts)-1].(*ast.ReturnStmt); ok && rs.Value != nil {
		lastExpr = sub.compileExpr(rs.Value)
	}

	if rangeExpr, ok := s.Collection.(*ast.RangeExpr); ok {
		start := c.compileExpr(rangeExpr.Start)
		end := c.compileExpr(rangeExpr.End)
		return fmt.Sprintf("func() %s {\n%s\tvar _result %s\n%s\tfor %s := int64(%s); %s <= %s; %s++ {\n%s%s\t\t_result = append(_result, %s)\n%s\t}\n%s\treturn _result\n%s}()",
			resultType, c.indent, resultType, c.indent, s.VarName, start, s.VarName, end, s.VarName,
			sub.b.String(), c.indent, lastExpr, c.indent, c.indent, c.indent)
	}

	coll := c.compileExpr(s.Collection)
	return fmt.Sprintf("func() %s {\n%s\tvar _result %s\n%s\tfor _, %s := range %s {\n%s%s\t\t_result = append(_result, %s)\n%s\t}\n%s\treturn _result\n%s}()",
		resultType, c.indent, resultType, c.indent, s.VarName, coll,
		sub.b.String(), c.indent, lastExpr, c.indent, c.indent, c.indent)
}

// compileBlock compiles a block body with indentation.
func (c *compiler) compileBlock(block *ast.Block) {
	old := c.indent
	c.indent += "\t"
	for _, stmt := range block.Stmts {
		c.compileStmt(stmt)
	}
	c.indent = old
}

// compileAssign: x = expr, x += expr, etc.
func (c *compiler) compileAssign(s *ast.AssignStmt) {
	target := c.compileExpr(s.Target)
	val := c.compileExpr(s.Value)
	c.write("%s %s %s", target, s.Op, val)
}

// --- Phase 2 expression compilers ---

// compileList emits a concrete slice when semantic analysis resolved the element type.
func (c *compiler) compileList(e *ast.ListExpr) string {
	var items []string
	for _, item := range e.Items {
		items = append(items, c.compileExpr(item))
	}
	goType := c.goTypeForExpr(e)
	if goType == "" {
		goType = "[]any"
	}
	return goType + "{" + strings.Join(items, ", ") + "}"
}

func (c *compiler) compileTypedList(e *ast.ListExpr, listType *ast.TypeRef) string {
	items := make([]string, 0, len(e.Items))
	for _, item := range e.Items {
		items = append(items, c.compileExpr(item))
	}
	goType := resolveGoType(listType)
	if _, isModel := c.models[listType.Name]; isModel {
		goType = "[]*" + listType.Name
	}
	return goType + "{" + strings.Join(items, ", ") + "}"
}

// compileTemplate: "hello ${name}: ${count} items"
// Generates strings.Builder for zero-alloc string concatenation.
// String expressions are written directly; int64 uses strconv.AppendInt.
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
	var b strings.Builder
	b.WriteString("func() string {\n")
	fmt.Fprintf(&b, "%s\tvar _sb strings.Builder\n", c.indent)
	for _, part := range e.Parts {
		if lit, ok := part.(*ast.Literal); ok && lit.Kind == token.String {
			fmt.Fprintf(&b, "%s\t_sb.WriteString(%q)\n", c.indent, lit.Value)
		} else {
			compiled := c.compileExpr(part)
			if c.isEnumExpr(part) {
				fmt.Fprintf(&b, "%s\t_sb.WriteString(string(%s))\n", c.indent, compiled)
			} else if c.isStringExpr(part) {
				fmt.Fprintf(&b, "%s\t_sb.WriteString(%s)\n", c.indent, compiled)
			} else if c.isIntExpr(part) {
				fmt.Fprintf(&b, "%s\t_sb.WriteString(strconv.FormatInt(int64(%s), 10))\n", c.indent, compiled)
			} else {
				fmt.Fprintf(&b, "%s\tfmt.Fprintf(&_sb, \"%%v\", %s)\n", c.indent, compiled)
			}
		}
	}
	fmt.Fprintf(&b, "%s\treturn _sb.String()\n%s}()", c.indent, c.indent)
	return b.String()
}

// isStringExpr checks if an expression is likely a string type.
// Uses heuristics: MemberExpr on known string fields, string literals, etc.
func (c *compiler) isStringExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok {
			if vt, ok := c.vars[ident.Name]; ok && vt.isModel {
				if m, ok := c.models[vt.name]; ok {
					for _, f := range m.Fields {
						if f.Name == e.Field && f.Type != nil && f.Type.Name == "String" {
							return true
						}
					}
				}
			}
		}
	case *ast.Ident:
		if vt, ok := c.vars[e.Name]; ok {
			if vt.name == "String" || vt.name == "string" {
				return true
			}
		}
	case *ast.Literal:
		return e.Kind == token.String
	case *ast.CallExpr:
		// String method calls (e.g. .trim(), .lowercase())
		if member, ok := e.Func.(*ast.MemberExpr); ok {
			return c.isStringExpr(member.Object)
		}
	}
	return false
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

// compileRange: 1..10 → not directly expressible, used in for loops
// compileRange: 0..10 as standalone expression → generates inline slice
func (c *compiler) compileRange(e *ast.RangeExpr) string {
	start := c.compileExpr(e.Start)
	end := c.compileExpr(e.End)
	return fmt.Sprintf("lux.IntRange(%s, %s)", start, end)
}

// compileObject: { name: "lin", age: 18 } → struct literal
func (c *compiler) compileObject(e *ast.ObjectExpr) string {
	var fields []string
	for _, f := range e.Fields {
		val := c.compileExpr(f.Value)
		fields = append(fields, fmt.Sprintf("%s: %s", str.Capitalize(f.Name), val))
	}
	prefix := ""
	if e.TypeName != "" {
		prefix = e.TypeName
	} else if c.api != nil && c.api.ReturnType != nil && c.isTypeDecl(c.api.ReturnType.Name) {
		prefix = c.api.ReturnType.Name
	}
	return prefix + "{" + strings.Join(fields, ", ") + "}"
}

// compileWhen: when { cond -> expr, else -> expr }
func (c *compiler) compileWhen(e *ast.WhenExpr) string {
	// Detect channel select: when { <-ch -> ... } → Go select
	if e.Subject == nil && c.hasChannelBranch(e) {
		return c.compileSelect(e)
	}

	// when is compiled inline as a helper variable + switch
	retType := c.inferWhenReturnType(e)
	var b strings.Builder
	fmt.Fprintf(&b, "func() %s {\n", retType)
	// Check if any branch uses is-type → type switch
	hasIsType := false
	for _, br := range e.Branches {
		if br.IsType != "" {
			hasIsType = true
			break
		}
	}

	if hasIsType && e.Subject != nil {
		// Type switch: when(x) { is String -> ..., is Int -> ... }
		subj := c.compileExpr(e.Subject)
		fmt.Fprintf(&b, "%s\tswitch %s.(type) {\n", c.indent, subj)
		for _, br := range e.Branches {
			body := c.compileExpr(br.Body)
			if br.IsType != "" {
				fmt.Fprintf(&b, "%s\tcase %s:\n%s\t\treturn %s\n", c.indent, br.IsType, c.indent, body)
			} else if br.Condition != nil {
				cond := c.compileExpr(br.Condition)
				fmt.Fprintf(&b, "%s\tcase %s:\n%s\t\treturn %s\n", c.indent, cond, c.indent, body)
			}
		}
	} else if e.Subject != nil {
		subj := c.compileExpr(e.Subject)
		fmt.Fprintf(&b, "%s\tswitch %s {\n", c.indent, subj)
		for _, br := range e.Branches {
			cond := c.compileExpr(br.Condition)
			body := c.compileExpr(br.Body)
			fmt.Fprintf(&b, "%s\tcase %s:\n%s\t\treturn %s\n", c.indent, cond, c.indent, body)
		}
	} else {
		fmt.Fprintf(&b, "%s\tswitch {\n", c.indent)
		for _, br := range e.Branches {
			cond := c.compileExpr(br.Condition)
			body := c.compileExpr(br.Body)
			fmt.Fprintf(&b, "%s\tcase %s:\n%s\t\treturn %s\n", c.indent, cond, c.indent, body)
		}
	}
	if e.Else != nil {
		elseExpr := c.compileExpr(e.Else)
		fmt.Fprintf(&b, "%s\tdefault:\n%s\t\treturn %s\n", c.indent, c.indent, elseExpr)
	}
	fmt.Fprintf(&b, "%s\t}\n", c.indent)
	if e.Else == nil {
		fmt.Fprintf(&b, "%s\treturn %s\n", c.indent, zeroValueForType(retType))
	}
	fmt.Fprintf(&b, "%s}()", c.indent)
	return b.String()
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
	retType := c.inferWhenReturnType(e)
	var b strings.Builder
	fmt.Fprintf(&b, "func() %s {\n%s\tselect {\n", retType, c.indent)

	for _, br := range e.Branches {
		if u, ok := br.Condition.(*ast.UnaryExpr); ok && u.Op == "<-" {
			ch := c.compileExpr(u.Value)
			// Check if body is a lambda with a named param: { msg -> ... }
			if lambda, ok := br.Body.(*ast.LambdaExpr); ok && len(lambda.Params) > 0 {
				param := lambda.Params[0]
				fmt.Fprintf(&b, "%s\tcase %s := <-%s:\n", c.indent, param, ch)
				sub := c.subCompiler()
				sub.indent = c.indent + "\t\t"
				for _, stmt := range lambda.Body.Stmts {
					sub.compileStmt(stmt)
				}
				b.WriteString(sub.b.String())
			} else {
				fmt.Fprintf(&b, "%s\tcase %s:\n", c.indent, c.compileExpr(br.Condition))
				body := c.compileExpr(br.Body)
				fmt.Fprintf(&b, "%s\t\t%s\n", c.indent, body)
			}
		} else {
			// Non-channel branch (e.g., timeout) — compile as regular case
			cond := c.compileExpr(br.Condition)
			fmt.Fprintf(&b, "%s\tcase %s:\n", c.indent, cond)
			body := c.compileExpr(br.Body)
			fmt.Fprintf(&b, "%s\t\t%s\n", c.indent, body)
		}
	}

	if e.Else != nil {
		fmt.Fprintf(&b, "%s\tdefault:\n", c.indent)
		elseExpr := c.compileExpr(e.Else)
		fmt.Fprintf(&b, "%s\t\t%s\n", c.indent, elseExpr)
	}

	zeroVal := zeroValueForType(retType)
	fmt.Fprintf(&b, "%s\t}\n%s\treturn %s\n%s}()", c.indent, c.indent, zeroVal, c.indent)
	return b.String()
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
	if ident, ok := member.Object.(*ast.Ident); ok && ident.Name == "it" {
		return member.Field
	}
	return ""
}

// compileLambda: { it -> it.price } → func(it any) any { ... }
func (c *compiler) compileLambda(e *ast.LambdaExpr) string {
	params := "it"
	if len(e.Params) > 0 {
		params = strings.Join(e.Params, ", ")
	}
	sub := c.subCompiler()
	sub.indent = c.indent + "\t"
	for _, stmt := range e.Body.Stmts {
		sub.compileStmt(stmt)
	}
	return fmt.Sprintf("func(%s any) any {\n%s%s}", params, sub.b.String(), c.indent)
}

// compileTransaction: tx { ... } → app.DB.Tx(ctx, func(ctx) error { ... })
func (c *compiler) compileTransaction(e *ast.TransactionExpr) string {
	sub := c.subCompiler()
	sub.indent = c.indent + "\t"
	for _, stmt := range e.Body.Stmts {
		sub.compileStmt(stmt)
	}
	return fmt.Sprintf("app.DB.Tx(ctx, func(ctx context.Context) error {\n%s%s\treturn nil\n%s})",
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
		nativeFunctions:    c.nativeFunctions,
		functions:          c.functions,
		selectionFunctions: c.selectionFunctions,
		clientSelection:    c.clientSelection,
		loadSelections:     c.loadSelections,
		loadSelection:      c.loadSelection,
		hasLoadSelection:   c.hasLoadSelection,
	}
}

// compileAsync: async { body } → go func() { body }()
func (c *compiler) compileAsync(e *ast.AsyncExpr) string {
	sub := c.subCompiler()
	sub.indent = c.indent + "\t"
	sub.inAsync = true
	for _, stmt := range e.Body.Stmts {
		sub.compileStmt(stmt)
	}
	return fmt.Sprintf("go func() {\n%s%s}()", sub.b.String(), c.indent)
}

type awaitTask struct {
	varName   string
	goType    string
	expr      string
	source    ast.Expr
	isQuery   bool
	valueType valType
}

// compileAwaitBindings compiles the documented destructuring form:
// val (a, b) = await { queryA(); queryB() }.
func (c *compiler) compileAwaitBindings(e *ast.AwaitExpr, names []string) {
	tasks := make([]awaitTask, 0, len(names))
	for _, stmt := range e.Body.Stmts {
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok || exprStmt.Expr == nil {
			continue
		}
		if len(tasks) >= len(names) {
			return
		}
		tasks = append(tasks, c.newAwaitTask(names[len(tasks)], exprStmt.Expr))
	}
	c.emitAwaitTasks(tasks)
}

func (c *compiler) newAwaitTask(name string, value ast.Expr) awaitTask {
	task := awaitTask{varName: name, expr: c.compileExpr(value), source: value, isQuery: c.isModelQuery(value)}
	if task.isQuery {
		task.valueType = c.resolveQueryType(value)
		task.goType = goTypeForValType(task.valueType)
	} else {
		valueType, ok := c.valTypeFromExpr(value)
		if ok {
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
		return "*" + value.name
	}
	base := mapBaseType(value.name)
	if value.isList {
		return "[]" + base
	}
	if value.nullable && !isNilableGoType(base) {
		return "*" + base
	}
	return base
}

// compileAwaitStmt retains the legacy standalone form used by older schemas:
// await { val a = queryA(); val b = queryB() }.
func (c *compiler) compileAwaitStmt(e *ast.AwaitExpr) {

	var tasks []awaitTask
	var afterStmts []ast.Stmt

	for _, stmt := range e.Body.Stmts {
		vs, ok := stmt.(*ast.ValStmt)
		if !ok {
			afterStmts = append(afterStmts, stmt)
			continue
		}
		tasks = append(tasks, c.newAwaitTask(vs.Name, vs.Value))
	}

	if len(tasks) == 0 {
		// No val statements — just compile sequentially
		for _, stmt := range e.Body.Stmts {
			c.compileStmt(stmt)
		}
		return
	}

	c.emitAwaitTasks(tasks)

	for _, stmt := range afterStmts {
		c.compileStmt(stmt)
	}
}

func (c *compiler) emitAwaitTasks(tasks []awaitTask) {
	for _, t := range tasks {
		c.write("var %s %s", t.varName, t.goType)
		c.vars[t.varName] = t.valueType
	}

	groups := c.awaitAggregateGroups(tasks)
	c.write("g, gctx := errgroup.WithContext(ctx)")
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
	for _, t := range tasks {
		c.write("_ = %s", t.varName)
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
	chain := flattenChain(task.source)
	if !task.isQuery || len(chain) < 2 {
		return awaitAggregate{}, false
	}
	root, ok := chain[0].expr.(*ast.Ident)
	if !ok || c.models[root.Name] == nil || c.isRemoteModel(root.Name) {
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
		specs[index] = compileAggregateSpec(task)
	}
	c.write("g.Go(func() error {")
	c.write("\t%s, err := %s.AggregateBatch(gctx, %s)", result, base, strings.Join(specs, ", "))
	c.write("\tif err != nil { return err }")
	for index, task := range group.tasks {
		c.write("\t%s = %s[%d]", task.varName, result, index)
	}
	c.write("\treturn nil")
	c.write("})")
}

func compileAggregateSpec(task awaitAggregate) string {
	fields := []string{"Function: lux.Aggregate" + str.Capitalize(task.function)}
	if task.column != "" {
		fields = append(fields, fmt.Sprintf("Column: %q", str.ToSnakeCase(task.column)))
	}
	if len(task.conditions) > 0 {
		fields = append(fields, "Conditions: []lux.Condition{"+strings.Join(task.conditions, ", ")+"}")
	}
	return "lux.AggregateSpec{" + strings.Join(fields, ", ") + "}"
}

func (c *compiler) emitAwaitTask(task awaitTask) {
	c.write("g.Go(func() error {")
	if task.isQuery {
		c.write("\tvar err error")
		expr := strings.Replace(task.expr, "(ctx,", "(gctx,", 1)
		expr = strings.Replace(expr, "(ctx)", "(gctx)", 1)
		c.write("\t%s, err = %s", task.varName, expr)
		c.write("\treturn err")
	} else {
		c.write("\t%s = %s", task.varName, task.expr)
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
	obj := c.compileExpr(member.Object)
	arg := func(i int) string {
		if i < len(e.Args) {
			return c.compileExpr(e.Args[i].Value)
		}
		return ""
	}
	nargs := len(e.Args)

	if result := compileStringTransform(member.Field, obj, arg, nargs); result != "" {
		return result
	}
	if result := compileStringQuery(member.Field, obj, arg, nargs); result != "" {
		return result
	}
	return compileStringConvert(member.Field, obj)
}

// compileStringTransform handles string transform methods (returns string).
func compileStringTransform(method, obj string, arg func(int) string, nargs int) string {
	switch method {
	case "lowercase":
		return fmt.Sprintf("strings.ToLower(%s)", obj)
	case "uppercase":
		return fmt.Sprintf("strings.ToUpper(%s)", obj)
	case "trim":
		return fmt.Sprintf("strings.TrimSpace(%s)", obj)
	case "trimStart":
		if nargs > 0 {
			return fmt.Sprintf("strings.TrimLeft(%s, %s)", obj, arg(0))
		}
		return fmt.Sprintf("strings.TrimLeft(%s, \" \")", obj)
	case "trimEnd":
		if nargs > 0 {
			return fmt.Sprintf("strings.TrimRight(%s, %s)", obj, arg(0))
		}
		return fmt.Sprintf("strings.TrimRight(%s, \" \")", obj)
	case "reversed":
		return fmt.Sprintf("str.Reverse(%s)", obj)
	case "replace", "replaceAll":
		if nargs >= 2 {
			return fmt.Sprintf("strings.ReplaceAll(%s, %s, %s)", obj, arg(0), arg(1))
		}
	case "substring":
		if nargs >= 2 {
			return fmt.Sprintf("%s[%s:%s]", obj, arg(0), arg(1))
		}
		if nargs == 1 {
			return fmt.Sprintf("%s[%s:]", obj, arg(0))
		}
	case "repeat":
		if nargs > 0 {
			return fmt.Sprintf("strings.Repeat(%s, int(%s))", obj, arg(0))
		}
	case "padStart":
		if nargs >= 2 {
			return fmt.Sprintf("str.PadLeft(%s, int(%s), %s)", obj, arg(0), arg(1))
		}
	case "padEnd":
		if nargs >= 2 {
			return fmt.Sprintf("str.PadRight(%s, int(%s), %s)", obj, arg(0), arg(1))
		}
	case "split":
		if nargs > 0 {
			return fmt.Sprintf("strings.Split(%s, %s)", obj, arg(0))
		}
	}
	return ""
}

// compileStringQuery handles string query methods (returns bool/int).
func compileStringQuery(method, obj string, arg func(int) string, nargs int) string {
	switch method {
	case "contains":
		if nargs > 0 {
			return fmt.Sprintf("strings.Contains(%s, %s)", obj, arg(0))
		}
	case "startsWith":
		if nargs > 0 {
			return fmt.Sprintf("strings.HasPrefix(%s, %s)", obj, arg(0))
		}
	case "endsWith":
		if nargs > 0 {
			return fmt.Sprintf("strings.HasSuffix(%s, %s)", obj, arg(0))
		}
	case "isEmpty":
		return fmt.Sprintf("(len(%s) == 0)", obj)
	case "matches":
		if nargs > 0 {
			return fmt.Sprintf("str.Matches(%s, %s)", arg(0), obj)
		}
	case "length", "size":
		return fmt.Sprintf("int64(len(%s))", obj)
	}
	return ""
}

// compileStringConvert handles string type conversion methods.
func compileStringConvert(method, obj string) string {
	switch method {
	case "toInt":
		return fmt.Sprintf("convert.StringToInt(%s)", obj)
	case "toFloat":
		return fmt.Sprintf("convert.StringToFloat(%s)", obj)
	}
	return ""
}
