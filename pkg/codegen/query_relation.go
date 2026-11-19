package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Reuse lexical demand analysis to identify query results that escape as the
// declared response. Hydrate at the query boundary, before business mutations.
func analyzeQueryRelations(body *ast.Block, resultType *ast.TypeRef, models map[string]*ast.ModelDecl, functions map[string]*ast.FnDecl) map[ast.Expr]loadProjection {
	found := false
	ast.WalkExprs(body, func(expr ast.Expr) {
		if call, ok := expr.(*ast.CallExpr); ok && !found {
			_, found = queryRelationSource(call, models, functions)
		}
	})
	if !found {
		return nil
	}
	analysis := loadDemandAnalysis{models: models, resultType: resultType, collectionQueries: true,
		demands: make(map[*ast.NamedArg]*loadDemand), origins: make(map[ast.Expr]*ast.NamedArg)}
	analysis.source = func(call *ast.CallExpr) (string, bool) {
		return queryRelationSource(call, models, functions)
	}
	analysis.block(body, nil, true, true)
	selected := make(map[ast.Expr]loadProjection)
	for expr, argument := range analysis.origins {
		demand := analysis.demands[argument]
		paths := relationBusinessPaths(demand, models)
		if demand.client || len(paths) > 0 {
			selected[expr] = projectionForDemand(demand, pruneSelectionPrefixes(paths))
		}
	}
	return selected
}

func queryRelationSource(call *ast.CallExpr, models map[string]*ast.ModelDecl, functions map[string]*ast.FnDecl) (string, bool) {
	if ident, ok := call.Func.(*ast.Ident); ok {
		fn := functions[ident.Name]
		if isCompiledLocalFunction(fn) && fn.ReturnType != nil && modelHasStoredRelations(models[fn.ReturnType.Name], models) {
			return fn.ReturnType.Name, true
		}
	}
	return relationQueryModel(call, models)
}

// A whole related value needs concrete stored fields. A leaf selection has no
// children and would erase an existing client subtree when the trees merge.
func relationBusinessPaths(demand *loadDemand, models map[string]*ast.ModelDecl) [][]string {
	var paths [][]string
	for _, path := range demand.paths {
		field := modelField(models[demand.model], path[0])
		if field == nil || field.Type == nil || models[field.Type.Name] == nil || field.Computed != nil {
			continue
		}
		target := models[demand.model]
		for _, name := range path {
			field := modelField(target, name)
			if field == nil || field.Type == nil {
				target = nil
				break
			}
			target = models[field.Type.Name]
		}
		if target == nil {
			paths = append(paths, path)
			continue
		}
		for _, field := range target.Fields {
			if field.Type != nil && field.Computed == nil && models[field.Type.Name] == nil {
				child := append([]string(nil), path...)
				paths = append(paths, append(child, field.Name))
			}
		}
	}
	return paths
}

func modelHasStoredRelations(model *ast.ModelDecl, models map[string]*ast.ModelDecl) bool {
	if model == nil {
		return false
	}
	for _, field := range model.Fields {
		if field.Type != nil && field.Computed == nil && models[field.Type.Name] != nil {
			return true
		}
	}
	return false
}

func relationQueryModel(expr ast.Expr, models map[string]*ast.ModelDecl) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return "", false
	}
	switch member.Field {
	case "find", "findMany", "findFirst", "first", "all", "load":
		name := strings.TrimSuffix(member.Object.GetTypeTag(), "QueryBuilder")
		if root, ok := member.Object.(*ast.Ident); ok && models[root.Name] != nil {
			name = root.Name
		}
		return name, modelHasStoredRelations(models[name], models)
	}
	return "", false
}

func (c *compiler) compileExpr(expr ast.Expr) string {
	if c.expressionBindings != nil {
		if value, found := c.expressionBindings[expr]; found {
			return value
		}
	}
	previous := c.pagingQuery
	c.pagingQuery = c.isPagedQuery(expr)
	value := c.compileExprValue(expr)
	c.pagingQuery = previous
	if _, selected := c.queryRelations[expr]; !selected {
		return value
	}
	if _, query := relationQueryModel(expr, c.models); !query {
		return value // Local functions hydrate their own query branches.
	}
	return c.hydratedQuery(expr, value)
}

// The typed pair remains a query expression in every nullable/branch/await ABI.
// No runtime provenance map, model flag or serialization roundtrip is needed.
func (c *compiler) hydratedQuery(expr ast.Expr, value string) string {
	resultType := c.resolveQueryType(expr)
	method, goType := "", "*"+resultType.name
	if resultType.isList {
		method, goType = "List", "[]*"+resultType.name
	}
	selection := c.clientSelection
	if selection == "" {
		selection = "nil"
	}
	if projection := c.queryRelations[expr]; projection.logic != "" {
		selection = c.compiledProjectionSelection(projection)
	}
	result, failure, returned := "result, err", "nil, err", "result, nil"
	returns := goType + ", error"
	if c.isPagedQuery(expr) {
		result, failure, returned = "result, total, err", "nil, 0, err", "result, total, nil"
		returns = goType + ", int64, error"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "func() (%s) {\n%s := %s\nif err != nil { return %s }\n", returns, result, value, failure)
	present := "result != nil"
	if resultType.isList {
		present = "len(result) > 0"
	}
	fmt.Fprintf(&b, "if %s && has%sSelectedRelations(%s) {\n", present, resultType.name, selection)
	fmt.Fprintf(&b, "if err := resolve%s%sRelations(ctx, app, result, %s); err != nil { return %s }\n", resultType.name, method, selection, failure)
	fmt.Fprintf(&b, "}\nreturn %s\n}()", returned)
	return b.String()
}

func (c *compiler) functionSelection(call *ast.CallExpr, name string) string {
	if projection, selected := c.querySelections[call]; selected {
		return c.compiledProjectionSelection(projection)
	}
	if projection, selected := c.queryRelations[call]; selected && projection.logic != "" {
		return c.compiledProjectionSelection(projection)
	}
	_, selected := c.queryRelations[call]
	if c.clientSelection != "" && (selected || c.explicitSelections[name]) {
		return c.clientSelection
	}
	return "nil"
}

func (g *GeneratorContext) explicitCallableSelections(functions map[string]*ast.FnDecl) map[string]bool {
	if g.explicitSelections != nil {
		return g.explicitSelections
	}
	return collectSelectionAwareFunctions(functions)
}
