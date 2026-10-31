package semantic

import (
	"cmp"
	"slices"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// CacheDependencyKind prevents a model and an external resource with the same
// spelling from becoming the same invalidation source.
type CacheDependencyKind uint8

const (
	CacheModelDependency CacheDependencyKind = iota
	CacheExternalDependency
)

type CacheDependency struct {
	Kind   CacheDependencyKind
	Name   string
	Module string // Owner of a model; external resources use their declared name.
}

// Reuse the actual resolved receiver, including aliased query builders, rather
// than infer database ownership from a result type or a variable's spelling.
func (a *Analyzer) recordCacheModelRead(expr ast.Expr, model *ResolvedType) {
	if a.cacheModelReads == nil || model == nil || model.Kind != TypeModel {
		return
	}
	symbol := a.scope.Lookup(model.Name)
	if symbol == nil || symbol.Kind != SymModel {
		return
	}
	a.cacheModelReads[expr] = CacheDependency{Kind: CacheModelDependency,
		Name: model.Name, Module: a.typeOwners.ownerOf(model.Name)}
}

func (node *cacheEffectNode) addDependency(dependency CacheDependency) {
	if node.dependencies == nil {
		node.dependencies = make(map[CacheDependency]bool)
	}
	node.dependencies[dependency] = true
}

func (a *Analyzer) addInferredCacheDependency(node *cacheEffectNode) {
	rest, ok := extractActionPrefix(node.name)
	if !ok {
		return
	}
	_, model := a.findModelInName(stripTopFirstPrefix(rest))
	if model != nil {
		node.addDependency(CacheDependency{Kind: CacheModelDependency,
			Name: model.Name, Module: a.typeOwners.ownerOf(model.Name)})
		if strings.HasPrefix(node.name, "delete") {
			node.effect = "inferred model deletion"
		}
	}
}

// Called only after all cacheable declarations have passed semantic validation.
func (a *Analyzer) declaredNativeCacheDependencies(directives []*ast.Directive) map[CacheDependency]bool {
	var dependencies map[CacheDependency]bool
	for _, directive := range directives {
		if directive.Name != "cacheable" {
			continue
		}
		list := directive.Args[0].Value.(*ast.ListExpr)
		dependencies = make(map[CacheDependency]bool, len(list.Items))
		for _, item := range list.Items {
			switch item := item.(type) {
			case *ast.Ident:
				dependencies[CacheDependency{Kind: CacheModelDependency, Name: item.Name, Module: a.typeOwners.ownerOf(item.Name)}] = true
			case *ast.Literal:
				dependencies[CacheDependency{Kind: CacheExternalDependency, Name: item.Value}] = true
			}
		}
	}
	return dependencies
}

// Each (callable, dependency) pair propagates at most once. Recursive calls and
// diamond-shaped graphs cannot duplicate resources or grow the work list forever.
func collectExecutionCacheDependencies(nodes []*cacheEffectNode) map[*ast.ApiDecl][]CacheDependency {
	sources := make(map[CacheDependency][]*cacheEffectNode)
	result := make(map[*ast.ApiDecl][]CacheDependency)
	for _, node := range nodes {
		node.dependencyVisit = 0
		if node.cached {
			result[node.api] = []CacheDependency{}
		}
		for dependency := range node.dependencies {
			sources[dependency] = append(sources[dependency], node)
		}
	}
	var pending []*cacheEffectNode
	visit := 0
	for dependency, seeds := range sources {
		visit++
		pending = append(pending[:0], seeds...)
		pending = propagateCacheDependency(pending, dependency, visit, result)
	}
	for _, dependencies := range result {
		slices.SortFunc(dependencies, compareCacheDependency)
	}
	return result
}

// Visit stamps share one work queue instead of allocating a dependency map for
// every intermediate helper. Declared/read dependency sets remain unchanged.
func propagateCacheDependency(pending []*cacheEffectNode, dependency CacheDependency, visit int, result map[*ast.ApiDecl][]CacheDependency) []*cacheEffectNode {
	for index := 0; index < len(pending); index++ {
		node := pending[index]
		if node.dependencyVisit == visit {
			continue
		}
		node.dependencyVisit = visit
		if node.cached {
			result[node.api] = append(result[node.api], dependency)
		}
		pending = append(pending, node.callers...)
	}
	return pending
}

func compareCacheDependency(left, right CacheDependency) int {
	if order := cmp.Compare(left.Kind, right.Kind); order != 0 {
		return order
	}
	if order := cmp.Compare(left.Module, right.Module); order != 0 {
		return order
	}
	return cmp.Compare(left.Name, right.Name)
}
