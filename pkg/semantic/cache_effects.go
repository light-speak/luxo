package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type cacheEffectNode struct {
	name            string
	module          string
	pos             token.Position
	body            *ast.Block
	cached          bool
	effect          string
	origin          string
	callers         []*cacheEffectNode
	api             *ast.ApiDecl
	dependencies    map[CacheDependency]bool
	dependencyVisit int
}

// validateCacheEffects rejects known effects that a response-cache hit would
// skip, including native implementations without a read-only dependency contract.
func (a *Analyzer) validateCacheEffects(files []*ast.File) {
	if len(a.errors) != 0 || !hasCachedAPI(files) {
		return
	}
	nodes, byName := a.cacheEffectNodes(files)
	for _, node := range nodes {
		a.scanCacheEffects(node, byName)
	}
	propagateCacheEffects(nodes)
	for _, node := range nodes {
		if node.cached && node.effect != "" {
			a.addError(node.pos, "@cache cannot skip side effects: API '%s' reaches '%s' (%s) / 响应缓存不能跳过副作用，请移除该 API 的 @cache", node.name, node.origin, node.effect)
		}
	}
	if len(a.errors) == 0 {
		a.executionCacheDependencies = collectExecutionCacheDependencies(nodes)
	}
}

func hasCachedAPI(files []*ast.File) bool {
	for _, file := range files {
		for _, api := range file.APIs {
			if hasNamedDirective(api.Directives, "cache") {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) cacheEffectNodes(files []*ast.File) ([]*cacheEffectNode, map[callableKey]*cacheEffectNode) {
	var nodes []*cacheEffectNode
	byName := make(map[callableKey]*cacheEffectNode)
	a.visitCallables(files, func(declaration callableDeclaration) {
		if byName[declaration.key] != nil {
			return
		}
		node := &cacheEffectNode{name: declaration.key.name, module: declaration.key.module,
			pos: declaration.pos, body: declaration.body, api: declaration.api,
			cached: declaration.api != nil && hasNamedDirective(declaration.directives, "cache")}
		node.dependencies = a.declaredNativeCacheDependencies(declaration.directives)
		if declaration.api != nil && node.body == nil && !hasNamedDirective(declaration.directives, "native") {
			a.addInferredCacheDependency(node)
		}
		if hasNamedDirective(declaration.directives, "native") && !hasNamedDirective(declaration.directives, "cacheable") {
			node.effect = "native requires @cacheable(dependsOn: [...])"
		}
		byName[declaration.key] = node
		nodes = append(nodes, node)
	})
	return nodes, byName
}

func (a *Analyzer) scanCacheEffects(node *cacheEffectNode, byName map[callableKey]*cacheEffectNode) {
	ast.Walk(node.body, ast.Visitor{
		Stmt: func(statement ast.Stmt) {
			switch statement := statement.(type) {
			case *ast.EmitStmt:
				node.effect = "event publication"
			case *ast.AssignStmt:
				if statement.Atomic {
					node.effect = "atomic model update"
				}
			}
		},
		Expr: func(expression ast.Expr) {
			if dependency, ok := a.cacheModelReads[expression]; ok {
				node.addDependency(dependency)
			}
			switch expression := expression.(type) {
			case *ast.AsyncExpr:
				node.effect = "asynchronous operation"
			case *ast.CallExpr:
				if a.cacheModelWrite(expression) {
					node.effect = "model write"
				}
				if name, ok := expression.Func.(*ast.Ident); ok {
					if _, target, found := resolveCallable(byName, node.module, name.Name); found {
						target.callers = append(target.callers, node)
					}
				}
			}
		},
	})
}

func (a *Analyzer) cacheModelWrite(call *ast.CallExpr) bool {
	switch function := call.Func.(type) {
	case *ast.MemberExpr:
		return isModelWriteMethod(function.Field) && function.Object.IsModelType()
	case *ast.Ident:
		if !isModelWriteMethod(function.Name) || len(call.Args) == 0 {
			return false
		}
		model, ok := call.Args[0].Value.(*ast.Ident)
		return ok && a.types[model.Name] != nil && a.types[model.Name].Kind == TypeModel
	}
	return false
}

func isModelWriteMethod(name string) bool {
	switch name {
	case "create", "createMany", "update", "updateMany", "delete", "deleteMany", "upsert", "save":
		return true
	}
	return false
}

// Reverse propagation visits each call edge once, including recursive graphs.
// It does not repeatedly walk helper bodies for each cached API.
func propagateCacheEffects(nodes []*cacheEffectNode) {
	var pending []*cacheEffectNode
	for _, node := range nodes {
		if node.effect != "" {
			node.origin = node.name
			pending = append(pending, node)
		}
	}
	for i := 0; i < len(pending); i++ {
		node := pending[i]
		for _, caller := range node.callers {
			if caller.effect == "" {
				caller.effect, caller.origin = node.effect, node.origin
				pending = append(pending, caller)
			}
		}
	}
}
