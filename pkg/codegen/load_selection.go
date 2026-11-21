package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

// A projection belongs to one load expression, not a source variable name.
type loadProjection struct {
	logic           string
	client          bool
	private         bool
	columns         [][]string
	clientSelection *loadClientSelection
}

type loadClientSelection struct{ paths []loadClientPath }

type loadClientPath struct{ source, response []string }

func (projection loadProjection) singleColumn() string {
	if len(projection.columns) == 1 && len(projection.columns[0]) == 1 {
		return projection.columns[0][0]
	}
	return ""
}

func projectionForDemand(demand *loadDemand, paths [][]string) loadProjection {
	logic := "nil"
	if !demand.whole {
		logic = compileSelectionLiteral(paths)
	}
	return loadProjection{logic: logic, client: demand.client, columns: paths, clientSelection: demand.clientSelection}
}

// Batch functions receive immutable selection metadata. Bind fixed projections
// once per handler, retaining request-specific client selection at each call.
func writeLoadProjectionBindings(b *strings.Builder, body *ast.Block, projections map[*ast.NamedArg]loadProjection) {
	if len(projections) == 0 {
		return
	}
	index := 0
	ast.WalkExprs(body, func(expr ast.Expr) {
		call, ok := expr.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return
		}
		projection, ok := projections[call.Args[0]]
		if !ok || projection.logic == "[]*selection.Field{}" || projection.client && projection.singleColumn() != "" {
			return
		}
		name := fmt.Sprintf("_loadFields%d", index)
		index++
		fmt.Fprintf(b, "\t%s := %s\n", name, projection.logic)
		projection.logic = name
		projections[call.Args[0]] = projection
	})
}

type loadOrigin struct {
	demand *loadDemand
	path   []string
}

type loadDemand struct {
	model           string
	paths           [][]string
	client          bool
	whole           bool
	object          *loadObject
	clientSelection *loadClientSelection
}

type loadDemandAnalysis struct {
	models            map[string]*ast.ModelDecl
	resultType        *ast.TypeRef
	demands           map[*ast.NamedArg]*loadDemand
	scopes            []map[string][]loadOrigin
	source            func(*ast.CallExpr) (string, bool)
	origins           map[ast.Expr]*ast.NamedArg
	loopYields        []loadOrigin
	selectedQueries   bool
	collectionQueries bool
	objectStorage     bool
	controlDepth      int
}

// Selected queries seed lexical origins at select(), including saved builders.
func analyzeSelectedQueryProjections(body *ast.Block, models map[string]*ast.ModelDecl, resultType *ast.TypeRef, functionSets ...map[string]*ast.FnDecl) map[ast.Expr]loadProjection {
	var functions map[string]*ast.FnDecl
	if len(functionSets) > 0 {
		functions = functionSets[0]
	}
	found := false
	storage := walkLoadInputs(body, func(expr ast.Expr) {
		if call, ok := expr.(*ast.CallExpr); ok && !found {
			found = selectedQuerySource(call, models, functions) != ""
		}
	})
	if !found {
		return nil
	}
	analysis := loadDemandAnalysis{models: models, resultType: resultType, selectedQueries: true, objectStorage: storage,
		demands: make(map[*ast.NamedArg]*loadDemand), origins: make(map[ast.Expr]*ast.NamedArg)}
	analysis.source = func(call *ast.CallExpr) (string, bool) {
		model := selectedQuerySource(call, models, functions)
		return model, model != ""
	}
	analysis.block(body, nil, true, resultType != nil)
	result := make(map[ast.Expr]loadProjection, len(analysis.origins))
	for expr, argument := range analysis.origins {
		demand := analysis.demands[argument]
		paths := pruneSelectionPrefixes(demand.paths)
		projection := projectionForDemand(demand, paths)
		for _, path := range paths {
			field := modelField(models[demand.model], path[0])
			if field != nil && (hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal")) {
				projection.private = true
			}
		}
		result[expr] = projection
	}
	return result
}

func queryColumnArguments(paths [][]string) string {
	var names []string
	for _, path := range paths {
		if !slices.Contains(names, path[0]) {
			names = append(names, path[0])
		}
	}
	slices.Sort(names)
	var out strings.Builder
	for _, name := range names {
		out.WriteString(", ")
		out.WriteString(strconv.Quote(name))
	}
	return out.String()
}

