package semantic

import (
	"sort"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func (a *Analyzer) checkExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	if expr == nil {
		return nil
	}

	var resolved *ResolvedType
	switch e := expr.(type) {
	case *ast.Literal:
		resolved = a.checkLiteralExpr(e)
	case *ast.Ident:
		resolved = a.checkIdentExpr(e, scope)
	case *ast.MemberExpr:
		resolved = a.checkMemberExpr(e, scope)
	case *ast.CallExpr:
		resolved = a.checkCallExpr(e, scope)
	case *ast.BinaryExpr:
		resolved = a.checkBinaryExpr(e, scope)
	case *ast.UnaryExpr:
		resolved = a.checkUnaryExpr(e, scope)
	case *ast.ElvisExpr:
		resolved = a.checkElvisExpr(e, scope)
	case *ast.BangElvisExpr:
		a.checkExpr(e.Left, scope)
		a.checkExpr(e.Right, scope)
	case *ast.WhenExpr:
		resolved = a.checkWhenExpr(e, scope)
	case *ast.LambdaExpr:
		resolved = a.checkLambdaExpr(e, scope)
	default:
		resolved = a.checkCompositeExpr(expr, scope)
	}

	// Write type tag into AST node for codegen to read
	if resolved != nil {
		setExprResolvedType(expr, resolved)
		if resolved.Kind == TypeGeneric && resolved.Name == "Result" && a.resultOperand == 0 {
			a.addError(expr.GetPos(), "Result<T> must be consumed directly with '?' / Result<T> 必须直接使用 '?' 解包")
		}
	}
	return resolved
}

// checkCompositeExpr handles composite expression types (list, object, range, transaction, template).
func (a *Analyzer) checkCompositeExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	switch e := expr.(type) {
	case *ast.ListExpr:
		return a.checkListExpr(e, scope)
	case *ast.ObjectExpr:
		return a.checkObjectExpr(e, scope)
	case *ast.RangeExpr:
		return a.checkRangeExpr(e, scope)
	case *ast.TransactionExpr:
		return a.checkTransactionExpr(e, scope)
	case *ast.TemplateString:
		return a.checkTemplateString(e, scope)
	case *ast.YieldExpr:
		return a.checkExpr(e.Value, scope)
	case *ast.AsyncExpr:
		childScope := scope.Child()
		a.checkBlock(e.Body, childScope)
		return nil
	case *ast.AwaitExpr:
		return a.checkAwaitExpr(e, scope)
	case *ast.ForStmt:
		return a.checkForStmt(e, scope)
	}
	// unreachable: all known ast.Expr types are covered by checkExpr + checkCompositeExpr switches;
	// kept as defensive fallback required by Go's type system.
	return nil
}

func (a *Analyzer) checkListExpr(e *ast.ListExpr, scope *Scope) *ResolvedType {
	if len(e.Items) == 0 {
		return &ResolvedType{Kind: TypeUnknown, IsList: true}
	}

	var elementType *ResolvedType
	for _, item := range e.Items {
		actual := a.checkExpr(item, scope)
		if actual == nil {
			continue
		}
		if actual.Kind == TypeUnknown && actual.Name == "null" {
			a.addError(item.GetPos(), "list elements cannot be null / 列表元素不能为 null")
			continue
		}
		if elementType == nil {
			elementType = actual
			continue
		}
		if !sameResolvedType(elementType, actual) {
			a.addError(item.GetPos(), "list element type mismatch: expected '%s', got '%s' / 列表元素类型不匹配：需要 '%s'，得到 '%s'",
				formatResolvedType(elementType), formatResolvedType(actual),
				formatResolvedType(elementType), formatResolvedType(actual))
		}
	}
	if elementType == nil {
		return &ResolvedType{Kind: TypeUnknown, IsList: true}
	}
	result := cloneResolvedType(elementType)
	result.IsList = true
	result.Nullable = false
	return result
}

func setExprResolvedType(expr ast.Expr, resolved *ResolvedType) {
	if expr == nil || resolved == nil {
		return
	}
	expr.SetTypeTag(resolved.Name)
	expr.SetNullable(resolved.Nullable)
	expr.SetListType(resolved.IsList)
}

func isEmptyListExpr(expr ast.Expr) bool {
	list, ok := expr.(*ast.ListExpr)
	return ok && len(list.Items) == 0
}

func cloneResolvedType(typ *ResolvedType) *ResolvedType {
	if typ == nil {
		return nil
	}
	result := *typ
	return &result
}

func collectionElementType(collection *ResolvedType) *ResolvedType {
	if collection == nil {
		return nil
	}
	if collection.Kind == TypeGeneric && collection.Name == "Channel" && len(collection.TypeArgs) > 0 {
		return cloneResolvedType(collection.TypeArgs[0])
	}
	if !collection.IsList {
		return nil
	}
	result := cloneResolvedType(collection)
	result.IsList = false
	result.Nullable = false
	return result
}

func sameResolvedType(left, right *ResolvedType) bool {
	if left == nil || right == nil {
		return true
	}
	return left.Name == right.Name && left.Kind == right.Kind && left.IsList == right.IsList && left.Nullable == right.Nullable &&
		typeArgsAssignable(left.TypeArgs, right.TypeArgs) && typeArgsAssignable(right.TypeArgs, left.TypeArgs)
}

func (a *Analyzer) inferForExprType(body *ast.Block) *ResolvedType {
	var yieldType *ResolvedType
	hasYield := false
	ast.WalkExprs(body, func(expr ast.Expr) {
		yieldExpr, ok := expr.(*ast.YieldExpr)
		if !ok {
			return
		}
		hasYield = true
		actual := a.resolvedExprType(yieldExpr.Value)
		if actual == nil || actual.Kind == TypeUnknown && actual.Name == "null" {
			return
		}
		if yieldType == nil {
			yieldType = actual
			return
		}
		if !sameResolvedType(yieldType, actual) {
			a.addError(yieldExpr.GetPos(), "yield type mismatch: expected '%s', got '%s' / yield 类型不匹配：需要 '%s'，得到 '%s'",
				formatResolvedType(yieldType), formatResolvedType(actual),
				formatResolvedType(yieldType), formatResolvedType(actual))
		}
	})
	if hasYield {
		if yieldType == nil {
			return nil
		}
		result := cloneResolvedType(yieldType)
		result.Nullable = true
		result.IsList = false
		return result
	}

	value := blockResultExpr(body)
	result := a.resolvedExprType(value)
	if result == nil {
		return nil
	}
	result.IsList = true
	result.Nullable = false
	return result
}

