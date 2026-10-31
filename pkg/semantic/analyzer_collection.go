package semantic

import "github.com/light-speak/luxo/pkg/ast"

func (a *Analyzer) checkCollectionCall(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "map" && member.Field != "filter" || !member.Object.IsListType() {
		return nil, false
	}
	input := a.resolvedExprType(member.Object)
	if input == nil || input.Kind == TypeUnknown {
		a.addError(call.Pos, "collection receiver requires a known element type / 集合接收者需要明确的元素类型")
		return nil, true
	}
	if input.Nullable {
		a.addError(call.Pos, "collection receiver must be non-null / 集合接收者必须先解除可空性")
		return nil, true
	}
	lambda := a.collectionLambda(call)
	if lambda == nil {
		return nil, true
	}
	element := cloneResolvedType(input)
	element.IsList = false
	output := a.checkCollectionLambda(lambda, element, scope, member.Field == "filter")
	if output == nil {
		return nil, true
	}
	if member.Field == "filter" {
		return input, true
	}
	return output.AsList(), true
}

func (a *Analyzer) collectionLambda(call *ast.CallExpr) *ast.LambdaExpr {
	if len(call.Args) == 1 && call.Args[0].Name == "" {
		if lambda, ok := call.Args[0].Value.(*ast.LambdaExpr); ok && len(lambda.Params) <= 1 && lambda.Body != nil && len(lambda.Body.Stmts) > 0 {
			return lambda
		}
	}
	a.addError(call.Pos, "collection method requires one non-empty lambda with one element parameter / 集合方法需要一个非空、单元素参数的 lambda")
	return nil
}

func (a *Analyzer) checkCollectionLambda(lambda *ast.LambdaExpr, element *ResolvedType, scope *Scope, filter bool) *ResolvedType {
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	child := scope.Child()
	child.Define(&Symbol{Name: parameter, Kind: SymVariable, Type: element})
	previousLambda, previousReturn := a.inLambda, a.expectedReturn
	a.inLambda, a.expectedReturn = true, nil
	if filter {
		a.expectedReturn = a.types["Boolean"]
	}
	a.checkBlock(lambda.Body, child)
	a.inLambda, a.expectedReturn = previousLambda, previousReturn
	value := collectionResultExpr(lambda.Body)
	output := a.resolvedExprType(value)
	if output == nil || output.Kind == TypeUnknown || output.Kind == TypeVoid || output.IsList || output.Nullable {
		a.addError(lambda.Pos, "collection lambda must return a known non-null element value / 集合 lambda 必须返回已知类型的非空元素值")
		return nil
	}
	if filter {
		a.validateReturnValue(lambda.Pos, a.types["Boolean"], output)
		output = a.types["Boolean"]
	}
	a.checkCollectionReturns(lambda.Body, output, false)
	setExprResolvedType(lambda, output)
	return output
}

func collectionResultExpr(body *ast.Block) ast.Expr {
	for _, statement := range body.Stmts {
		if result, ok := statement.(*ast.ReturnStmt); ok {
			return result.Value
		}
		if isTerminating(statement) {
			return nil
		}
	}
	return blockResultExpr(body)
}

func (a *Analyzer) checkCollectionReturns(body *ast.Block, expected *ResolvedType, inLoop bool) {
	for _, statement := range body.Stmts {
		switch value := statement.(type) {
		case *ast.ReturnStmt:
			if value.Value == nil {
				a.addError(value.Pos, "collection lambda must return a value / 集合 lambda 必须返回值")
			} else {
				a.validateReturnValue(value.Pos, expected, a.resolvedExprType(value.Value))
			}
		case *ast.IfStmt:
			a.checkCollectionReturns(value.Then, expected, inLoop)
		case *ast.ForStmt:
			a.checkCollectionReturns(value.Body, expected, true)
		case *ast.BreakStmt, *ast.ContinueStmt:
			if !inLoop {
				a.addError(statement.GetPos(), "loop control cannot escape a collection lambda / 循环控制不能越过集合 lambda 边界")
			}
		}
		if isTerminating(statement) {
			return
		}
	}
}
