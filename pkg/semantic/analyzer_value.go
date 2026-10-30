package semantic

import "github.com/light-speak/luxo/pkg/ast"

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
	previousLambda, previousReturn := a.inLambda, a.expectedReturn
	a.inLambda, a.expectedReturn = true, nil
	a.checkResultBlock(block.Body, child, required)
	a.inLambda, a.expectedReturn = previousLambda, previousReturn
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
			a.checkCollectionReturns(block.Body, result, false)
		}
		setExprResolvedType(block, result)
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
