package semantic

import (
	"slices"

	"github.com/light-speak/luxo/pkg/ast"
)

func findGroupedCall(call *ast.CallExpr) *ast.CallExpr {
	for call != nil {
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok {
			return nil
		}
		if member.Field == "groupBy" {
			return call
		}
		call, _ = member.Object.(*ast.CallExpr)
	}
	return nil
}

func (a *Analyzer) checkGroupedQuery(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	call.Group = nil
	if ident, ok := call.Func.(*ast.Ident); ok && ident.Name == "groupBy" {
		symbol := scope.Lookup(ident.Name)
		if symbol == nil || symbol.Kind != SymFn && symbol.Kind != SymApi {
			a.addError(call.Pos, "groupBy requires an explicit type projection using Model.groupBy(...).select / groupBy 必须使用模型链和显式 type 投影")
			return nil, true
		}
	}
	group := findGroupedCall(call)
	if group == nil {
		return nil, false
	}
	input := group.Func.(*ast.MemberExpr).Object
	startErrors := len(a.errors)
	model := a.checkExpr(input, scope)
	if model == nil || model.IsList || model.Nullable {
		return nil, false
	}
	if model.Kind == TypeQueryBuilder {
		model = model.ModelType
	}
	if model == nil || model.Kind != TypeModel {
		return nil, false
	}
	if !groupQueryInput(input, model.Name, scope) {
		a.addError(group.Pos, "groupBy requires a model query, not a model value or mutation / groupBy 需要模型查询，不能接在模型值或写入操作后")
		return nil, true
	}
	a.checkCrossModuleCRUD(group)
	a.recordCacheModelRead(group, model)
	if a.inLambda {
		a.addError(call.Pos, "database query inside collection lambda is forbidden / 集合 lambda 内禁止数据库查询")
	}
	projection, conditions := groupedQueryTail(call, group)
	if projection == nil {
		a.addError(call.Pos, "groupBy requires an immediate explicit type projection using select / groupBy 必须紧接显式 type 的 select 投影")
		return nil, true
	}
	plan := &ast.GroupProjection{Input: input, Model: model.Name}
	plan.Keys = a.checkGroupKeys(group, model)
	result := a.checkGroupProjection(projection, model, plan)
	if result == nil {
		return nil, true
	}
	for _, condition := range conditions {
		a.checkGroupHaving(condition, model, plan, scope)
	}
	if len(a.errors) == startErrors {
		call.Group = plan
	}
	return result.AsList(), true
}

func groupQueryInput(expression ast.Expr, model string, scope *Scope) bool {
	for {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			break
		}
		member, ok := call.Func.(*ast.MemberExpr)
		if !ok {
			return false
		}
		switch member.Field {
		case "where", "orderBy", "limit", "offset":
			expression = member.Object
		default:
			return false
		}
	}
	ident, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	if ident.Name == model {
		return true
	}
	symbol := scope.Lookup(ident.Name)
	return symbol != nil && symbol.Type != nil && symbol.Type.Kind == TypeQueryBuilder
}

// A query lambda contains one expression, not executable per-row statements.
func groupLambda(call *ast.CallExpr) (ast.Expr, string) {
	if len(call.Args) != 1 || call.Args[0].Name != "" {
		return nil, ""
	}
	lambda, ok := call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return nil, ""
	}
	return lambda.SingleExpression()
}

func (a *Analyzer) checkGroupKeys(call *ast.CallExpr, model *ResolvedType) []string {
	expression, parameter := groupLambda(call)
	if expression == nil {
		a.addError(call.Pos, "groupBy requires at least one grouping key in a field lambda / groupBy 必须通过字段 lambda 指定分组键")
		return nil
	}
	items := []ast.Expr{expression}
	if list, ok := expression.(*ast.ListExpr); ok {
		items = list.Items
	}
	if len(items) == 0 {
		a.addError(call.Pos, "groupBy requires at least one grouping key / groupBy 至少需要一个分组键")
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		field := a.groupModelField(item, parameter, model)
		if field == nil {
			continue
		}
		if slices.Contains(keys, field.Name) {
			a.addError(item.GetPos(), "duplicate grouping field '%s' / 分组字段 '%s' 重复", field.Name, field.Name)
			continue
		}
		keys = append(keys, field.Name)
	}
	return keys
}

