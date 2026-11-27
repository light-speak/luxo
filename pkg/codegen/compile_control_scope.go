package codegen

import (
	"maps"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

type controlScope struct {
	label  string
	header int
	indent string
	prefix string
}

// A Go expression closure that contains an outward return must remain in the
// enclosing function. Only its generated value completion uses this target.
type inlineValueScope struct {
	controlScope
	outer  *inlineValueScope
	result string
	when   *ast.WhenExpr
	branch bool
	page   string
	yield  bool
}

func (c *compiler) expressionCompiler(expr ast.Expr) *compiler {
	if !containsAwaitReturn(expr) {
		return c.valueCompiler()
	}
	sub := c.statementCompiler()
	sub.resultTmp++
	sub.inlineValue = &inlineValueScope{
		controlScope: controlScope{prefix: "_inlineExit", indent: c.indent},
		outer:        c.inlineValue,
		result:       "_inlineValue" + strconv.Itoa(sub.resultTmp),
	}
	return &sub
}

func containsAwaitReturn(expr ast.Expr) bool {
	found := false
	ast.WalkExpr(expr, func(value ast.Expr) {
		if await, ok := value.(*ast.AwaitExpr); ok && !found {
			ast.Walk(await.Body, ast.Visitor{Stmt: func(stmt ast.Stmt) {
				if _, returned := stmt.(*ast.ReturnStmt); returned {
					found = true
				}
			}})
		}
	})
	return found
}

func (c *compiler) writeValueCompletion(indent, value string) {
	if c.inlineValue == nil {
		c.writeValueReturn(indent, "", value)
		return
	}
	c.writeInlineValue(c.inlineValue, indent, value)
}

func (c *compiler) writeValueReturn(indent, prefix, value string) {
	c.b.WriteString(c.indent)
	c.b.WriteString(indent)
	c.b.WriteString("return ")
	c.b.WriteString(prefix)
	c.b.WriteString(value)
	c.b.WriteByte('\n')
}

func (c *compiler) writeConditionalValue(condition, value string) {
	if c.inlineValue == nil {
		c.b.WriteString(c.indent)
		c.b.WriteString("if ")
		c.b.WriteString(condition)
		c.b.WriteString(" { return ")
		c.b.WriteString(value)
		c.b.WriteString(" }\n")
		return
	}
	c.write("if %s {", condition)
	c.writeValueCompletion("\t", value)
	c.write("}")
}

func (c *compiler) writeInlineValue(scope *inlineValueScope, indent, value string) {
	c.write("%s%s = %s", indent, scope.result, value)
	c.write("%sbreak %s", indent, c.controlLabel(&scope.controlScope))
}

func (c *compiler) writeYieldResult(prefix, value string) {
	for scope := c.inlineValue; scope != nil; scope = scope.outer {
		if scope.yield {
			c.writeInlineValue(scope, "", prefix+value)
			return
		}
	}
	c.writeValueReturn("", prefix, value)
}

func (c *compiler) inlineWhenReturn() *inlineValueScope {
	for scope := c.inlineValue; scope != nil; scope = scope.outer {
		if scope.branch {
			return scope
		}
	}
	return nil
}

func (c *compiler) writeInlineWhenValue(scope *inlineValueScope, expr ast.Expr) {
	value := "nil"
	if expr != nil {
		when := scope.when
		var expected *ast.TypeRef
		if when.IsNullable() {
			expected = &ast.TypeRef{Name: when.GetTypeTag(), IsList: when.IsListType(), Nullable: true}
		}
		value = c.compileValueResult(expr, expected, scope.page, expected == nil && c.models[when.GetTypeTag()] != nil)
	}
	c.writeInlineValue(scope, "", value)
}

func (c *compiler) writeInlineValueScope(sub *compiler, goType, body string) string {
	scope := sub.inlineValue
	c.closureError = sub.closureError
	c.write("var %s %s", scope.result, goType)
	if scope.label != "" {
		c.write("%s:", scope.label)
	}
	c.write("switch {")
	c.write("default:")
	c.b.WriteString(body)
	c.write("}")
	return scope.result
}

func (c *compiler) controlLabel(scope *controlScope) string {
	if scope.label == "" {
		c.resultTmp++
		scope.label = scope.prefix + strconv.Itoa(c.resultTmp)
	}
	return scope.label
}

func writeControlScope(target *strings.Builder, body string, scope *controlScope) {
	if scope.label == "" {
		target.WriteString(body)
		return
	}
	target.WriteString(body[:scope.header])
	target.WriteString(scope.indent + scope.label + ":\n")
	target.WriteString(body[scope.header:])
}

func (c *compiler) withLoopScope(emit func()) {
	var body strings.Builder
	parent, previous := c.b, c.loopScope
	scope := &controlScope{prefix: "_loop", indent: c.indent}
	c.b, c.loopScope = &body, scope
	emit()
	c.b, c.loopScope = parent, previous
	writeControlScope(parent, body.String(), scope)
}

func (c *compiler) markLoopHeader() {
	if c.loopScope != nil {
		c.loopScope.header = c.b.Len()
	}
}

func (c *compiler) compileLoopJump(keyword string) {
	if c.loopScope == nil {
		c.write("%s", keyword)
		return
	}
	c.write("%s %s", keyword, c.controlLabel(c.loopScope))
}

func (c *compiler) compileLoopHeader(loop *ast.ForStmt) {
	if loop.VarName == "" {
		c.markLoopHeader()
		c.write("for {")
		if loop.Collection != nil {
			indent := c.indent
			c.indent += "\t"
			condition := c.compileBooleanValue(loop.Collection)
			c.write("if !(%s) { break }", condition)
			c.indent = indent
		}
		return
	}
	name := c.bindings.name(loop.VarName)
	if bounds, ok := loop.Collection.(*ast.RangeExpr); ok {
		c.compileRangeLoopHeader(name, bounds)
		return
	}
	collection := c.compileMaterializedValue(loop.Collection, nil)
	c.markLoopHeader()
	if c.isChannelVar(loop.Collection) {
		c.write("for %s := range %s {", name, collection)
	} else {
		c.write("for _, %s := range %s {", name, collection)
	}
}

// Statement branches retain their enclosing Go return/error and loop scopes.
func (c *compiler) statementCompiler() compiler {
	sub := *c
	sub.b = &strings.Builder{}
	sub.vars = maps.Clone(c.vars)
	sub.paginationTotals = maps.Clone(c.paginationTotals)
	return sub
}

func (c *compiler) compileWhenStatement(expr *ast.WhenExpr) {
	sub := c.statementCompiler()
	sub.inlineWhen = false
	sub.statementWhen = nil
	scope := &controlScope{prefix: "_when", indent: c.indent}
	subject := ""
	if expr.Subject != nil {
		subject = sub.compileBooleanValue(expr.Subject)
		if !isStaticWhenNullSubject(expr.Subject) {
			sub.resultTmp++
			name := "_subject" + strconv.Itoa(sub.resultTmp)
			sub.write("%s := %s", name, subject)
			subject = name
		}
	}
	sub.statementWhen = scope
	scope.header = sub.b.Len()
	sub.write("switch {")
	sub.write("default:")
	sub.indent += "\t"
	if expr.Subject == nil && c.hasChannelBranch(expr) {
		sub.compileSelectBody(expr)
	} else if sub.sealedWhenOwner(expr) != nil {
		sub.compileSealedWhenBranches(expr, subject)
	} else if !isStaticWhenNullSubject(expr.Subject) && hasWhenTypeBranch(expr) {
		sub.compileWhenSwitch(expr, subject+".(type)")
	} else {
		sub.compileWhenConditions(expr, subject)
	}
	sub.indent = c.indent
	sub.write("}")
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	c.closureError = sub.closureError
	writeControlScope(c.b, sub.b.String(), scope)
}

func hasWhenTypeBranch(expr *ast.WhenExpr) bool {
	for _, branch := range expr.Branches {
		if branch.IsType != "" {
			return true
		}
	}
	return false
}