func blockResultExpr(body *ast.Block) ast.Expr {
	if body == nil || len(body.Stmts) == 0 {
		return nil
	}
	switch stmt := body.Stmts[len(body.Stmts)-1].(type) {
	case *ast.ExprStmt:
		return stmt.Expr
	case *ast.ReturnStmt:
		return stmt.Value
	default:
		return nil
	}
}

func (a *Analyzer) resolvedExprType(expr ast.Expr) *ResolvedType {
	if expr == nil || expr.GetTypeTag() == "" {
		return nil
	}
	base := a.types[expr.GetTypeTag()]
	if base == nil {
		base = &ResolvedType{Kind: TypeUnknown, Name: expr.GetTypeTag()}
	}
	result := cloneResolvedType(base)
	result.Nullable = expr.IsNullable()
	result.IsList = expr.IsListType()
	return result
}

func (a *Analyzer) checkObjectExpr(e *ast.ObjectExpr, scope *Scope) *ResolvedType {
	if e.TypeName == "" {
		for _, f := range e.Fields {
			a.checkExpr(f.Value, scope)
		}
		return nil
	}

	typ, ok := a.types[e.TypeName]
	if !ok {
		for _, f := range e.Fields {
			a.checkExpr(f.Value, scope)
		}
		a.addErrorWithSuggestion(e.Pos, e.TypeName, "unknown type '%s' / 未知类型 '%s'", e.TypeName, e.TypeName)
		return nil
	}
	if a.moduleMap != nil && a.currentFile != nil {
		if mi := a.moduleMap[a.currentFile.Name]; mi != nil {
			a.checkNameVisibility(e.TypeName, mi, e.Pos)
		}
	}
	a.validateObjectExprFields(e, typ, scope)
	return typ.AsNonNull()
}

func (a *Analyzer) validateObjectExprFields(e *ast.ObjectExpr, typ *ResolvedType, scope *Scope) {
	provided := make(map[string]bool, len(e.Fields))
	for _, value := range e.Fields {
		actual := a.checkExpr(value.Value, scope)
		if provided[value.Name] {
			a.addError(value.Value.GetPos(), "duplicate field '%s' in '%s' / '%s' 中字段 '%s' 重复", value.Name, typ.Name, typ.Name, value.Name)
			continue
		}
		provided[value.Name] = true

		field := typ.LookupField(value.Name)
		if field == nil {
			a.addFieldError(value.Value.GetPos(), typ, value.Name)
			continue
		}
		if field.Type != nil && actual != nil && !isTypeAssignable(field.Type, actual) {
			a.addError(value.Value.GetPos(), "field '%s' expects '%s', got '%s' / 字段 '%s' 需要 '%s'，得到 '%s'",
				value.Name, formatResolvedType(field.Type), formatResolvedType(actual),
				value.Name, formatResolvedType(field.Type), formatResolvedType(actual))
		}
	}

	if typ.Kind == TypeCustom {
		a.checkRequiredObjectFields(e, typ, provided)
	}
}

func (a *Analyzer) checkRequiredObjectFields(e *ast.ObjectExpr, typ *ResolvedType, provided map[string]bool) {
	names := make([]string, 0, len(typ.Fields))
	for name := range typ.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := typ.Fields[name]
		if provided[name] || field.Nullable || field.HasDefault || field.Computed || field.IsMethod {
			continue
		}
		a.addError(e.Pos, "missing required field '%s' in '%s' / '%s' 缺少必填字段 '%s'", name, typ.Name, typ.Name, name)
	}
}

func isTypeAssignable(expected, actual *ResolvedType) bool {
	if expected == nil || actual == nil {
		return true
	}
	if actual.Kind == TypeUnknown {
		return actual.Name != "null" || expected.Nullable
	}
	if expected.Kind == TypeUnknown {
		return true
	}
	if actual.Nullable && !expected.Nullable {
		return false
	}
	if expected.IsList != actual.IsList {
		return false
	}
	if expected.Name == actual.Name {
		return typeArgsAssignable(expected.TypeArgs, actual.TypeArgs)
	}
	return actual.inheritsFrom(expected.Name)
}

func typeArgsAssignable(expected, actual []*ResolvedType) bool {
	if len(expected) == 0 || len(actual) == 0 {
		return true
	}
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if !isTypeAssignable(expected[i], actual[i]) {
			return false
		}
	}
	return true
}

func formatResolvedType(typ *ResolvedType) string {
	name := typ.Name
	if typ.IsList {
		name = "[" + name + "]"
	}
	if typ.Nullable {
		name += "?"
	}
	return name
}

func (a *Analyzer) checkRangeExpr(e *ast.RangeExpr, scope *Scope) *ResolvedType {
	start := a.checkExpr(e.Start, scope)
	end := a.checkExpr(e.End, scope)
	intType := a.types["Int"]
	if start != nil && !isTypeAssignable(intType, start) {
		a.addError(e.Start.GetPos(), "range start must be Int, got '%s' / range 起点必须是 Int，得到 '%s'", formatResolvedType(start), formatResolvedType(start))
	}
	if end != nil && !isTypeAssignable(intType, end) {
		a.addError(e.End.GetPos(), "range end must be Int, got '%s' / range 终点必须是 Int，得到 '%s'", formatResolvedType(end), formatResolvedType(end))
	}
	return intType.AsList()
}

func (a *Analyzer) checkTransactionExpr(e *ast.TransactionExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	prev := a.inTransaction
	a.inTransaction = true
	a.checkBlock(e.Body, childScope)
	a.inTransaction = prev
	return nil
}

func (a *Analyzer) checkTemplateString(e *ast.TemplateString, scope *Scope) *ResolvedType {
	for _, part := range e.Parts {
		a.checkExpr(part, scope)
	}
	return &ResolvedType{Kind: TypeString, Name: "String"}
}

func (a *Analyzer) checkLiteralExpr(e *ast.Literal) *ResolvedType {
	return a.literalType(e)
}

func (a *Analyzer) checkIdentExpr(e *ast.Ident, scope *Scope) *ResolvedType {
	// check if it's a type name
	if typ, ok := a.types[e.Name]; ok {
		return typ
	}
	// check local scope (includes parent scopes)
	resolved := scope.ResolvedTypeOf(e.Name)
	if resolved != nil {
		// mark variable as used
		if sym := scope.Lookup(e.Name); sym != nil {
			sym.Used = true
		}
		return resolved
	}
	sym := scope.Lookup(e.Name)
	if sym != nil {
		sym.Used = true
		return sym.Type
	}
	// don't error on built-in operations used as expressions
	if isBuiltinOp(e.Name) {
		return nil
	}
	a.addErrorWithSuggestion(e.Pos, e.Name, "undefined: '%s' / 未定义: '%s'", e.Name, e.Name)
	return nil
}

