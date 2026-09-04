package semantic

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Pass 2: Resolve Inheritance ==========

func (a *Analyzer) resolveInheritance(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		for _, parentName := range m.Parents {
			parent, ok := a.types[parentName]
			if !ok {
				a.addErrorWithSuggestion(m.Pos, parentName, "type '%s' not found / 类型 '%s' 未找到", parentName, parentName)
				continue
			}
			typ.Parents = append(typ.Parents, parent)
		}
	}
}

// ========== Pass 3: Resolve Fields ==========

func (a *Analyzer) resolveFields(file *ast.File) {
	a.resolveModelFields(file)
	a.resolveInterfaceFields(file)
	a.resolveTypeFields(file)
	a.resolveEventTypes(file)
	a.resolveApiTypes(file)
	a.resolveFnTypes(file)
	a.resolveExtendFields(file)
}

func (a *Analyzer) resolveEventTypes(file *ast.File) {
	for _, event := range file.Events {
		seen := make(map[string]bool, len(event.Params))
		for _, param := range event.Params {
			if seen[param.Name] {
				a.addError(param.Pos, "duplicate parameter '%s' in event '%s' / 事件 '%s' 中参数 '%s' 重复", param.Name, event.Name, event.Name, param.Name)
				continue
			}
			seen[param.Name] = true
			resolved := a.resolveTypeRef(param.Type, param.Pos)
			if resolved == nil || resolved.Kind == TypeUnknown {
				continue
			}
			if param.Type.IsList && param.Type.Nullable {
				a.addError(param.Pos, "event parameter '%s.%s' cannot be a nullable list / 事件参数 '%s.%s' 不能是可空列表", event.Name, param.Name, event.Name, param.Name)
			}
			switch resolved.Kind {
			case TypeInt, TypeFloat, TypeString, TypeBool, TypeDateTime, TypeDuration, TypeUUID, TypeDecimal, TypeBytes, TypeJSON, TypeModel, TypeCustom, TypeEnum:
			default:
				a.addError(param.Pos, "event parameter '%s.%s' has unsupported wire type '%s' / 事件参数 '%s.%s' 使用了不支持的传输类型 '%s'",
					event.Name, param.Name, formatTypeRef(param.Type), event.Name, param.Name, formatTypeRef(param.Type))
			}
		}
	}
}

func (a *Analyzer) resolveModelFields(file *ast.File) {
	for _, m := range file.Models {
		typ := a.types[m.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range m.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				if _, exists := typ.Fields[fi.Name]; exists {
					a.addError(f.Pos, "duplicate field '%s' in model '%s' / 模型 '%s' 中字段 '%s' 重复", fi.Name, m.Name, m.Name, fi.Name)
					continue
				}
				typ.Fields[fi.Name] = fi
			}
			// @visible requires nullable field — non-nullable fields can't be conditionally hidden
			if hasModelDirective(f.Directives, "visible") && f.Type != nil && !f.Type.Nullable {
				a.addError(f.Pos, "@visible requires nullable field (use %s? instead of %s) / @visible 字段必须可空", f.Type.Name, f.Type.Name)
			}
		}
		a.validatePrimaryKeyFields(m)
		a.checkDirectives(m.Directives, OnModel)
		// @withAuth: inject .createToken(), .verify(), .refreshToken() methods
		if hasModelDirective(m.Directives, "withAuth") {
			a.injectWithAuthMethods(typ, m.Directives)
		}
		// @hash: inject .verifyPassword() method on the model
		for _, f := range m.Fields {
			if hasModelDirective(f.Directives, "hash") {
				if existing, exists := typ.Fields["verifyPassword"]; exists && !existing.IsMethod {
					a.addError(f.Pos, "field 'verifyPassword' conflicts with @hash injected method / 字段 'verifyPassword' 与 @hash 注入方法冲突")
					break
				}
				typ.Fields["verifyPassword"] = &FieldInfo{
					Name:     "verifyPassword",
					Type:     a.types["Boolean"],
					IsMethod: true,
					Doc:      ".verifyPassword(plain: String): Boolean — Verify plaintext against @hash field / 校验明文与 @hash 字段",
				}
				break
			}
		}
	}
}

func (a *Analyzer) validatePrimaryKeyFields(model *ast.ModelDecl) {
	var first *ast.FieldDecl
	for _, field := range model.Fields {
		if !hasModelDirective(field.Directives, "id") {
			continue
		}
		if field.Type == nil || field.Type.Nullable || field.Type.IsList || !isPrimaryKeyType(field.Type.Name) {
			typeName := "unknown"
			if field.Type != nil {
				typeName = field.Type.Name
				if field.Type.IsList {
					typeName = "[" + typeName + "]"
				}
				if field.Type.Nullable {
					typeName += "?"
				}
			}
			a.addError(field.Pos, "@id field '%s.%s' must be a non-null Int, String, or UUID, got '%s' / @id 字段 '%s.%s' 必须是非空 Int、String 或 UUID，实际为 '%s'",
				model.Name, field.Name, typeName, model.Name, field.Name, typeName)
		}
		if first == nil {
			first = field
			continue
		}
		a.addError(field.Pos, "model '%s' has multiple @id fields ('%s' and '%s') / 模型 '%s' 有多个 @id 字段（'%s' 和 '%s'）",
			model.Name, first.Name, field.Name, model.Name, first.Name, field.Name)
	}
}

