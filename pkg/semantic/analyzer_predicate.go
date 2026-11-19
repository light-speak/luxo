package semantic

import (
	"hash/maphash"
	"math/bits"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
)

// Member dispatch excludes lists and admits only model/query-builder receivers before this helper.
// Keep both fallbacks when inconsistent receiver metadata bypasses that dispatch.
func (a *Analyzer) rejectSaveQueryReceiver(member *ast.MemberExpr, receiver *ResolvedType, scope *Scope) bool {
	if receiver.IsList {
		return false
	}
	if receiver.Kind != TypeQueryBuilder {
		if receiver.Kind != TypeModel {
			return false
		}
		name, ok := member.Object.(*ast.Ident)
		if !ok {
			return false
		}
		symbol := scope.Lookup(name.Name)
		if symbol == nil || symbol.Kind != SymModel {
			return false
		}
	}
	a.addError(member.Pos, "save requires a model instance receiver; a model type or query builder has no field values to save / save 要求模型实例；模型类型或查询构建器没有可保存的字段值")
	return true
}

func (a *Analyzer) rejectInstanceQueryReceiver(member *ast.MemberExpr, receiver *ResolvedType, scope *Scope) bool {
	if a.callTarget != member || receiver.Kind != TypeModel || receiver.IsList || !inferred.RequiresModelQueryReceiver(member.Field) {
		return false
	}
	if field := receiver.LookupField(member.Field); field != nil && field.IsMethod {
		return false
	}
	if name, ok := member.Object.(*ast.Ident); ok {
		if symbol := scope.Lookup(name.Name); symbol != nil && symbol.Kind == SymModel {
			return false
		}
	}
	a.addError(member.Pos, "query method '%s' requires a model type or query builder receiver", member.Field)
	return true
}

func isDeclaredFunctionCall(call *ast.CallExpr, scope *Scope) bool {
	name, ok := call.Func.(*ast.Ident)
	if !ok {
		return false
	}
	symbol := scope.Lookup(name.Name)
	return symbol != nil && symbol.Kind == SymFn
}

func (a *Analyzer) checkQueryWhereArguments(call *ast.CallExpr, scope *Scope) {
	arguments, method, model := a.queryWhereArguments(call, scope)
	if model == nil {
		return
	}
	if method == "updateMany" {
		a.checkBulkUpdateArguments(call, arguments, model)
		return
	}
	_, index, message := inferred.QueryWhere(arguments)
	if message == "" {
		return
	}
	position := call.Pos
	if argument := arguments[index]; argument != nil && argument.Value != nil {
		position = argument.Value.GetPos()
	}
	a.addError(position, "%s%s", method, message)
}

func (a *Analyzer) queryWhereArguments(call *ast.CallExpr, scope *Scope) ([]*ast.NamedArg, string, *ResolvedType) {
	switch target := call.Func.(type) {
	case *ast.Ident:
		if inferred.ModelTerminalType(target.Name) == "" || scope.Lookup(target.Name) != nil {
			return nil, "", nil
		}
		if len(call.Args) > 0 && call.Args[0] != nil && call.Args[0].Name == "" {
			if model, ok := call.Args[0].Value.(*ast.Ident); ok {
				if symbol := scope.Lookup(model.Name); symbol != nil && symbol.Kind == SymModel {
					return call.Args[1:], target.Name, symbol.Type
				}
			}
		}
		a.addError(call.Pos, "%s requires a positional model argument", target.Name)
	case *ast.MemberExpr:
		if inferred.ModelTerminalType(target.Field) == "" || target.DeclaredField || target.Object.IsListType() {
			return nil, "", nil
		}
		if model := a.resolveChainModelType(call, scope); model != nil {
			return call.Args, target.Field, model
		}
	}
	return nil, "", nil
}

