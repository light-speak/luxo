package codegen

import (
	"github.com/light-speak/luxo/pkg/ast"
	"strconv"
)

func (c *compiler) compileChannelValue(s *ast.ValStmt) bool {
	if !c.isChannelConstructor(s.Value) {
		return false
	}
	if s.Type == nil || s.Type.Name != "Channel" || len(s.Type.TypeArgs) != 1 {
		panic(&InternalCompilerError{Detail: "Channel constructor missing explicit element type"})
	}
	call := s.Value.(*ast.CallExpr)
	capacity := "0"
	if len(call.Args) > 0 {
		capacity = "_luxoCapacity" + s.Name
		c.write("%s := int64(%s)", capacity, c.compileExpr(call.Args[0].Value))
		c.write("if %s < 0 || uint64(%s) > uint64(^uint(0)>>1) {", capacity, capacity)
		c.writeErrorReturn("\t", "errors.BadRequest")
		c.write("}")
	}
	c.write("%s := make(%s, %s)", s.Name, compiledFunctionGoType(s.Type, c.models), capacity)
	c.vars[s.Name] = valType{isChan: true, name: "Channel", typeRef: s.Type}
	return true
}

func (c *compiler) channelTypeRef(expr ast.Expr) *ast.TypeRef {
	if name, ok := expr.(*ast.Ident); ok {
		return c.vars[name.Name].typeRef
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if name, ok := call.Func.(*ast.Ident); ok && c.functions[name.Name] != nil {
			return c.functions[name.Name].ReturnType
		}
	}
	if receive, ok := expr.(*ast.UnaryExpr); ok && receive.Op == "<-" {
		if ref := c.channelTypeRef(receive.Value); ref != nil && len(ref.TypeArgs) == 1 {
			return ref.TypeArgs[0]
		}
	}
	return nil
}

func (c *compiler) compileChannelSendValue(channel, value ast.Expr) string {
	ref := c.channelTypeRef(channel)
	if ref == nil || len(ref.TypeArgs) != 1 {
		return c.compileExpr(value)
	}
	expected := ref.TypeArgs[0]
	expression := c.compileFunctionArgument(value, expected)
	if !expected.Nullable || expected.IsList || value.IsNullable() || isNullLiteral(value) || c.models[expected.Name] != nil {
		return expression
	}
	c.resultTmp++
	name := "_channelValue" + strconv.Itoa(c.resultTmp)
	concrete := *expected
	concrete.Nullable = false
	c.write("var %s %s = %s", name, compiledFunctionGoType(&concrete, c.models), expression)
	return "&" + name
}
