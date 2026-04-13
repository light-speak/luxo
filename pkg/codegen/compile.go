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
func compileAPIBody(b *strings.Builder, api *ast.ApiDecl, models map[string]*ast.ModelDecl) {
	name := api.Name
	fmt.Fprintf(b, "func handle%s(app *App) api.HandlerFunc {\n", str.Capitalize(name))
	fmt.Fprintf(b, "\treturn func(ctx context.Context, req *api.Request) error {\n")

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
		b:      b,
		indent: "\t\t",
		models: models,
		api:    api,
	}
	for _, stmt := range api.Body.Stmts {
		c.compileStmt(stmt)
	}

	fmt.Fprintf(b, "\t}\n}\n\n")
}

// compiler holds state during body compilation.
type compiler struct {
	b      *strings.Builder
	indent string
	models map[string]*ast.ModelDecl
	api    *ast.ApiDecl
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
	default:
		c.write("// TODO: unsupported statement %T", stmt)
	}
}

// compileVal: val x = expr
func (c *compiler) compileVal(s *ast.ValStmt) {
	expr := c.compileExpr(s.Value)
	if c.isModelQuery(s.Value) {
		// Model queries return (*T, error)
		c.write("%s, err := %s", s.Name, expr)
		c.write("if err != nil {\n%s\treturn err\n%s}", c.indent, c.indent)
	} else {
		c.write("%s := %s", s.Name, expr)
	}
}

// compileReturn: return expr
func (c *compiler) compileReturn(s *ast.ReturnStmt) {
	if s.Value == nil {
		c.write("return nil")
		return
	}
	expr := c.compileExpr(s.Value)
	if c.isModelQuery(s.Value) {
		// Model query result — use WriteJSON
		c.write("%s.WriteJSON(req.Buf, req.Select)", expr)
	} else if c.isModelIdent(s.Value) {
		// Model variable — use WriteJSON
		c.write("%s.WriteJSON(req.Buf, req.Select)", expr)
	} else {
		// Scalar value — write directly to buf
		c.write("req.Buf.AppendJSON(%s)", expr)
	}
	c.write("return nil")
}

// compileThrow: throw ErrorName(args)
func (c *compiler) compileThrow(s *ast.ThrowStmt) {
	expr := c.compileExpr(s.Error)
	c.write("return %s", expr)
}

// compileExprStmt: standalone expression (including elvis guard)
func (c *compiler) compileExprStmt(s *ast.ExprStmt) {
	if elvis, ok := s.Expr.(*ast.ElvisExpr); ok {
		c.compileElvisGuard(elvis)
		return
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
	old := c.indent
	c.indent += "\t"
	for _, stmt := range s.Then.Stmts {
		c.compileStmt(stmt)
	}
	c.indent = old
	c.write("}")
}

// compileElvisGuard: x ?: throw Error
// For pointers: x == nil → throw
// For !x (bool negation): !x is false (x is true) → throw
func (c *compiler) compileElvisGuard(e *ast.ElvisExpr) {
	right := c.compileExpr(e.Right)

	// Check if left is !expr (UnaryExpr with !)
	if unary, ok := e.Left.(*ast.UnaryExpr); ok && unary.Op == "!" {
		// !exists ?: throw → if exists { return ... }
		inner := c.compileExpr(unary.Value)
		c.write("if %s {", inner)
		c.write("\treturn %s", right)
		c.write("}")
		return
	}

	// Default: pointer nil check — x ?: throw → if x == nil { return ... }
	left := c.compileExpr(e.Left)
	c.write("if %s == nil {", left)
	c.write("\treturn %s", right)
	c.write("}")
}

// compileEmit: emit EventName(args)
func (c *compiler) compileEmit(s *ast.EmitStmt) {
	var args []string
	for _, a := range s.Args {
		args = append(args, fmt.Sprintf("%s: %s", str.Capitalize(a.Name), c.compileExpr(a.Value)))
	}
	c.write("if err := Emit%s(ctx, app.EventBus, %sEvent{%s}); err != nil {",
		s.EventName, s.EventName, strings.Join(args, ", "))
	c.write("\treturn err")
	c.write("}")
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
	case *ast.UnaryExpr:
		return c.compileUnary(e)
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
	// error.NotFound → errors.NotFound (built-in error package)
	if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "error" {
		return fmt.Sprintf("errors.%s", str.Capitalize(e.Field))
	}
	obj := c.compileExpr(e.Object)
	return fmt.Sprintf("%s.%s", obj, str.Capitalize(e.Field))
}

// compileCall: handles Model.where(...).first(), Model.create(...), etc.
func (c *compiler) compileCall(e *ast.CallExpr) string {
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

	for i, link := range links {
		switch link.method {
		case "where":
			c.compileWhereChain(&b, modelName, link.args, i == 0)

		case "find":
			// find(id: val) → Where(ModelWhere.Id.Eq(val)).First(ctx)
			if len(link.args) > 0 {
				val := c.compileExpr(link.args[0].Value)
				fmt.Fprintf(&b, "app.%s.Where(%sWhere.Id.Eq(%s)).First(ctx)", modelName, modelName, val)
			}
			return b.String()

		case "first":
			b.WriteString(".First(ctx)")
			return b.String()

		case "all":
			b.WriteString(".All(ctx)")
			return b.String()

		case "exists":
			b.WriteString(".Exists(ctx)")
			return b.String()

		case "create":
			// create(field: val, ...) → Create().SetField(val)...Exec(ctx)
			fmt.Fprintf(&b, "app.%s.Create()", modelName)
			for _, arg := range link.args {
				val := c.compileExpr(arg.Value)
				fmt.Fprintf(&b, ".Set%s(%s)", str.Capitalize(arg.Name), val)
			}
			// Check if next link is NOT a method — auto-add .Exec(ctx)
			if i == len(links)-1 {
				b.WriteString(".Exec(ctx)")
			}

		case "exec":
			b.WriteString(".Exec(ctx)")
			return b.String()

		case "select":
			b.WriteString(".Select(selection.SQLColumns(req.Select)...)")

		default:
			// Unknown method — ensure model client is seeded
			if i == 0 {
				fmt.Fprintf(&b, "app.%s", modelName)
			}
			fmt.Fprintf(&b, ".%s(", str.Capitalize(link.method))
			var args []string
			for _, a := range link.args {
				args = append(args, c.compileExpr(a.Value))
			}
			b.WriteString(strings.Join(args, ", "))
			b.WriteString(")")
		}
	}
	return b.String()
}

// compileWhereChain compiles a where() link into the query builder.
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
		b.WriteString(c.compileWhereArg(modelName, arg.Value))
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
	return fmt.Sprintf("%s %s %s", left, e.Op, right)
}

// compileUnary: throw expr, !expr, -expr
func (c *compiler) compileUnary(e *ast.UnaryExpr) string {
	if e.Op == "throw" {
		return c.compileThrowExpr(e.Value)
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

// isModelIdent checks if an expression is a variable that holds a model value.
// Used by compileReturn to determine if WriteJSON should be called.
func (c *compiler) isModelIdent(expr ast.Expr) bool {
	if c.api == nil || c.api.Body == nil {
		return false
	}
	if ident, ok := expr.(*ast.Ident); ok {
		for _, stmt := range c.api.Body.Stmts {
			if vs, ok := stmt.(*ast.ValStmt); ok && vs.Name == ident.Name {
				return c.isModelQuery(vs.Value)
			}
		}
	}
	return false
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
			case "first", "all", "create", "exec", "find", "exists":
				return true
			}
		}
	}
	return false
}