func (a *Analyzer) checkBulkUpdateArguments(call *ast.CallExpr, arguments []*ast.NamedArg, model *ResolvedType) {
	a.checkDuplicateBulkSetters(arguments)
	for _, argument := range arguments {
		var field inferred.MutationField
		if argument != nil && argument.Name != "where" {
			if resolved := model.LookupField(argument.Name); resolved != nil && resolved.Type != nil {
				field = inferred.MutationField{TypeName: resolved.Type.Name, List: resolved.Type.IsList,
					Nullable: resolved.Type.Nullable, Computed: resolved.Computed || resolved.IsMethod,
					Immutable: hasDirective(resolved.Directives, "immutable"), Relation: resolved.Type.Kind == TypeModel}
			}
		}
		_, message := inferred.MutationArgument(argument, field)
		if message != "" {
			position := call.Pos
			if argument != nil && argument.Value != nil {
				position = argument.Value.GetPos()
			}
			a.addError(position, "updateMany%s", message)
		}
	}
}

var bulkSetterHashSeed = maphash.MakeSeed()

// A bulk call may combine where predicates, but each named setter is unique.
func (a *Analyzer) checkDuplicateBulkSetters(arguments []*ast.NamedArg) {
	if len(arguments) < 2 {
		return
	}
	// Keep common calls on the stack and bound probing to a half-full table.
	var local [256]int
	slots := local[:]
	if len(arguments) > len(local)/2 {
		slots = make([]int, 1<<(bits.Len(uint(len(arguments)-1))+1))
	}
	mask := uint64(len(slots) - 1)
	for position, argument := range arguments {
		if argument == nil || argument.Name == "" || argument.Name == "where" || argument.Value == nil {
			continue
		}
		index := maphash.String(bulkSetterHashSeed, argument.Name) & mask
		for slots[index] != 0 {
			previous := arguments[slots[index]-1]
			if argument.Name == previous.Name {
				a.addError(argument.Value.GetPos(), "duplicate argument '%s' / 参数 '%s' 重复", argument.Name, argument.Name)
				break
			}
			index = (index + 1) & mask
		}
		slots[index] = position + 1
	}
}

func (a *Analyzer) checkOrderByArguments(call *ast.CallExpr, resolved *ResolvedType) bool {
	if !isOrderByMethod(call) || resolved == nil || resolved.Kind != TypeQueryBuilder {
		return false
	}
	a.recordCacheModelRead(call, resolved.ModelType)
	for _, argument := range call.Args {
		a.checkQueryOrder(argument.Value, resolved.ModelType)
	}
	return true
}

func (a *Analyzer) checkCRUDOrderArgument(argument *ast.NamedArg, fields, outer *Scope) {
	if argument.Name == "order" && fields != outer {
		a.checkQueryOrder(argument.Value, fields.LookupLocal("it").Type)
	}
}

func (a *Analyzer) checkScopeOrders(file *ast.File) {
	for _, declaration := range file.Models {
		for _, scope := range declaration.Scopes {
			a.checkScopeOrderExpression(scope.Expr, a.types[declaration.Name])
		}
	}
}

// Scope query modifiers form a single outer chain. Their values and predicates
// are separate expressions, not additional modifiers of the scope's model.
func (a *Analyzer) checkScopeOrderExpression(expression ast.Expr, model *ResolvedType) {
	for expression != nil {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			return
		}
		var method string
		expression = nil
		switch target := call.Func.(type) {
		case *ast.Ident:
			method = target.Name
		case *ast.MemberExpr:
			method, expression = target.Field, target.Object
		}
		if method == "orderBy" {
			for _, argument := range call.Args {
				a.checkQueryOrder(argument.Value, model)
			}
		}
	}
}

func (a *Analyzer) checkQueryOrder(expression ast.Expr, model *ResolvedType) {
	if model == nil || model.Kind != TypeModel {
		return
	}
	name, _, alternate := inferred.ParseOrderExpression(expression)
	if name != "" {
		field := model.LookupField(name)
		if alternate != "" && (field == nil || !storedOrderField(field)) {
			name = alternate
			field = model.LookupField(name)
		}
		if field != nil {
			if storedOrderField(field) {
				return
			}
		} else if a.managedOrderField(model.Name, name) {
			return
		}
	}
	a.addError(expression.GetPos(), "order requires a stored field of model '%s' with optional .asc/.desc / 排序需要当前模型的持久字段，可选 .asc/.desc", model.Name)
}

