package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func (c *compiler) authMethod(call *ast.CallExpr) *ast.MemberExpr {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || (member.Field != "createToken" && member.Field != "createRefreshToken" && member.Field != "refreshToken") {
		return nil
	}
	value, known := c.valTypeFromExpr(member.Object)
	if !known || value.isList {
		return nil
	}
	model := c.models[value.name]
	if model == nil || !hasDirective(model.Directives, "withAuth") {
		return nil
	}
	return member
}

func (c *compiler) compileAuthMethod(call *ast.CallExpr) (string, bool) {
	member := c.authMethod(call)
	if member == nil {
		return "", false
	}
	object := c.compileExpr(member.Object)
	args := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		args = append(args, c.compileExpr(arg.Value))
	}
	c.resultTmp++
	result := fmt.Sprintf("_result%d", c.resultTmp)
	method := str.Capitalize(member.Field)
	if member.Field == "createToken" && len(args) == 1 {
		method = "CreateTokenWithExpiry"
	}
	c.write("%s, err := %s.%s(%s)", result, object, method, strings.Join(args, ", "))
	c.writeErrorGuard()
	return result, true
}
