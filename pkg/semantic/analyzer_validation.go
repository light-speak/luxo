package semantic

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Sealed Variant Field Injection ==========

// injectSealedVariantFields creates a child scope where the subject variable
// has the variant's fields accessible (e.g., result.transactionId after is Success).
func (a *Analyzer) injectSealedVariantFields(scope *Scope, sealedType *ResolvedType, variantName string, subject ast.Expr) *Scope {
	ident, ok := subject.(*ast.Ident)
	if !ok {
		return scope
	}
	// find the variant
	for _, v := range sealedType.Variants {
		if v.Name == variantName {
			// create a narrowed type with the variant's fields
			narrowed := &ResolvedType{
				Kind:   TypeModel,
				Name:   sealedType.Name,
				Fields: make(map[string]*FieldInfo),
			}
			for _, f := range v.Fields {
				narrowed.Fields[f.Name] = f
			}
			child := scope.Child()
			child.Define(&Symbol{
				Name: ident.Name,
				Kind: SymVariable,
				Type: narrowed,
			})
			return child
		}
	}
	return scope
}

// ========== @withAuth Method Injection ==========

func hasModelDirective(directives []*ast.Directive, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

// injectStreamIt injects `it` into scope for @stream API bodies.
// `it` has the event's fields so `it.projectId == projectId` works.
func (a *Analyzer) injectStreamIt(api *ast.ApiDecl, scope *Scope, file *ast.File) {
	var eventName string
	for _, d := range api.Directives {
		if d.Name == "stream" && len(d.Args) > 0 {
			if ident, ok := d.Args[0].Value.(*ast.Ident); ok {
				eventName = ident.Name
			}
		}
	}
	if eventName == "" {
		return
	}
	// Find the event declaration across all files (event may be in another file)
	var event *ast.EventDecl
	for _, f := range a.files {
		for _, ev := range f.Events {
			if ev.Name == eventName {
				event = ev
				break
			}
		}
		if event != nil {
			break
		}
	}
	if event == nil {
		return
	}
	// Build a type for `it` with event's params as fields
	itType := &ResolvedType{Kind: TypeModel, Name: eventName, Fields: make(map[string]*FieldInfo)}
	for _, p := range event.Params {
		pType := a.resolveTypeRef(p.Type, api.Pos)
		itType.Fields[p.Name] = &FieldInfo{Name: p.Name, Type: pType}
	}
	scope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
		Type: itType,
		Used: true, // implicit, don't warn unused
	})
}

func (a *Analyzer) injectWithAuthMethods(typ *ResolvedType, directives []*ast.Directive) {
	typ.Fields["createToken"] = &FieldInfo{
		Name:     "createToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".createToken(expires?: Duration): String — Generate JWT token / 生成 JWT 令牌",
	}
	typ.Fields["verify"] = &FieldInfo{
		Name:     "verify",
		Type:     a.types["Boolean"],
		IsMethod: true,
		Doc:      ".verify(plain: String): Boolean — Verify password against @hash field / 校验 @hash 字段的密码",
	}
	// refreshToken always injected — whether refresh is enabled is controlled
	// by JWT_REFRESH env var at runtime, not at compile time.
	typ.Fields["refreshToken"] = &FieldInfo{
		Name:     "refreshToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".refreshToken(token: String): String — Refresh JWT token / 刷新 JWT 令牌",
	}

	// Inject stores fields into Identity type so my.<field> works for all stored fields
	identityType := a.types["Identity"]
	for _, d := range directives {
		if d.Name != "withAuth" {
			continue
		}
		for _, arg := range d.Args {
			if arg.Name != "stores" {
				continue
			}
			list, ok := arg.Value.(*ast.ListExpr)
			if !ok {
				break
			}
			for _, elem := range list.Items {
				ident, ok := elem.(*ast.Ident)
				if !ok {
					continue
				}
				// skip fields already defined with type (id, load)
				// but override nil-typed defaults (role) with actual type
				if existing, exists := identityType.Fields[ident.Name]; exists && existing.Type != nil {
					continue
				}
				// resolve type from the model's fields, skip methods
				fieldType := typ.Fields[ident.Name]
				if fieldType != nil && !fieldType.IsMethod {
					identityType.Fields[ident.Name] = &FieldInfo{
						Name: ident.Name,
						Type: fieldType.Type,
					}
				}
			}
		}
	}
}

// ========== Unused Variable Warning ==========

// checkUnusedVariables warns about variables declared but never used.
func (a *Analyzer) checkUnusedVariables(scope *Scope) {
	for _, sym := range scope.AllSymbols() {
		if sym.Kind != SymVariable {
			continue
		}
		if sym.Used {
			continue
		}
		// skip _ prefixed variables and auto-injected symbols
		if len(sym.Name) > 0 && sym.Name[0] == '_' {
			continue
		}
		if sym.Name == "my" || sym.Name == "it" || sym.Name == "request" {
			continue
		}
		// Use name length so the diagnostic range highlights the full identifier
		a.addWarningWithLen(sym.Pos, len(sym.Name), "variable '%s' is declared but never used / 变量 '%s' 已声明但未使用", sym.Name, sym.Name)
	}
}

