package inferred

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Helpers do not inherit the API pagination window. Include object arguments,
// object results and writes through model/nullable references in the analysis.
func (p *paginationAnalysis) function(call *ast.CallExpr, function *ast.FnDecl) pageValue {
	bindings := p.functionBindings(call, function)
	if function.Body == nil {
		for _, value := range bindings {
			escapePageObject(value, call, make(map[*pageObject]bool))
		}
		return p.opaqueFunctionResult(call, function)
	}
	if p.active[function.Name] {
		for _, value := range bindings {
			escapePageObject(value, call, make(map[*pageObject]bool))
		}
		return p.unknownObject(call)
	}
	if p.active == nil {
		p.active = make(map[string]bool)
	}
	p.active[function.Name] = true
	defer delete(p.active, function.Name)
	sub := paginationAnalysis{models: p.models, functions: p.functions, active: p.active}
	sub.block(function.Body, bindings, true, true)
	for _, query := range sub.queries {
		p.invalidateQuery(query, call.GetPos())
	}
	for query, position := range sub.invalid {
		p.invalidateQuery(query, position)
	}
	if len(sub.returns) == 1 {
		value := p.bindObject(sub.returns[0], pageFunctionResultType(function))
		value.bad = len(pageQueries(value)) != 0
		return value
	}
	var result pageValue
	for _, value := range sub.returns {
		result = mergePageValues(result, value)
	}
	result.bad = len(result.queries) != 0
	return result
}

func (p *paginationAnalysis) functionBindings(call *ast.CallExpr, function *ast.FnDecl) map[string]pageValue {
	bindings := p.parameters(function.Params)
	for index, argument := range call.Args {
		value := p.expression(argument.Value)
		p.consume(value, argument.Value)
		name := argument.Name
		if name == "" && index < len(function.Params) {
			name = function.Params[index].Name
		}
		for _, param := range function.Params {
			if param.Name == name {
				value = p.bindObject(value, param.Type)
				break
			}
		}
		if bindings == nil {
			bindings = make(map[string]pageValue)
		}
		bindings[name] = value
	}
	return bindings
}

func (p *paginationAnalysis) opaqueFunctionResult(call *ast.CallExpr, function *ast.FnDecl) pageValue {
	resultType := pageFunctionResultType(function)
	if call.IsListType() || resultType != nil && resultType.IsList {
		return pageValue{owner: call, queries: []ast.Expr{call}, bad: true}
	}
	return p.unknownObject(call)
}

func pageFunctionResultType(function *ast.FnDecl) *ast.TypeRef {
	ref := function.ReturnType
	if ref != nil && ref.Name == "Result" && len(ref.TypeArgs) == 1 {
		return ref.TypeArgs[0]
	}
	return ref
}

func (p *paginationAnalysis) invalidateQuery(query ast.Expr, position token.Position) {
	if p.invalid == nil {
		p.invalid = make(map[ast.Expr]token.Position)
	}
	p.invalid[query] = position
}

func escapePageObject(value pageValue, origin ast.Expr, seen map[*pageObject]bool) {
	object := value.object
	if object == nil || seen[object] {
		return
	}
	seen[object] = true
	object.opaque = origin
	object.eachField(func(field pageValue) bool {
		escapePageObject(field, origin, seen)
		return true
	})
	object.fields = nil
	object.initial = &pageObject{opaque: origin}
}
