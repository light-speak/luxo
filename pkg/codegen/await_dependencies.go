package codegen

import "github.com/light-speak/luxo/pkg/ast"

// Dependency metadata only exists during compilation. Generated requests retain
// typed variables and task groups, without runtime dependency maps or dispatch.
type awaitBatches struct {
	groups  [][]awaitTask
	levels  map[string]int
	refs    asyncCaptures
	aliases map[int][]awaitBatchAlias
}

type awaitBatchAlias struct {
	name   string
	source string
}

func (b *awaitBatches) add(c *compiler, declaration *ast.ValStmt) {
	level := b.level(declaration.Value, c.vars)
	task := c.prepareAwaitDeclaration(declaration)
	b.enqueue(level, task)
	if len(declaration.Names) != 0 {
		b.addAliases(c, declaration.Names, level, task)
		return
	}
	b.bind(c, declaration.Name, task.varName, level, task.valueType)
}

func (b *awaitBatches) enqueue(level int, task awaitTask) {
	for len(b.groups) <= level {
		b.groups = append(b.groups, nil)
	}
	b.groups[level] = append(b.groups[level], task)
}

func (b *awaitBatches) addAliases(c *compiler, names []string, level int, task awaitTask) {
	if b.aliases == nil {
		b.aliases = make(map[int][]awaitBatchAlias)
	}
	for _, name := range names {
		if name == "_" {
			continue
		}
		alias := c.discardedBindingName()
		b.bind(c, name, alias, level, task.valueType)
		b.aliases[level] = append(b.aliases[level], awaitBatchAlias{name: alias, source: task.varName})
	}
}

func (b *awaitBatches) bind(c *compiler, name, destination string, level int, value valType) {
	if name == "_" {
		return
	}
	if b.levels == nil {
		b.levels = make(map[string]int)
	}
	b.levels[name] = level
	c.bindings[name] = destination
	c.vars[name] = value
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
	for level, group := range b.groups {
		c.emitAwaitTasks(group)
		for _, alias := range b.aliases[level] {
			c.compileLocalAlias(alias.name, alias.source)
			c.write("_ = %s", c.bindings.name(alias.name))
		}
	}
	b.groups = b.groups[:0]
	clear(b.levels)
	clear(b.aliases)
}