// ========== Event Cycle Detection ==========

// checkEventCycles detects circular event dependencies among on-listeners.
// For each on EventA { ... emit EventB(...) ... }, edge A → B is added.
// DFS detects cycles and reports them as errors.
func (a *Analyzer) checkEventCycles() {
	graph := a.buildEventGraph()
	if len(graph) == 0 {
		return
	}
	a.detectCycles(graph)
}

// buildEventGraph walks all OnDecl bodies looking for EmitStmt nodes
// and returns an adjacency map: event → list of emitted events.
func (a *Analyzer) buildEventGraph() map[string][]string {
	graph := map[string][]string{}
	for _, file := range a.files {
		for _, on := range file.Listeners {
			if on.Body == nil {
				continue
			}
			emitted := collectEmits(on.Body)
			if len(emitted) > 0 {
				graph[on.EventName] = append(graph[on.EventName], emitted...)
			}
		}
	}
	return graph
}

// collectEmits recursively collects all event names from EmitStmt in a block.
func collectEmits(block *ast.Block) []string {
	var result []string
	for _, stmt := range block.Stmts {
		if emit, ok := stmt.(*ast.EmitStmt); ok {
			result = append(result, emit.EventName)
		}
	}
	return result
}

// detectCycles runs DFS on the event graph to find and report cycles.
func (a *Analyzer) detectCycles(graph map[string][]string) {
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	color := map[string]int{}
	parent := map[string]string{}

	var dfs func(node string)
	dfs = func(node string) {
		color[node] = gray
		for _, next := range graph[node] {
			if color[next] == gray {
				// cycle found — reconstruct path
				path := a.buildCyclePath(next, node, parent)
				a.addError(token.Position{},
					"circular event dependency detected: %s / 检测到循环事件依赖: %s",
					path, path)
				return
			}
			if color[next] == white {
				parent[next] = node
				dfs(next)
			}
		}
		color[node] = black
	}

	for node := range graph {
		if color[node] == white {
			dfs(node)
		}
	}
}

// buildCyclePath reconstructs the cycle path string from parent map.
func (a *Analyzer) buildCyclePath(cycleStart, cycleEnd string, parent map[string]string) string {
	var path []string
	cur := cycleEnd
	for i := 0; cur != cycleStart && i < len(parent)+1; i++ {
		path = append([]string{cur}, path...)
		next, ok := parent[cur]
		if !ok {
			break
		}
		cur = next
	}
	path = append([]string{cycleStart}, path...)
	path = append(path, cycleStart)
	return strings.Join(path, " → ")
}

// ========== Directive Validation ==========

func (a *Analyzer) checkDirectives(directives []*ast.Directive, ctx DirectiveContext) {
	for _, d := range directives {
		a.validateDirective(d, ctx, "")
	}
}

func (a *Analyzer) checkFieldDirectives(f *ast.FieldDecl) {
	if f.Type == nil {
		return
	}
	typeName := f.Type.Name
	for _, d := range f.Directives {
		a.validateDirective(d, OnField, typeName)
	}
	// computed field directives (@count, @sum, @avg, etc.)
	if f.Computed != nil {
		for _, d := range f.Computed.Directives {
			a.validateDirective(d, OnComputed, typeName)
		}
	}
}

// ========== Error Helpers ==========

