package semantic

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/internal/builtinerror"
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
			// Narrow the payload view without changing the whole sum's identity.
			narrowed := *sealedType
			narrowed.Nullable = false
			narrowed.Fields = make(map[string]*FieldInfo, len(v.Fields))
			for _, f := range v.Fields {
				narrowed.Fields[f.Name] = f
			}
			child := scope.Child()
			symbol := Symbol{Name: ident.Name, Kind: SymVariable, Type: &narrowed}
			if original := scope.Lookup(ident.Name); original != nil {
				symbol = *original
			}
			child.Define(&symbol)
			child.Narrow(ident.Name, &narrowed)
			return child
		}
	}
	return scope
}

// ========== @withAuth Method Injection ==========

func (a *Analyzer) checkAuthMethodArguments(call *ast.CallExpr, argTypes []*ResolvedType) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok {
		return
	}
	switch member.Field {
	case "createToken", "createRefreshToken", "refreshToken", "verify":
	default:
		if !strings.HasPrefix(member.Field, "verify") {
			return
		}
	}
	model := a.types[member.Object.GetTypeTag()]
	if model == nil || model.Kind != TypeModel || member.Object.IsListType() {
		return
	}
	field := model.LookupField(member.Field)
	if field == nil || !field.IsMethod {
		return
	}
	if hasDirective(field.Directives, "hash") {
		if model.Fields[member.Field] == nil {
			a.addError(call.Pos, "method '%s' has no matching @hash field on model '%s'", member.Field, model.Name)
			return
		}
		a.checkFunctionArguments(call, hashVerificationParameters(member.Field), argTypes)
		return
	}
	// Model inheritance expands fields, not the parent authentication directive.
	if model.Fields[member.Field] == nil {
		a.addError(call.Pos, "method '%s' requires @withAuth on model '%s'", member.Field, model.Name)
		return
	}
	var parameters []*ast.ParamDecl
	switch member.Field {
	case "createToken":
		// Only marks omission as valid here; generated code reads the configured
		// duration at runtime instead of compiling this marker as a default.
		parameters = []*ast.ParamDecl{{Name: "expires", Type: &ast.TypeRef{Name: "Duration"}, Default: &ast.Literal{}}}
	case "refreshToken":
		parameters = []*ast.ParamDecl{{Name: "token", Type: &ast.TypeRef{Name: "String"}}}
	case "verify":
		if len(effectiveHashFields(model)) != 1 {
			a.addError(call.Pos, "verify requires exactly one @hash field; use a field-specific verification method")
			return
		}
		parameters = hashVerificationParameters(member.Field).Params
	}
	a.checkFunctionArguments(call, &ast.FnDecl{Name: member.Field, Params: parameters}, argTypes)
}

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

func (a *Analyzer) injectWithAuthMethods(typ *ResolvedType) {
	a.injectAuthMethod(typ, &FieldInfo{
		Name:     "createToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".createToken(expires?: Duration): String — Generate access token / 生成访问令牌",
	})
	a.injectAuthMethod(typ, &FieldInfo{
		Name:     "createRefreshToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".createRefreshToken(): String — Generate refresh token when enabled / 启用刷新时签发刷新令牌",
	})
	a.injectAuthMethod(typ, &FieldInfo{
		Name:     "verify",
		Type:     a.types["Boolean"],
		IsMethod: true,
		Doc:      ".verify(plain: String): Boolean — Verify the single @hash field / 仅校验唯一的 @hash 字段",
	})
	// refreshToken always injected — whether refresh is enabled is controlled
	// by JWT_REFRESH env var at runtime, not at compile time.
	a.injectAuthMethod(typ, &FieldInfo{
		Name:     "refreshToken",
		Type:     a.types["String"],
		IsMethod: true,
		Doc:      ".refreshToken(token: String): String — Exchange refresh for access token / 使用刷新令牌换取访问令牌",
	})

}