func (a *Analyzer) checkMemberExpr(e *ast.MemberExpr, scope *Scope) *ResolvedType {
	objType := a.checkExpr(e.Object, scope)
	if objType == nil {
		return nil
	}

	if e.SafeCall && !objType.Nullable {
		a.addWarning(e.Pos, "unnecessary safe call (?.) on non-null type '%s' / 对非空类型 '%s' 使用了不必要的安全调用 (?.)", objType.Name, objType.Name)
	}

	a.warnStringContains(e, objType)

	// Duration properties: Int.days, Int.hours, Int.minutes, Int.seconds, Int.milliseconds
	// Only Int, not Float — duration multiplier must be integer
	if objType.Kind == TypeInt {
		switch e.Field {
		case "days", "hours", "minutes", "seconds", "milliseconds":
			return a.types["Duration"]
		}
	}

	if result, ok := a.resolveMemberMethod(e, objType); ok {
		return result
	}

	return a.resolveFieldAccess(e, objType)
}

// warnStringContains warns about .contains() on String (non-list) without @search.
func (a *Analyzer) warnStringContains(e *ast.MemberExpr, objType *ResolvedType) {
	if e.Field == "contains" && objType.Kind == TypeString && !objType.IsList {
		if !a.hasSearchDirective(e.Object) {
			a.addWarning(e.Pos, "'contains' generates LIKE '%%%%...%%%%' which causes full table scan, consider @search for full-text index / 'contains' 会生成 LIKE 模糊查询导致全表扫描，建议使用 @search 全文索引")
		}
	}
}

// resolveMemberMethod attempts to resolve the member access as a built-in method call.
// Returns (result, true) if resolved, (nil, false) if it should fall through to field access.
func (a *Analyzer) resolveMemberMethod(e *ast.MemberExpr, objType *ResolvedType) (*ResolvedType, bool) {
	// debug chain methods — valid on all types, return self
	if isDebugMethod(e.Field) {
		return objType, true
	}
	// query builder chain methods — Model.where(...).sum(...) etc.
	if objType.Kind == TypeModel && isQueryMethod(e.Field) {
		return a.resolveQueryMethod(e.Field, objType), true
	}
	if objType.Kind == TypeQueryBuilder && isQueryMethod(e.Field) {
		return a.resolveQueryMethod(e.Field, objType.ModelType), true
	}
	// collection methods on list types
	if objType.IsList && isCollectionMethod(e.Field) {
		return a.resolveCollectionMethod(e.Field, objType), true
	}
	// .let scope function — works on any type
	if e.Field == "let" {
		return nil, true
	}
	return nil, false
}

// resolveFieldAccess resolves a field or enum value access on a type.
func (a *Analyzer) resolveFieldAccess(e *ast.MemberExpr, objType *ResolvedType) *ResolvedType {
	field := objType.LookupField(e.Field)
	if field == nil {
		if objType.Kind == TypeEnum {
			for _, v := range objType.EnumValues {
				if v == e.Field {
					return objType
				}
			}
		}
		a.addFieldError(e.Pos, objType, e.Field)
		return nil
	}

	// Cross-module field visibility: only extend-declared fields are accessible
	a.checkExtendFieldVisibility(e, objType)

	// method fields require () to call
	if field.IsMethod && !a.inCallFunc {
		a.addError(e.Pos, "'%s' is a method, use %s() / '%s' 是方法，请使用 %s()", e.Field, e.Field, e.Field, e.Field)
		return field.Type
	}

	result := field.Type
	if result != nil && e.SafeCall {
		result = result.AsNullable()
	}
	return result
}

// checkExtendFieldVisibility checks that field access on cross-module model types
// only accesses fields declared in the extend block.
func (a *Analyzer) checkExtendFieldVisibility(e *ast.MemberExpr, objType *ResolvedType) {
	if a.moduleMap == nil || a.currentFile == nil || objType.Kind != TypeModel {
		return
	}
	ownerMod := a.typeOwners.ownerOf(objType.Name)
	if ownerMod == "" {
		return
	}
	mi := a.moduleMap[a.currentFile.Name]
	if mi == nil || ownerMod == mi.Name || a.isCommonModule(ownerMod) || mi.IsCommon {
		return
	}
	// Cross-module model: check if field is declared in extend
	fields := a.extendFieldVisible[mi.Name]
	if fields == nil || fields[objType.Name] == nil || !fields[objType.Name][e.Field] {
		a.addError(e.Pos,
			"field '%s' not declared in extend %s — add it to your extend block / 字段 '%s' 未在 extend %s 中声明",
			e.Field, objType.Name, e.Field, objType.Name)
	}
}

func (a *Analyzer) checkCallExpr(e *ast.CallExpr, scope *Scope) *ResolvedType {
	prev := a.inCallFunc
	a.inCallFunc = true
	a.checkExpr(e.Func, scope)
	a.inCallFunc = prev

	// check CRUD inside lambda (function-style and chain-style)
	if a.inLambda && isCRUDCall(e) {
		a.addError(e.Pos, "database query inside collection lambda is forbidden, use batch query instead / 集合 lambda 内禁止数据库查询，请使用批量查询")
	}

	// Built-in function return types: now() → DateTime, today() → DateTime
	if ident, ok := e.Func.(*ast.Ident); ok {
		switch ident.Name {
		case "now", "today":
			return a.types["DateTime"]
		}
	}

	// transaction { ... } — parsed as CallExpr(Ident("transaction"), [LambdaExpr])
	// Set inTransaction for the lambda body; also suppress inLambda since
	// transaction blocks are NOT collection lambdas.
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "transaction" {
		return a.checkTransactionCall(e, scope)
	}

	// my.load(field, ...) — field names are not variables, skip checking args
	if a.isMyMethodCall(e) {
		return nil
	}

	// Cross-module model restriction: only load() allowed, not find/where/create etc.
	a.checkCrossModuleCRUD(e)

	callScope := a.injectCRUDScope(e, scope)
	isCRUD := isCRUDIdent(e)

	// orderBy(field.desc) — sort expressions are not normal expressions, skip checking
	if isOrderByMethod(e) {
		return a.inferCallReturnType(e)
	}

	argTypes := make([]*ResolvedType, len(e.Args))
	for i, arg := range e.Args {
		// skip order/select/include/distinct — these use query modifier syntax (field.desc)
		if isCRUD && isQueryModifierArg(arg.Name) {
			continue
		}
		argTypes[i] = a.checkExpr(arg.Value, callScope)
		// Named args in CRUD create: also mark variable usage in outer scope
		// (callScope may shadow variables with model fields)
		if isCRUD && arg.Name != "" {
			if ident, ok := arg.Value.(*ast.Ident); ok {
				if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable {
					sym.Used = true
				}
			}
		}
	}
	a.checkLoadArguments(e, argTypes)

	// 1.3: check create required fields (function-style and chain-style)
	if ident, ok := e.Func.(*ast.Ident); ok && ident.Name == "create" {
		a.checkCreateRequiredFields(e)
	}
	if method, _ := chainCRUDInfo(e); method == "create" {
		a.checkChainCreateRequiredFields(e)
	}

	return a.inferCallReturnType(e)
}

