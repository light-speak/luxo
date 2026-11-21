package semantic

import (
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Pass 4: Check Bodies ==========

func (a *Analyzer) checkGlobalDeclarations(file *ast.File) {
	// global val/var declarations — var is forbidden at global scope
	for _, g := range file.Globals {
		if g.Mutable {
			a.addError(g.Pos, "global 'var' is not allowed, use 'val' for global constants or 'cache' for mutable state / 全局不允许 'var'，使用 'val' 定义全局常量或使用 'cache' 管理可变状态")
		}
		a.checkValStmt(g, a.scope)
	}
}

func (a *Analyzer) checkBodies(file *ast.File) {
	a.checkScopeOrders(file)
	for _, api := range file.APIs {
		a.checkAuthPermissionConditions(api.Directives)
		if api.Body != nil {
			scope := a.callableScope(api.Params, api.Pos)
			a.injectAuthenticatedIdentity(scope, api.Directives)
			// @stream(EventName): inject `it` with event's field types
			a.injectStreamIt(api, scope, file)
			var expected *ResolvedType
			if !hasNamedDirective(api.Directives, "stream") {
				expected = a.apiBodyReturnType(api)
			}
			a.checkCallableBody(api.Body, scope, expected)
			a.validateStreamMatcherResult(api)
			a.checkUnusedVariables(scope)
		}
	}

	for _, fn := range file.Functions {
		a.checkAuthPermissionConditions(fn.Directives)
		if fn.Body != nil {
			scope := a.callableScope(fn.Params, fn.Pos)
			if hasNamedDirective(fn.Directives, "service") {
				a.injectAuthenticatedIdentity(scope, fn.Directives)
			}
			expected := a.symbolReturnType(fn.Name)
			if expected == nil {
				expected = &ResolvedType{Kind: TypeVoid, Name: "Void"}
			}
			a.checkCallableBody(fn.Body, scope, expected)
			a.checkUnusedVariables(scope)
		}
	}

	for _, mw := range file.Middlewares {
		a.checkDirectives(mw.Directives, OnMiddleware)
	}

	for _, on := range file.Listeners {
		a.checkListener(on)
	}
}

func (a *Analyzer) callableScope(params []*ast.ParamDecl, pos token.Position) *Scope {
	scope := a.scope.Child()
	// Parameters become reachable and expire together with the body scope.
	symbols := make([]Symbol, len(params))
	for index, param := range params {
		symbols[index] = Symbol{Name: param.Name, Kind: SymParam, Type: a.resolveTypeRef(param.Type, pos), Pos: pos}
		scope.Define(&symbols[index])
	}
	return scope
}

func (a *Analyzer) checkListener(listener *ast.OnDecl) {
	if len(listener.Params) > 1 {
		a.addError(listener.Pos, "listener accepts at most one payload binding")
	}
	if listener.Native && listener.Body != nil {
		a.addError(listener.Pos, "native listener must not declare a body / native 监听器不能声明函数体")
	}
	if !listener.Native && listener.Body == nil {
		a.addError(listener.Pos, "listener requires a body or @native / 监听器必须提供函数体或 @native")
	}
	declaration := a.findEvent(listener.EventName)
	if declaration == nil {
		a.addError(listener.Pos, "unknown event '%s' / 未知事件 '%s'", listener.EventName, listener.EventName)
		return
	}
	if listener.Body == nil {
		return
	}
	eventType := &ResolvedType{Kind: TypeCustom, Name: declaration.Name, Fields: make(map[string]*FieldInfo, len(declaration.Params))}
	for _, parameter := range declaration.Params {
		fieldType := a.resolveTypeRef(parameter.Type, listener.Pos)
		eventType.Fields[parameter.Name] = &FieldInfo{Name: parameter.Name, Type: fieldType}
	}
	scope := a.scope.Child()
	names := listener.Params
	if len(names) == 0 {
		names = []string{"it"}
	}
	for _, name := range names {
		if !scope.Define(&Symbol{Name: name, Kind: SymParam, Type: eventType, Used: true}) {
			a.addError(listener.Pos, "duplicate listener binding '%s' / 监听器绑定 '%s' 重复", name, name)
		}
	}
	a.checkCallableBody(listener.Body, scope, &ResolvedType{Kind: TypeVoid, Name: "Void"})
}

// Only same-named fn/API bodies need a second return-type slot.
func (a *Analyzer) apiBodyReturnType(api *ast.ApiDecl) *ResolvedType {
	if typ, exists := a.apiBodyReturns[api]; exists {
		return typ
	}
	return a.symbolReturnType(api.Name)
}

func (a *Analyzer) symbolReturnType(name string) *ResolvedType {
	symbol := a.scope.Lookup(name)
	if symbol == nil {
		return nil
	}
	return symbol.Type
}

func (a *Analyzer) checkCallableBody(body *ast.Block, scope *Scope, expected *ResolvedType) {
	previous := a.expectedReturn
	a.expectedReturn = expected
	defer func() { a.expectedReturn = previous }()

	a.checkResultBlock(body, scope, expected != nil && expected.Kind != TypeVoid)
	if expected == nil || expected.Kind == TypeVoid {
		return
	}
	if len(body.Stmts) == 0 {
		if expected != nil {
			a.addError(body.Pos, "callable must return '%s' / 可调用声明必须返回 '%s'", formatResolvedType(expected), formatResolvedType(expected))
		}
		return
	}
	last := body.Stmts[len(body.Stmts)-1]
	switch statement := last.(type) {
	case *ast.ReturnStmt, *ast.ThrowStmt:
		return
	case *ast.ExprStmt:
		// Exact concrete metadata needs no reconstructed nullable/list type.
		// Generic arguments and inheritance still use full assignability checks.
		expression := statement.Expr
		if expression != nil && expected.Name != "" && len(expected.TypeArgs) == 0 &&
			expected.Name == expression.GetTypeTag() && expected.IsList == expression.IsListType() &&
			(expected.Nullable || !expression.IsNullable()) {
			return
		}
		a.validateReturnValue(statement.GetPos(), expected, a.resolvedExprType(statement.Expr))
	default:
		a.addError(last.GetPos(), "callable must return '%s' / 可调用声明必须返回 '%s'", formatResolvedType(expected), formatResolvedType(expected))
	}
}

func (a *Analyzer) validateReturnValue(pos token.Position, expected, actual *ResolvedType) {
	if expected == nil || actual == nil || isTypeAssignable(expected, actual) {
		return
	}
	a.addError(pos, "return expects '%s', got '%s' / 返回值需要 '%s'，得到 '%s'",
		formatResolvedType(expected), formatResolvedType(actual),
		formatResolvedType(expected), formatResolvedType(actual))
}

func (a *Analyzer) validateStreamMatcherResult(api *ast.ApiDecl) {
	stream, _ := findAPIDirective(api.Directives, "stream")
	native, _ := findAPIDirective(api.Directives, "native")
	if stream == nil || native != nil || api.Body == nil || len(api.Body.Stmts) != 1 {
		return
	}
	stmt, ok := api.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || stmt.Expr == nil {
		return
	}
	resolved := a.resolvedExprType(stmt.Expr)
	if !resolved.IsNonNullBoolean() {
		actual := "unknown"
		if resolved != nil {
			actual = formatResolvedType(resolved)
		}
		a.addError(stmt.GetPos(), "@stream matcher expression must be Boolean, got '%s' / @stream 匹配器表达式必须是 Boolean，实际为 '%s'", actual, actual)
	}
}

func (a *Analyzer) checkBlock(block *ast.Block, scope *Scope) {
	a.checkResultBlock(block, scope, false)
}

func isTerminating(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BreakStmt:
		return true
	case *ast.ContinueStmt:
		return true
	case *ast.ThrowStmt:
		return true
	}
	return false
}