func isPrimaryKeyType(name string) bool {
	return name == "Int" || name == "String" || name == "UUID"
}

func (a *Analyzer) validateComputedFields(files []*ast.File) {
	models := make(map[string]*ast.ModelDecl)
	modelModules := make(map[string]string)
	for _, file := range files {
		for _, model := range file.Models {
			models[model.Name] = model
			modelModules[model.Name] = a.fileModule(file.Name)
		}
	}
	for _, model := range models {
		for _, field := range model.Fields {
			if field.Computed != nil {
				a.validateComputedField(model, field, models, modelModules)
			}
		}
	}
}

func (a *Analyzer) validateComputedField(model *ast.ModelDecl, field *ast.FieldDecl, models map[string]*ast.ModelDecl, modelModules map[string]string) {
	var aggregate *ast.Directive
	for _, directive := range field.Computed.Directives {
		if !isComputedAggregateDirective(directive.Name) {
			continue
		}
		if aggregate != nil {
			a.addError(directive.Pos,
				"computed field '%s.%s' must declare exactly one aggregate / 计算字段 '%s.%s' 只能声明一个聚合",
				model.Name, field.Name, model.Name, field.Name)
			return
		}
		aggregate = directive
	}
	if aggregate == nil {
		a.addError(field.Pos,
			"computed field '%s.%s' must declare exactly one aggregate / 计算字段 '%s.%s' 必须声明且只能声明一个聚合",
			model.Name, field.Name, model.Name, field.Name)
		return
	}
	if field.Computed.Body != nil {
		a.addError(field.Pos,
			"aggregate computed field '%s.%s' cannot also declare a body / 聚合计算字段 '%s.%s' 不能同时声明函数体",
			model.Name, field.Name, model.Name, field.Name)
		return
	}
	relationName, targetName, ok := computedAggregateTarget(aggregate)
	if !ok {
		a.addError(aggregate.Pos,
			"@%s requires a list relation%s / @%s 需要一个列表关系%s",
			aggregate.Name, computedAggregateTargetSuffix(aggregate.Name),
			aggregate.Name, computedAggregateTargetSuffix(aggregate.Name))
		return
	}
	relation := findModelField(model, relationName)
	if relation == nil || relation.Computed != nil || relation.Type == nil || !relation.Type.IsList || models[relation.Type.Name] == nil {
		a.addError(aggregate.Pos,
			"@%s target '%s' must be a list relation / @%s 目标 '%s' 必须是列表关系",
			aggregate.Name, relationName, aggregate.Name, relationName)
		return
	}
	targetModel := models[relation.Type.Name]
	if modelModules[model.Name] != modelModules[targetModel.Name] {
		a.addError(aggregate.Pos,
			"computed aggregate relation '%s.%s' must be stored in the same module / 计算聚合关系 '%s.%s' 必须存储在同一模块",
			model.Name, relationName, model.Name, relationName)
		return
	}
	if !a.validateComputedRelationKeys(model, relation, targetModel) {
		return
	}
	if aggregate.Name == "count" {
		a.validateComputedAggregateResult(model, field, aggregate.Name, "Int", "")
		return
	}
	targetField := findModelField(targetModel, targetName)
	if targetField == nil || targetField.Type == nil || targetField.Computed != nil {
		a.addError(aggregate.Pos,
			"aggregate target field '%s.%s' does not exist / 聚合目标字段 '%s.%s' 不存在",
			targetModel.Name, targetName, targetModel.Name, targetName)
		return
	}
	if targetField.Type.IsList || !isAggregateNumericType(targetField.Type.Name) {
		a.addError(aggregate.Pos,
			"@%s target field '%s.%s' must be numeric / @%s 目标字段 '%s.%s' 必须是数值类型",
			aggregate.Name, targetModel.Name, targetName, aggregate.Name, targetModel.Name, targetName)
		return
	}
	want := targetField.Type.Name
	if aggregate.Name == "avg" {
		want = "Float"
	}
	a.validateComputedAggregateResult(model, field, aggregate.Name, want, targetField.Type.Name)
}

func (a *Analyzer) validateComputedRelationKeys(model *ast.ModelDecl, relation *ast.FieldDecl, target *ast.ModelDecl) bool {
	localName, remoteName := computedRelationKeyNames(model, relation)
	local := findModelField(model, localName)
	if !validComputedKey(local, false) {
		a.addError(relation.Pos,
			"computed relation '%s.%s' local key '%s.%s' must be a non-null Int, String, or UUID / 计算关系 '%s.%s' 的本地键 '%s.%s' 必须是非空 Int、String 或 UUID",
			model.Name, relation.Name, model.Name, localName, model.Name, relation.Name, model.Name, localName)
		return false
	}
	remote := findModelField(target, remoteName)
	if remote == nil {
		a.addError(relation.Pos,
			"computed relation '%s.%s' remote key '%s.%s' does not exist / 计算关系 '%s.%s' 的远端键 '%s.%s' 不存在",
			model.Name, relation.Name, target.Name, remoteName, model.Name, relation.Name, target.Name, remoteName)
		return false
	}
	if !validComputedKey(remote, true) || local.Type.Name != remote.Type.Name {
		a.addError(relation.Pos,
			"computed relation '%s.%s' key types must match (%s.%s: %s, %s.%s: %s) / 计算关系 '%s.%s' 的键类型必须一致",
			model.Name, relation.Name, model.Name, localName, local.Type.Name, target.Name, remoteName, computedFieldTypeName(remote), model.Name, relation.Name)
		return false
	}
	return true
}