// checkTransactionCall handles transaction { ... } which is parsed as
// CallExpr(Ident("transaction"), [LambdaExpr]). It sets inTransaction
// and suppresses inLambda for the body.
func (a *Analyzer) checkTransactionCall(e *ast.CallExpr, scope *Scope) *ResolvedType {
	prevTx := a.inTransaction
	prevLambda := a.inLambda
	a.inTransaction = true
	a.inLambda = false
	for _, arg := range e.Args {
		// If the arg is a LambdaExpr, check its body directly without
		// entering checkLambdaExpr (which would set inLambda=true).
		if lambda, ok := arg.Value.(*ast.LambdaExpr); ok {
			childScope := scope.Child()
			childScope.Define(&Symbol{
				Name: "it",
				Kind: SymVariable,
			})
			a.checkBlock(lambda.Body, childScope)
		} else {
			a.checkExpr(arg.Value, scope)
		}
	}
	a.inTransaction = prevTx
	a.inLambda = prevLambda
	return nil
}

// isMyMethodCall returns true if the call is my.load(...) or similar my.xxx() builtin.
func (a *Analyzer) isMyMethodCall(e *ast.CallExpr) bool {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "my"
}

// injectCRUDScope creates a child scope with model fields injected for CRUD operations.
// Handles both function-style find(User, where: ...) and chain-style User.find(where: ...).
func (a *Analyzer) injectCRUDScope(e *ast.CallExpr, scope *Scope) *Scope {
	var modelType *ResolvedType

	// function-style: find(User, where: ...)
	if ident, ok := e.Func.(*ast.Ident); ok && isCRUDOp(ident.Name) && len(e.Args) > 0 {
		if modelIdent, ok := e.Args[0].Value.(*ast.Ident); ok {
			modelType = a.types[modelIdent.Name]
		}
	}

	// chain-style: User.find(where: ...), User.where(email == ...)
	if _, modelName := chainCRUDInfo(e); modelName != "" {
		modelType = a.types[modelName]
	}

	// chain on query builder variable: orders.sum(total) where orders is TypeQueryBuilder
	if modelType == nil {
		modelType = a.resolveChainModelType(e, scope)
	}

	if modelType == nil {
		return scope
	}

	callScope := scope.Child()
	a.defineFieldsInScope(callScope, modelType)
	for _, parent := range modelType.Parents {
		a.defineFieldsInScope(callScope, parent)
	}
	// inject 'it' as a reference to the model type for disambiguation
	callScope.Define(&Symbol{
		Name: "it",
		Kind: SymVariable,
		Type: modelType,
	})
	// warn about ambiguous identifiers: field name shadows an outer scope symbol
	a.warnAmbiguousCRUDFields(e, callScope, scope)
	return callScope
}

// warnAmbiguousCRUDFields warns when a CRUD-injected field name shadows a symbol from the outer scope.
// Only checks "where:" args — other named args (id:, title:, etc.) have clear semantics.
func (a *Analyzer) warnAmbiguousCRUDFields(e *ast.CallExpr, callScope *Scope, outerScope *Scope) {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	}
}

// collectAmbiguousIdents walks an expression tree and warns about identifiers
// that match both a CRUD-injected field and an outer scope symbol.
func (a *Analyzer) collectAmbiguousIdents(expr ast.Expr, callScope *Scope, outerScope *Scope) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Ident:
		// bare ident is not ambiguous on its own — only same-name comparison (x == x) is.
		// No executable code here; Go coverage considers this switch branch uncoverable.
	case *ast.BinaryExpr:
		a.checkBinaryAmbiguity(e, callScope, outerScope)
	case *ast.UnaryExpr:
		a.collectAmbiguousIdents(e.Value, callScope, outerScope)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			a.collectAmbiguousIdents(arg.Value, callScope, outerScope)
		}
	case *ast.MemberExpr:
		a.collectAmbiguousIdents(e.Object, callScope, outerScope)
	}
}

func (a *Analyzer) checkBinaryAmbiguity(e *ast.BinaryExpr, callScope *Scope, outerScope *Scope) {
	if itMemberField(e.Left) != "" || itMemberField(e.Right) != "" {
		return
	}
	leftIdent, leftOk := e.Left.(*ast.Ident)
	rightIdent, rightOk := e.Right.(*ast.Ident)
	if leftOk && rightOk && leftIdent.Name == rightIdent.Name {
		fieldSym := callScope.LookupLocal(leftIdent.Name)
		outerSym := outerScope.Lookup(leftIdent.Name)
		if fieldSym != nil && fieldSym.Kind == SymField && outerSym != nil {
			a.addWarning(leftIdent.Pos,
				"ambiguous: '%s' == '%s' — both refer to same name, use 'it.%s' for field / 歧义：'%s' == '%s'，请用 'it.%s' 指定字段",
				leftIdent.Name, rightIdent.Name, leftIdent.Name, leftIdent.Name, rightIdent.Name, leftIdent.Name)
		}
		return
	}
	a.collectAmbiguousIdents(e.Left, callScope, outerScope)
	a.collectAmbiguousIdents(e.Right, callScope, outerScope)
}

// itMemberField returns the field name if expr is `it.fieldName`, empty string otherwise.
func itMemberField(expr ast.Expr) string {
	m, ok := expr.(*ast.MemberExpr)
	if !ok {
		return ""
	}
	ident, ok := m.Object.(*ast.Ident)
	if !ok || ident.Name != "it" {
		return ""
	}
	return m.Field
}

// defineFieldsInScope defines all fields from a type into the given scope.
func (a *Analyzer) defineFieldsInScope(scope *Scope, typ *ResolvedType) {
	for fieldName, field := range typ.Fields {
		scope.Define(&Symbol{
			Name: fieldName,
			Kind: SymField,
			Type: field.Type,
		})
	}
}