func (a *Analyzer) checkStmt(stmt ast.Stmt, scope *Scope) {
	switch s := stmt.(type) {
	case *ast.ValStmt:
		a.checkValStmt(s, scope)

	case *ast.IfStmt:
		condition := a.checkExpr(s.Condition, scope)
		if condition != nil && !condition.IsNonNullBoolean() {
			a.addError(s.Pos, "if condition requires non-null Boolean / if 条件必须是非空 Boolean")
		}
		childScope := scope.Child()
		narrowCondition(s.Condition, true, scope, childScope)
		a.checkBlock(s.Then, childScope)

	case *ast.ForStmt:
		a.checkForStmt(s, scope)

	case *ast.ReturnStmt:
		if s.Value == nil {
			if a.expectedReturn != nil && a.expectedReturn.Kind != TypeVoid {
				a.addError(s.GetPos(), "return expects '%s', got 'Void' / 返回值需要 '%s'，得到 'Void'", formatResolvedType(a.expectedReturn), formatResolvedType(a.expectedReturn))
			}
			break
		}
		a.validateReturnValue(s.GetPos(), a.expectedReturn, a.checkExpr(s.Value, scope))

	case *ast.ThrowStmt:
		s.Error = a.checkThrownValue(s.Error, scope, s.Pos)

	case *ast.EmitStmt:
		a.checkEmitArguments(s, scope)

	case *ast.AssignStmt:
		a.checkAssignStmt(s, scope)

	case *ast.BreakStmt, *ast.ContinueStmt:
		if a.loopDepth == 0 {
			a.addError(stmt.GetPos(), "loop control requires a local loop scope / 循环跳转必须位于当前执行作用域的 for 内部")
		}

	case *ast.ExprStmt:
		if branch, ok := s.Expr.(*ast.WhenExpr); ok {
			a.setExprResolvedType(branch, a.checkWhen(branch, scope, false))
		} else if guard, ok := s.Expr.(*ast.BangElvisExpr); ok {
			a.checkBangElvisGuard(guard, scope)
			a.setExprResolvedType(guard, &ResolvedType{Kind: TypeVoid, Name: "Void"})
		} else if s.Expr != nil {
			a.checkExpr(s.Expr, scope)
		}
	}
}

