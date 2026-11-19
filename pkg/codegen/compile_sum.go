package codegen

import "github.com/light-speak/luxo/pkg/ast"

// Evaluate and accumulate once per input element, preserving the selector type.
func (c *compiler) compileSumOf(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "sumOf" || !member.Object.IsListType() || len(call.Args) != 1 {
		return "", false
	}
	lambda, ok := call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return "", false
	}
	prefix := c.collectionPrefix(lambda)
	items, result := prefix+"Items", prefix+"Sum"
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.compileVal(&ast.ValStmt{Name: items, Value: member.Object})
	goType := c.goTypeForExpr(lambda)
	sub.write("var %s %s", result, goType)
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	goParameter := sub.bindings.name(parameter)
	sub.write("for _, %s := range %s {", goParameter, items)
	sub.indent += "\t"
	sub.write("_ = %s", goParameter)
	element, _ := c.valTypeFromExpr(member.Object)
	element.isList, element.nullable = false, false
	sub.vars[parameter] = element
	value := sub.compileCollectionValue(lambda)
	sub.write("%sValue := %s", prefix, value)
	sub.writeSumAddition(lambda.GetTypeTag(), result, prefix+"Value")
	sub.indent = c.indent + "\t"
	sub.write("}")
	sub.write("return %s", result)
	return c.finishValueClosure(sub, goType, sub.b.String()), true
}

func (c *compiler) writeSumAddition(kind, result, value string) {
	switch kind {
	case "Int":
		c.write("if (%s > 0 && %s > math.MaxInt64 - %s) || (%s < 0 && %s < math.MinInt64 - %s) {", value, result, value, value, result, value)
		c.writeErrorReturn("\t", `errors.BadRequest.WithData(&errors.ParamError{Param:"sumOf", Error:"Int64 sum overflow"})`)
		c.write("}")
		c.write("%s += %s", result, value)
	case "Float":
		c.write("%s += %s", result, value)
		c.write("if !codec.IsFiniteFloat(%s) {", result)
		c.writeErrorReturn("\t", "codec.ErrNonFiniteFloat")
		c.write("}")
	case "Decimal":
		addition := c.compileDecimalArithmetic(result, value, "Add")
		c.write("%s = %s", result, addition)
	default:
		panic(&InternalCompilerError{Detail: "sumOf requires a numeric selector"})
	}
}