func storedOrderField(field *FieldInfo) bool {
	if field.Computed || field.IsMethod || field.Type == nil {
		return false
	}
	switch field.Type.Kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeBytes, TypeJSON, TypeEnum:
		return true
	default:
		return false
	}
}

// Managed timestamps have database columns without declared wire fields. Only
// these missing names need a declaration lookup; normal fields use their type.
func (a *Analyzer) managedOrderField(model, name string) bool {
	if name != "createdAt" && name != "updatedAt" && name != "deletedAt" {
		return false
	}
	for _, file := range a.files {
		for _, declaration := range file.Models {
			if declaration.Name == model {
				if name == "deletedAt" {
					return hasModelDirective(declaration.Directives, "soft")
				}
				return !hasModelDirective(declaration.Directives, "noTime")
			}
		}
	}
	return false
}

func (a *Analyzer) checkQueryArgument(call *ast.CallExpr, argument *ast.NamedArg, scope, outer *Scope, callType *ResolvedType) *ResolvedType {
	member, ok := call.Func.(*ast.MemberExpr)
	if ok && member.Field == "where" && callType != nil && callType.Kind == TypeQueryBuilder {
		if argument.Name != "" {
			return a.checkExpr(argument.Value, outer)
		}
		return a.checkWhereArgument(argument.Value, scope, outer, callType.ModelType)
	}
	if argument.Name == "where" && isCRUDIdent(call) && scope != outer {
		if row := scope.LookupLocal("it"); row != nil && row.Type != nil && row.Type.Kind == TypeModel {
			return a.checkWhereArgument(argument.Value, scope, outer, row.Type)
		}
	}
	return a.checkExpr(argument.Value, scope)
}

func (a *Analyzer) checkWhereArgument(expression ast.Expr, fields, outer *Scope, model *ResolvedType) *ResolvedType {
	parameter := "it"
	if lambda, ok := expression.(*ast.LambdaExpr); ok {
		expression, parameter = lambda.SingleExpression()
		if expression == nil {
			a.addError(lambda.Pos, "where requires a single predicate expression / where 需要单个条件表达式")
			return nil
		}
		// The injected query scope already owns the implicit row binding.
		// Reuse it without changing the caller's symbols or duplicating a map.
		row := fields.LookupLocal(parameter)
		if fields == outer || row == nil || row.Kind != SymVariable || row.Type != model {
			fields = fields.Child()
			fields.Define(&Symbol{Name: parameter, Kind: SymVariable, Type: model})
		}
		a.rejectPredicateQueries(lambda.Body, fields)
	}
	values := queryValueScope{parent: outer, parameter: parameter, model: model}
	previous := a.inLambda
	a.inLambda = true
	result := a.checkWhereExpression(expression, fields, &values)
	a.inLambda = previous
	a.checkWhereMembership(expression, parameter, model)
	if result != nil && !result.IsNonNullBoolean() {
		a.addError(expression.GetPos(), "where predicate must return non-null Boolean / where 条件必须返回非空 Boolean")
	}
	return result
}

// Simple constants and other identifiers cannot reference the query row. Only
// complex values need the guarded child; never mutate caller-owned bindings.
type queryValueScope struct {
	parent    *Scope
	parameter string
	model     *ResolvedType
	guarded   *Scope
}