func (a *Analyzer) checkValStmt(s *ast.ValStmt, scope *Scope) {
	var exprType *ResolvedType
	if _, channel := channelConstructor(s.Value); channel {
		exprType = a.checkChannelInitializer(s, scope)
	} else {
		exprType = a.checkExpr(s.Value, scope)
	}
	if exprType != nil && exprType.Kind == TypeVoid {
		a.addError(s.Pos, "void operation cannot initialize a variable / 无返回值操作不能初始化变量")
		return
	}
	if s.Type != nil {
		declaredType := a.resolveTypeRef(s.Type, s.Pos)
		if exprType != nil && !isEmptyListExpr(s.Value) && !isTypeAssignable(declaredType, exprType) {
			a.addError(s.Pos, "variable '%s' expects '%s', got '%s' / 变量 '%s' 需要 '%s'，得到 '%s'",
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType),
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType))
		}
		exprType = declaredType
		if isEmptyListExpr(s.Value) {
			a.setExprResolvedType(s.Value, declaredType)
		}
	}
	if len(s.Names) > 0 {
		a.defineDestructured(s, exprType, scope)
	} else {
		pos := s.NamePos
		if pos.Line == 0 {
			pos = s.Pos // fallback for older AST without NamePos
		}
		a.defineVariable(scope, s.Name, exprType, pos, s.Mutable)
	}
}