// inferCallReturnType infers the return type of a call expression.
func (a *Analyzer) inferCallReturnType(e *ast.CallExpr) *ResolvedType {
	// function-style CRUD: find(User, ...)
	if ident, ok := e.Func.(*ast.Ident); ok {
		if isCRUDOp(ident.Name) && len(e.Args) > 0 {
			return a.inferCRUDReturnType(e, ident.Name)
		}
		sym := a.scope.Lookup(ident.Name)
		if sym != nil {
			return sym.Type
		}
		return nil
	}
	// chain-style CRUD: User.find(...)
	if method, modelName := chainCRUDInfo(e); method != "" {
		return a.inferChainCRUDReturnType(e, method, modelName)
	}
	return nil
}

// inferCRUDReturnType infers the return type for CRUD operations.
func (a *Analyzer) inferCRUDReturnType(e *ast.CallExpr, opName string) *ResolvedType {
	// aggregate/groupBy/raw/paginate have dynamic return types
	if isDynamicReturnCRUD(opName) {
		return nil
	}
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return nil
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok {
		return nil
	}
	switch opName {
	case "find":
		return a.inferFindReturnType(e, modelType)
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany", "count":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	}
	// unreachable: all known CRUD ops are handled above or filtered by isDynamicReturnCRUD;
	// kept as defensive fallback.
	return modelType
}

// isDynamicReturnCRUD returns true for CRUD ops with dynamic return types.
func isDynamicReturnCRUD(opName string) bool {
	switch opName {
	case "aggregate", "groupBy", "raw", "paginate":
		return true
	}
	return false
}

// inferFindReturnType returns list type for where queries, non-nullable for id queries
// (find auto-throws NotFound when record is missing; use first() for nullable).
func (a *Analyzer) inferFindReturnType(e *ast.CallExpr, modelType *ResolvedType) *ResolvedType {
	for _, arg := range e.Args {
		if arg.Name == "where" {
			return modelType.AsList()
		}
	}
	return modelType
}

// inferLoadReturnType infers the return type for Model.load(...).
// No named args → PK load → nullable model (single result)
// Named args → FK/multi-condition load → list of models
func (a *Analyzer) inferLoadReturnType(e *ast.CallExpr, modelType *ResolvedType) *ResolvedType {
	hasNamedArgs := false
	for _, arg := range e.Args {
		if arg.Name != "" {
			hasNamedArgs = true
			break
		}
	}
	if hasNamedArgs {
		return modelType.AsList() // FK load returns list
	}
	return modelType // PK load returns single (nullable)
}

func (a *Analyzer) checkLoadArguments(e *ast.CallExpr, argTypes []*ResolvedType) {
	method, modelName := chainCRUDInfo(e)
	if method != "load" {
		return
	}
	modelType := a.types[modelName]
	if modelType == nil {
		return
	}
	if len(e.Args) == 0 {
		a.addError(e.Pos, "%s.load requires one primary key or named lookup fields / %s.load 需要主键或命名查询字段", modelName, modelName)
		return
	}
	namedCount := 0
	for _, arg := range e.Args {
		if arg.Name != "" {
			namedCount++
		}
	}
	if namedCount == 0 {
		a.checkPrimaryKeyLoadArguments(e, modelType, argTypes)
		return
	}
	if namedCount != len(e.Args) {
		a.addError(e.Pos, "%s.load cannot mix positional and named arguments / %s.load 不能混用位置参数和命名参数", modelName, modelName)
		return
	}
	a.checkNamedLoadArguments(e, modelType, argTypes)
}

func (a *Analyzer) checkPrimaryKeyLoadArguments(e *ast.CallExpr, modelType *ResolvedType, argTypes []*ResolvedType) {
	if len(e.Args) != 1 {
		a.addError(e.Pos, "%s.load primary-key form expects exactly one argument / %s.load 主键形式只接受一个参数", modelType.Name, modelType.Name)
		return
	}
	idField := resolvedPrimaryKeyField(modelType)
	if idField == nil || idField.Type == nil || argTypes[0] == nil {
		return
	}
	if !isTypeAssignable(idField.Type, argTypes[0]) {
		a.addError(e.Args[0].Value.GetPos(), "load key expects '%s', got '%s' / load 主键需要 '%s'，得到 '%s'",
			formatResolvedType(idField.Type), formatResolvedType(argTypes[0]), formatResolvedType(idField.Type), formatResolvedType(argTypes[0]))
	}
}

func resolvedPrimaryKeyField(modelType *ResolvedType) *FieldInfo {
	if modelType == nil {
		return nil
	}
	for _, field := range modelType.Fields {
		for _, directive := range field.Directives {
			if directive == "id" {
				return field
			}
		}
	}
	return modelType.LookupField("id")
}

func (a *Analyzer) checkNamedLoadArguments(e *ast.CallExpr, modelType *ResolvedType, argTypes []*ResolvedType) {
	seen := make(map[string]bool, len(e.Args))
	for i, arg := range e.Args {
		if seen[arg.Name] {
			a.addError(arg.Value.GetPos(), "duplicate load field '%s' / load 字段 '%s' 重复", arg.Name, arg.Name)
			continue
		}
		seen[arg.Name] = true
		field := modelType.LookupField(arg.Name)
		if field == nil || field.Type == nil {
			a.addError(arg.Value.GetPos(), "model '%s' has no load field '%s' / 模型 '%s' 没有 load 字段 '%s'", modelType.Name, arg.Name, modelType.Name, arg.Name)
			continue
		}
		if !isLoadKeyType(field) {
			a.addError(arg.Value.GetPos(), "load field '%s' must be Int, String, or UUID / load 字段 '%s' 必须是 Int、String 或 UUID", arg.Name, arg.Name)
			continue
		}
		if !a.isLoadFieldVisible(modelType.Name, arg.Name) {
			a.addError(arg.Value.GetPos(), "load field '%s' not declared in extend %s / load 字段 '%s' 未在 extend %s 中声明", arg.Name, modelType.Name, arg.Name, modelType.Name)
			continue
		}
		if argTypes[i] != nil && !isTypeAssignable(field.Type, argTypes[i]) {
			a.addError(arg.Value.GetPos(), "load field '%s' expects '%s', got '%s' / load 字段 '%s' 需要 '%s'，得到 '%s'",
				arg.Name, formatResolvedType(field.Type), formatResolvedType(argTypes[i]), arg.Name, formatResolvedType(field.Type), formatResolvedType(argTypes[i]))
		}
	}
}

func isLoadKeyType(field *FieldInfo) bool {
	if field.Nullable || field.Computed || field.Type.Nullable || field.Type.IsList {
		return false
	}
	switch field.Type.Kind {
	case TypeInt, TypeString, TypeUUID:
		return true
	default:
		return false
	}
}

func (a *Analyzer) isLoadFieldVisible(modelName, fieldName string) bool {
	if a.moduleMap == nil || a.currentFile == nil {
		return true
	}
	ownerModule := a.typeOwners.ownerOf(modelName)
	currentModule := a.moduleMap[a.currentFile.Name]
	if currentModule == nil || ownerModule == "" || ownerModule == currentModule.Name || a.isCommonModule(ownerModule) || currentModule.IsCommon {
		return true
	}
	return a.extendFieldVisible[currentModule.Name] != nil && a.extendFieldVisible[currentModule.Name][modelName] != nil &&
		a.extendFieldVisible[currentModule.Name][modelName][fieldName]
}

// inferChainCRUDReturnType infers the return type for chain-style CRUD: Model.find(...), Model.create(...), etc.
func (a *Analyzer) inferChainCRUDReturnType(e *ast.CallExpr, method string, modelName string) *ResolvedType {
	if isDynamicReturnCRUD(method) {
		return nil
	}
	modelType, ok := a.types[modelName]
	if !ok {
		return nil
	}
	switch method {
	case "find":
		return a.inferFindReturnType(e, modelType)
	case "load":
		return a.inferLoadReturnType(e, modelType)
	case "findFirst":
		return modelType.AsNullable()
	case "findMany", "createMany":
		return modelType.AsList()
	case "create", "update", "upsert", "delete", "save":
		return modelType
	case "deleteMany", "updateMany", "count":
		return a.types["Int"]
	case "exists":
		return a.types["Boolean"]
	}
	// unreachable: all known CRUD ops are handled above or filtered by isDynamicReturnCRUD;
	// kept as defensive fallback.
	return modelType
}

// checkChainCreateRequiredFields checks required fields for chain-style User.create(...).
func (a *Analyzer) checkChainCreateRequiredFields(e *ast.CallExpr) {
	_, modelName := chainCRUDInfo(e)
	modelType, ok := a.types[modelName]
	if !ok || modelType.Fields == nil {
		return
	}
	provided := collectProvidedFields(e.Args)
	for _, field := range modelType.Fields {
		if isAutoManagedField(field) || provided[field.Name] {
			continue
		}
		a.addWarning(e.Pos, "missing required field '%s' in %s.create(...) / %s.create(...) 缺少必填字段 '%s'",
			field.Name, modelName, modelName, field.Name)
	}
}

// checkCreateRequiredFields checks that all required fields are provided in create().
func (a *Analyzer) checkCreateRequiredFields(e *ast.CallExpr) {
	if len(e.Args) == 0 {
		return
	}
	modelIdent, ok := e.Args[0].Value.(*ast.Ident)
	if !ok {
		return
	}
	modelType, ok := a.types[modelIdent.Name]
	if !ok || modelType.Fields == nil {
		return
	}
	provided := collectProvidedFields(e.Args[1:])
	for _, field := range modelType.Fields {
		if isAutoManagedField(field) || provided[field.Name] {
			continue
		}
		a.addWarning(e.Pos, "missing required field '%s' in create(%s, ...) / create(%s, ...) 缺少必填字段 '%s'",
			field.Name, modelIdent.Name, modelIdent.Name, field.Name)
	}
}

func collectProvidedFields(args []*ast.NamedArg) map[string]bool {
	provided := map[string]bool{}
	for _, arg := range args {
		if arg.Name != "" {
			provided[arg.Name] = true
		}
	}
	return provided
}

// isAutoManagedField returns true for fields that don't need explicit values in create().
func isAutoManagedField(field *FieldInfo) bool {
	if field.Nullable || field.HasDefault || field.Computed || field.IsMethod {
		return true
	}
	if hasDirective(field.Directives, "auto") || hasDirective(field.Directives, "id") {
		return true
	}
	// Relation fields (model type or list of model) are not database columns
	if field.Type != nil && (field.Type.Kind == TypeModel || field.Type.IsList) {
		return true
	}
	return field.Name == "createdAt" || field.Name == "updatedAt" || field.Name == "deletedAt" || field.Name == "id"
}

func hasDirective(directives []string, name string) bool {
	for _, d := range directives {
		if d == name {
			return true
		}
	}
	return false
}

// isCRUDIdent returns true if the call is a CRUD operation (function-style or chain-style).
// isCRUDCall returns true if the call is a CRUD operation (function-style or chain-style).
func isCRUDCall(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok && isDBQueryOp(ident.Name) {
		return true
	}
	if member, ok := e.Func.(*ast.MemberExpr); ok && isDBQueryOp(member.Field) {
		return true
	}
	return false
}

func isCRUDIdent(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok {
		return isCRUDOp(ident.Name)
	}
	// chain-style: Model.find(...), User.create(...)
	if member, ok := e.Func.(*ast.MemberExpr); ok {
		return isCRUDOp(member.Field)
	}
	return false
}

// chainCRUDInfo extracts the CRUD method name and model name from a chain-style call.
// Supports both direct chain (User.find(...)) and query chain (User.where(...)).
// Returns ("", "") if not a chain-style CRUD/query call.
func chainCRUDInfo(e *ast.CallExpr) (method string, modelName string) {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return "", ""
	}
	if !isCRUDOp(member.Field) && !isQueryMethod(member.Field) {
		return "", ""
	}
	// direct: User.where(...)
	if ident, ok := member.Object.(*ast.Ident); ok {
		return member.Field, ident.Name
	}
	return member.Field, ""
}