func (c *compiler) compiledQueryColumns(model string, projection loadProjection, client string) string {
	g := c.generator
	if g == nil {
		g = defaultGenerator()
	}
	if g.querySQLSelectors == nil {
		g.querySQLSelectors = make(map[string]bool)
	}
	g.querySQLSelectors[model] = true
	if !projection.client {
		client = "[]*selection.Field{}"
	} else {
		client = compiledClientSelection(projection, client)
	}
	selector := "select" + model + "SQLColumns"
	if hasPrivateSQLFields(c.models[model]) {
		selector = "(*" + model + ")(nil).loadSQLColumns"
	}
	return selector + "(" + client + queryColumnArguments(projection.columns) + ")"
}

func compiledClientSelection(projection loadProjection, client string) string {
	if projection.clientSelection == nil {
		return client
	}
	var expression strings.Builder
	paths := projection.clientSelection.paths
	for index := 1; index < len(paths); index++ {
		expression.WriteString("selection.Merge(")
	}
	for index, path := range paths {
		if index > 0 {
			expression.WriteString(", ")
		}
		for _, field := range path.source {
			expression.WriteString("[]*selection.Field{{Name: ")
			expression.WriteString(strconv.Quote(field))
			expression.WriteString(", Children: ")
		}
		if len(path.response) == 0 {
			expression.WriteString(client)
		} else {
			expression.WriteString("selection.At(")
			expression.WriteString(client)
			for _, field := range path.response {
				expression.WriteString(", ")
				expression.WriteString(strconv.Quote(field))
			}
			expression.WriteByte(')')
		}
		for range path.source {
			expression.WriteString("}}")
		}
		if index > 0 {
			expression.WriteByte(')')
		}
	}
	return expression.String()
}

func selectedQuerySource(call *ast.CallExpr, models map[string]*ast.ModelDecl, functions map[string]*ast.FnDecl) string {
	if ident, ok := call.Func.(*ast.Ident); ok {
		fn := functions[ident.Name]
		if isCompiledLocalFunction(fn) && fn.ReturnType != nil && inferred.StructuredType(fn.ReturnType.Name) {
			return fn.ReturnType.Name
		}
	}
	return selectedQueryModel(call, models)
}

func selectedQueryModel(call *ast.CallExpr, models map[string]*ast.ModelDecl) string {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "select" || len(call.Args) != 0 {
		return ""
	}
	if root, ok := member.Object.(*ast.Ident); ok && models[root.Name] != nil {
		return root.Name
	}
	name := strings.TrimSuffix(member.Object.GetTypeTag(), "QueryBuilder")
	if member.Object.GetTypeTag() != name && models[name] != nil {
		return name
	}
	return ""
}

func analyzeLoadProjections(body *ast.Block, models map[string]*ast.ModelDecl, resultType *ast.TypeRef) map[*ast.NamedArg]loadProjection {
	found := false
	storage := walkLoadInputs(body, func(expr ast.Expr) {
		if _, load := directLoadModel(expr); load {
			found = true
		}
	})
	if !found {
		return nil
	}
	analysis := loadDemandAnalysis{models: models, resultType: resultType, demands: make(map[*ast.NamedArg]*loadDemand), objectStorage: storage}
	result := analysis.block(body, nil, true, resultType != nil)
	if resultType == nil {
		analysis.consume(result)
	}
	projections := make(map[*ast.NamedArg]loadProjection, len(analysis.demands))
	for argument, demand := range analysis.demands {
		paths := pruneSelectionPrefixes(demand.paths)
		projection := projectionForDemand(demand, paths)
		projections[argument] = projection
	}
	return projections
}

