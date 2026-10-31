package codegen

import (
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func (c *compiler) compileCollectionCall(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "map" && member.Field != "filter" || !member.Object.IsListType() || len(call.Args) != 1 {
		return "", false
	}
	lambda, ok := call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return "", false
	}
	prefix := c.collectionPrefix(lambda)
	items, result := prefix+"Items", prefix+"Result"
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.compileVal(&ast.ValStmt{Name: items, Value: member.Object})
	goType := c.goTypeForExpr(call)
	index := "_"
	if member.Field == "map" {
		index = prefix + "Index"
		sub.write("%s := make(%s, len(%s))", result, goType, items)
	} else {
		sub.write("var %s %s", result, goType)
	}
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	sub.write("for %s, %s := range %s {", index, parameter, items)
	sub.indent += "\t"
	sub.write("_ = %s", parameter)
	element, _ := c.valTypeFromExpr(member.Object)
	element.isList = false
	sub.vars[parameter] = element
	value := sub.compileCollectionValue(lambda)
	if member.Field == "map" {
		sub.write("%s[%s] = %s", result, index, value)
	} else {
		sub.write("if %s { %s = append(%s, %s) }", value, result, result, parameter)
	}
	sub.indent = c.indent + "\t"
	sub.write("}")
	sub.write("return %s", result)
	return c.finishValueClosure(sub, goType, sub.b.String()), true
}

// Keep generated loop state separate from captured names and nested parameters.
func (c *compiler) collectionPrefix(lambda *ast.LambdaExpr) string {
	for {
		c.resultTmp++
		prefix := "_collection" + strconv.Itoa(c.resultTmp)
		conflict := false
		ast.WalkExpr(lambda, func(expr ast.Expr) {
			switch value := expr.(type) {
			case *ast.Ident:
				conflict = conflict || strings.HasPrefix(value.Name, prefix)
			case *ast.LambdaExpr:
				for _, name := range value.Params {
					conflict = conflict || strings.HasPrefix(name, prefix)
				}
			}
		})
		if !conflict {
			return prefix
		}
	}
}

func (c *compiler) compileCollectionValue(lambda *ast.LambdaExpr) string {
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.valueModel = c.models[lambda.GetTypeTag()] != nil
	sub.compileValueBranch(lambda)
	return c.finishValueClosure(sub, c.goTypeForExpr(lambda), sub.b.String())
}