func (s *queryValueScope) forExpr(expr ast.Expr) *Scope {
	switch value := expr.(type) {
	case *ast.Literal:
		return s.parent
	case *ast.Ident:
		if value.Name != s.parameter {
			return s.parent
		}
	}
	if s.guarded == nil {
		referencesRow := false
		ast.WalkExpr(expr, func(expression ast.Expr) {
			if identifier, ok := expression.(*ast.Ident); ok && identifier.Name == s.parameter {
				referencesRow = true
			}
		})
		if !referencesRow {
			return s.parent
		}
		s.guarded = s.parent.Child()
		s.guarded.Define(&Symbol{Name: s.parameter, Kind: SymQueryRow, Type: s.model})
	}
	return s.guarded
}

func (a *Analyzer) checkWhereExpression(expression ast.Expr, fields *Scope, values *queryValueScope) *ResolvedType {
	if unary, ok := expression.(*ast.UnaryExpr); ok && unary.Op == "!" {
		resolved := a.checkBooleanNegation(a.checkWhereExpression(unary.Value, fields, values), unary.Pos)
		a.setExprResolvedType(unary, resolved)
		return resolved
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		if result, handled := a.checkWhereStringCall(call, fields, values); handled {
			return result
		}
	}
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok {
		return a.checkWhereValue(expression, fields, values)
	}
	var left, right *ResolvedType
	if binary.Op == "&&" || binary.Op == "||" {
		left = a.checkWhereExpression(binary.Left, fields, values)
		right = a.checkWhereExpression(binary.Right, fields, values)
	} else {
		left = a.checkExpr(binary.Left, fields)
		right = a.checkExpr(binary.Right, values.forExpr(binary.Right))
	}
	resolved := a.checkBinaryOp(binary.Op, left, right, binary.Pos)
	if resolved != nil {
		a.setExprResolvedType(expression, resolved)
	}
	return resolved
}

func (a *Analyzer) checkWhereValue(expression ast.Expr, fields *Scope, values *queryValueScope) *ResolvedType {
	if field := membershipField(expression, values.parameter, values.model); field != nil {
		if !groupScalarField(field) {
			a.addError(expression.GetPos(), "predicate requires a stored scalar field / 查询条件需要已存储的标量字段")
		}
		return a.checkExpr(expression, fields)
	}
	return a.checkExpr(expression, values.forExpr(expression))
}

func (a *Analyzer) checkWhereStringCall(call *ast.CallExpr, fields *Scope, values *queryValueScope) (*ResolvedType, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "contains" && member.Field != "startsWith" && member.Field != "endsWith" {
		return nil, false
	}
	field := membershipField(member.Object, values.parameter, values.model)
	if field == nil || !groupScalarField(field) || field.Type.Kind != TypeString {
		a.addError(call.Pos, "string predicate requires a stored String field / 字符串条件需要已存储的 String 字段")
		return nil, true
	}
	// SQL predicates inspect nullable columns; they do not dereference Go values.
	a.checkExpr(member.Object, fields)
	a.warnStringContains(member, field.Type)
	result := a.types["Boolean"]
	a.setExprResolvedType(member, result)
	if len(call.Args) != 1 || call.Args[0].Name != "" {
		a.addError(call.Pos, "string predicate requires one unnamed String argument / 字符串条件需要一个未命名 String 参数")
		return nil, true
	}
	argument := call.Args[0].Value
	actual := a.checkExpr(argument, values.forExpr(argument))
	if actual == nil || actual.Kind != TypeString || actual.IsList || actual.Nullable {
		a.addError(argument.GetPos(), "string predicate requires a non-null String value / 字符串条件需要非空 String 值")
	}
	a.setExprResolvedType(call, result)
	return result, true
}

// Aggregate methods are valid inside group projections but nested database
// queries cannot be used as a row predicate's expression.
func (a *Analyzer) rejectPredicateQueries(body *ast.Block, scope *Scope) {
	ast.WalkExprs(body, func(expression ast.Expr) {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			return
		}
		if member, ok := call.Func.(*ast.MemberExpr); ok && isQueryMethod(member.Field) {
			if a.resolveModelFromExpr(member.Object, scope) != nil {
				a.addError(call.Pos, "database query inside query predicate is forbidden / 查询条件内禁止数据库查询")
			}
		}
	})
}