func (a *loadDemandAnalysis) block(body *ast.Block, bindings map[string][]loadOrigin, lastValue, publicReturn bool) []loadOrigin {
	if body == nil {
		return nil
	}
	if bindings == nil {
		bindings = make(map[string][]loadOrigin)
	}
	a.scopes = append(a.scopes, bindings)
	var result []loadOrigin
	for index, statement := range body.Stmts {
		if value, ok := statement.(*ast.ExprStmt); ok && lastValue && index == len(body.Stmts)-1 {
			result = append(result, a.expression(value.Expr)...)
			if publicReturn {
				a.returned(result)
			}
			continue
		}
		result = append(result, a.statement(statement, publicReturn)...)
	}
	a.scopes = a.scopes[:len(a.scopes)-1]
	return result
}

func (a *loadDemandAnalysis) lookup(name string) ([]loadOrigin, bool) {
	for index := len(a.scopes) - 1; index >= 0; index-- {
		if value, exists := a.scopes[index][name]; exists {
			return value, true
		}
	}
	return nil, false
}

func (a *loadDemandAnalysis) statement(statement ast.Stmt, publicReturn bool) []loadOrigin {
	switch value := statement.(type) {
	case *ast.ValStmt:
		origins := a.bindObject(a.expression(value.Value), value.Value, value.Type)
		scope := a.scopes[len(a.scopes)-1]
		scope[value.Name] = origins
		for _, name := range value.Names {
			scope[name] = origins
		}
	case *ast.AssignStmt:
		a.assignment(value)
	case *ast.ReturnStmt:
		origins := a.expression(value.Value)
		if publicReturn {
			a.returned(origins)
		}
		return origins
	case *ast.ExprStmt:
		// Evaluate effects and field reads; a discarded object is not a consumer
		// of its full contents (notably for standalone null guards).
		a.expression(value.Expr)
	case *ast.ThrowStmt:
		a.consume(a.expression(value.Error))
	case *ast.EmitStmt:
		for _, argument := range value.Args {
			a.consume(a.expression(argument.Value))
		}
	case *ast.IfStmt:
		a.consume(a.expression(value.Condition))
		return a.conditionalBlock(value.Then, nil, false, publicReturn)
	case *ast.ForStmt:
		return a.loop(value, false, publicReturn)
	}
	return nil
}

func (a *loadDemandAnalysis) assignment(value *ast.AssignStmt) {
	if member, ok := value.Target.(*ast.MemberExpr); ok {
		base := a.expression(member.Object)
		origins := a.expression(value.Value)
		if a.assignObject(base, member, origins, value) {
			return
		}
		a.consume(origins)
		a.consume(a.expression(value.Target))
		return
	}
	origins := a.expression(value.Value)
	// Mutation may carry values across branches or loop iterations. Preserve
	// the complete declared object contract rather than dropping earlier reads.
	if target, ok := value.Target.(*ast.Ident); ok {
		for index := len(a.scopes) - 1; index >= 0; index-- {
			if previous, exists := a.scopes[index][target.Name]; exists {
				if a.assignObjectBinding(a.scopes[index], target, previous, origins, value) {
					return
				}
				a.consume(origins)
				a.consume(previous)
				a.scopes[index][target.Name] = append(slices.Clone(previous), origins...)
				return
			}
		}
	}
	// Member targets returned above. Semantic analysis permits no other
	// writable target, so there is no second target expression to consume.
	a.consume(origins)
}

func (a *loadDemandAnalysis) loop(loop *ast.ForStmt, lastValue, publicReturn bool) []loadOrigin {
	origins := a.expression(loop.Collection)
	// Every batch loader already fetches its correlation keys. Presence/iteration
	// alone must not guess an external model's primary-key name.
	a.presence(origins)
	return a.conditionalBlock(loop.Body, map[string][]loadOrigin{loop.VarName: origins}, lastValue, publicReturn)
}

func (a *loadDemandAnalysis) loopValue(loop *ast.ForStmt) []loadOrigin {
	previous := a.loopYields
	a.loopYields = nil
	// Ordinary nested loops share this target; value loops save their own target.
	result := a.loop(loop, !containsYield(loop.Body), false)
	result = append(result, a.loopYields...)
	a.loopYields = previous
	return result
}

func (a *loadDemandAnalysis) presence(origins []loadOrigin) {
	for _, origin := range origins {
		if len(origin.path) > 0 {
			a.consume([]loadOrigin{origin})
		}
	}
}