func directGroupField(expression ast.Expr, parameter string) string {
	member, ok := expression.(*ast.MemberExpr)
	if !ok || member.SafeCall {
		return ""
	}
	ident, ok := member.Object.(*ast.Ident)
	if !ok || ident.Name != parameter {
		return ""
	}
	return member.Field
}

func (a *Analyzer) groupModelField(expression ast.Expr, parameter string, model *ResolvedType) *FieldInfo {
	name := directGroupField(expression, parameter)
	if name == "" {
		a.addError(expression.GetPos(), "group operation requires a direct model field / 分组操作必须引用模型的直接字段")
		return nil
	}
	field := model.LookupField(name)
	if field == nil {
		a.addError(expression.GetPos(), "unknown grouping field '%s' / 未知分组字段 '%s'", name, name)
		return nil
	}
	if !groupScalarField(field) {
		a.addError(expression.GetPos(), "unsupported grouping field '%s' / 分组暂不支持字段 '%s' 的类型", name, name)
		return nil
	}
	return field
}

func groupScalarField(field *FieldInfo) bool {
	if field.Computed || field.IsMethod || field.Type == nil || field.Type.IsList {
		return false
	}
	switch field.Type.Kind {
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeEnum:
		return true
	default:
		return false
	}
}

func (a *Analyzer) checkGroupProjection(call *ast.CallExpr, model *ResolvedType, plan *ast.GroupProjection) *ResolvedType {
	expression, parameter := groupLambda(call)
	object, ok := expression.(*ast.ObjectExpr)
	if !ok || object.TypeName == "" {
		a.addError(call.Pos, "select requires an explicit type projection / select 需要显式 type 构造结果")
		return nil
	}
	result := a.types[object.TypeName]
	if result == nil || result.Kind != TypeCustom {
		a.addError(object.Pos, "group projection must use a declared type, not a model / 分组结果必须使用已声明的 type，不能使用 model")
		return nil
	}
	if a.moduleMap != nil && a.currentFile != nil {
		if module := a.moduleMap[a.currentFile.Name]; module != nil {
			a.checkNameVisibility(object.TypeName, module, object.Pos)
		}
	}
	plan.Result = result.Name
	if len(result.Fields) == 0 {
		a.addError(object.Pos, "group projection requires at least one result field / 分组投影至少需要一个结果字段")
	}
	provided := make(map[string]bool, len(object.Fields))
	for _, entry := range object.Fields {
		if entry == nil || entry.Value == nil {
			a.addError(object.Pos, "group result field requires a value / 分组结果字段需要值")
			continue
		}
		a.checkGroupResultField(entry, parameter, model, result, plan, provided)
	}
	for _, name := range sortedFieldNames(result) {
		if !provided[name] {
			a.addError(object.Pos, "missing result field '%s' in group projection / 分组投影缺少结果字段 '%s'", name, name)
		}
	}
	setExprResolvedType(object, result)
	return result
}

func sortedFieldNames(result *ResolvedType) []string {
	names := fieldNames(result)
	slices.Sort(names)
	return names
}

