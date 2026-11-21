package codegen

import (
	"maps"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

type awaitBindingAlias struct {
	name      string
	taskIndex int
}

// compileAwaitBindings compiles the documented destructuring form:
// val (a, b) = await { queryA(); queryB() }.
func (c *compiler) compileAwaitBindings(expr *ast.AwaitExpr, names []string, expected ...*ast.TypeRef) {
	var declared *ast.TypeRef
	if len(expected) != 0 {
		declared = expected[0]
	}
	tasks := make([]awaitTask, 0, len(names))
	var aliases []awaitBindingAlias
	for _, statement := range expr.Body.Stmts {
		value, ok := statement.(*ast.ExprStmt)
		if !ok || value.Expr == nil {
			continue
		}
		if len(tasks) >= len(names) {
			return
		}
		name := names[len(tasks)]
		task, shadowed := c.prepareAwaitResult(name, value.Expr, declared)
		if shadowed {
			aliases = append(aliases, awaitBindingAlias{name: name, taskIndex: len(tasks)})
		}
		tasks = append(tasks, task)
	}
	c.emitAwaitTasks(tasks)
	for _, alias := range aliases {
		task := tasks[alias.taskIndex]
		c.write("%s := %s", c.bindings.name(alias.name), c.bindings.name(task.varName))
		c.vars[alias.name] = task.valueType
	}
}

// A shadowing destination is declared after the group joins, keeping each RHS
// in the original Go lexical environment without changing compiler name maps.
func (c *compiler) prepareAwaitResult(name string, value ast.Expr, declared *ast.TypeRef) (awaitTask, bool) {
	_, shadowed := c.vars[name]
	shadowed = shadowed || c.models[name] != nil
	if declared == nil && !shadowed {
		return c.newAwaitTask(name, value), false
	}
	destination := name
	if shadowed {
		destination = c.discardedBindingName()
	}
	return c.prepareAwaitBinding(destination, &ast.ValStmt{Name: name, Type: declared, Value: value}), shadowed
}

// Standalone await owns its lexical scope. Task initializers are compiled before
// installing their destinations, so a shadowing initializer still reads its parent.
func (c *compiler) compileAwaitStmt(expr *ast.AwaitExpr) {
	sub := *c
	sub.b = &strings.Builder{}
	sub.vars = maps.Clone(c.vars)
	sub.bindings = maps.Clone(c.bindings)
	sub.paginationTotals = maps.Clone(c.paginationTotals)
	if sub.bindings == nil {
		sub.bindings = make(localBindings)
	}
	sub.indent += "\t"
	sub.compileAwaitStatements(expr.Body.Stmts)
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	c.closureError = sub.closureError
	c.write("{")
	c.b.WriteString(sub.b.String())
	c.write("}")
}

func (c *compiler) compileAwaitStatements(statements []ast.Stmt) {
	var batches awaitBatches
	for _, statement := range statements {
		declaration, ok := statement.(*ast.ValStmt)
		if ok {
			batches.add(c, declaration)
			continue
		}
		batches.emit(c)
		c.compileStmt(statement)
	}
	batches.emit(c)
}

func (c *compiler) prepareAwaitDeclaration(statement *ast.ValStmt) awaitTask {
	return c.prepareAwaitBinding(c.discardedBindingName(), statement)
}

func (c *compiler) prepareAwaitBinding(destination string, statement *ast.ValStmt) awaitTask {
	task := c.newAwaitTask(destination, statement.Value)
	initialType := task.goType
	local := *statement
	local.Name = c.discardedBindingName()
	sub := c.errorCompiler()
	sub.indent = c.indent + "\t\t"
	sub.compileVal(&local)
	if value, known := sub.vars[local.Name]; known {
		task.valueType = value
		task.goType = goTypeForValType(value)
	}
	if task.goType == initialType {
		if aggregate, ok := c.awaitAggregate(0, task); ok {
			task.aggregate = &aggregate
		}
	}
	sub.write("%s = %s", c.bindings.name(task.varName), local.Name)
	task.body = sub.b.String()
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	return task
}