func (a *Analyzer) injectAuthMethod(typ *ResolvedType, method *FieldInfo) {
	if existing := typ.LookupField(method.Name); existing != nil && !existing.IsMethod {
		a.addError(existing.Pos, "field '%s' conflicts with @withAuth injected method", method.Name)
		return
	}
	typ.Fields[method.Name] = method
}

func (a *Analyzer) injectStoredIdentityFields(typ *ResolvedType, directives []*ast.Directive) {
	injectIdentityStores(a.types["Identity"], typ, directives)
}

func injectIdentityStores(identityType, typ *ResolvedType, directives []*ast.Directive) {
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
				// Preserve declared fields and methods; resolve untyped defaults
				// (id and role) from the stored field's actual type.
				if existing, exists := identityType.Fields[ident.Name]; exists && existing.Type != nil {
					continue
				}
				// resolve type from the model's fields, skip methods
				fieldType := typ.LookupField(ident.Name)
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
	var bodies map[callableKey]*ast.Block
	for _, file := range a.files {
		for _, on := range file.Listeners {
			if on.Body == nil {
				continue
			}
			emitted, nextBodies := collectReachableEmits(on.Body, a.fileModule(file.Name), bodies, a)
			bodies = nextBodies
			if len(emitted) > 0 {
				graph[on.EventName] = append(graph[on.EventName], emitted...)
			}
		}
	}
	return graph
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
	seenAuth := false
	for _, d := range directives {
		if d.Name == "auth" {
			if seenAuth {
				a.addError(d.Pos, "duplicate @auth directive / @auth 不能重复声明")
			}
			seenAuth = true
		}
		a.validateDirective(d, ctx, "")
	}
}

func (a *Analyzer) checkFieldDirectives(f *ast.FieldDecl) {
	if f.Type == nil {
		return
	}
	typeName := f.Type.Name
	seenVisible := false
	seenBy := false
	for _, d := range f.Directives {
		if d.Name == "by" {
			target := a.types[typeName]
			if target == nil || target.Kind != TypeModel {
				a.addError(d.Pos, "@by requires a model relation / @by 只能用于模型关系字段")
			}
			if seenBy {
				a.addError(d.Pos, "duplicate @by directive / @by 不能重复声明")
			}
			seenBy = true
		}
		if d.Name == "visible" {
			if seenVisible {
				a.addError(d.Pos, "duplicate @visible directive / @visible 不能重复声明；请在一个条件表达式中明确组合规则")
			} else if !f.Type.Nullable {
				// @visible requires nullable field — non-nullable fields can't be conditionally hidden
				a.addError(f.Pos, "@visible requires nullable field (use %s? instead of %s) / @visible 字段必须可空", f.Type.Name, f.Type.Name)
			}
			seenVisible = true
		}
		a.validateDirective(d, OnField, typeName)
		if d.Name == "filterable" && !a.supportsFilterableType(f.Type) {
			a.addError(d.Pos, "@filterable requires a supported scalar or enum (Int, Float, String, Boolean, DateTime, UUID, Decimal); lists, JSON, Bytes, Duration and structured types are not implemented / @filterable 仅支持已实现的标量和枚举；列表、JSON、Bytes、Duration及结构类型尚未实现")
		}
	}
	// computed field directives (@count, @sum, @avg, etc.)
	if f.Computed != nil {
		for _, d := range f.Computed.Directives {
			a.validateDirective(d, OnComputed, typeName)
		}
	}
}

