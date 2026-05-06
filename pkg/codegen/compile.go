package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/token"
)

// compileAPIBody generates Go handler code from a .luxo API body.
// The generated function reads params from req, executes the body, and writes the result.
func compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	name := api.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

	// @auth — require authenticated identity
	if d := findDirective(api.Directives, "auth"); d != nil {
		writeAuthCheck(b, "\t\t", d)
	}

	// Parse params
	for _, p := range api.Params {
		goType := resolveGoType(p.Type)
		method := paramMethod(goType)
		if method == "" {
			// Custom type — use ParamJSON with struct target
			fmt.Fprintf(b, "\t\tvar %s %s\n", p.Name, goType)
			fmt.Fprintf(b, "\t\tif err := req.ParamJSON(%q, &%s); err != nil {\n", p.Name, p.Name)
			fmt.Fprintf(b, "\t\t\treturn err\n\t\t}\n")
		} else {
			fmt.Fprintf(b, "\t\t%s, err := req.Param%s(%q)\n", p.Name, method, p.Name)
			fmt.Fprintf(b, "\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n")
		}
	}

	// Compile body statements
	c := &compiler{
		b:        b,
		indent:   "\t\t",
		models:   models,
		enums:    enums,
		api:      api,
		vars:     make(map[string]valType),
		paginate: hasDirective(api.Directives, "paginate"),
	}
	for _, stmt := range api.Body.Stmts {
		c.compileStmt(stmt)
	}

	fmt.Fprintf(b, "\t}\n}\n\n")
}

// compileFnBody generates Go handler code from a .luxo fn body.
// Reuses the same compilation pipeline as API bodies.
func compileFnBody(b *strings.Builder, fn *ast.FnDecl, models map[string]*ast.ModelDecl, enums map[string]bool) {
	// Convert FnDecl to ApiDecl for code reuse — they share the same structure
	api := &ast.ApiDecl{
		Pos:        fn.Pos,
		Name:       fn.Name,
		Params:     fn.Params,
		ReturnType: fn.ReturnType,
		Directives: fn.Directives,
		Body:       fn.Body,
	}
	compileAPIBody(b, api, models, enums)
}

// valType tracks the resolved type of a val variable.
type valType struct {
	isModel bool   // true if this is a *Model or []*Model
	isList  bool   // true if this is a list (e.g., []*Model)
	isChan  bool   // true if this is a channel (Channel<T>)
	name    string // model name or luxo type name (Int/String/Boolean/Float)
}

// compiler holds state during body compilation.
type compiler struct {
	b           *strings.Builder
	indent      string
	models      map[string]*ast.ModelDecl
	enums       map[string]bool // enum type names
	api         *ast.ApiDecl
	vars        map[string]valType // variable name → resolved type
	inAsync     bool               // true inside async { } — no return err
	paginate    bool               // true when API has @paginate
	hasTotalVar bool               // true after _total is assigned (paginated query)
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
		c.write("// TODO: unsupported statement %T", stmt)
	}
}