// checkCrossModuleCRUD restricts cross-module models to load() only.
// e.g., User.find(id) in post module → error; User.load(id) → ok.
func (a *Analyzer) checkCrossModuleCRUD(e *ast.CallExpr) {
	if a.moduleMap == nil || a.currentFile == nil {
		return
	}
	method, modelName := chainCRUDInfo(e)
	if modelName == "" {
		return
	}
	ownerMod := a.typeOwners.ownerOf(modelName)
	if ownerMod == "" {
		return
	}
	mi := a.moduleMap[a.currentFile.Name]
	if mi == nil {
		return
	}
	// Same module or common module — all ops allowed
	if ownerMod == mi.Name || a.isCommonModule(ownerMod) || mi.IsCommon {
		return
	}
	// Cross-module: only load is allowed
	if method != "load" {
		a.addError(e.Pos,
			"cross-module model '%s' can only use 'load', not '%s' — use %s.load(id) / 跨模块 model '%s' 只能用 load，不能用 '%s'",
			modelName, method, modelName, modelName, method)
	}
}

// resolveChainModelType resolves the underlying model type from a chain call on a variable.
// e.g., orders.sum(total) where orders: TypeQueryBuilder → returns the model type.
func (a *Analyzer) resolveChainModelType(e *ast.CallExpr, scope *Scope) *ResolvedType {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return nil
	}
	return a.resolveModelFromExpr(member.Object, scope)
}

