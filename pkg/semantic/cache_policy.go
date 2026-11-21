package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/token"
)

func (a *Analyzer) checkCacheDirective(d *ast.Directive) {
	if len(d.Args) == 1 && d.Args[0].Name == "policy" {
		a.resolveCachePolicy(d)
		return
	}
	a.addError(d.Pos, "@cache requires an explicit policy reference; declare mode and TTL in luxo.cache.json / @cache 必须显式引用策略，请在 luxo.cache.json 声明 mode 和 TTL")
}

func (a *Analyzer) resolveCachePolicy(d *ast.Directive) {
	literal, ok := directiveLiteral(d.Args[0])
	if !ok || literal.Kind != token.String || literal.Value == "" {
		a.addError(d.Pos, "@cache policy must be a non-empty static string / @cache policy 必须是非空静态字符串")
		return
	}
	policy, ok := a.cachePolicies.Lookup(literal.Value)
	if !ok {
		a.addError(d.Pos, "unknown cache policy %q / 未声明的缓存策略 %q", literal.Value, literal.Value)
		return
	}
	if a.resolvedCachePolicies == nil {
		a.resolvedCachePolicies = make(map[*ast.Directive]cachepolicy.Policy)
	}
	a.resolvedCachePolicies[d] = policy
}
