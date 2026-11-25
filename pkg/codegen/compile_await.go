package codegen

import (
	"maps"

	"github.com/light-speak/luxo/pkg/ast"
)

type awaitBindingAlias struct {
	name      string
	taskIndex int
}

// compileAwaitBindings lowers single or destructured await result declarations.
func (c *compiler) compileAwaitBindings(expr *ast.AwaitExpr, names []string, expected ...*ast.TypeRef) {
	var declared *ast.TypeRef
	if len(expected) != 0 {
		declared = expected[0]
	}
	statements := expr.Body.Stmts
	if len(statements) > 1 && hasAwaitBodyStatements(statements) {
		c.compileMixedAwaitBindings(statements, names, declared)
		return
	}
	tasks := make([]awaitTask, 0, len(names))
	var aliases []awaitBindingAlias
	for _, statement := range statements {
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
		c.bindAwaitResult(alias.name, tasks[alias.taskIndex])
	}
	if len(tasks) == 1 && len(names) > 1 {
		c.bindAwaitResults(names[1:], tasks)
	}
}

func (c *compiler) bindAwaitResult(name string, task awaitTask) {
	c.write("%s := %s", c.bindings.name(name), c.bindings.name(task.varName))
	c.vars[name] = task.valueType
}

func (c *compiler) bindAwaitResults(names []string, tasks []awaitTask) {
	for index, name := range names {
		// One result is shared by the destinations without repeating its task.
		if len(tasks) == 1 {
			index = 0
		}
		c.bindAwaitResult(name, tasks[index])
	}
}

func hasAwaitBodyStatements(statements []ast.Stmt) bool {
	for _, statement := range statements {
		if _, result := statement.(*ast.ExprStmt); statement != nil && !result {
			return true
		}
	}
	return false
}

func (c *compiler) compileMixedAwaitBindings(statements []ast.Stmt, names []string, declared *ast.TypeRef) {
	sub := c.awaitBlockCompiler()
	results := make([]awaitTask, 0, len(names))
	var batches awaitBatches
	for _, statement := range statements {
		if statement == nil {
			continue
		}
		value, result := statement.(*ast.ExprStmt)
		if !result {
			sub.compileAwaitBodyStatement(&batches, statement)
			continue
		}
		if value.Expr == nil {
			continue
		}
		if len(results) == len(names) {
			return
		}
		// Compile the result before later declarations can shadow its inputs.
		level := batches.level(value.Expr, sub.vars)
		task := sub.prepareAwaitBinding(sub.discardedBindingName(), &ast.ValStmt{Type: declared, Value: value.Expr})
		batches.enqueue(level, task)
		results = append(results, task)
	}
	if len(results) != 1 && len(results) != len(names) {
		return
	}
	batches.emit(&sub)

	// Only result slots cross the lexical block. User names are bound after
	// the join, so an initializer can still read an outer name it shadows.
	for index := range results {
		task := &results[index]
		destination := sub.discardedBindingName()
		c.write("var %s %s", destination, task.goType)
		sub.write("%s = %s", destination, task.varName)
		task.varName = destination
	}
	c.writeAwaitBlock(&sub)
	c.bindAwaitResults(names, results)
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
	sub := c.awaitBlockCompiler()
	sub.compileAwaitStatements(expr.Body.Stmts)
	c.writeAwaitBlock(&sub)
}

func (c *compiler) awaitBlockCompiler() compiler {
	sub := c.statementCompiler()
	sub.bindings = maps.Clone(c.bindings)
	if sub.bindings == nil {
		sub.bindings = make(localBindings)
	}
	sub.indent += "\t"
	return sub
}

func (c *compiler) writeAwaitBlock(sub *compiler) {
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	c.closureError = sub.closureError
	c.write("{")
	c.b.WriteString(sub.b.String())
	c.write("}")
}

func (c *compiler) compileAwaitStatements(statements []ast.Stmt) {
	var batches awaitBatches
	for _, statement := range statements {
		c.compileAwaitBodyStatement(&batches, statement)
	}
	batches.emit(c)
}

func (c *compiler) compileAwaitBodyStatement(batches *awaitBatches, statement ast.Stmt) {
	if declaration, ok := statement.(*ast.ValStmt); ok {
		batches.add(c, declaration)
		return
	}
	batches.emit(c)
	c.compileStmt(statement)
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
