package semantic

import (
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func channelConstructor(expr ast.Expr) (*ast.CallExpr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	name, ok := call.Func.(*ast.Ident)
	return call, ok && name.Name == "Channel"
}

func channelElement(typ *ResolvedType) *ResolvedType {
	if typ == nil || typ.Name != "Channel" || typ.Nullable || typ.IsList || len(typ.TypeArgs) != 1 {
		return nil
	}
	return typ.TypeArgs[0]
}

func (a *Analyzer) checkChannelInitializer(s *ast.ValStmt, scope *Scope) *ResolvedType {
	call, _ := channelConstructor(s.Value)
	typ := a.resolveTypeRef(s.Type, s.Pos)
	if channelElement(typ) == nil {
		a.addError(s.Pos, "Channel requires an explicit Channel<T> declaration / Channel 必须显式声明 Channel<T> 元素类型")
	}
	if len(call.Args) > 1 {
		a.addError(call.Pos, "Channel accepts at most one capacity / Channel 最多接受一个容量参数")
	}
	for _, arg := range call.Args {
		capacity := a.checkExpr(arg.Value, scope)
		if capacity == nil || capacity.Kind != TypeInt || capacity.Nullable || capacity.IsList || arg.Name != "" {
			a.addError(arg.Value.GetPos(), "Channel capacity must be a non-null Int / Channel 容量必须是非空 Int")
		}
		if unary, ok := arg.Value.(*ast.UnaryExpr); ok && unary.Op == "-" {
			if literal, ok := unary.Value.(*ast.Literal); ok && literal.Kind == token.Int && literal.Value != "0" {
				a.addError(unary.Pos, "Channel capacity must not be negative / Channel 容量不能为负数")
			}
		}
	}
	setExprResolvedType(call, typ)
	return typ
}

func (a *Analyzer) checkChannelCall(call *ast.CallExpr, scope *Scope) (*ResolvedType, bool) {
	if _, ok := channelConstructor(call); ok {
		a.addError(call.Pos, "Channel requires an explicit Channel<T> declaration / Channel 必须显式声明 Channel<T> 元素类型")
		return nil, true
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "close" {
		return nil, false
	}
	if channelElement(a.checkExpr(member.Object, scope)) == nil {
		return nil, false
	}
	if len(call.Args) != 0 {
		a.addError(call.Pos, "Channel.close accepts no arguments / Channel.close 不接受参数")
	}
	return &ResolvedType{Kind: TypeVoid, Name: "Void"}, true
}

func (a *Analyzer) checkChannelReceive(typ *ResolvedType, pos token.Position) *ResolvedType {
	if element := channelElement(typ); element != nil {
		return element
	}
	a.addError(pos, "receive requires Channel<T> / 接收操作需要 Channel<T>")
	return nil
}

func (a *Analyzer) checkChannelSend(channel, value *ResolvedType, pos token.Position) *ResolvedType {
	element := channelElement(channel)
	if element == nil {
		a.addError(pos, "send requires Channel<T> / 发送操作需要 Channel<T>")
	} else if value == nil || !isTypeAssignable(element, value) {
		a.addError(pos, "channel element type mismatch / 通道发送值与元素类型不匹配")
	}
	return &ResolvedType{Kind: TypeVoid, Name: "Void"}
}

func isChannelSelect(expr *ast.WhenExpr) bool {
	if expr.Subject != nil {
		return false
	}
	for _, branch := range expr.Branches {
		if receive, ok := branch.Condition.(*ast.UnaryExpr); ok && receive.Op == "<-" {
			return true
		}
	}
	return false
}

func (a *Analyzer) checkChannelSelect(expr *ast.WhenExpr, scope *Scope) *ResolvedType {
	var result *ResolvedType
	for _, branch := range expr.Branches {
		receive, ok := branch.Condition.(*ast.UnaryExpr)
		if !ok || receive.Op != "<-" {
			a.addError(expr.Pos, "channel select requires receive branches / 通道多路等待只接受接收分支")
			continue
		}
		element := a.checkExpr(receive, scope)
		body := a.checkChannelBranch(branch.Body, element, scope)
		result = a.mergeWhenBranchType(result, body, expr.Pos)
	}
	if expr.Else != nil {
		result = a.mergeWhenBranchType(result, a.checkExpr(expr.Else, scope), expr.Pos)
	}
	return result
}

func (a *Analyzer) checkChannelBranch(expr ast.Expr, element *ResolvedType, scope *Scope) *ResolvedType {
	lambda, ok := expr.(*ast.LambdaExpr)
	if !ok {
		return a.checkExpr(expr, scope)
	}
	if len(lambda.Params) > 1 {
		a.addError(lambda.Pos, "receive branch accepts at most one value / 接收分支最多接受一个值参数")
	}
	child := scope.Child()
	if len(lambda.Params) == 1 {
		child.Define(&Symbol{Name: lambda.Params[0], Kind: SymVariable, Type: element})
	}
	a.checkBlock(lambda.Body, child)
	result := a.resolvedExprType(blockResultExpr(lambda.Body))
	if result != nil {
		setExprResolvedType(lambda, result)
	}
	return result
}
