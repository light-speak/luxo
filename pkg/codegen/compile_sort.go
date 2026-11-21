package codegen

import "github.com/light-speak/luxo/pkg/ast"

// Decorate before sorting so key evaluation is ordered, fallible and exactly once.
func (c *compiler) compileSortBy(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "sortBy" || !member.Object.IsListType() || len(call.Args) != 1 {
		return "", false
	}
	lambda, ok := call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return "", false
	}
	prefix := c.collectionPrefix(lambda)
	items, entries, entry := prefix+"Items", prefix+"Entries", prefix+"Entry"
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.compileVal(&ast.ValStmt{Name: items, Value: member.Object})
	goType := c.goTypeForExpr(call)
	sub.write("type %s struct { value %s; key %s }", entry, goType[2:], c.goTypeForExpr(lambda))
	sub.write("%s := make([]%s, len(%s))", entries, entry, items)
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	goParameter := sub.bindings.name(parameter)
	sub.write("for %sIndex, %s := range %s {", prefix, goParameter, items)
	sub.indent += "\t"
	sub.write("_ = %s", goParameter)
	element, _ := c.valTypeFromExpr(member.Object)
	element.isList, element.nullable = false, false
	sub.vars[parameter] = element
	value := sub.compileSortKey(lambda, prefix)
	sub.write("%s[%sIndex] = %s{value: %s, key: %s}", entries, prefix, entry, goParameter, value)
	sub.indent = c.indent + "\t"
	sub.write("}")
	sub.write("slices.SortStableFunc(%s, func(a,b %s) int {", entries, entry)
	writeSortComparison(sub, lambda.GetTypeTag())
	sub.write("})")
	sub.write("%sResult := make(%s, len(%s))", prefix, goType, entries)
	sub.write("for i := range %s { %sResult[i] = %s[i].value }", entries, prefix, entries)
	sub.write("return %sResult", prefix)
	return c.finishValueClosure(sub, goType, sub.b.String()), true
}

func (c *compiler) compileSortKey(lambda *ast.LambdaExpr, prefix string) string {
	value := c.compileCollectionValue(lambda)
	if lambda.GetTypeTag() != "Float" {
		return value
	}
	c.write("%sKey := %s", prefix, value)
	value = prefix + "Key"
	c.write("if !codec.IsFiniteFloat(%s) {", value)
	c.writeErrorReturn("\t", "codec.ErrNonFiniteFloat")
	c.write("}")
	return value
}

func writeSortComparison(c *compiler, key string) {
	switch key {
	case "DateTime":
		c.write("return a.key.Compare(b.key)")
	case "Decimal":
		c.write("return lux.CompareDecimal(a.key, b.key)")
	default:
		c.write("if a.key < b.key { return -1 }; if a.key > b.key { return 1 }; return 0")
	}
}
