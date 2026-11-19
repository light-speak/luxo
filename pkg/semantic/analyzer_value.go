package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) checkIsolatedLoopBlock(block *ast.Block, scope *Scope) {
	previous := a.loopDepth
	a.loopDepth = 0
	a.checkBlock(block, scope)
	a.loopDepth = previous
}

func (a *Analyzer) checkAsyncExpr(expr *ast.AsyncExpr, scope *Scope) *ResolvedType {
	if a.asyncResult == nil {
		a.asyncResult = &ResolvedType{Kind: TypeVoid, Name: "Void"}
	}
	previous := a.expectedReturn
	a.expectedReturn = a.asyncResult
	a.checkIsolatedLoopBlock(expr.Body, scope.Child())
	a.expectedReturn = previous
	return a.asyncResult
}

// A branch block has its own return boundary. Its result is the first
// unconditional return or the final expression, never an implicit zero value.
func (a *Analyzer) checkWhenBranch(expr ast.Expr, scope *Scope, required bool) *ResolvedType {
	block, ok := expr.(*ast.LambdaExpr)
	if !ok {
		result := a.checkExpr(expr, scope)
		if required && isVoidResult(result) {
			a.addError(expr.GetPos(), "when branch must produce a value / when 值分支必须产生结果")
		}
		return result
	}
	return a.checkWhenBlock(block, scope.Child(), required)
}

func (a *Analyzer) checkWhenBlock(block *ast.LambdaExpr, child *Scope, required bool) *ResolvedType {
	previousReturn, previousDepth := a.expectedReturn, a.loopDepth
	a.expectedReturn = nil
	if required {
		a.loopDepth = 0
	}
	a.checkResultBlock(block.Body, child, required)
	a.expectedReturn, a.loopDepth = previousReturn, previousDepth
	var result *ResolvedType
	var value ast.Expr
	if block.Body != nil {
		value = collectionResultExpr(block.Body)
		result = a.resolvedExprType(value)
	}
	if required && (value == nil || isVoidResult(result)) && !blockThrows(block.Body) {
		a.addError(block.Pos, "when branch must produce a value / when 值分支必须产生结果")
	}
	if result != nil {
		if required {
			a.checkCollectionReturns(block.Body, result)
		}
		a.setExprResolvedType(block, result)
	}
	return result
}

func (a *Analyzer) checkResultBlock(block *ast.Block, scope *Scope, required bool) {
	if block == nil {
		return
	}
	terminated := false
	for i, statement := range block.Stmts {
		if terminated {
			a.addWarning(statement.GetPos(), "unreachable code / 不可达代码")
			break
		}
		if last, ok := statement.(*ast.ExprStmt); ok && required && i == len(block.Stmts)-1 {
			a.checkExpr(last.Expr, scope)
		} else {
			a.checkStmt(statement, scope)
		}
		terminated = isTerminating(statement)
	}
}

// The normal caller supplies a parsed lambda body, including a non-nil empty block.
// Retain the nil-body fallback for incomplete metadata.
func blockThrows(block *ast.Block) bool {
	if block == nil {
		return false
	}
	for _, statement := range block.Stmts {
		if _, ok := statement.(*ast.ThrowStmt); ok {
			return true
		}
		if isTerminating(statement) {
			return false
		}
	}
	return false
}

// Void call metadata has no registered user type; reconstruction retains its
// name with TypeUnknown. It still cannot supply an expression result.
func isVoidResult(result *ResolvedType) bool {
	return result != nil && (result.Kind == TypeVoid || result.Kind == TypeUnknown && result.Name == "Void")
}