// Keep unsupported query declarations out of generated public schemas. Nullable
// scalar fields retain ordinary comparison semantics; no null operator is added.
func (a *Analyzer) supportsFilterableType(ref *ast.TypeRef) bool {
	if ref.IsList {
		return false
	}
	switch ref.Name {
	case "Int", "Float", "String", "Boolean", "DateTime", "UUID", "Decimal":
		return true
	default:
		typ := a.types[ref.Name]
		return typ != nil && typ.Kind == TypeEnum
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
	match := closestSpelling{target: name, distance: 3}
	for typeName := range a.types {
		match.consider(typeName)
	}
	for _, sym := range a.scope.symbols {
		match.consider(sym.Name)
	}
	return match.name
}

func fieldNames(t *ResolvedType) []string {
	var names []string
	for name := range t.Fields {
		names = append(names, name)
	}
	return names
}

func findClosestString(target string, candidates []string) string {
	match := closestSpelling{target: target, distance: 3}
	for _, candidate := range candidates {
		match.consider(candidate)
	}
	return match.name
}

type closestSpelling struct {
	target   string
	name     string
	distance int
}

func (match *closestSpelling) consider(candidate string) {
	// Length difference is a lower bound; ties cannot replace the first match.
	lengthDelta := len(candidate) - len(match.target)
	if lengthDelta >= match.distance || lengthDelta <= -match.distance {
		return
	}
	if distance := spellingDistanceBelow(match.target, candidate, match.distance); distance < match.distance {
		match.distance = distance
		match.name = candidate
	}
}

// hasSearchDirective checks if the expression's underlying field has @search directive.
func (a *Analyzer) hasSearchDirective(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	if !ok {
		return false
	}
	// Member resolution already checked the receiver in its lexical scope.
	// Rechecking it at the root would lose collection and function parameters.
	objType := a.resolvedExprType(member.Object)
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
	return builtin.DebugMethod(name) != ""
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
	return builtin.CollectionMethod(name) != builtin.NotCollectionMethod
}

func (a *Analyzer) resolveCollectionMethod(method string, listType *ResolvedType) *ResolvedType {
	intType := a.types["Int"]
	boolType := a.types["Boolean"]
	stringType := a.types["String"]

	switch method {
	case "size":
		return intType
	case "isEmpty", "any":
		return boolType
	case "joinToString":
		return stringType
	case "filter", "sortBy":
		return listType // returns same list type
	default:
		// Lambda-dependent results are resolved by checkCollectionCall.
		return nil
	}
}

// resolveQueryMethod resolves the return type for a chain query method on a model.
// modelType is the underlying model type (not the query builder).
func (a *Analyzer) resolveQueryMethod(method string, receiver *ResolvedType, member *ast.MemberExpr, scope *Scope) *ResolvedType {
	modelType := receiver
	if receiver.Kind == TypeQueryBuilder {
		modelType = receiver.ModelType
	}
	qb := &ResolvedType{
		Kind:      TypeQueryBuilder,
		Name:      modelType.Name + "QueryBuilder",
		Fields:    modelType.Fields,
		ModelType: modelType,
	}

	switch method {
	case "where", "groupBy", "orderBy", "limit", "offset", "select":
		// chaining methods — return a query builder
		return qb
	case "sum", "avg", "min", "max":
		// The call's selected field determines its result type.
		return nil
	case "count":
		return a.types["Int"]
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
	case "save":
		if member != nil && a.rejectSaveQueryReceiver(member, receiver, scope) {
			return nil
		}
		if member != nil && a.callTarget != member {
			if receiver.LookupField(member.Field) != nil {
				return a.resolveFieldAccess(member, receiver)
			}
			a.addError(member.Pos, "save requires an invocation; callable values are not supported / save 必须显式调用，不能作为函数值使用")
			return nil
		}
		return modelType
	case "create", "update", "upsert", "delete":
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

// Spelling suggestions consider fewer than three edits. Search only paths
// that can improve the current distance, preserving byte-wise comparisons.
func spellingDistanceBelow(left, right string, limit int) int {
	delta := len(left) - len(right)
	if delta >= limit || -delta >= limit {
		return limit
	}
	for len(left) != 0 && len(right) != 0 && left[0] == right[0] {
		left, right = left[1:], right[1:]
	}
	if len(left) == 0 || len(right) == 0 {
		return len(left) + len(right)
	}
	if limit == 1 {
		return limit
	}
	best := spellingDistanceBelow(left[1:], right[1:], limit-1)
	if best > 0 {
		best = spellingDistanceBelow(left[1:], right, best)
	}
	if best > 0 {
		best = spellingDistanceBelow(left, right[1:], best)
	}
	return 1 + best
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

func isBuiltinErrorName(name string) bool {
	return builtinerror.GoName(name) != ""
}

// Resolve identity stores only after every model's fields are available.
// An explicit default excludes other subjects before body analysis.
func (a *Analyzer) selectDefaultIdentity(files []*ast.File) {
	var selected *ast.ModelDecl
	var sole *ast.ModelDecl
	count := 0
	for _, file := range files {
		for _, model := range file.Models {
			if hasModelDirective(model.Directives, "withAuth") {
				sole = model
				count++
			}
			// Parent fields must be resolved before injected methods can check conflicts.
			if typ := a.types[model.Name]; typ != nil {
				a.injectHashMethods(typ)
				if hasModelDirective(model.Directives, "withAuth") {
					a.injectWithAuthMethods(typ)
				}
			}
			if !a.isDefaultIdentity(model) {
				continue
			}
			if selected != nil {
				a.addError(model.Pos, "multiple default auth subjects: '%s' and '%s' / 存在多个默认认证主体", selected.Name, model.Name)
				continue
			}
			selected = model
		}
	}
	if selected == nil && count == 1 {
		selected = sole
	}
	a.prepareAuthIdentities(files)
	a.bindDefaultAuthSubject(files, selected, count > 1)
	if selected != nil {
		a.injectStoredIdentityFields(a.types[selected.Name], selected.Directives)
		return
	}
}

func (a *Analyzer) isDefaultIdentity(model *ast.ModelDecl) bool {
	selected := false
	for _, directive := range model.Directives {
		if directive.Name != "withAuth" {
			continue
		}
		a.checkWithAuthArgumentNames(directive, a.types[model.Name])
		for _, arg := range directive.Args {
			if arg.Name != "default" {
				continue
			}
			literal, ok := arg.Value.(*ast.Literal)
			if !ok || (literal.Kind != token.True && literal.Kind != token.False) {
				a.addError(directive.Pos, "@withAuth default requires a Boolean literal / @withAuth default 必须是 Boolean 字面量")
				continue
			}
			selected = literal.Kind == token.True
		}
	}
	return selected
}

func (a *Analyzer) checkWithAuthArgumentNames(directive *ast.Directive, model *ResolvedType) {
	var seen uint8
	for _, arg := range directive.Args {
		var bit uint8
		switch arg.Name {
		case "stores":
			bit = 1
			a.checkAuthStoreList(directive, arg.Value, model)
		case "default":
			bit = 2
		default:
			a.addError(directive.Pos, "unknown @withAuth argument '%s' / 未知 @withAuth 参数 '%s'", arg.Name, arg.Name)
			continue
		}
		if seen&bit != 0 {
			a.addError(directive.Pos, "duplicate @withAuth argument '%s' / @withAuth 参数 '%s' 不能重复声明", arg.Name, arg.Name)
		}
		seen |= bit
	}
}

func (a *Analyzer) checkAuthStoreList(directive *ast.Directive, value ast.Expr, model *ResolvedType) {
	list, ok := value.(*ast.ListExpr)
	if !ok {
		a.addError(directive.Pos, "@withAuth stores requires an explicit field list / @withAuth stores 必须是显式字段列表")
		return
	}
	seen := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		field, ok := item.(*ast.Ident)
		if !ok {
			a.addError(directive.Pos, "@withAuth stores requires field names / @withAuth stores 只允许字段名")
			continue
		}
		if seen[field.Name] {
			a.addError(directive.Pos, "@withAuth stores repeats field '%s' / @withAuth stores 重复字段 '%s'", field.Name, field.Name)
		}
		seen[field.Name] = true
		if model != nil {
			stored := model.LookupField(field.Name)
			if stored == nil || stored.Type == nil || stored.IsMethod {
				a.addError(directive.Pos, "@withAuth stores references unknown or non-data field '%s' / @withAuth stores 引用了不存在或非数据字段 '%s'", field.Name, field.Name)
			}
		}
	}
}