func (a *loadDemandAnalysis) returned(origins []loadOrigin) {
	a.returnedAt(origins, nil, nil)
}

func (a *loadDemandAnalysis) returnedAt(origins []loadOrigin, path []string, seen map[*loadObject]bool) {
	for _, origin := range origins {
		if origin.demand.object != nil {
			object := origin.demand.object
			if seen[object] {
				a.consumeOrigins(object.base, seen)
				object.eachField(func(fields []loadOrigin) { a.consumeOrigins(fields, seen) })
				continue
			}
			if seen == nil {
				seen = make(map[*loadObject]bool)
			}
			seen[object] = true
			a.returnedAt(object.base, path, seen)
			object.eachNamedField(func(name string, fields []loadOrigin) {
				a.returnedAt(fields, append(slices.Clone(path), name), seen)
			})
			delete(seen, object)
			continue
		}
		demand := origin.demand
		if a.models[demand.model] == nil || len(origin.path) == 0 && (len(path) != 0 || a.resultType != nil && demand.model == a.resultType.Name) {
			demand.addClientPath(origin.path, path)
		} else {
			a.consume([]loadOrigin{origin})
		}
	}
}

func (demand *loadDemand) addClientPath(source, response []string) {
	if len(source) == 0 && len(response) == 0 {
		demand.client, demand.clientSelection = true, nil
		return
	}
	if demand.client && demand.clientSelection == nil {
		return
	}
	demand.client = true
	if demand.clientSelection == nil {
		demand.clientSelection = &loadClientSelection{}
	}
	for _, previous := range demand.clientSelection.paths {
		if slices.Equal(previous.source, source) && slices.Equal(previous.response, response) {
			return
		}
	}
	demand.clientSelection.paths = append(demand.clientSelection.paths, loadClientPath{slices.Clone(source), slices.Clone(response)})
}

func (a *loadDemandAnalysis) consume(origins []loadOrigin) {
	a.consumeOrigins(origins, nil)
}

func (a *loadDemandAnalysis) consumeOrigins(origins []loadOrigin, seen map[*loadObject]bool) {
	for _, origin := range origins {
		if origin.demand.object != nil {
			if seen == nil {
				seen = make(map[*loadObject]bool)
			}
			if !seen[origin.demand.object] {
				seen[origin.demand.object] = true
				a.consumeOrigins(origin.demand.object.base, seen)
				origin.demand.object.eachField(func(fields []loadOrigin) { a.consumeOrigins(fields, seen) })
			}
			continue
		}
		if a.models[origin.demand.model] == nil {
			a.add(origin, nil)
		} else if model := a.originModel(origin); model != nil {
			// Passing an object to an opaque/native consumer promises its declared
			// fields. Encode that explicit type contract as a static projection.
			for _, field := range model.Fields {
				a.add(origin, []string{field.Name})
			}
		} else if len(origin.path) > 0 {
			a.add(origin, nil)
		}
	}
}

func (a *loadDemandAnalysis) originModel(origin loadOrigin) *ast.ModelDecl {
	modelName := origin.demand.model
	for _, name := range origin.path {
		field := modelField(a.models[modelName], name)
		if field == nil || field.Type == nil {
			return nil
		}
		modelName = field.Type.Name
	}
	return a.models[modelName]
}

func (a *loadDemandAnalysis) add(origin loadOrigin, suffix []string) {
	path := origin.path
	if len(suffix) > 0 {
		path = append(slices.Clone(path), suffix...)
	}
	demand := origin.demand
	if a.models[demand.model] == nil {
		if demand.whole {
			return
		}
		if len(path) == 0 {
			demand.whole, demand.paths = true, nil
			return
		}
		for _, previous := range demand.paths {
			if len(previous) <= len(path) && slices.Equal(previous, path[:len(previous)]) {
				return
			}
		}
		demand.paths = slices.DeleteFunc(demand.paths, func(previous []string) bool {
			return len(path) <= len(previous) && slices.Equal(path, previous[:len(path)])
		})
	}
	if len(path) > 0 && !slices.ContainsFunc(demand.paths, func(previous []string) bool { return slices.Equal(previous, path) }) {
		demand.paths = append(demand.paths, path)
	}
}
