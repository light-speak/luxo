package codegen

import (
	"fmt"
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
)

func (c *compiler) compileSealedComparison(expression *ast.BinaryExpr, left, right string) (string, bool) {
	if expression.Op != "==" && expression.Op != "!=" || expression.Left.IsListType() || expression.Right.IsListType() {
		return "", false
	}
	owner := c.generator.sealedDeclaration(expression.Left.GetTypeTag())
	if owner == nil {
		owner = c.generator.sealedDeclaration(expression.Right.GetTypeTag())
	}
	if owner == nil {
		return "", false
	}
	var result string
	if isNullLiteral(expression.Left) {
		result = c.compileSealedNullComparison(expression.Right, right)
	} else if isNullLiteral(expression.Right) {
		result = c.compileSealedNullComparison(expression.Left, left)
	} else {
		if expression.Left.GetTypeTag() != owner.Name || expression.Right.GetTypeTag() != owner.Name || !c.generator.sealed.equality.comparable[owner.Name] {
			panic(&InternalCompilerError{Detail: "sealed equality requires compatible comparable payloads"})
		}
		if expression.Left.IsNullable() || expression.Right.IsNullable() {
			result = c.compileSealedNullableComparison(expression, left, right)
		} else {
			result = c.compileSealedEqualityCall(left, right)
		}
	}
	if expression.Op == "!=" {
		result = "!(" + result + ")"
	}
	return result, true
}

func (c *compiler) compileSealedEqualityCall(left, right string) string {
	c.resultTmp++
	name := "_sealedEqual" + strconv.Itoa(c.resultTmp)
	c.write("%s, %sErr := (%s).LuxoEqual(%s)", name, name, left, right)
	c.write("if %sErr != nil {", name)
	c.writeErrorReturn("\t", name+"Err")
	c.write("}")
	return name
}

func (c *compiler) rejectInvalidSealed(expression, guard string) {
	c.write("if %s!(%s).LuxoIsValid() {", guard, expression)
	c.writeErrorReturn("\t", `fmt.Errorf("invalid sealed value in equality")`)
	c.write("}")
}

func (c *compiler) compileSealedNullComparison(expression ast.Expr, code string) string {
	c.resultTmp++
	name := "_sealedNull" + strconv.Itoa(c.resultTmp)
	c.write("%s := %s", name, code)
	guard := ""
	if expression.IsNullable() {
		guard = name + " != nil && "
	}
	c.rejectInvalidSealed(name, guard)
	if expression.IsNullable() {
		return name + " == nil"
	}
	return "false"
}

func (c *compiler) compileSealedNullableComparison(expression *ast.BinaryExpr, left, right string) string {
	c.resultTmp++
	name := "_sealedOptional" + strconv.Itoa(c.resultTmp)
	l, r := name+"Left", name+"Right"
	c.write("%s := %s", l, left)
	c.write("%s := %s", r, right)
	c.write("var %s bool", name)
	condition, nilResult := "", "false"
	if expression.Left.IsNullable() {
		condition = l + " == nil"
		left = "*" + l
	} else {
		left = l
	}
	if expression.Right.IsNullable() {
		if condition != "" {
			condition += " || "
			nilResult = l + " == " + r
		}
		condition += r + " == nil"
		right = "*" + r
	} else {
		right = r
	}
	c.write("if %s {", condition)
	for _, value := range []struct {
		name     string
		nullable bool
	}{{l, expression.Left.IsNullable()}, {r, expression.Right.IsNullable()}} {
		guard := ""
		if value.nullable {
			guard = value.name + " != nil && "
		}
		c.rejectInvalidSealed(value.name, guard)
	}
	c.write("%s = %s", name, nilResult)
	c.write("} else {")
	result := c.compileSealedEqualityCall(left, right)
	c.write("%s = %s", name, result)
	c.write("}")
	return name
}

func (c *compiler) sealedWhenPatternCondition(expression *ast.WhenExpr, branch *ast.WhenBranch, subject string) (string, uint16) {
	if branch.IsType != "" {
		tag := c.generator.modelFieldID(expression.Subject.GetTypeTag(), branch.IsType)
		condition := fmt.Sprintf("%s.LuxoVariantTag() == %d", subject, tag)
		if expression.Subject.IsNullable() {
			condition = subject + " != nil && " + condition
		}
		return condition, uint16(tag)
	}
	if isNullLiteral(branch.Condition) {
		if expression.Subject.IsNullable() {
			return subject + " == nil", 0
		}
		return "false", 0
	}
	right := c.compileBooleanValue(branch.Condition)
	return c.compileWhenEquality(expression.Subject, branch.Condition, subject, right), 0
}

func (c *compiler) compileSealedWhenPatterns(expression *ast.WhenExpr, subject string) {
	for _, branch := range expression.Branches {
		condition, tag := c.sealedWhenPatternCondition(expression, branch, subject)
		c.write("if %s {", condition)
		c.indent += "\t"
		c.compileSealedWhenBranch(expression, branch, tag)
		c.indent = c.indent[:len(c.indent)-1]
		c.write("} else {")
		c.indent += "\t"
	}
	if expression.Else == nil {
		c.writeErrorReturn("", `fmt.Errorf("unmatched sealed variant")`)
	} else {
		c.compileValueBranch(expression.Else)
	}
	for range expression.Branches {
		c.indent = c.indent[:len(c.indent)-1]
		c.write("}")
	}
}
