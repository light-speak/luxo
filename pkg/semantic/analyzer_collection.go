package semantic

import (
	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

func (a *Analyzer) checkCollectionCall(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if ok && len(call.Args) != 0 {
		a.checkLengthCallArguments(call, member)
	}
	if ok && member.Field == "joinToString" && member.Object.IsListType() {
		return a.checkJoinToString(call, member, scope), true
	}
	if !ok || !typedCollectionMethod(member.Field) || !member.Object.IsListType() {
		return nil, false
	}
	input := a.resolvedExprType(member.Object)
	if input == nil || input.Kind == TypeUnknown {
		a.addError(call.Pos, "collection receiver requires a known element type / 集合接收者需要明确的元素类型")
		return nil, true
	}
	if input.Nullable && !member.SafeCall {
		a.addError(call.Pos, "collection receiver must be non-null / 集合接收者必须先解除可空性")
		return nil, true
	}
	if input.Nullable {
		input = cloneResolvedType(input)
		input.Nullable = false
	}
	lambda := a.collectionLambda(call)
	if lambda == nil {
		return nil, true
	}
	element := cloneResolvedType(input)
	element.IsList = false
	if member.Field == "forEach" {
		output := &ResolvedType{Kind: TypeVoid, Name: "Void"}
		a.checkForEachLambda(lambda, element, scope, output)
		return output, true
	}
	output := a.checkCollectionLambda(lambda, element, scope, member.Field == "filter" || member.Field == "any")
	if output == nil {
		return nil, true
	}
	if member.Field == "sumOf" {
		return a.checkSumOfResult(call, output), true
	}
	if member.Field == "sortBy" {
		switch output.Name {
		case "Int", "Float", "String", "Duration", "DateTime", "Decimal":
		default:
			a.addError(call.Pos, "sortBy requires an ordered scalar key")
		}
		return input, true
	}
	if member.Field == "any" {
		return a.types["Boolean"], true
	}
	if member.Field == "filter" {
		return input, true
	}
	return output.AsList(), true
}

func (a *Analyzer) checkLengthCallArguments(call *ast.CallExpr, member *ast.MemberExpr) {
	switch member.Field {
	case "size", "isEmpty":
		if !member.Object.IsListType() && member.Object.GetTypeTag() != "String" {
			return
		}
	case "length":
		if member.Object.IsListType() || member.Object.GetTypeTag() != "String" {
			return
		}
	default:
		return
	}
	a.addError(call.Pos, "'%s' accepts no arguments / '%s' 不接受参数", member.Field, member.Field)
}

func (a *Analyzer) checkJoinToString(call *ast.CallExpr, member *ast.MemberExpr, scope *Scope) *ResolvedType {
	input := a.resolvedExprType(member.Object)
	if input == nil {
		a.addError(call.Pos, "joinToString requires a known element type / joinToString 需要明确的元素类型")
		return nil
	}
	if input.Nullable && !member.SafeCall {
		a.addError(call.Pos, "joinToString receiver must be non-null / joinToString 接收者必须先解除可空性")
	}
	element := cloneResolvedType(input)
	element.IsList, element.Nullable = false, false
	if !templateScalar(element) {
		a.addError(call.Pos, "joinToString requires scalar or enum elements; convert explicitly to String / joinToString 要求标量或枚举元素，请显式转换为 String")
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" {
		a.addError(call.Pos, "joinToString requires one positional String separator / joinToString 需要一个位置 String 分隔符参数")
		return a.types["String"]
	}
	separator := a.checkExpr(call.Args[0].Value, scope)
	if separator == nil || separator.Kind != TypeString || separator.IsList || separator.Nullable {
		a.addError(call.Pos, "joinToString separator must be non-null String / joinToString 分隔符必须为非空 String")
	}
	return a.types["String"]
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
	a.checkIsolatedLoopBlock(lambda.Body, child)
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
	a.checkCollectionReturns(lambda.Body, output)
	a.setExprResolvedType(lambda, output)
	return output
}

func (a *Analyzer) checkForEachLambda(lambda *ast.LambdaExpr, element *ResolvedType, scope *Scope, output *ResolvedType) {
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	child := scope.Child()
	child.Define(&Symbol{Name: parameter, Kind: SymVariable, Type: element})
	previousLambda, previousReturn := a.inLambda, a.expectedReturn
	a.inLambda, a.expectedReturn = true, output
	a.checkIsolatedLoopBlock(lambda.Body, child)
	a.inLambda, a.expectedReturn = previousLambda, previousReturn
	a.checkCollectionReturns(lambda.Body, output)
	a.setExprResolvedType(lambda, output)
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

func (a *Analyzer) checkCollectionReturns(body *ast.Block, expected *ResolvedType) {
	for _, statement := range body.Stmts {
		switch value := statement.(type) {
		case *ast.ReturnStmt:
			if value.Value == nil {
				if expected.Kind == TypeVoid {
					continue
				}
				a.addError(value.Pos, "collection lambda must return a value / 集合 lambda 必须返回值")
			} else {
				a.validateReturnValue(value.Pos, expected, a.resolvedExprType(value.Value))
			}
		case *ast.IfStmt:
			a.checkCollectionReturns(value.Then, expected)
		case *ast.ForStmt:
			a.checkCollectionReturns(value.Body, expected)
		}
		if isTerminating(statement) {
			return
		}
	}
}

func (a *Analyzer) checkSumOfResult(call *ast.CallExpr, output *ResolvedType) *ResolvedType {
	switch output.Name {
	case "Int", "Float", "Decimal":
	default:
		a.addError(call.Pos, "sumOf requires an Int, Float or Decimal value")
	}
	return output
}

func typedCollectionMethod(name string) bool {
	return builtin.CollectionMethod(name) == builtin.CollectionLambda
}

func (a *Analyzer) collectionMethodNeedsInvocation(member *ast.MemberExpr) bool {
	kind := builtin.CollectionMethod(member.Field)
	return a.callTarget != member && (kind == builtin.CollectionLambda || kind == builtin.CollectionJoin)
}