// Failed declarations preserve the earlier binding; discards bind no name.
func (a *Analyzer) defineVariable(scope *Scope, name string, typ *ResolvedType, pos token.Position, mutable bool) {
	if name == "_" {
		return
	}
	if !scope.Define(&Symbol{Name: name, Kind: SymVariable, Type: typ, Pos: pos, Mutable: mutable}) {
		a.addError(pos, "duplicate variable binding '%s' in the same scope / 同一作用域重复声明变量 '%s'", name, name)
	}
}

// defineDestructured unpacks a tuple type into individual variables.
// val (a, b, c) = await { expr1; expr2; expr3 }
// a gets type of expr1, b gets type of expr2, c gets type of expr3.
func (a *Analyzer) defineDestructured(s *ast.ValStmt, exprType *ResolvedType, scope *Scope) {
	for i, name := range s.Names {
		var varType *ResolvedType
		if exprType != nil && exprType.Kind == TypeTuple && i < len(exprType.Tuple) {
			varType = exprType.Tuple[i]
		} else if exprType != nil && exprType.Kind != TypeTuple {
			// non-tuple assigned to destructuring — all get same type
			varType = exprType
		}
		a.defineVariable(scope, name, varType, s.Pos, s.Mutable)
	}
	// warn if count mismatch
	if exprType != nil && exprType.Kind == TypeTuple && len(s.Names) != len(exprType.Tuple) {
		a.addError(s.Pos,
			"destructuring count mismatch: %d variables but tuple has %d elements / 解构数量不匹配：%d 个变量但元组有 %d 个元素",
			len(s.Names), len(exprType.Tuple), len(s.Names), len(exprType.Tuple))
	}
}

func (a *Analyzer) checkAssignStmt(s *ast.AssignStmt, scope *Scope) {
	targetType := a.checkExpr(s.Target, scope)
	operandType := targetType
	valueType := a.checkExpr(s.Value, scope)
	if !a.isWritableTarget(s.Target, scope) {
		a.addError(s.Pos, "assignment target is not writable / 赋值目标不可写")
	}
	if ident, ok := s.Target.(*ast.Ident); ok {
		if s.Op == "=" {
			ident.NarrowedTag = false
		}
		scope.invalidateNarrowing(ident.Name)
		if declared := scope.ResolvedTypeOf(ident.Name); declared != nil {
			if s.Op == "=" {
				a.setExprResolvedType(s.Target, declared)
			}
			targetType = declared
		}
		if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable && !sym.Mutable {
			a.addError(s.Pos, "cannot assign to immutable variable '%s' (declared with val) / 不能给不可变变量 '%s' 赋值（使用 val 声明）", ident.Name, ident.Name)
		}
	}
	if s.Op != "=" {
		valueType = a.checkBinaryOp(strings.TrimSuffix(s.Op, "="), operandType, valueType, s.Pos)
	}
	emptyList := s.Op == "=" && isEmptyListExpr(s.Value) && targetType != nil && targetType.IsList
	if !emptyList && !isTypeAssignable(targetType, valueType) {
		a.addError(s.Pos, "assignment expects '%s', got '%s' / 赋值需要 '%s'，得到 '%s'",
			formatResolvedType(targetType), formatResolvedType(valueType),
			formatResolvedType(targetType), formatResolvedType(valueType))
	}
	// 1.4: mark compound assignment on model member as atomic
	if s.Op == "+=" || s.Op == "-=" {
		if member, ok := s.Target.(*ast.MemberExpr); ok {
			if objType := a.resolvedExprType(member.Object); objType != nil && objType.Kind == TypeModel {
				s.Atomic = true
			}
		}
	}
}