// compileVal: val x = expr
func (c *compiler) compileVal(s *ast.ValStmt) {
	// Check for ? operator: val x = riskyCall()? → error propagation
	inner, hasQuestion := unwrapQuestion(s.Value)
	if hasQuestion {
		expr := c.compileExpr(inner)
		c.write("%s, err := %s", s.Name, expr)
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
		return
	}

	expr := c.compileExpr(s.Value)
	if c.isModelQuery(s.Value) {
		qt := c.resolveQueryType(s.Value)
		// @paginate + all → AllWithCount returns (results, total, error)
		if c.paginate && qt.isList {
			c.write("%s, _total, err := %s", s.Name, expr)
			c.hasTotalVar = true
		} else {
			c.write("%s, err := %s", s.Name, expr)
		}
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
		c.vars[s.Name] = qt
	} else {
		// Wrap bare integer literals in int64() to match Luxo's Int = int64
		if lit, ok := s.Value.(*ast.Literal); ok && lit.Kind == token.Int {
			expr = fmt.Sprintf("int64(%s)", expr)
		}
		c.write("%s := %s", s.Name, expr)
		// Track channel variables for close() and for-range codegen
		if c.isChannelConstructor(s.Value) {
			c.vars[s.Name] = valType{isChan: true}
		}
		// Track when-expression type based on API return type
		if _, ok := s.Value.(*ast.WhenExpr); ok && c.api != nil && c.api.ReturnType != nil {
			c.vars[s.Name] = valType{name: c.api.ReturnType.Name}
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
	if s.Value == nil {
		c.write("return nil")
		return
	}
	expr := c.compileExpr(s.Value)

	// 1. Direct model query chain — extract to temp var, then WriteLuxo
	if c.isModelQuery(s.Value) {
		qt := c.resolveQueryType(s.Value)
		// Extract query result to temp variable (query returns (result, error))
		c.write("_result, err := %s", expr)
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
		if qt.isList {
			c.write("WriteColumnar%s(req.Buf, _result, req.FieldMask)", qt.name)
		} else {
			c.write("_result.WriteLuxo(req.Buf, req.FieldMask)")
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

// writeReturnByType emits binary output code based on tracked variable type.
// Always writes Luxo binary — Luvia converts to JSON if needed.
func (c *compiler) writeReturnByType(expr string, vt valType) {
	if vt.isModel {
		if vt.isList {
			if c.paginate && c.hasTotalVar {
				c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", vt.name, expr)
				c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, _total)")
				c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.Page))")
				c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(req.PageSize))")
			} else {
				c.write("WriteColumnar%s(req.Buf, %s, req.FieldMask)", vt.name, expr)
			}
		} else {
			c.write("%s.WriteLuxo(req.Buf, req.FieldMask)", expr)
		}
		return
	}
	switch vt.name {
	case "Int":
		c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(%s))", expr)
	case "Float":
		c.write("req.Buf.B = codec.AppendFixed64(req.Buf.B, %s)", expr)
	case "Boolean":
		c.write("req.Buf.B = codec.AppendBool(req.Buf.B, %s)", expr)
	case "String":
		c.write("req.Buf.B = codec.AppendString(req.Buf.B, %s)", expr)
	default:
		c.write("_ = %s // unsupported return type for binary encoding", expr)
	}
}

// writeScalarReturn emits binary output for non-model return values.
func (c *compiler) writeScalarReturn(expr string) {
	if c.api != nil && c.api.ReturnType != nil {
		switch c.api.ReturnType.Name {
		case "Int":
			c.write("req.Buf.B = codec.AppendSvarint(req.Buf.B, int64(%s))", expr)
			return
		case "Float":
			c.write("req.Buf.B = codec.AppendFixed64(req.Buf.B, %s)", expr)
			return
		case "Boolean":
			c.write("req.Buf.B = codec.AppendBool(req.Buf.B, %s)", expr)
			return
		case "String":
			c.write("req.Buf.B = codec.AppendString(req.Buf.B, %s)", expr)
			return
		}
	}
	c.write("_ = %s // unsupported return type for binary encoding", expr)
}

// compileThrow: throw ErrorName(args)
func (c *compiler) compileThrow(s *ast.ThrowStmt) {
	expr := c.compileThrowExpr(s.Error)
	c.write("return %s", expr)
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
	// Standalone ? operator: riskyCall()? — check and propagate error
	if inner, hasQ := unwrapQuestion(s.Expr); hasQ {
		expr := c.compileExpr(inner)
		c.write("if err := %s; err != nil {", expr)
		c.write("\treturn err")
		c.write("}")
		return
	}
	// Model queries as standalone statements need error checking
	if c.isModelQuery(s.Expr) {
		expr := c.compileExpr(s.Expr)
		c.write("if _, err := %s; err != nil {", expr)
		c.write("\treturn err")
		c.write("}")
		return
	}
	// Transaction as standalone statement — check error
	// transaction { ... } is parsed as CallExpr(Ident("transaction"), lambda)
	if call, ok := s.Expr.(*ast.CallExpr); ok {
		if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "transaction" {
			expr := c.compileExpr(s.Expr)
			c.write("if err := %s; err != nil {", expr)
			c.write("\treturn err")
			c.write("}")
			return
		}
	}
	expr := c.compileExpr(s.Expr)
	c.write("%s", expr)
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
		c.write("\treturn %s", right)
		c.write("}")
		return
	}

	// Bool method call (returns (bool, error)): val, err := x; if !val { throw }
	if isErrorReturningBool(e.Left) {
		left := c.compileExpr(e.Left)
		c.write("_ok, _err := %s", left)
		c.write("if _err != nil {")
		c.write("\treturn _err")
		c.write("}")
		c.write("if !_ok {")
		c.write("\treturn %s", right)
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
	c.write("\treturn %s", right)
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
		c.write("\treturn _err")
		c.write("}")
		c.write("if _ok {")
		c.write("\treturn %s", right)
		c.write("}")
		return
	}

	left := c.compileExpr(e.Left)
	c.write("if %s {", left)
	c.write("\treturn %s", right)
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

