package codegen

import (
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

func (c *compiler) checkCollectionCallBoundary(call *ast.CallExpr) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || !member.Object.IsListType() {
		return
	}
	kind := builtin.CollectionMethod(member.Field)
	if kind == builtin.NotCollectionMethod || kind == builtin.CollectionProperty && len(call.Args) != 0 {
		panic(&InternalCompilerError{Detail: "unsupported list call: " + member.Field})
	}
}

// Properties and their existing zero-argument aliases share one materialized receiver.
func (c *compiler) compileLengthMember(member *ast.MemberExpr) string {
	switch member.Field {
	case "size", "isEmpty":
		if !member.Object.IsListType() && member.Object.GetTypeTag() != "String" {
			return ""
		}
	case "length":
		if member.Object.IsListType() || member.Object.GetTypeTag() != "String" {
			return ""
		}
	default:
		return ""
	}
	return compileStringQuery(member.Field, func() string {
		return c.compileMaterializedValue(member.Object, nil)
	}, nil, 0)
}

func isValueCollectionCall(call *ast.CallExpr) bool {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || !member.Object.IsListType() {
		return false
	}
	switch member.Field {
	case "map", "filter", "any", "sortBy", "sumOf", "joinToString":
		return true
	default:
		return false
	}
}

func (c *compiler) compileJoinToString(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "joinToString" || !member.Object.IsListType() {
		return "", false
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value.GetTypeTag() != "String" || call.Args[0].Value.IsNullable() || call.Args[0].Value.IsListType() {
		panic(&InternalCompilerError{Detail: "joinToString requires one non-null String separator"})
	}
	element, ok := c.valTypeFromExpr(member.Object)
	if !ok || !c.joinScalar(element.name) {
		panic(&InternalCompilerError{Detail: "joinToString requires scalar or enum elements"})
	}
	prefix := c.collectionPrefix(&ast.LambdaExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: call}}}})
	items, separator, builder, item := prefix+"Items", prefix+"Separator", prefix+"Builder", prefix+"Item"
	sub := c.valueCompiler()
	sub.indent += "\t"
	sub.compileVal(&ast.ValStmt{Name: items, Value: member.Object})
	sub.compileVal(&ast.ValStmt{Name: separator, Value: call.Args[0].Value})
	if element.name == "String" {
		sub.write("return strings.Join(%s, %s)", items, separator)
		return c.finishValueClosure(sub, "string", sub.b.String()), true
	}
	sub.write("var %s strings.Builder", builder)
	sub.write("for %sIndex, %s := range %s {", prefix, item, items)
	sub.indent += "\t"
	sub.write("if %sIndex != 0 { %s.WriteString(%s) }", prefix, builder, separator)
	element.isList, element.nullable = false, false
	sub.vars[item] = element
	value := &ast.Ident{Name: item}
	value.SetTypeTag(element.name)
	part := sub.prepareTemplatePart(value, 0)
	method := "WriteString"
	if part.numeric {
		method = "Write"
	}
	sub.write("%s.%s(%s)", builder, method, part.value)
	sub.indent = c.indent + "\t"
	sub.write("}")
	sub.write("return %s.String()", builder)
	return c.finishValueClosure(sub, "string", sub.b.String()), true
}

func (c *compiler) joinScalar(name string) bool {
	switch name {
	case "Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Decimal":
		return true
	default:
		return c.enums[name]
	}
}

func (c *compiler) compileCollectionCall(call *ast.CallExpr) (string, bool) {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "map" && member.Field != "filter" && member.Field != "any" || !member.Object.IsListType() || len(call.Args) != 1 {
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
	} else if member.Field == "filter" {
		sub.write("var %s %s", result, goType)
	}
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	goParameter := sub.bindings.name(parameter)
	sub.write("for %s, %s := range %s {", index, goParameter, items)
	sub.indent += "\t"
	sub.write("_ = %s", goParameter)
	element, _ := c.valTypeFromExpr(member.Object)
	element.isList = false
	element.nullable = false
	sub.vars[parameter] = element
	value := sub.compileCollectionValue(lambda)
	if member.Field == "map" {
		sub.write("%s[%s] = %s", result, index, value)
	} else if member.Field == "filter" {
		sub.write("if %s { %s = append(%s, %s) }", value, result, result, goParameter)
	} else {
		sub.write("if %s { return true }", value)
	}
	sub.indent = c.indent + "\t"
	sub.write("}")
	if member.Field == "any" {
		sub.write("return false")
	} else {
		sub.write("return %s", result)
	}
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

func (c *compiler) compileForEachStatement(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.Field != "forEach" || !member.Object.IsListType() || len(call.Args) != 1 {
		return false
	}
	lambda, ok := call.Args[0].Value.(*ast.LambdaExpr)
	if !ok {
		return false
	}
	parameter := "it"
	if len(lambda.Params) != 0 {
		parameter = lambda.Params[0]
	}
	items := c.collectionPrefix(lambda) + "Items"
	c.compileVal(&ast.ValStmt{Name: items, Value: member.Object})
	goParameter := c.bindings.name(parameter)
	c.write("for _, %s := range %s {", goParameter, items)
	c.write("\t_ = %s", goParameter)
	sub := c.errorCompiler()
	sub.indent += "\t\t"
	element, _ := c.valTypeFromExpr(member.Object)
	element.isList = false
	element.nullable = false
	sub.vars[parameter] = element
	for _, statement := range lambda.Body.Stmts {
		sub.compileStmt(statement)
	}
	c.resultTmp = max(c.resultTmp, sub.resultTmp)
	c.write("\tif err := func() error {")
	c.b.WriteString(sub.b.String())
	c.write("\t\treturn nil")
	c.write("\t}(); err != nil {")
	c.writeErrorReturn("\t\t", "err")
	c.write("\t}")
	c.write("}")
	return true
}