// Assignment requires a storage location, not a computed property or declaration.
func (a *Analyzer) isWritableTarget(target ast.Expr, scope *Scope) bool {
	switch value := target.(type) {
	case *ast.Ident:
		symbol := scope.Lookup(value.Name)
		return symbol != nil && (symbol.Kind == SymVariable || symbol.Kind == SymParam)
	case *ast.MemberExpr:
		if value.SafeCall {
			return false
		}
		if receiver, ok := value.Object.(*ast.Ident); ok {
			symbol := scope.Lookup(receiver.Name)
			if symbol == nil || (symbol.Kind != SymVariable && symbol.Kind != SymParam) {
				return false
			}
		}
		receiver := a.resolvedExprType(value.Object)
		if receiver == nil || receiver.IsList || receiver.Nullable {
			return false
		}
		if receiver.Kind != TypeModel && receiver.Kind != TypeCustom && receiver.Kind != TypeInterface {
			return false
		}
		field := receiver.LookupField(value.Field)
		return field != nil && !field.IsMethod
	default:
		return false
	}
}

func (a *Analyzer) checkForStmt(s *ast.ForStmt, scope *Scope) *ResolvedType {
	return a.checkForBody(s, scope, false)
}

func (a *Analyzer) checkForBody(s *ast.ForStmt, scope *Scope, required bool) *ResolvedType {
	childScope := scope.Child()
	if s.Collection != nil {
		collType := a.checkExpr(s.Collection, childScope)
		if s.VarName == "" && collType != nil && !collType.IsNonNullBoolean() {
			a.addError(s.Pos, "for condition requires non-null Boolean / for 条件必须是非空 Boolean")
		}
		if s.VarName != "" {
			itemType := collectionElementType(collType)
			childScope.Define(&Symbol{
				Name: s.VarName,
				Kind: SymVariable,
				Type: itemType,
				Pos:  s.Pos,
			})
		}
	}
	a.loopDepth++
	a.checkResultBlock(s.Body, childScope, required)
	a.loopDepth--
	return a.inferForExprType(s.Body)
}

// checkAwaitExpr collects the type of each expression statement in the await
// block and returns a tuple type so that destructuring can assign each variable
// its own type: val (user, posts) = await { getUser(id); getPosts(uid) }
func (a *Analyzer) checkAwaitExpr(e *ast.AwaitExpr, scope *Scope) *ResolvedType {
	if a.inAwait {
		a.addError(e.Pos, "nested await is not allowed / 不允许嵌套 await")
		return nil
	}
	prevAwait := a.inAwait
	previousDepth := a.loopDepth
	a.inAwait = true
	a.loopDepth = 0
	defer func() { a.inAwait, a.loopDepth = prevAwait, previousDepth }()
	childScope := scope.Child()
	var elements []*ResolvedType
	if e.Body != nil {
		for _, stmt := range e.Body.Stmts {
			if es, ok := stmt.(*ast.ExprStmt); ok && es.Expr != nil {
				t := a.checkExpr(es.Expr, childScope)
				elements = append(elements, t)
			} else {
				a.checkStmt(stmt, childScope)
			}
		}
	}
	// single expression — return its type directly, not a tuple
	if len(elements) == 1 {
		return elements[0]
	}
	if len(elements) > 1 {
		return &ResolvedType{
			Kind:  TypeTuple,
			Name:  "Tuple",
			Tuple: elements,
		}
	}
	return nil
}

// Only authenticated handlers provide a validated identity.
func (a *Analyzer) injectAuthenticatedIdentity(scope *Scope, directives []*ast.Directive) {
	if hasNamedDirective(directives, "auth") {
		scope.Define(&Symbol{Name: "my", Kind: SymVariable, Type: a.authIdentity(directives)})
	}
}

// Identity store types are complete before field conditions are checked.
func (a *Analyzer) checkFieldConditions(file *ast.File) {
	for _, model := range file.Models {
		a.checkAuthPermissionConditions(model.Directives)
		a.checkFieldConditionsForFields(model.Fields)
	}
	for _, declaration := range file.Types {
		previousParameters := a.typeParameters
		a.typeParameters = declaration.TypeParams
		a.checkFieldConditionsForFields(declaration.Fields)
		a.typeParameters = previousParameters
	}
	for _, declaration := range file.Interfaces {
		a.checkFieldConditionsForFields(declaration.Fields)
	}
	for _, declaration := range file.Extends {
		a.checkFieldConditionsForFields(declaration.Fields)
	}
}