func computedRelationKeyNames(model *ast.ModelDecl, relation *ast.FieldDecl) (local, remote string) {
	local = computedModelPrimaryKeyName(model)
	for _, directive := range relation.Directives {
		if directive.Name != "by" {
			continue
		}
		if len(directive.Args) > 0 {
			if ident, ok := directive.Args[0].Value.(*ast.Ident); ok {
				remote = ident.Name
			}
		}
		if len(directive.Args) > 1 {
			if ident, ok := directive.Args[1].Value.(*ast.Ident); ok {
				local = ident.Name
			}
		}
		return local, remote
	}
	return local, lowerFirstIdentifier(model.Name) + upperFirstIdentifier(local)
}

func computedModelPrimaryKeyName(model *ast.ModelDecl) string {
	for _, field := range model.Fields {
		if hasModelDirective(field.Directives, "id") {
			return field.Name
		}
	}
	if findModelField(model, "id") != nil {
		return "id"
	}
	return "id"
}

func validComputedKey(field *ast.FieldDecl, nullableAllowed bool) bool {
	return field != nil && field.Type != nil && field.Computed == nil && !field.Type.IsList &&
		(nullableAllowed || !field.Type.Nullable) && isPrimaryKeyType(field.Type.Name)
}

func computedFieldTypeName(field *ast.FieldDecl) string {
	if field == nil || field.Type == nil {
		return "unknown"
	}
	return field.Type.Name
}

func (a *Analyzer) validateComputedAggregateResult(model *ast.ModelDecl, field *ast.FieldDecl, function, want, targetType string) {
	if field.Type != nil && !field.Type.Nullable && !field.Type.IsList && field.Type.Name == want {
		return
	}
	if function == "sum" || function == "min" || function == "max" {
		a.addError(field.Pos,
			"@%s computed field '%s.%s' must match target type %s / @%s 计算字段 '%s.%s' 必须与目标类型 %s 一致",
			function, model.Name, field.Name, targetType, function, model.Name, field.Name, targetType)
		return
	}
	a.addError(field.Pos,
		"@%s computed field '%s.%s' must have type %s / @%s 计算字段 '%s.%s' 必须是 %s 类型",
		function, model.Name, field.Name, want, function, model.Name, field.Name, want)
}

func isComputedAggregateDirective(name string) bool {
	switch name {
	case "count", "sum", "avg", "min", "max":
		return true
	default:
		return false
	}
}

func computedAggregateTarget(directive *ast.Directive) (relation, field string, ok bool) {
	if len(directive.Args) != 1 {
		return "", "", false
	}
	switch target := directive.Args[0].Value.(type) {
	case *ast.Ident:
		return target.Name, "", directive.Name == "count"
	case *ast.MemberExpr:
		owner, ownerOK := target.Object.(*ast.Ident)
		if !ownerOK || directive.Name == "count" {
			return "", "", false
		}
		return owner.Name, target.Field, true
	default:
		return "", "", false
	}
}

func computedAggregateTargetSuffix(function string) string {
	if function == "count" {
		return " such as @count(posts) / 例如 @count(posts)"
	}
	return " and numeric field such as @" + function + "(posts.amount) / 以及数值字段，例如 @" + function + "(posts.amount)"
}