func (a *Analyzer) addError(pos token.Position, format string, args ...any) {
	a.errors = append(a.errors, Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addWarning(pos token.Position, format string, args ...any) {
	a.warnings = append(a.warnings, Warning{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addWarningWithLen(pos token.Position, nameLen int, format string, args ...any) {
	a.warnings = append(a.warnings, Warning{
		Pos:     pos,
		NameLen: nameLen,
		Message: fmt.Sprintf(format, args...),
	})
}

func (a *Analyzer) addErrorWithSuggestion(pos token.Position, name string, format string, args ...any) {
	err := Error{
		Pos:     pos,
		Message: fmt.Sprintf(format, args...),
	}
	// find closest match
	if suggestion := a.findClosest(name); suggestion != "" {
		err.Suggestion = fmt.Sprintf("did you mean '%s'?", suggestion)
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) addFieldError(pos token.Position, objType *ResolvedType, fieldName string) {
	msg := fmt.Sprintf("'%s' has no field '%s' / '%s' 没有字段 '%s'", objType.Name, fieldName, objType.Name, fieldName)
	err := Error{Pos: pos, Message: msg}

	// suggest closest field name
	if objType.Fields != nil {
		closest := findClosestString(fieldName, fieldNames(objType))
		if closest != "" {
			err.Suggestion = fmt.Sprintf("did you mean '%s'?", closest)
		}
	}
	a.errors = append(a.errors, err)
}

func (a *Analyzer) findClosest(name string) string {
	var candidates []string
	for typeName := range a.types {
		candidates = append(candidates, typeName)
	}
	for _, sym := range a.scope.AllSymbols() {
		candidates = append(candidates, sym.Name)
	}
	return findClosestString(name, candidates)
}

func fieldNames(t *ResolvedType) []string {
	var names []string
	for name := range t.Fields {
		names = append(names, name)
	}
	return names
}

func findClosestString(target string, candidates []string) string {
	best := ""
	bestDist := 3 // max edit distance to suggest
	for _, c := range candidates {
		d := editDistance(target, c)
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// hasSearchDirective checks if the expression's underlying field has @search directive.
func (a *Analyzer) hasSearchDirective(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	objType := a.checkExpr(member.Object, a.scope)
	if objType == nil {
		return false
	}
	field := objType.LookupField(member.Field)
	if field == nil {
		return false
	}
	for _, d := range field.Directives {
		if d == "search" {
			return true
		}
	}
	return false
}

func isDebugMethod(name string) bool {
	switch name {
	case "d", "i", "w", "e":
		return true
	}
	return false
}

// isQueryMethod returns true if the name is a chain query method on models.
func isQueryMethod(name string) bool {
	switch name {
	case "where", "groupBy", "orderBy",
		"sum", "avg", "count", "min", "max",
		"select", "first", "all",
		"limit", "offset",
		"aggregate", "paginate", "exists",
		"deleteMany", "save":
		return true
	}
	// CRUD ops are also valid chain methods on models (e.g. User.find(...))
	return isCRUDOp(name)
}

func isCollectionMethod(name string) bool {
	switch name {
	case "map", "filter", "sumOf", "count", "any", "firstOrNull",
		"sortBy", "groupBy", "forEach", "flatMap", "joinToString",
		"take", "takeLast", "size", "isEmpty", "contains",
		"first", "last", "lastOrNull", "reversed", "shuffled",
		"distinct", "distinctBy", "drop", "dropLast",
		"indexOf", "zip", "chunked", "windowed", "all", "none",
		"minOf", "maxOf", "sortByDesc", "isNotEmpty", "let":
		return true
	}
	return false
}

func (a *Analyzer) resolveCollectionMethod(method string, listType *ResolvedType) *ResolvedType {
	intType := a.types["Int"]
	boolType := a.types["Boolean"]
	stringType := a.types["String"]

	switch method {
	case "size", "count":
		return intType
	case "isEmpty", "any", "contains":
		return boolType
	case "joinToString":
		return stringType
	case "filter", "sortBy", "take", "takeLast":
		return listType // returns same list type
	case "map", "flatMap", "groupBy":
		return nil // type depends on lambda, can't infer here
	case "forEach":
		return nil // void
	case "firstOrNull":
		// returns single element, nullable
		return &ResolvedType{
			Kind:     listType.Kind,
			Name:     listType.Name,
			Nullable: true,
			Fields:   listType.Fields,
			Parents:  listType.Parents,
		}
	case "sumOf":
		return a.types["Float"]
	default:
		// distinct, reversed, shuffled, drop, dropLast, zip, chunked, windowed, indexOf, etc.
		return nil
	}
}

// resolveQueryMethod resolves the return type for a chain query method on a model.
// modelType is the underlying model type (not the query builder).
func (a *Analyzer) resolveQueryMethod(method string, modelType *ResolvedType) *ResolvedType {
	qb := &ResolvedType{
		Kind:      TypeQueryBuilder,
		Name:      modelType.Name + "QueryBuilder",
		Fields:    modelType.Fields,
		ModelType: modelType,
	}

	switch method {
	case "where", "groupBy", "orderBy", "limit", "offset":
		// chaining methods — return a query builder
		return qb
	case "sum", "avg":
		return a.types["Float"]
	case "count":
		return a.types["Int"]
	case "min", "max":
		// could be any numeric type; Float is a safe approximation
		return a.types["Float"]
	case "select":
		// select { ... } — return type depends on the lambda; can't infer here
		return nil
	case "first":
		return modelType.AsNullable()
	case "all":
		return modelType.AsList()
	// chain-style CRUD operations: Model.find(...), Model.create(...), etc.
	case "find":
		// return type depends on args (where → list, id → non-nullable); resolved in checkCallExpr
		return nil
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	case "aggregate", "raw", "paginate":
		return nil
	default:
		return nil
	}
}

func editDistance(a, b string) int {
	m, n := len(a), len(b)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}
	// Two-row DP: O(n) space instead of O(m*n).
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func isBuiltinOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "count", "exists",
		"aggregate", "groupBy", "raw", "paginate", "save",
		"throw", "transaction", "cache", "storage",
		"mail", "task", "services", "error", "request",
		"Channel", "Result", "my",
		"http", "json", "time", "math", "crypto", "convert",
		"regex", "base64", "url", "uuid",
		"print", "env", "now", "today",
		"verifyHash", "generateToken":
		return true
	}
	return false
}

func kindToSymbol(kind TypeKind) SymbolKind {
	switch kind {
	case TypeModel:
		return SymModel
	case TypeInterface:
		return SymInterface
	case TypeEnum:
		return SymEnum
	case TypeSealed:
		return SymSealed
	default:
		return SymType
	}
}