func (a *Analyzer) checkGroupResultField(entry *ast.NamedArg, parameter string, model, result *ResolvedType, plan *ast.GroupProjection, provided map[string]bool) {
	position := entry.Value.GetPos()
	if provided[entry.Name] {
		a.addError(position, "duplicate result field '%s' / 结果字段 '%s' 重复", entry.Name, entry.Name)
		return
	}
	provided[entry.Name] = true
	field := result.LookupField(entry.Name)
	if field == nil {
		a.addError(position, "unknown result field '%s' / 未知结果字段 '%s'", entry.Name, entry.Name)
		return
	}
	column, actual := a.groupColumn(entry.Value, parameter, model, plan.Keys)
	if actual == nil {
		return
	}
	if field.Type == nil {
		a.addError(position, "result field '%s' has no resolved type / 结果字段 '%s' 缺少有效类型", entry.Name, entry.Name)
		return
	}
	if !sameResolvedTypeIgnoringNullability(field.Type, actual) || actual.Nullable && !field.Type.Nullable {
		a.addError(position, "projection type mismatch for '%s': expected %s, got %s / 分组投影字段 '%s' 的类型不一致", entry.Name, formatResolvedType(field.Type), formatResolvedType(actual), entry.Name)
		return
	}
	column.Field = entry.Name
	column.Type, column.Nullable = field.Type.Name, field.Type.Nullable
	plan.Columns = append(plan.Columns, column)
	setExprResolvedType(entry.Value, actual)
}

func (a *Analyzer) groupColumn(expression ast.Expr, parameter string, model *ResolvedType, keys []string) (ast.GroupColumn, *ResolvedType) {
	if member, ok := expression.(*ast.MemberExpr); ok {
		name := ""
		if directGroupField(member, parameter) == "key" {
			if len(keys) != 1 {
				a.addError(member.Pos, "name a grouping key explicitly for multiple keys / 多字段分组需要按名称指定分组键")
				return ast.GroupColumn{}, nil
			}
			name = keys[0]
		} else if directGroupField(member.Object, parameter) == "key" && !member.SafeCall {
			name = member.Field
		}
		if !slices.Contains(keys, name) {
			a.addError(member.Pos, "'%s' is not a grouping key / '%s' 不是分组键", name, name)
			return ast.GroupColumn{}, nil
		}
		return ast.GroupColumn{Source: name}, model.LookupField(name).Type
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		return a.groupAggregate(call, parameter, model)
	}
	a.addError(expression.GetPos(), "projection value must be a group key or aggregate / 投影值必须是分组键或聚合表达式")
	return ast.GroupColumn{}, nil
}

func (a *Analyzer) groupAggregate(call *ast.CallExpr, parameter string, model *ResolvedType) (ast.GroupColumn, *ResolvedType) {
	name := directGroupField(call.Func, parameter)
	column := ast.GroupColumn{Aggregate: name}
	if name == "count" {
		if len(call.Args) != 0 {
			a.addError(call.Pos, "count accepts no arguments in a grouped projection / 分组投影的 count 不接受参数")
			return column, nil
		}
		return column, a.types["Int"]
	}
	if name != "sum" && name != "avg" && name != "min" && name != "max" {
		a.addError(call.Pos, "unsupported group aggregate '%s' / 不支持的分组聚合 '%s'", name, name)
		return column, nil
	}
	expression, row := groupLambda(call)
	if expression == nil {
		a.addError(call.Pos, "group aggregate requires one field lambda / 分组聚合需要一个字段 lambda")
		return column, nil
	}
	field := a.groupModelField(expression, row, model)
	if field == nil {
		return column, nil
	}
	column.Source = field.Name
	return column, a.groupAggregateType(call, name, field.Type)
}

func (a *Analyzer) groupAggregateType(call *ast.CallExpr, operation string, field *ResolvedType) *ResolvedType {
	if operation == "sum" || operation == "avg" {
		if field.Kind != TypeInt && field.Kind != TypeFloat && field.Kind != TypeDecimal {
			a.addError(call.Pos, "%s requires a numeric field / %s 需要数值字段", operation, operation)
			return nil
		}
		if operation == "avg" && field.Kind == TypeInt {
			return a.types["Float"]
		}
		// Preserve the existing COALESCE(SUM/AVG, 0) contract.
		return field.AsNonNull()
	}
	switch field.Kind {
	case TypeInt, TypeFloat, TypeDecimal, TypeString, TypeDateTime, TypeDuration:
		return field
	default:
		a.addError(call.Pos, "%s does not support this field type / %s 不支持此字段类型", operation, operation)
		return nil
	}
}
