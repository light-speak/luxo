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
func (c *compiler) statementCompiler() *compiler {
	sub := *c
	sub.b = &strings.Builder{}
	sub.vars = maps.Clone(c.vars)
	sub.paginationTotals = maps.Clone(c.paginationTotals)
	return &sub
}

func (c *compiler) compileWhenStatement(expr *ast.WhenExpr) {
	sub := c.statementCompiler()
	scope := &controlScope{prefix: "_when", indent: c.indent}
	sub.whenScope = scope
	subject := ""
	if expr.Subject != nil {
		subject = sub.compileBooleanValue(expr.Subject)
		sub.resultTmp++
		name := "_subject" + strconv.Itoa(sub.resultTmp)
		sub.write("%s := %s", name, subject)
		subject = name
	}
	scope.header = sub.b.Len()
	sub.write("switch {")
	sub.write("default:")
	sub.indent += "\t"
	if expr.Subject == nil && c.hasChannelBranch(expr) {
		sub.compileSelectBody(expr)
	} else if sub.sealedWhenOwner(expr) != nil {
		sub.compileSealedWhenBranches(expr, subject)
	} else if hasWhenTypeBranch(expr) {
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