// resolveModelFromExpr extracts the underlying model type from an expression.
// Handles: Ident (variable), MemberExpr (Model.where), CallExpr (Order.where(...).sum).
func (a *Analyzer) resolveModelFromExpr(expr ast.Expr, scope *Scope) *ResolvedType {
	switch obj := expr.(type) {
	case *ast.Ident:
		// variable: orders.sum(total) — look up variable type
		sym := scope.Lookup(obj.Name)
		if sym != nil && sym.Type != nil {
			if sym.Type.Kind == TypeQueryBuilder && sym.Type.ModelType != nil {
				return sym.Type.ModelType
			}
			if sym.Type.Kind == TypeModel {
				return sym.Type
			}
		}
		// model name directly: Order.sum(total)
		if t := a.types[obj.Name]; t != nil && t.Kind == TypeModel {
			return t
		}
	case *ast.CallExpr:
		// chained call: Order.where(...).sum(total) — recurse into the call's func
		if m, ok := obj.Func.(*ast.MemberExpr); ok {
			return a.resolveModelFromExpr(m.Object, scope)
		}
	}
	return nil
}

// isQueryModifierArg returns true for CRUD named args that use query modifier syntax.
// isOrderByMethod returns true if the call is an orderBy chain method
// whose args are sort expressions (field.desc, field.asc), not normal expressions.
func isOrderByMethod(e *ast.CallExpr) bool {
	member, ok := e.Func.(*ast.MemberExpr)
	if !ok {
		return false
	}
	return member.Field == "orderBy"
}

func isQueryModifierArg(name string) bool {
	switch name {
	case "order", "select", "include", "distinct",
		"by", "sum", "avg", "min", "max",
		"having", "page", "pageSize", "cursor", "limit",
		"params":
		return true
	}
	return false
}

func isCRUDOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "count", "exists",
		"aggregate", "groupBy", "raw", "paginate",
		"save", "load":
		return true
	}
	return false
}

// isDBQueryOp returns true for operations that trigger a database query.
// Used to forbid DB queries inside collection lambdas.
// Excludes aggregate methods (sum, avg, count, etc.) which are valid in groupBy lambdas.
func isDBQueryOp(name string) bool {
	switch name {
	case "find", "findFirst", "findMany",
		"create", "createMany",
		"update", "updateMany",
		"delete", "deleteMany",
		"upsert", "raw", "load":
		return true
	}
	return false
}

// isAggregateFieldRefCall returns true for aggregate/groupBy calls whose args
// are field name references (not variables) and should not be expression-checked.
func isAggregateFieldRefCall(e *ast.CallExpr) bool {
	if ident, ok := e.Func.(*ast.Ident); ok {
		return ident.Name == "aggregate" || ident.Name == "groupBy"
	}
	if member, ok := e.Func.(*ast.MemberExpr); ok {
		return member.Field == "aggregate" || member.Field == "groupBy"
	}
	return false
}

func (a *Analyzer) checkBinaryExpr(e *ast.BinaryExpr, scope *Scope) *ResolvedType {
	left := a.checkExpr(e.Left, scope)
	right := a.checkExpr(e.Right, scope)
	return a.checkBinaryOp(e.Op, left, right, e.Pos)
}

func (a *Analyzer) checkUnaryExpr(e *ast.UnaryExpr, scope *Scope) *ResolvedType {
	if e.Op == "?" {
		a.resultOperand++
	}
	inner := a.checkExpr(e.Value, scope)
	if e.Op == "?" {
		a.resultOperand--
	}
	if e.Op == "throw" {
		return nil // throw never returns
	}
	if e.Op == "?" {
		if _, ok := e.Value.(*ast.CallExpr); !ok {
			a.addError(e.Pos, "operator '?' must be applied directly to a Result<T> call / 运算符 '?' 必须直接用于 Result<T> 调用")
		}
		if inner == nil || inner.Kind != TypeGeneric || inner.Name != "Result" || len(inner.TypeArgs) != 1 {
			name := "unknown"
			if inner != nil && inner.Name != "" {
				name = inner.Name
			}
			a.addError(e.Pos, "operator '?' requires Result<T>, got '%s' / 运算符 '?' 需要 Result<T>，得到 '%s'", name, name)
			return &ResolvedType{Kind: TypeUnknown, Name: "Unknown"}
		}
		return cloneResolvedType(inner.TypeArgs[0])
	}
	if e.Op == "!" && inner != nil && inner.Kind != TypeBool {
		a.addError(e.Pos, "operator '!' requires Boolean, got '%s' / 运算符 '!' 需要 Boolean 类型，得到 '%s'", inner.Name, inner.Name)
	}
	return inner
}

func (a *Analyzer) checkElvisExpr(e *ast.ElvisExpr, scope *Scope) *ResolvedType {
	leftType := a.checkExpr(e.Left, scope)
	a.checkExpr(e.Right, scope)
	if leftType == nil {
		return nil
	}
	// after ?:, the left side is narrowed to non-null
	if ident, ok := e.Left.(*ast.Ident); ok {
		scope.Narrow(ident.Name, leftType.AsNonNull())
	}
	return leftType.AsNonNull()
}

func (a *Analyzer) checkWhenExpr(e *ast.WhenExpr, scope *Scope) *ResolvedType {
	var subjectType *ResolvedType
	if e.Subject != nil {
		subjectType = a.checkExpr(e.Subject, scope)
	}
	// check duplicate branches
	seenBranches := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			if seenBranches[b.IsType] {
				a.addError(e.Pos, "duplicate when branch 'is %s' / 重复的 when 分支 'is %s'", b.IsType, b.IsType)
			}
			seenBranches[b.IsType] = true
		}
		if b.Condition != nil {
			a.checkExpr(b.Condition, scope)
		}
		if b.Body != nil {
			// sealed variant narrowing: inject variant fields into subject
			branchScope := scope
			if b.IsType != "" && subjectType != nil && subjectType.Kind == TypeSealed {
				branchScope = a.injectSealedVariantFields(scope, subjectType, b.IsType, e.Subject)
			}
			a.checkExpr(b.Body, branchScope)
		}
	}
	if e.Else != nil {
		a.checkExpr(e.Else, scope)
	}
	// sealed exhaustiveness check
	a.checkWhenExhaustive(e, subjectType)
	return nil
}

