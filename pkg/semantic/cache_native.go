package semantic

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Native dependency declarations are checked even outside a cached call graph.
// Read-only behavior remains a contract of the native implementation, not a
// claim that the compiler has inspected its Go body.
func (a *Analyzer) validateCacheableDeclarations(files []*ast.File) {
	a.visitCallables(files, func(declaration callableDeclaration) {
		seen := false
		for _, directive := range declaration.directives {
			if directive.Name != "cacheable" {
				continue
			}
			if seen {
				a.addError(directive.Pos, "duplicate @cacheable declaration / @cacheable 不能重复声明")
			}
			seen = true
			if !hasNamedDirective(declaration.directives, "native") {
				a.addError(directive.Pos, "@cacheable requires @native / @cacheable 仅用于声明 native 的只读依赖契约")
			}
			a.validateCacheableDependencies(directive)
		}
	})
}

func (a *Analyzer) validateCacheableDependencies(directive *ast.Directive) {
	if len(directive.Args) != 1 || directive.Args[0].Name != "dependsOn" {
		a.addError(directive.Pos, "@cacheable requires exactly one explicit dependsOn list / @cacheable 必须显式声明唯一 dependsOn 列表")
		return
	}
	list, ok := directive.Args[0].Value.(*ast.ListExpr)
	if !ok {
		a.addError(directive.Pos, "@cacheable dependsOn must be a static list / @cacheable dependsOn 必须是静态列表")
		return
	}
	seen := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		key := a.cacheableDependencyKey(item)
		if key == "" {
			a.addError(item.GetPos(), "@cacheable dependency must be a declared model or nonempty external resource string / @cacheable 依赖必须是已声明模型或非空外部资源字符串")
			continue
		}
		if seen[key] {
			a.addError(item.GetPos(), "duplicate @cacheable dependency / @cacheable 依赖不能重复")
		}
		seen[key] = true
	}
}

func (a *Analyzer) cacheableDependencyKey(item ast.Expr) string {
	switch item := item.(type) {
	case *ast.Ident:
		if model := a.scope.Lookup(item.Name); model != nil && model.Kind == SymModel {
			return "model:" + item.Name
		}
	case *ast.Literal:
		if item.Kind == token.String && strings.TrimSpace(item.Value) != "" {
			return "external:" + item.Value
		}
	}
	return ""
}
