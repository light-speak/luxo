package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Pass 4: Check Bodies ==========

func (a *Analyzer) checkBodies(file *ast.File) {
	// global val/var declarations — var is forbidden at global scope
	for _, g := range file.Globals {
		if g.Mutable {
			a.addError(g.Pos, "global 'var' is not allowed, use 'val' for global constants or 'cache' for mutable state / 全局不允许 'var'，使用 'val' 定义全局常量或使用 'cache' 管理可变状态")
		}
		a.checkValStmt(g, a.scope)
	}

	for _, api := range file.APIs {
		if api.Body != nil {
			scope := a.scope.Child()
			// add params to scope
			for _, p := range api.Params {
				pType := a.resolveTypeRef(p.Type, api.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  api.Pos,
				})
			}
			// add my (current authenticated user) to scope
			scope.Define(&Symbol{
				Name: "my",
				Kind: SymVariable,
				Type: a.types["Identity"],
			})
			// @stream(EventName): inject `it` with event's field types
			a.injectStreamIt(api, scope, file)
			var expected *ResolvedType
			if !hasNamedDirective(api.Directives, "stream") {
				expected = a.symbolReturnType(api.Name)
			}
			a.checkCallableBody(api.Body, scope, expected)
			a.validateStreamMatcherResult(api)
			a.checkUnusedVariables(scope)
		}
	}

	for _, fn := range file.Functions {
		if fn.Body != nil {
			scope := a.scope.Child()
			for _, p := range fn.Params {
				pType := a.resolveTypeRef(p.Type, fn.Pos)
				scope.Define(&Symbol{
					Name: p.Name,
					Kind: SymParam,
					Type: pType,
					Pos:  fn.Pos,
				})
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
		if on.Body != nil {
			scope := a.scope.Child()
			// Build event type from event declaration
			var eventType *ResolvedType
			for _, f := range a.files {
				for _, ev := range f.Events {
					if ev.Name == on.EventName {
						evType := &ResolvedType{Kind: TypeModel, Name: ev.Name, Fields: make(map[string]*FieldInfo)}
						for _, p := range ev.Params {
							pType := a.resolveTypeRef(p.Type, on.Pos)
							evType.Fields[p.Name] = &FieldInfo{Name: p.Name, Type: pType}
						}
						eventType = evType
						break
					}
				}
				if eventType != nil {
					break
				}
			}
			// add lambda-style params to scope with event type
			if len(on.Params) > 0 {
				for _, p := range on.Params {
					scope.Define(&Symbol{
						Name: p,
						Kind: SymParam,
						Type: eventType,
					})
				}
			} else if eventType != nil {
				// No named params — inject `it` with event type
				scope.Define(&Symbol{
					Name: "it",
					Kind: SymVariable,
					Type: eventType,
					Used: true,
				})
			}
			a.checkBlock(on.Body, scope)
		}
	}
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

	a.checkBlock(body, scope)
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
	if resolved == nil || resolved.Kind != TypeBool || resolved.IsList || resolved.Nullable {
		actual := "unknown"
		if resolved != nil {
			actual = formatResolvedType(resolved)
		}
		a.addError(stmt.GetPos(), "@stream matcher expression must be Boolean, got '%s' / @stream 匹配器表达式必须是 Boolean，实际为 '%s'", actual, actual)
	}
}

func (a *Analyzer) checkBlock(block *ast.Block, scope *Scope) {
	if block == nil {
		return
	}
	terminated := false
	for _, stmt := range block.Stmts {
		if terminated {
			a.addWarning(stmt.GetPos(), "unreachable code / 不可达代码")
			break
		}
		a.checkStmt(stmt, scope)
		if isTerminating(stmt) {
			terminated = true
		}
	}
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
		a.checkExpr(s.Condition, scope)
		childScope := scope.Child()
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
		a.checkExpr(s.Error, scope)

	case *ast.EmitStmt:
		for _, arg := range s.Args {
			a.checkExpr(arg.Value, scope)
		}

	case *ast.AssignStmt:
		a.checkAssignStmt(s, scope)

	case *ast.BreakStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ContinueStmt:
		// valid in any loop context, no check needed at semantic level

	case *ast.ExprStmt:
		if s.Expr != nil {
			a.checkExpr(s.Expr, scope)
		}
	}
}

func (a *Analyzer) checkValStmt(s *ast.ValStmt, scope *Scope) {
	exprType := a.checkExpr(s.Value, scope)
	if s.Type != nil {
		declaredType := a.resolveTypeRef(s.Type, s.Pos)
		if exprType != nil && !isEmptyListExpr(s.Value) && !isTypeAssignable(declaredType, exprType) {
			a.addError(s.Pos, "variable '%s' expects '%s', got '%s' / 变量 '%s' 需要 '%s'，得到 '%s'",
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType),
				s.Name, formatResolvedType(declaredType), formatResolvedType(exprType))
		}
		exprType = declaredType
		setExprResolvedType(s.Value, declaredType)
	}
	if len(s.Names) > 0 {
		a.defineDestructured(s, exprType, scope)
	} else {
		pos := s.NamePos
		if pos.Line == 0 {
			pos = s.Pos // fallback for older AST without NamePos
		}
		scope.Define(&Symbol{
			Name:    s.Name,
			Kind:    SymVariable,
			Type:    exprType,
			Pos:     pos,
			Mutable: s.Mutable,
		})
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
		scope.Define(&Symbol{
			Name:    name,
			Kind:    SymVariable,
			Type:    varType,
			Pos:     s.Pos,
			Mutable: s.Mutable,
		})
	}
	// warn if count mismatch
	if exprType != nil && exprType.Kind == TypeTuple && len(s.Names) != len(exprType.Tuple) {
		a.addError(s.Pos,
			"destructuring count mismatch: %d variables but tuple has %d elements / 解构数量不匹配：%d 个变量但元组有 %d 个元素",
			len(s.Names), len(exprType.Tuple), len(s.Names), len(exprType.Tuple))
	}
}

func (a *Analyzer) checkAssignStmt(s *ast.AssignStmt, scope *Scope) {
	a.checkExpr(s.Target, scope)
	a.checkExpr(s.Value, scope)
	if ident, ok := s.Target.(*ast.Ident); ok {
		if sym := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymVariable && !sym.Mutable {
			a.addError(s.Pos, "cannot assign to immutable variable '%s' (declared with val) / 不能给不可变变量 '%s' 赋值（使用 val 声明）", ident.Name, ident.Name)
		}
	}
	// 1.4: mark compound assignment on model member as atomic
	if s.Op == "+=" || s.Op == "-=" {
		if member, ok := s.Target.(*ast.MemberExpr); ok {
			if objType := a.checkExpr(member.Object, scope); objType != nil && objType.Kind == TypeModel {
				s.Atomic = true
			}
		}
	}
}

func (a *Analyzer) checkForStmt(s *ast.ForStmt, scope *Scope) *ResolvedType {
	childScope := scope.Child()
	if s.Collection != nil {
		collType := a.checkExpr(s.Collection, childScope)
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
	a.checkBlock(s.Body, childScope)
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
	a.inAwait = true
	defer func() { a.inAwait = prevAwait }()
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