func findModelField(model *ast.ModelDecl, name string) *ast.FieldDecl {
	for _, field := range model.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func isAggregateNumericType(name string) bool {
	return name == "Int" || name == "Float" || name == "Decimal"
}

func (a *Analyzer) resolveInterfaceFields(file *ast.File) {
	for _, i := range file.Interfaces {
		typ := a.types[i.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range i.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveTypeFields(file *ast.File) {
	for _, t := range file.Types {
		typ := a.types[t.Name]
		if typ == nil {
			continue
		}
		if typ.Fields == nil {
			typ.Fields = make(map[string]*FieldInfo)
		}
		for _, f := range t.Fields {
			fi := a.resolveFieldDecl(f)
			if fi != nil {
				typ.Fields[fi.Name] = fi
			}
		}
	}
}

func (a *Analyzer) resolveApiTypes(file *ast.File) {
	for _, api := range file.APIs {
		sym := a.scope.Lookup(api.Name)
		if sym == nil {
			continue
		}
		retType := a.resolveTypeRef(api.ReturnType, api.Pos)
		sym.Type = retType
		if api.ReturnType != nil && api.ReturnType.Name == "Result" {
			a.addError(api.ReturnType.Pos, "API return types must be response values, not Result<T> / API 返回类型必须是响应值，不能是 Result<T>")
		}

		for _, p := range api.Params {
			a.resolveTypeRef(p.Type, api.Pos)
		}
		a.checkDirectives(api.Directives, OnApi)
		a.validateStreamAPI(api)
		a.validateNativeReturnType(api)
		a.validateScopeDirective(api)
	}
}

func (a *Analyzer) validateStreamAPI(api *ast.ApiDecl) {
	stream, count := findAPIDirective(api.Directives, "stream")
	if stream == nil {
		return
	}
	if count > 1 {
		a.addError(stream.Pos, "@stream may only be declared once / @stream 只能声明一次")
	}
	if api.ReturnType == nil {
		a.addError(api.Pos, "@stream API must declare a return type / @stream API 必须声明返回类型")
	} else if api.ReturnType.Nullable {
		a.addError(api.ReturnType.Pos, "@stream return type must be non-nullable / @stream 返回类型必须非空")
	}
	for _, incompatible := range []string{"cache", "paginate", "scope"} {
		if directive, _ := findAPIDirective(api.Directives, incompatible); directive != nil {
			a.addError(directive.Pos, "@stream cannot be combined with @%s / @stream 不能与 @%s 组合使用", incompatible, incompatible)
		}
	}

	native, _ := findAPIDirective(api.Directives, "native")
	if native != nil && api.Body != nil {
		a.addError(api.Body.Pos, "@native @stream API cannot declare a Luxo matcher body / @native @stream API 不能声明 Luxo 匹配器主体")
	}
	if len(stream.Args) == 0 {
		if native == nil {
			a.addError(stream.Pos, "@stream without an event source requires @native / 没有事件源的 @stream 必须使用 @native")
		}
		return
	}

	ident, ok := stream.Args[0].Value.(*ast.Ident)
	if !ok || ident.Name == "" {
		a.addError(stream.Pos, "@stream event source must be an event identifier / @stream 事件源必须是事件标识符")
		return
	}
	event := a.findEvent(ident.Name)
	if event == nil {
		a.addError(stream.Pos, "@stream event '%s' does not exist / @stream 事件 '%s' 不存在", ident.Name, ident.Name)
		return
	}
	if api.ReturnType != nil {
		matches := 0
		for _, param := range event.Params {
			if sameTypeRefExact(param.Type, api.ReturnType) {
				matches++
			}
		}
		if matches != 1 {
			a.addError(stream.Pos, "@stream event '%s' must contain exactly one payload parameter of type '%s', got %d / @stream 事件 '%s' 必须包含且仅包含一个 '%s' 类型的载荷参数，实际为 %d 个",
				ident.Name, formatTypeRef(api.ReturnType), matches, ident.Name, formatTypeRef(api.ReturnType), matches)
		}
	}
	if native == nil {
		a.validateGeneratedStreamMatcher(api, event)
	}
}

func findAPIDirective(directives []*ast.Directive, name string) (*ast.Directive, int) {
	var found *ast.Directive
	count := 0
	for _, directive := range directives {
		if directive.Name == name {
			if found == nil {
				found = directive
			}
			count++
		}
	}
	return found, count
}

func (a *Analyzer) findEvent(name string) *ast.EventDecl {
	for _, file := range a.files {
		for _, event := range file.Events {
			if event.Name == name {
				return event
			}
		}
	}
	return nil
}

func sameTypeRefExact(left, right *ast.TypeRef) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Name != right.Name || left.Nullable != right.Nullable || left.IsList != right.IsList || len(left.TypeArgs) != len(right.TypeArgs) || len(left.Tuple) != len(right.Tuple) {
		return false
	}
	for i := range left.TypeArgs {
		if !sameTypeRefExact(left.TypeArgs[i], right.TypeArgs[i]) {
			return false
		}
	}
	for i := range left.Tuple {
		if !sameTypeRefExact(left.Tuple[i], right.Tuple[i]) {
			return false
		}
	}
	return true
}

func formatTypeRef(ref *ast.TypeRef) string {
	if ref == nil {
		return ""
	}
	name := ref.Name
	if ref.IsList {
		name = "[" + name + "]"
	}
	if ref.Nullable {
		name += "?"
	}
	return name
}

func (a *Analyzer) validateGeneratedStreamMatcher(api *ast.ApiDecl, event *ast.EventDecl) {
	if api.Body == nil {
		return
	}
	if len(api.Body.Stmts) != 1 {
		a.addError(api.Body.Pos, "@stream matcher body must contain exactly one boolean expression / @stream 匹配器主体必须且只能包含一个布尔表达式")
		return
	}
	exprStmt, ok := api.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || exprStmt.Expr == nil {
		a.addError(api.Body.Pos, "@stream matcher body must contain exactly one boolean expression / @stream 匹配器主体必须且只能包含一个布尔表达式")
		return
	}

	eventParams := make(map[string]*ast.ParamDecl, len(event.Params))
	for _, param := range event.Params {
		eventParams[param.Name] = param
	}
	apiParams := make(map[string]*ast.ParamDecl, len(api.Params))
	for _, param := range api.Params {
		apiParams[param.Name] = param
	}
	reported := make(map[string]bool)
	ast.WalkExprs(api.Body, func(expr ast.Expr) {
		a.validateStreamMatcherExpr(expr, eventParams, apiParams, reported)
	})
}

func (a *Analyzer) validateStreamMatcherExpr(expr ast.Expr, eventParams, apiParams map[string]*ast.ParamDecl, reported map[string]bool) {
	switch value := expr.(type) {
	case *ast.Literal, *ast.UnaryExpr:
		return
	case *ast.BinaryExpr:
		if value.Op == "in" || value.Op == "is" {
			a.addError(value.Pos, "operator '%s' is not supported by generated @stream matchers; use @native / 生成的 @stream 匹配器不支持运算符 '%s'，请使用 @native", value.Op, value.Op)
		}
	case *ast.Ident:
		if param := apiParams[value.Name]; param != nil {
			a.validateStreamMatcherType(value.Pos, "parameter", value.Name, param.Type, reported)
		}
	case *ast.MemberExpr:
		ident, ok := value.Object.(*ast.Ident)
		if !ok {
			a.addError(value.Pos, "generated @stream matchers only support direct it.field, my.field, and enum member access / 生成的 @stream 匹配器仅支持直接访问 it.field、my.field 和枚举成员")
			return
		}
		if ident.Name == "it" {
			if param := eventParams[value.Field]; param != nil {
				a.validateStreamMatcherType(value.Pos, "event field", value.Field, param.Type, reported)
			}
			return
		}
		if ident.Name == "my" {
			return
		}
		if typ := a.types[ident.Name]; typ != nil && typ.Kind == TypeEnum {
			return
		}
		a.addError(value.Pos, "generated @stream matchers do not support member access on '%s'; use @native / 生成的 @stream 匹配器不支持访问 '%s' 的成员，请使用 @native", ident.Name, ident.Name)
	case *ast.CallExpr, *ast.ElvisExpr, *ast.BangElvisExpr, *ast.WhenExpr, *ast.LambdaExpr, *ast.ListExpr, *ast.ObjectExpr, *ast.RangeExpr, *ast.TransactionExpr, *ast.YieldExpr, *ast.AsyncExpr, *ast.AwaitExpr:
		a.addError(expr.GetPos(), "expression is not supported by generated @stream matchers; use @native / 生成的 @stream 匹配器不支持该表达式，请使用 @native")
	}
}

func (a *Analyzer) validateStreamMatcherType(pos token.Position, source, name string, ref *ast.TypeRef, reported map[string]bool) {
	key := source + ":" + name
	if reported[key] {
		return
	}
	supported := ref != nil && !ref.IsList && !ref.Nullable
	if supported {
		switch ref.Name {
		case "Int", "Float", "String", "Boolean", "Duration", "UUID":
			return
		default:
			typ := a.types[ref.Name]
			if typ != nil && typ.Kind == TypeEnum {
				return
			}
		}
	}
	reported[key] = true
	typeName := formatTypeRef(ref)
	a.addError(pos, "%s '%s' of type '%s' cannot be used in a generated @stream matcher; use @native / %s '%s' 的类型 '%s' 不能用于生成的 @stream 匹配器，请使用 @native",
		source, name, typeName, source, name, typeName)
}

// validateBareAPI checks that APIs without body or @native have a valid inferred name.
// Validates: action prefix + known model + valid field segments after By.
func (a *Analyzer) validateBareAPI(api *ast.ApiDecl) {
	if api.Body != nil || len(api.Params) > 0 || api.ReturnType != nil {
		return
	}
	for _, d := range api.Directives {
		if d.Name == "native" {
			return
		}
	}

	name := api.Name
	rest, ok := extractActionPrefix(name)
	if !ok {
		a.addError(api.Pos, "inferred API '%s' must start with get/list/count/exists/delete + ModelName", name)
		return
	}

	rest = stripTopFirstPrefix(rest)

	modelName, modelType := a.findModelInName(rest)
	if modelName == "" {
		a.addError(api.Pos, "inferred API '%s' does not reference a known model", name)
		return
	}

	a.validateAfterModel(api.Pos, name, rest[len(modelName):], modelType)
}

// extractActionPrefix extracts the rest after a valid action prefix.
func extractActionPrefix(name string) (string, bool) {
	for _, p := range []string{"list", "get", "count", "exists", "delete"} {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return name[len(p):], true
		}
	}
	return "", false
}

// stripTopFirstPrefix strips Top10/First5 prefixes from list actions.
func stripTopFirstPrefix(rest string) string {
	if strings.HasPrefix(rest, "Top") || strings.HasPrefix(rest, "First") {
		i := 3
		if strings.HasPrefix(rest, "First") {
			i = 5
		}
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		return rest[i:]
	}
	return rest
}

// findModelInName finds the longest matching model name at the start of rest.
func (a *Analyzer) findModelInName(rest string) (string, *ResolvedType) {
	var bestName string
	var bestType *ResolvedType
	for mn, mt := range a.types {
		if mt.Kind != TypeModel {
			continue
		}
		plural := mn + "s"
		if strings.HasPrefix(rest, plural) && len(plural) > len(bestName) {
			bestName = plural
			bestType = mt
		} else if strings.HasPrefix(rest, mn) && len(mn) > len(bestName) {
			bestName = mn
			bestType = mt
		}
	}
	return bestName, bestType
}

// validateAfterModel validates the part after the model name (By/OrderBy + fields).
func (a *Analyzer) validateAfterModel(pos token.Position, apiName, afterModel string, modelType *ResolvedType) {
	if afterModel == "" {
		return // "listUsers" — valid
	}
	if strings.HasPrefix(afterModel, "OrderBy") {
		return // "listUsersOrderByNameDesc" — valid
	}
	if !strings.HasPrefix(afterModel, "By") {
		a.addError(pos, "inferred API '%s' — expected 'By' after model name", apiName)
		return
	}

	afterBy := afterModel[2:]

	// Strip trailing OrderBy... clause before field validation
	if idx := strings.Index(afterBy, "OrderBy"); idx > 0 {
		afterBy = afterBy[:idx]
	}

	if afterBy == "" {
		a.addError(pos, "inferred API '%s' is incomplete — missing field name after By", apiName)
		return
	}
	if strings.HasSuffix(afterBy, "And") || strings.HasSuffix(afterBy, "Or") {
		suffix := afterBy[len(afterBy)-2:]
		if strings.HasSuffix(afterBy, "And") {
			suffix = "And"
		}
		a.addError(pos, "inferred API '%s' is incomplete — missing field name after %s", apiName, suffix)
		return
	}

	if modelType.Fields != nil {
		a.validateFieldSegments(pos, apiName, afterBy, modelType)
	}
}

// validateFieldSegments checks that each field reference in a By-clause exists in the model.
func (a *Analyzer) validateFieldSegments(pos token.Position, apiName, afterBy string, model *ResolvedType) {
	// Known operator suffixes to strip before field matching
	opSuffixes := []string{
		"GreaterThanEqual", "LessThanEqual", "GreaterThan", "LessThan",
		"NotContaining", "StartingWith", "EndingWith", "Containing",
		"IsNotNull", "IsNull", "NotNull", "Between", "NotIn", "In",
		"NotLike", "Like", "IgnoreCase", "Not", "True", "False",
		"After", "Before", "OrderBy",
	}

	// Split by And/Or
	segments := splitByAndOr(afterBy)

	for _, seg := range segments {
		// Strip operator suffix
		field := seg
		for _, op := range opSuffixes {
			if strings.HasSuffix(field, op) {
				field = field[:len(field)-len(op)]
				break
			}
		}

		// Strip OrderBy tail (OrderByNameDesc → before OrderBy)
		if idx := strings.Index(field, "OrderBy"); idx >= 0 {
			field = field[:idx]
		}

		if field == "" {
			continue // operator-only segment like "True"/"IsNull"
		}

		// Lowercase first char to match field names
		fieldName := strings.ToLower(field[:1]) + field[1:]
		// Check explicit fields + implicit timestamp fields
		if _, ok := model.Fields[fieldName]; !ok {
			if fieldName != "createdAt" && fieldName != "updatedAt" && fieldName != "deletedAt" {
				a.addError(pos, "inferred API '%s' — unknown field '%s'", apiName, fieldName)
			}
		}
	}
}

// splitByAndOr splits "EmailAndNameOrAge" into ["Email", "Name", "Age"].
// splitByAndOr splits "EmailAndNameOrAge" into ["Email", "Name", "Age"].
// Only splits at PascalCase boundaries: the char before "And"/"Or" must be lowercase
// and the char after must be uppercase, to avoid splitting field names like "OrderId".
func splitByAndOr(s string) []string {
	var parts []string
	for len(s) > 0 {
		idx, sepLen := findAndOrBoundary(s)
		if idx < 0 {
			parts = append(parts, s)
			break
		}
		if idx > 0 {
			parts = append(parts, s[:idx])
		}
		s = s[idx+sepLen:]
	}
	return parts
}

// findAndOrBoundary finds the first "And"/"Or" at a PascalCase word boundary.
func findAndOrBoundary(s string) (int, int) {
	for i := 0; i < len(s); i++ {
		// Check "And" at position i
		if i+3 <= len(s) && s[i:i+3] == "And" {
			if isPascalBoundary(s, i, 3) {
				return i, 3
			}
		}
		// Check "Or" at position i
		if i+2 <= len(s) && s[i:i+2] == "Or" {
			if isPascalBoundary(s, i, 2) {
				return i, 2
			}
		}
	}
	return -1, 0
}

// isPascalBoundary checks if the separator at position idx is at a PascalCase word boundary.
func isPascalBoundary(s string, idx, sepLen int) bool {
	// Must not be at the start (need a preceding field)
	if idx == 0 {
		return false
	}
	// Char before must be lowercase (end of previous field name)
	prev := s[idx-1]
	if prev < 'a' || prev > 'z' {
		return false
	}
	// Char after separator must be uppercase (start of next field) or end of string
	after := idx + sepLen
	if after >= len(s) {
		return true // trailing And/Or
	}
	return s[after] >= 'A' && s[after] <= 'Z'
}

// validateNativeReturnType checks that @native APIs declare a return type.
func (a *Analyzer) validateNativeReturnType(api *ast.ApiDecl) {
	for _, d := range api.Directives {
		if d.Name == "native" && api.ReturnType == nil {
			a.addError(api.Pos, "@native API must declare a return type")
			return
		}
	}
}

// validateScopeDirective checks @scope usage on an API:
// 1. The return type must be a model (not a custom type, enum, etc.)
// 2. Each scope name must exist on the target model
func (a *Analyzer) validateScopeDirective(api *ast.ApiDecl) {
	for _, d := range api.Directives {
		if d.Name != "scope" {
			continue
		}
		// resolve the base return type name (strip list wrapper)
		baseTypeName := ""
		if api.ReturnType != nil {
			baseTypeName = api.ReturnType.Name
		}
		if baseTypeName == "" {
			a.addWarning(d.Pos, "@scope can only be used on APIs returning a model type / @scope 只能用在返回模型类型的 API 上")
			return
		}
		typ, ok := a.types[baseTypeName]
		if !ok {
			return // type not found — already reported by resolveTypeRef
		}
		if typ.Kind != TypeModel {
			a.addWarning(d.Pos, "@scope can only be used on APIs returning a model type, '%s' is %s / @scope 只能用在返回模型类型的 API 上，'%s' 是 %s",
				baseTypeName, typ.Kind, baseTypeName, typ.Kind)
			return
		}
		// validate each scope name arg exists on the model
		modelScopes := a.findModelScopes(baseTypeName)
		for _, arg := range d.Args {
			scopeName := ""
			if ident, ok := arg.Value.(*ast.Ident); ok {
				scopeName = ident.Name
			}
			if scopeName == "" {
				continue
			}
			if !stringInSlice(scopeName, modelScopes) {
				closest := findClosestString(scopeName, modelScopes)
				if closest != "" {
					a.addError(d.Pos, "scope '%s' not found on model '%s', did you mean '%s'? / 模型 '%s' 上未找到 scope '%s'，你是不是想写 '%s'？",
						scopeName, baseTypeName, closest, baseTypeName, scopeName, closest)
				} else {
					a.addError(d.Pos, "scope '%s' not found on model '%s' / 模型 '%s' 上未找到 scope '%s'",
						scopeName, baseTypeName, baseTypeName, scopeName)
				}
			}
		}
	}
}

// findModelScopes returns all scope names defined on a model across all files.
func (a *Analyzer) findModelScopes(modelName string) []string {
	var scopes []string
	for _, file := range a.files {
		for _, m := range file.Models {
			if m.Name == modelName {
				for _, sc := range m.Scopes {
					scopes = append(scopes, sc.Name)
				}
			}
		}
	}
	return scopes
}

func (a *Analyzer) resolveFnTypes(file *ast.File) {
	for _, fn := range file.Functions {
		sym := a.scope.Lookup(fn.Name)
		if sym == nil {
			continue
		}
		if fn.ReturnType != nil {
			sym.Type = a.resolveTypeRef(fn.ReturnType, fn.Pos)
		}
		for _, p := range fn.Params {
			a.resolveTypeRef(p.Type, fn.Pos)
		}
		a.checkDirectives(fn.Directives, OnFn)
		a.validateFunctionResult(fn)
	}
}

func (a *Analyzer) validateFunctionResult(fn *ast.FnDecl) {
	if fn.ReturnType == nil {
		return
	}
	isNative := hasNamedDirective(fn.Directives, "native")
	isService := hasNamedDirective(fn.Directives, "service")
	isResult := fn.ReturnType.Name == "Result"
	if isResult {
		if len(fn.ReturnType.TypeArgs) != 1 || fn.ReturnType.IsList || fn.ReturnType.Nullable {
			a.addError(fn.ReturnType.Pos, "Result must have exactly one non-null wrapper type argument / Result 必须且只能有一个非空包装类型参数")
		}
		if !isNative {
			a.addError(fn.ReturnType.Pos, "Result<T> is only valid for @native fn declarations / Result<T> 只能用于 @native fn 声明")
		}
		return
	}
	if isNative && !isService {
		a.addError(fn.ReturnType.Pos, "@native fn return type must be Result<T>; @service boundaries may return T directly / @native fn 返回类型必须是 Result<T>；@service 边界可以直接返回 T")
	}
}

func hasNamedDirective(directives []*ast.Directive, name string) bool {
	for _, directive := range directives {
		if directive.Name == name {
			return true
		}
	}
	return false
}

func (a *Analyzer) resolveExtendFields(file *ast.File) {
	// detect extend field conflicts: same model + same field name
	extendFields := map[string]map[string]token.Position{} // model → field → first pos
	for _, ext := range file.Extends {
		if _, ok := a.types[ext.Name]; !ok {
			// extend target might be in another module (resolved at gateway)
		}
		if extendFields[ext.Name] == nil {
			extendFields[ext.Name] = map[string]token.Position{}
		}
		for _, f := range ext.Fields {
			if firstPos, exists := extendFields[ext.Name][f.Name]; exists {
				a.addError(f.Pos,
					"extend field '%s.%s' conflicts with previous declaration at line %d / extend 字段 '%s.%s' 与第 %d 行的声明冲突",
					ext.Name, f.Name, firstPos.Line, ext.Name, f.Name, firstPos.Line)
			} else {
				extendFields[ext.Name][f.Name] = f.Pos
			}
			a.resolveFieldDecl(f)
		}
	}
}

func (a *Analyzer) validateFederationExtendFields() {
	for _, file := range a.files {
		currentModule := a.fileModule(file.Name)
		for _, ext := range file.Extends {
			target := a.types[ext.Name]
			if target == nil || currentModule == "" || a.typeOwners.ownerOf(ext.Name) == currentModule {
				continue
			}
			for _, field := range ext.Fields {
				a.validateFederationExtendField(currentModule, ext.Name, target, field)
			}
		}
	}
}

func (a *Analyzer) validateFederationExtendField(currentModule, modelName string, target *ResolvedType, field *ast.FieldDecl) {
	fieldType := a.resolveTypeRef(field.Type, field.Pos)
	if fieldType == nil {
		return
	}
	if ownerField := target.LookupField(field.Name); ownerField != nil {
		if !sameResolvedType(ownerField.Type, fieldType) {
			a.addError(field.Pos,
				"extend projection '%s.%s' must match owner type '%s', got '%s' / extend 投影 '%s.%s' 必须匹配所属模块类型 '%s'，实际为 '%s'",
				modelName, field.Name, formatResolvedType(ownerField.Type), formatResolvedType(fieldType),
				modelName, field.Name, formatResolvedType(ownerField.Type), formatResolvedType(fieldType))
		}
		return
	}
	if fieldType.Kind != TypeModel {
		a.addError(field.Pos,
			"scalar field '%s.%s' has no federation resolver; extend fields must reference a model / 标量字段 '%s.%s' 没有 Federation resolver，新增的 extend 字段必须引用模型",
			modelName, field.Name, modelName, field.Name)
		return
	}
	if owner := a.typeOwners.ownerOf(fieldType.Name); owner != currentModule {
		a.addError(field.Pos,
			"extend field '%s.%s' must resolve a model owned by module '%s' / extend 字段 '%s.%s' 必须解析当前模块 '%s' 所属的模型",
			modelName, field.Name, currentModule, modelName, field.Name, currentModule)
		return
	}
	a.validateFederationForeignKey(modelName, target, fieldType, field)
}

func (a *Analyzer) validateFederationForeignKey(modelName string, owner, related *ResolvedType, field *ast.FieldDecl) {
	primaryKey := resolvedPrimaryKeyField(owner)
	if primaryKey == nil || primaryKey.Type == nil {
		a.addError(field.Pos,
			"federation model '%s' requires a primary key / Federation 模型 '%s' 必须声明主键",
			modelName, modelName)
		return
	}
	foreignKey, localKey := federationRelationKeys(modelName, primaryKey.Name, field)
	if localKey != "" && localKey != primaryKey.Name {
		a.addError(field.Pos,
			"federation relation '%s.%s' must resolve by primary key '%s', got '%s' / Federation 关系 '%s.%s' 必须按主键 '%s' 解析，实际为 '%s'",
			modelName, field.Name, primaryKey.Name, localKey, modelName, field.Name, primaryKey.Name, localKey)
		return
	}
	foreignField := related.LookupField(foreignKey)
	if foreignField == nil || foreignField.Type == nil {
		a.addError(field.Pos,
			"federation relation '%s.%s' requires foreign-key field '%s' on model '%s' / Federation 关系 '%s.%s' 要求模型 '%s' 声明外键字段 '%s'",
			modelName, field.Name, foreignKey, related.Name, modelName, field.Name, related.Name, foreignKey)
		return
	}
	if foreignField.Type.IsList || foreignField.Type.Kind != primaryKey.Type.Kind || foreignField.Type.Name != primaryKey.Type.Name {
		a.addError(field.Pos,
			"foreign-key field '%s.%s' must match primary key '%s.%s' type '%s', got '%s' / 外键字段 '%s.%s' 必须匹配主键 '%s.%s' 的类型 '%s'，实际为 '%s'",
			related.Name, foreignKey, modelName, primaryKey.Name, formatResolvedType(primaryKey.Type), formatResolvedType(foreignField.Type),
			related.Name, foreignKey, modelName, primaryKey.Name, formatResolvedType(primaryKey.Type), formatResolvedType(foreignField.Type))
	}
}

func federationRelationKeys(modelName, primaryKey string, field *ast.FieldDecl) (string, string) {
	for _, directive := range field.Directives {
		if directive.Name != "by" {
			continue
		}
		foreignKey := directiveIdentifierArg(directive, 0)
		localKey := directiveIdentifierArg(directive, 1)
		if foreignKey != "" {
			return foreignKey, localKey
		}
	}
	return lowerFirstIdentifier(modelName) + upperFirstIdentifier(primaryKey), primaryKey
}

func directiveIdentifierArg(directive *ast.Directive, index int) string {
	if index >= len(directive.Args) {
		return ""
	}
	identifier, _ := directive.Args[index].Value.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func lowerFirstIdentifier(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToLower(r)) + value[size:]
}

func upperFirstIdentifier(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	return string(unicode.ToUpper(r)) + value[size:]
}

func (a *Analyzer) resolveFieldDecl(f *ast.FieldDecl) *FieldInfo {
	fi := &FieldInfo{
		Name:     f.Name,
		Pos:      f.Pos,
		Doc:      f.Doc,
		Computed: f.Computed != nil,
	}

	if f.Type != nil {
		fi.Type = a.resolveTypeRef(f.Type, f.Pos)
		fi.Nullable = f.Type.Nullable
	}

	if f.Default != nil {
		fi.HasDefault = true
	}

	for _, d := range f.Directives {
		fi.Directives = append(fi.Directives, d.Name)
	}

	a.checkFieldDirectives(f)
	a.checkTransformDirective(f, fi.Type)
	return fi
}

func (a *Analyzer) resolveTypeRef(ref *ast.TypeRef, pos token.Position) *ResolvedType {
	if ref == nil {
		return &ResolvedType{Kind: TypeVoid, Name: "Void"}
	}

	// Tuple type: (Post, Video, Product)
	if len(ref.Tuple) > 0 {
		result := &ResolvedType{
			Kind:     TypeTuple,
			Nullable: ref.Nullable,
		}
		for _, t := range ref.Tuple {
			resolved := a.resolveTypeRef(t, pos)
			result.Tuple = append(result.Tuple, resolved)
		}
		return result
	}

	name := ref.Name

	// Look up the type
	typ, ok := a.types[name]
	if !ok {
		a.addErrorWithSuggestion(pos, name, "unknown type '%s' / 未知类型 '%s'", name, name)
		return &ResolvedType{Kind: TypeUnknown, Name: name, Nullable: ref.Nullable}
	}

	result := &ResolvedType{
		Kind:       typ.Kind,
		Name:       typ.Name,
		Nullable:   ref.Nullable,
		IsList:     ref.IsList,
		Fields:     typ.Fields,
		Parents:    typ.Parents,
		Pos:        typ.Pos,
		Variants:   typ.Variants,
		EnumValues: typ.EnumValues,
	}

	// Generic args
	for _, arg := range ref.TypeArgs {
		result.TypeArgs = append(result.TypeArgs, a.resolveTypeRef(arg, pos))
	}

	return result
}
