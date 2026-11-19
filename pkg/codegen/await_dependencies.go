package codegen

import "github.com/light-speak/luxo/pkg/ast"

// Dependency metadata only exists during compilation. Generated requests retain
// typed variables and task groups, without runtime dependency maps or dispatch.
type awaitBatches struct {
	groups [][]awaitTask
	levels map[string]int
	refs   asyncCaptures
}

func (b *awaitBatches) add(c *compiler, declaration *ast.ValStmt) {
	level := b.level(declaration.Value, c.vars)
	task := c.prepareAwaitDeclaration(declaration)
	for len(b.groups) <= level {
		b.groups = append(b.groups, nil)
	}
	b.groups[level] = append(b.groups[level], task)
	if declaration.Name == "_" {
		return
	}
	if b.levels == nil {
		b.levels = make(map[string]int)
	}
	b.levels[declaration.Name] = level
	c.bindings[declaration.Name] = task.varName
	c.vars[declaration.Name] = task.valueType
}

func (b *awaitBatches) level(value ast.Expr, variables map[string]valType) int {
	if len(b.levels) == 0 {
		return 0
	}
	if b.refs.shadow == nil {
		b.refs.shadow = make(map[string]int)
		b.refs.found = make(map[string]bool)
	}
	clear(b.refs.shadow)
	clear(b.refs.found)
	b.refs.outer = variables
	b.refs.expression(value)
	level := 0
	for name := range b.refs.found {
		if previous, ok := b.levels[name]; ok {
			level = max(level, previous+1)
		}
	}
	return level
}

// Ordinary statements preserve source order and form barriers between groups.
func (b *awaitBatches) emit(c *compiler) {
	for _, group := range b.groups {
		c.emitAwaitTasks(group)
	}
	b.groups = b.groups[:0]
	clear(b.levels)
}