// compileEmit: emit EventName(args)
func (c *compiler) compileEmit(s *ast.EmitStmt) {
	var args []string
	for _, a := range s.Args {
		args = append(args, fmt.Sprintf("%s: %s", str.Capitalize(a.Name), c.compileExpr(a.Value)))
	}
	if c.inAsync {
		// Inside async goroutine — ignore emit error (cannot return)
		c.write("Emit%s(ctx, app.EventBus, %sEvent{%s})",
			s.EventName, s.EventName, strings.Join(args, ", "))
	} else {
		c.write("if err := Emit%s(ctx, app.EventBus, %sEvent{%s}); err != nil {",
			s.EventName, s.EventName, strings.Join(args, ", "))
		c.write("\treturn err")
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
		c.write("_yieldResult = %s\n", val)
		c.write("break\n")
		return "_yieldResult"
	default:
		return fmt.Sprintf("/* TODO: %T */", expr)
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
		if ident.Name == "my" {
			if e.Field == "id" {
				return "identity.ID()"
			}
			return fmt.Sprintf("identity.String(%q)", e.Field)
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
	obj := c.compileExpr(e.Object)
	return fmt.Sprintf("%s.%s", obj, str.Capitalize(e.Field))
}

// compileCall: handles Model.where(...).first(), Model.create(...), etc.
func (c *compiler) compileCall(e *ast.CallExpr) string {
	// Built-in functions: now(), crypto.randomHex(), etc.
	if result := c.compileBuiltinCall(e); result != "" {
		return result
	}

	// String methods: obj.lowercase() → strings.ToLower(obj)
	if result := c.compileStringMethod(e); result != "" {
		return result
	}

	// Channel(n) → make(chan any, n)
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "Channel" {
		size := "0"
		if len(e.Args) > 0 {
			size = c.compileExpr(e.Args[0].Value)
		}
		return fmt.Sprintf("make(chan any, %s)", size)
	}

	// transaction { body } → app.DB.Tx(ctx, func(tx *pg.DB) error { body; return nil })
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "transaction" {
		if len(e.Args) == 1 {
			if lambda, ok := e.Args[0].Value.(*ast.LambdaExpr); ok {
				sub := c.subCompiler()
				sub.indent = c.indent + "\t"
				for _, stmt := range lambda.Body.Stmts {
					sub.compileStmt(stmt)
				}
				return fmt.Sprintf("app.DB.Tx(ctx, func(tx *%s.DB) error {\n%s%s\treturn nil\n%s})",
					dbPkg, sub.b.String(), c.indent, c.indent)
			}
		}
	}

	// ch.close() → close(ch) for channel variables
	if member, ok := e.Func.(*ast.MemberExpr); ok && len(e.Args) == 0 {
		if ident, ok := member.Object.(*ast.Ident); ok {
			if member.Field == "close" {
				if vt, exists := c.vars[ident.Name]; exists && vt.isChan {
					return fmt.Sprintf("close(%s)", ident.Name)
				}
			}
		}
	}

	// Flatten the call chain to detect Model.method patterns
	chain := flattenChain(e)

	if len(chain) >= 2 {
		root := chain[0]
		if ident, ok := root.expr.(*ast.Ident); ok {
			if _, isModel := c.models[ident.Name]; isModel {
				return c.compileModelChain(ident.Name, chain[1:])
			}
		}
	}

	// Generic call — Go does not support named args, use positional only
	funcExpr := c.compileExpr(e.Func)
	var args []string
	for _, a := range e.Args {
		args = append(args, c.compileExpr(a.Value))
	}
	return fmt.Sprintf("%s(%s)", funcExpr, strings.Join(args, ", "))
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
		b.WriteString(".Select(selection.SQLColumns(req.Select)...)")
	case "orderBy":
		c.compileOrderByChain(b, link.args)
	case "limit", "offset":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
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
					c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
				}
			}
		}
	}
	fmt.Fprintf(b, "app.%s.Create()", modelName)
	for _, arg := range link.args {
		val := c.compileExpr(arg.Value)
		// Use hashed value if field has @hash directive
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && hasDirective(f.Directives, "hash") {
					val = "hashed" + str.Capitalize(arg.Name)
				}
			}
		}
		// Wrap with & if the model field is nullable (SetXxx expects pointer)
		if m != nil {
			for _, f := range m.Fields {
				if f.Name == arg.Name && f.Type != nil && f.Type.Nullable {
					val = "&" + val
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
func (c *compiler) compileWhereArg(modelName string, expr ast.Expr) string {
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

// compileBinary: a + b, a == b, etc.
func (c *compiler) compileBinary(e *ast.BinaryExpr) string {
	left := c.compileExpr(e.Left)
	right := c.compileExpr(e.Right)
	// DateTime +/- Duration → time.Add(duration) / time.Add(-duration)
	if isDurationExpr(e.Right) && (e.Op == "+" || e.Op == "-") {
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
		return fmt.Sprintf("hex.EncodeToString(luxocrypto.RandomBytes(%s))", n)
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
		// ? as standalone expression — propagate error
		operand := c.compileExpr(e.Value)
		return operand // actual error handling is in compileVal/compileExprStmt
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
			case "delete":
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
			case "first", "all", "create", "exec", "find", "load", "exists", "update", "delete", "count",
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
			c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
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
		// PK load: User.load(id) → app.loaders.ExtendUser.Load(ctx, id, nil)
		val := c.compileExpr(args[0].Value)
		fmt.Fprintf(b, "app.loaders.Extend%s.Load(ctx, %s, nil)", modelName, val)
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
		// Single FK: Post.load(userId: x) → app.loaders.PostByUserId.Load(ctx, x, nil)
		fmt.Fprintf(b, "app.loaders.%s.Load(ctx, %s, nil)", loaderName, vals[0])
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
		b.WriteString("}, nil)")
	}
}

func (c *compiler) compileTerminalMethod(b *strings.Builder, modelName string, link chainLink) bool {
	switch link.method {
	case "find":
		if len(link.args) > 0 {
			val := c.compileExpr(link.args[0].Value)
			fmt.Fprintf(b, "app.%s.Where(%sWhere.Id.Eq(%s)).First(ctx)", modelName, modelName, val)
		}
		return true
	case "load":
		c.compileLoad(b, modelName, link.args)
		return true
	}
	// Check if this is a terminal method first
	isTerminal := false
	switch link.method {
	case "delete", "all", "first", "exists", "exec", "count", "update", "upsert", "save", "sum", "avg", "min", "max":
		isTerminal = true
	}
	if !isTerminal {
		return false
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
		// field.desc → "field DESC", field.asc → "field ASC", field → "field ASC"
		if member, ok := arg.Value.(*ast.MemberExpr); ok {
			col := str.ToSnakeCase(c.compileExpr(member.Object))
			dir := strings.ToUpper(member.Field)
			clauses = append(clauses, fmt.Sprintf("%q", col+" "+dir))
		} else {
			col := str.ToSnakeCase(c.compileExpr(arg.Value))
			clauses = append(clauses, fmt.Sprintf("%q", col+" ASC"))
		}
	}
	fmt.Fprintf(b, ".OrderBy(%s)", strings.Join(clauses, ", "))
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

// compileForExpr: for as expression — collects results into a slice.
// val doubled = for item in items { item * 2 } → []any{...}
func (c *compiler) compileForExpr(s *ast.ForStmt) string {
	sub := c.subCompiler()
	sub.indent = c.indent + "\t\t"

	// The last statement in the body is the "yield" expression
	if len(s.Body.Stmts) == 0 {
		return "[]any{}"
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
		return fmt.Sprintf("func() []any {\n%s\tvar _result []any\n%s\tfor %s := int64(%s); %s <= %s; %s++ {\n%s%s\t\t_result = append(_result, %s)\n%s\t}\n%s\treturn _result\n%s}()",
			c.indent, c.indent, s.VarName, start, s.VarName, end, s.VarName,
			sub.b.String(), c.indent, lastExpr, c.indent, c.indent, c.indent)
	}

	coll := c.compileExpr(s.Collection)
	return fmt.Sprintf("func() []any {\n%s\tvar _result []any\n%s\tfor _, %s := range %s {\n%s%s\t\t_result = append(_result, %s)\n%s\t}\n%s\treturn _result\n%s}()",
		c.indent, c.indent, s.VarName, coll,
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

// compileList: [1, 2, 3] → []any{1, 2, 3}
func (c *compiler) compileList(e *ast.ListExpr) string {
	var items []string
	for _, item := range e.Items {
		items = append(items, c.compileExpr(item))
	}
	return "[]any{" + strings.Join(items, ", ") + "}"
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
			// Check if the expression is likely a string (member access on string field, or string var)
			if c.isStringExpr(part) {
				fmt.Fprintf(&b, "%s\t_sb.WriteString(%s)\n", c.indent, compiled)
			} else {
				fmt.Fprintf(&b, "%s\t_sb.WriteString(strconv.FormatInt(int64(%s), 10))\n", c.indent, compiled)
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
		// Check if the field is a known string field on a model
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
		// Check if the variable is known to be a string
		if vt, ok := c.vars[e.Name]; ok && vt.name == "String" {
			return true
		}
	case *ast.Literal:
		return e.Kind == token.String
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
	return "{" + strings.Join(fields, ", ") + "}"
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
	zeroVal := zeroValueForType(retType)
	fmt.Fprintf(&b, "%s\t}\n%s\treturn %s\n%s}()", c.indent, c.indent, zeroVal, c.indent)
	return b.String()
}

// inferWhenReturnType infers the Go return type for a when expression.
// Uses the API return type if available, otherwise falls back to "any".
func (c *compiler) inferWhenReturnType(e *ast.WhenExpr) string {
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
		b:      &b,
		indent: c.indent,
		models: c.models,
		enums:  c.enums,
		api:    c.api,
		vars:   childVars,
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

// compileAwaitStmt compiles await { val x = ...; val y = ... } into parallel errgroup execution.
// Each val statement becomes an independent goroutine task.
// Variables are declared in the outer scope, goroutines assign to them.
func (c *compiler) compileAwaitStmt(e *ast.AwaitExpr) {
	// Phase 1: collect val statements → parallel tasks; others → sequential after
	type awaitTask struct {
		varName string
		goType  string // Go type for var declaration
		expr    string // compiled expression
		isQuery bool   // true if model query (has error return)
	}

	var tasks []awaitTask
	var afterStmts []ast.Stmt

	for _, stmt := range e.Body.Stmts {
		vs, ok := stmt.(*ast.ValStmt)
		if !ok {
			afterStmts = append(afterStmts, stmt)
			continue
		}
		expr := c.compileExpr(vs.Value)
		goType := "any"
		isQuery := c.isModelQuery(vs.Value)
		if isQuery {
			qt := c.resolveQueryType(vs.Value)
			if qt.isModel {
				if qt.isList {
					goType = "[]*" + qt.name
				} else {
					goType = "*" + qt.name
				}
			} else if qt.name == "Boolean" {
				goType = "bool"
			} else if qt.name == "Int" {
				goType = "int64"
			}
		}
		tasks = append(tasks, awaitTask{
			varName: vs.Name,
			goType:  goType,
			expr:    expr,
			isQuery: isQuery,
		})
		// Register var type for downstream compileReturn
		if isQuery {
			c.vars[vs.Name] = c.resolveQueryType(vs.Value)
		}
	}

	if len(tasks) == 0 {
		// No val statements — just compile sequentially
		for _, stmt := range e.Body.Stmts {
			c.compileStmt(stmt)
		}
		return
	}

	// Phase 2: generate code — write directly to c.b

	// Declare variables in outer scope
	for _, t := range tasks {
		c.write("var %s %s", t.varName, t.goType)
	}

	// errgroup
	c.write("g, gctx := errgroup.WithContext(ctx)")

	for _, t := range tasks {
		c.write("g.Go(func() error {")
		if t.isQuery {
			c.write("\tvar err error")
			c.write("\t%s, err = %s", t.varName, strings.Replace(t.expr, "(ctx)", "(gctx)", 1))
			c.write("\treturn err")
		} else {
			c.write("\t%s = %s", t.varName, t.expr)
			c.write("\treturn nil")
		}
		c.write("})")
	}

	c.write("if err := g.Wait(); err != nil {")
	c.write("\treturn err")
	c.write("}")

	// Suppress unused variable warnings for await vars not referenced later
	for _, t := range tasks {
		c.write("_ = %s", t.varName)
	}

	// Compile sequential statements after await
	for _, stmt := range afterStmts {
		c.compileStmt(stmt)
	}
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
