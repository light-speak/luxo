package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) eventCallableBodies() map[callableKey]*ast.Block {
	bodies := make(map[callableKey]*ast.Block)
	a.visitCallables(a.files, func(declaration callableDeclaration) {
		if _, exists := bodies[declaration.key]; !exists {
			bodies[declaration.key] = declaration.body
		}
	})
	return bodies
}

type eventBody struct {
	module string
	body   *ast.Block
}

type eventEmitCollector struct {
	module   string
	bodies   map[callableKey]*ast.Block
	analyzer *Analyzer
	visited  map[callableKey]bool
	pending  []eventBody
	events   []string
}

// Each callable is expanded once per listener, including recursive helpers.
// A work list avoids recursion proportional to the function-call chain.
// Return the lazily populated index for reuse by subsequent listeners, without
// allocating an additional shared holder when no listener calls a helper.
func collectReachableEmits(block *ast.Block, module string, bodies map[callableKey]*ast.Block, analyzer *Analyzer) ([]string, map[callableKey]*ast.Block) {
	if block == nil || len(block.Stmts) == 0 {
		return nil, bodies
	}
	collector := eventEmitCollector{module: module, bodies: bodies, analyzer: analyzer}
	visitor := ast.Visitor{Stmt: collector.visitStatement}
	if analyzer != nil || len(bodies) != 0 {
		visitor.Expr = collector.visitExpression
	}
	ast.Walk(block, visitor)
	for index := 0; index < len(collector.pending); index++ {
		current := collector.pending[index]
		collector.module = current.module
		ast.Walk(current.body, visitor)
	}
	return collector.events, collector.bodies
}

func (c *eventEmitCollector) visitStatement(statement ast.Stmt) {
	if emit, ok := statement.(*ast.EmitStmt); ok {
		c.events = append(c.events, emit.EventName)
	}
}

func (c *eventEmitCollector) visitExpression(expression ast.Expr) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return
	}
	name, ok := call.Func.(*ast.Ident)
	if !ok {
		return
	}
	if c.bodies == nil && c.analyzer != nil {
		c.bodies = c.analyzer.eventCallableBodies()
	}
	key, body, found := resolveCallable(c.bodies, c.module, name.Name)
	if !found || body == nil || c.visited[key] {
		return
	}
	if c.visited == nil {
		c.visited = make(map[callableKey]bool)
	}
	c.visited[key] = true
	c.pending = append(c.pending, eventBody{key.module, body})
}
