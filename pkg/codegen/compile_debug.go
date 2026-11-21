package codegen

import (
	"strconv"

	"github.com/light-speak/luxo/internal/builtin"
	"github.com/light-speak/luxo/pkg/ast"
)

// Safe access to a literal null never evaluates a diagnostic or needs a Go type.
func isNullDebugChain(expr ast.Expr) bool {
	member, ok := expr.(*ast.MemberExpr)
	return ok && member.SafeCall && builtin.IsDebugProperty(member) &&
		(isNullLiteral(member.Object) || isNullDebugChain(member.Object))
}

func (c *compiler) compileDebugMember(member *ast.MemberExpr, expected *ast.TypeRef) (string, bool) {
	if !builtin.IsDebugProperty(member) {
		return "", false
	}
	method := builtin.DebugMethod(member.Field)
	if isLogTarget(member.Object) {
		return "luxolog." + method + "(" + c.compileMaterializedValue(member.Object, nil) + ")", true
	}
	valueType, known := c.valTypeFromExpr(member.Object)
	if !known {
		if expected == nil || !expected.IsList || !isEmptyDebugList(member.Object) {
			panic(&InternalCompilerError{Detail: "debug receiver has no static type"})
		}
		valueType = valType{name: expected.Name, isList: true, typeRef: expected}
	}
	ref := &ast.TypeRef{Name: valueType.name, IsList: valueType.isList, Nullable: valueType.nullable}
	if valueType.typeRef != nil {
		ref.TypeArgs = valueType.typeRef.TypeArgs
	}
	value := c.compileMaterializedValue(member.Object, ref)
	c.resultTmp++
	name := "_debugValue" + strconv.Itoa(c.resultTmp)
	c.write("var %s %s = %s", name, compiledFunctionGoType(ref, c.models), value)
	c.write("if luxolog.GetLevel() <= luxolog.Level%s {", method)
	c.write("\t_debugRecord := luxolog.NewRecord()")
	formatter := c.debugFormatter()
	formatter.writeValue(c.b, ref, name, "_debugRecord", "1", c.indent+"\t", c.models[ref.Name] != nil, true)
	c.write("\t_debugRecord.Write%s()", method)
	c.write("}")
	return name, true
}

func (c *compiler) debugFormatter() *debugLogGenerator {
	owner := c.generator.asyncSnapshotOwner
	if owner == nil {
		owner = c.generator
	}
	if owner.debugLogs == nil {
		enums := c.enums
		if owner.asyncSource != nil {
			enums = owner.allEnums(CollectEnumsFromResult(owner.asyncSource))
		}
		owner.debugLogs = &debugLogGenerator{owner: owner, source: owner.asyncSource, enums: enums}
	}
	return owner.debugLogs
}
