package codegen

import (
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
)

type selectReceive struct {
	channel string
	element valType
}

// Capture multiple operands before later receivers can change earlier values.
func (c *compiler) prepareSelectReceive(condition ast.Expr, capture bool) selectReceive {
	receive, valid := condition.(*ast.UnaryExpr)
	if !valid || receive.Op != "<-" {
		panic(&InternalCompilerError{Detail: "channel receive binding requires a receive expression"})
	}
	element := c.loopElementType(receive.Value)
	value := c.compileExpr(receive.Value)
	if capture {
		c.resultTmp++
		name := "_selectChannel" + strconv.Itoa(c.resultTmp)
		c.write("%s := %s", name, value)
		value = name
	}
	return selectReceive{channel: value, element: element}
}

func (c *compiler) compileSelectBranch(branch *ast.WhenBranch, receive selectReceive) {
	binding := ""
	var previous lexicalBinding
	if lambda, ok := branch.Body.(*ast.LambdaExpr); ok && len(lambda.Params) > 0 {
		name := lambda.Params[0]
		previous = c.saveBinding(name)
		c.vars[name] = receive.element
		delete(c.paginationTotals, name)
		binding = c.bindings.name(name)
		c.write("case %s := <-%s:", binding, receive.channel)
	} else {
		c.write("case <-%s:", receive.channel)
	}
	indent := c.indent
	c.indent += "\t"
	if binding != "" {
		c.write("_ = %s", binding)
	}
	c.compileValueBranch(branch.Body)
	if binding != "" {
		c.restoreBinding(previous)
	}
	c.indent = indent
}

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
	c.write("%s := make(%s, %s)", c.bindings.name(s.Name), compiledFunctionGoType(s.Type, c.models), capacity)
	c.vars[s.Name] = valType{isChan: true, name: "Channel", typeRef: s.Type}
	return true
}

func (c *compiler) channelTypeRef(expr ast.Expr) *ast.TypeRef {
	if ref := c.genericTypeRef(expr); ref != nil {
		return ref
	}
	if value, ok := unwrapQuestion(expr); ok {
		return unwrapResultType(c.channelTypeRef(value))
	}
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

func (c *compiler) genericTypeRef(expr ast.Expr) *ast.TypeRef {
	if c.generator == nil {
		return nil
	}
	return c.generator.genericExprTypes[expr]
}

func (c *compiler) compileChannelSendValue(channel, value ast.Expr) string {
	ref := c.channelTypeRef(channel)
	if ref == nil || len(ref.TypeArgs) != 1 {
		return c.compileExpr(value)
	}
	return c.compileAssignedValue(value, ref.TypeArgs[0])
}

// Channel iteration binds its declared payload, including nullability and nested types.
func (c *compiler) loopElementType(collection ast.Expr) valType {
	if channel := c.channelTypeRef(collection); channel != nil && channel.Name == "Channel" && !channel.IsList && len(channel.TypeArgs) == 1 {
		element := channel.TypeArgs[0]
		return valType{
			name: element.Name, typeRef: element, isList: element.IsList, nullable: element.Nullable,
			isChan:  element.Name == "Channel" && !element.IsList,
			isModel: c.models[element.Name] != nil,
		}
	}
	item, _ := c.valTypeFromExpr(collection)
	item.isList, item.nullable = false, false
	return item
}