func (a *Analyzer) checkAuthPermissionConditions(directives []*ast.Directive) {
	for _, directive := range directives {
		if directive.Name != "auth" {
			continue
		}
		for _, argument := range directive.Args {
			if argument.Name != "permission" {
				continue
			}
			lambda, ok := argument.Value.(*ast.LambdaExpr)
			if !ok {
				continue // The declaration pass reports an invalid argument type.
			}
			condition := (&ast.Directive{Body: lambda.Body}).SingleExpression()
			if condition == nil {
				a.addError(directive.Pos, "@auth permission requires one condition expression / @auth permission 必须包含一个条件表达式")
				continue
			}
			scope := a.scope.Child()
			scope.Define(&Symbol{Name: "my", Kind: SymVariable, Type: a.authIdentity(directives)})
			resolved := a.checkExpr(condition, scope)
			if resolved == nil || resolved.Name != "Boolean" || resolved.IsList || resolved.Nullable {
				a.addError(directive.Pos, "@auth permission must be a non-null Boolean / @auth permission 必须为非空 Boolean")
			}
		}
	}
}

func (a *Analyzer) checkFieldConditionsForFields(fields []*ast.FieldDecl) {
	for _, field := range fields {
		a.checkResolvedTransform(field)
		a.checkBeforeSaveDirective(field)
		for _, directive := range field.Directives {
			if directive.Name != "visible" {
				continue
			}
			expression := directive.SingleExpression()
			if expression == nil {
				continue // The declaration pass reports malformed directive bodies.
			}
			a.bindVisibleSubject(directive, expression)
			scope := a.scope.Child()
			scope.Define(&Symbol{Name: "my", Kind: SymVariable, Type: a.types["Identity"]})
			condition := a.checkExpr(expression, scope)
			if condition == nil || condition.Name != "Boolean" || condition.IsList || condition.Nullable {
				a.addError(directive.Pos, "@visible condition must be a non-null Boolean / @visible 条件必须为非空 Boolean")
			}
		}
	}
}

// Parsed and defaulted event arguments always carry values; retain the guard for incomplete metadata.
func (a *Analyzer) checkEmitArguments(statement *ast.EmitStmt, scope *Scope) {
	event := a.findEvent(statement.EventName)
	if event == nil {
		a.addError(statement.Pos, "unknown event '%s' / 未知事件 '%s'", statement.EventName, statement.EventName)
		a.checkObjectExprValues(statement.Args, scope)
		return
	}
	positions := functionParameterPositions(event.Params)
	assigned := make([]bool, len(event.Params))
	for _, argument := range statement.Args {
		if argument == nil || argument.Value == nil {
			a.addError(statement.Pos, "event argument requires a name and value / 事件参数需要名称和值")
			continue
		}
		actual := a.checkExpr(argument.Value, scope)
		position, exists := positions[argument.Name]
		if !exists {
			a.addError(argument.Value.GetPos(), "unknown argument '%s' in event '%s' / 事件 '%s' 中存在未知参数 '%s'", argument.Name, event.Name, event.Name, argument.Name)
			continue
		}
		if _, valid := a.rejectDuplicateFunctionArgument(argument, position, assigned); !valid {
			continue
		}
		assigned[position] = true
		parameter := event.Params[position]
		argument.ExpectedType = parameter.Type
		a.checkFunctionArgumentType(argument, actual, parameter)
	}
	a.checkMissingFunctionArguments(&ast.CallExpr{Pos: statement.Pos}, &ast.FnDecl{Name: event.Name, Params: event.Params}, assigned)
	for index, parameter := range event.Params {
		if !assigned[index] && parameter.Default != nil {
			statement.Args = append(statement.Args, &ast.NamedArg{
				Name: parameter.Name, Value: parameter.Default, ExpectedType: parameter.Type,
			})
		}
	}
}
