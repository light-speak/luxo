package semantic

import (
	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

func (a *Analyzer) checkStringCallArguments(call *ast.CallExpr, argumentTypes []*ResolvedType) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Object.IsListType() || member.Object.GetTypeTag() != "String" {
		return
	}
	signature, found := builtin.StringMethod(member.Field)
	if !found {
		return
	}
	positions, issue := signature.Bind(call.Args)
	if issue.Kind != "" {
		a.addError(call.Pos, "String.%s has %s argument %q (expects %d..%d arguments) / String.%s 参数绑定无效", member.Field, issue.Kind, issue.Name, signature.MinArgs, signature.MaxArgs, member.Field)
		return
	}
	for position, index := range positions {
		if index < 0 {
			continue
		}
		argument := call.Args[index]
		actual := argumentTypes[index]
		_, lambda := argument.Value.(*ast.LambdaExpr)
		if actual == nil && !lambda {
			continue
		}
		expected := signature.Parameters[position].Type
		if lambda || actual.Name != expected || actual.IsList || actual.Nullable {
			a.addError(argument.Value.GetPos(), "String.%s argument %d requires non-null %s / String.%s 第 %d 个参数必须为非空 %s", member.Field, position+1, expected, member.Field, position+1, expected)
		}
	}
}