// checkWhenExhaustive verifies when exhaustiveness for sealed/enum types and else requirement.
func (a *Analyzer) checkWhenExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	// when without subject (no sealed/enum) — must have else
	if subjectType == nil {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// enum exhaustiveness
	if subjectType.Kind == TypeEnum {
		a.checkEnumExhaustive(e, subjectType)
		return
	}
	if subjectType.Kind != TypeSealed {
		if e.Else == nil {
			a.addError(e.Pos, "when expression must have 'else' branch / when 表达式必须有 'else' 分支")
		}
		return
	}
	// if there's an else branch, exhaustiveness is guaranteed
	if e.Else != nil {
		return
	}
	// collect matched variant names
	matched := map[string]bool{}
	for _, b := range e.Branches {
		if b.IsType != "" {
			matched[b.IsType] = true
		}
	}
	// check for missing variants
	for _, v := range subjectType.Variants {
		if !matched[v.Name] {
			a.addError(e.Pos,
				"when on sealed type '%s' is not exhaustive, missing variant '%s' / when 未穷举密封类型 '%s'，缺少变体 '%s'",
				subjectType.Name, v.Name, subjectType.Name, v.Name)
		}
	}
}

// checkEnumExhaustive verifies that when on an enum type covers all values.
func (a *Analyzer) checkEnumExhaustive(e *ast.WhenExpr, subjectType *ResolvedType) {
	if e.Else != nil {
		return
	}
	// collect matched enum values from branch conditions (e.g. Role.ADMIN -> ...)
	matched := map[string]bool{}
	for _, b := range e.Branches {
		a.collectEnumValues(b.Condition, matched)
	}
	for _, v := range subjectType.EnumValues {
		if !matched[v] {
			a.addError(e.Pos,
				"when on enum '%s' is not exhaustive, missing value '%s' / when 未穷举枚举 '%s'，缺少值 '%s'",
				subjectType.Name, v, subjectType.Name, v)
		}
	}
}

func (a *Analyzer) collectEnumValues(expr ast.Expr, matched map[string]bool) {
	if expr == nil {
		return
	}
	if member, ok := expr.(*ast.MemberExpr); ok {
		matched[member.Field] = true
	}
}

func (a *Analyzer) checkLambdaExpr(e *ast.LambdaExpr, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	if len(e.Params) > 0 {
		// named params: { x -> ... } or { a, b -> ... }
		for _, name := range e.Params {
			childScope.Define(&Symbol{Name: name, Kind: SymVariable})
		}
	} else {
		// implicit 'it' parameter
		childScope.Define(&Symbol{Name: "it", Kind: SymVariable})
	}
	prev := a.inLambda
	previousReturn := a.expectedReturn
	a.inLambda = true
	a.expectedReturn = nil
	a.checkBlock(e.Body, childScope)
	a.inLambda = prev
	a.expectedReturn = previousReturn
	return nil
}

func (a *Analyzer) literalType(lit *ast.Literal) *ResolvedType {
	switch lit.Kind {
	case token.Int:
		return a.types["Int"]
	case token.Float:
		return a.types["Float"]
	case token.String:
		return a.types["String"]
	case token.Duration:
		return a.types["Duration"]
	case token.True, token.False:
		return a.types["Boolean"]
	case token.Null:
		return &ResolvedType{Kind: TypeUnknown, Name: "null", Nullable: true}
	}
	return nil
}

func (a *Analyzer) checkBinaryOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	if left == nil || right == nil {
		return nil
	}

	switch op {
	case "+", "-", "*", "/", "%":
		return a.checkArithmeticOp(op, left, right, pos)

	case "==", "!=":
		// 1.1: enum cannot compare with String
		if left.Kind == TypeEnum && right.Kind == TypeString {
			a.addError(pos, "cannot compare enum '%s' with String, use %s.VALUE / 不能用 String 和枚举 '%s' 比较，请使用 %s.VALUE", left.Name, left.Name, left.Name, left.Name)
		}
		if left.Kind == TypeString && right.Kind == TypeEnum {
			a.addError(pos, "cannot compare String with enum '%s', use %s.VALUE / 不能用 String 和枚举 '%s' 比较，请使用 %s.VALUE", right.Name, right.Name, right.Name, right.Name)
		}
		return a.types["Boolean"]

	case ">", ">=", "<", "<=":
		if !left.IsOrderable() {
			a.addError(pos, "operator '%s' requires orderable type, got '%s' / 运算符 '%s' 需要可排序类型，得到 '%s'", op, left.Name, op, left.Name)
		}
		return a.types["Boolean"]

	case "&&", "||":
		return a.types["Boolean"]

	case "in":
		return a.types["Boolean"]

	case "is":
		return a.types["Boolean"]
	}

	return nil
}

func (a *Analyzer) checkArithmeticOp(op string, left, right *ResolvedType, pos token.Position) *ResolvedType {
	// 1.2: nullable cannot be used in arithmetic
	if left.Nullable {
		a.addError(pos, "cannot use '%s' on nullable '%s?', unwrap first with ?: / 不能对可空类型 '%s?' 使用 '%s'，请先用 ?: 解包", op, left.Name, left.Name, op)
	}
	if right.Nullable {
		a.addError(pos, "cannot use '%s' on nullable '%s?', unwrap first with ?: / 不能对可空类型 '%s?' 使用 '%s'，请先用 ?: 解包", op, right.Name, right.Name, op)
	}
	// DateTime +/- Duration → DateTime
	if left.Kind == TypeDateTime && right.Kind == TypeDuration && (op == "+" || op == "-") {
		return a.types["DateTime"]
	}
	// Duration + Duration → Duration
	if left.Kind == TypeDuration && right.Kind == TypeDuration && (op == "+" || op == "-") {
		return a.types["Duration"]
	}
	if !left.IsNumeric() || !right.IsNumeric() {
		if op == "+" && left.Kind == TypeString {
			return left // string concatenation
		}
		a.addError(pos, "operator '%s' requires numeric types, got '%s' and '%s' / 运算符 '%s' 需要数字类型，得到 '%s' 和 '%s'", op, left.Name, right.Name, op, left.Name, right.Name)
	}
	if left.Kind == TypeFloat || right.Kind == TypeFloat {
		return a.types["Float"]
	}
	return a.types["Int"]
}
