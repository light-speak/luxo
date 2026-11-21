package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func (c *compiler) sealedWhenOwner(expression *ast.WhenExpr) *ast.SealedDecl {
	if expression.Subject == nil || expression.Subject.IsListType() {
		return nil
	}
	return c.generator.sealedDeclaration(expression.Subject.GetTypeTag())
}

func (c *compiler) compileSealedWhenValue(expression *ast.WhenExpr) (string, bool) {
	if c.sealedWhenOwner(expression) == nil {
		return "", false
	}
	sub := c.branchValueCompiler(expression)
	sub.indent += "\t"
	sub.resultTmp++
	subject := "_sealedSubject" + strconv.Itoa(sub.resultTmp)
	sub.write("%s := %s", subject, sub.compileExpr(expression.Subject))
	sub.compileSealedWhenBranches(expression, subject)
	return c.finishValueClosure(sub, c.inferWhenReturnType(expression), sub.b.String()), true
}

func (c *compiler) compileSealedWhenBranches(expression *ast.WhenExpr, subject string) {
	owner := c.sealedWhenOwner(expression)
	guard := "!" + subject + ".LuxoIsValid()"
	if expression.Subject.IsNullable() {
		guard = subject + " != nil && (" + guard + ")"
	}
	c.write("if %s {", guard)
	c.writeErrorReturn("\t", fmt.Sprintf("fmt.Errorf(%q)", owner.Name+": invalid sealed value"))
	c.write("}")
	for _, branch := range expression.Branches {
		if branch.IsType == "" && !isNullLiteral(branch.Condition) {
			c.compileSealedWhenPatterns(expression, subject)
			return
		}
	}
	c.write("switch {")
	for _, branch := range expression.Branches {
		tag := 0
		condition := "false"
		if expression.Subject.IsNullable() {
			condition = subject + " == nil"
		}
		if branch.IsType != "" {
			tag = c.generator.modelFieldID(owner.Name, branch.IsType)
			condition = subject + ".LuxoVariantTag() == " + strconv.Itoa(tag)
			if expression.Subject.IsNullable() {
				condition = subject + " != nil && " + condition
			}
		}
		c.write("case %s:", condition)
		c.indent += "\t"
		c.compileSealedWhenBranch(expression, branch, uint16(tag))
		c.indent = strings.TrimSuffix(c.indent, "\t")
	}
	c.write("default:")
	c.indent += "\t"
	if expression.Else == nil {
		c.writeErrorReturn("", fmt.Sprintf("fmt.Errorf(%q)", owner.Name+": unmatched sealed variant"))
	} else {
		c.compileValueBranch(expression.Else)
	}
	c.indent = strings.TrimSuffix(c.indent, "\t")
	c.write("}")
}

func (c *compiler) compileSealedWhenBranch(expression *ast.WhenExpr, branch *ast.WhenBranch, tag uint16) {
	ident, bound := expression.Subject.(*ast.Ident)
	if !bound || tag == 0 {
		c.compileValueBranch(branch.Body)
		return
	}
	previous := c.saveBinding(ident.Name)
	narrowed := previous.value
	narrowed.sealedTag = tag
	c.vars[ident.Name] = narrowed
	c.compileValueBranch(branch.Body)
	c.restoreBinding(previous)
}

func (c *compiler) compileSealedMember(expression *ast.MemberExpr) (string, bool) {
	if c.generator == nil || c.generator.sealed == nil {
		return "", false
	}
	ident, ok := expression.Object.(*ast.Ident)
	if !ok {
		return "", false
	}
	value := c.vars[ident.Name]
	if value.sealedTag == 0 {
		return "", false
	}
	owner := c.generator.sealedDeclaration(value.name)
	if owner == nil {
		panic(&InternalCompilerError{Detail: "sealed payload projection owner metadata is missing"})
	}
	for _, variant := range owner.Variants {
		if c.generator.modelFieldID(owner.Name, variant.Name) != int(value.sealedTag) {
			continue
		}
		id := c.generator.modelFieldID(owner.Name+"."+variant.Name, expression.Field)
		if id != 0 {
			return "(" + c.compileExpr(expression.Object) + ").AsVariant" + strconv.Itoa(int(value.sealedTag)) + "().F" + strconv.Itoa(id), true
		}
	}
	panic(&InternalCompilerError{Detail: "sealed payload projection field metadata is missing"})
}

// Payload value slots copy with their holder. Deeper ordinary object fields,
// nullable objects, models and indexed containers retain their existing aliases.
func (c *compiler) prepareSealedAssignment(target ast.Expr) {
	member, ok := target.(*ast.MemberExpr)
	if !ok {
		return
	}
	payload, ok := member.Object.(*ast.MemberExpr)
	if !ok {
		return
	}
	ident, ok := payload.Object.(*ast.Ident)
	if !ok {
		return
	}
	value := c.vars[ident.Name]
	if value.sealedTag == 0 {
		return
	}
	owner := c.generator.sealedDeclaration(value.name)
	if owner == nil {
		return
	}
	for _, variant := range owner.Variants {
		if c.generator.modelFieldID(owner.Name, variant.Name) != int(value.sealedTag) {
			continue
		}
		for _, field := range variant.Fields {
			if field.Name == payload.Field && !field.Type.Nullable && !field.Type.IsList && c.generator.typeDeclarations[field.Type.Name] != nil {
				receiver := c.compileExpr(ident)
				c.write("%s = (%s).LuxoCopyVariant%d()", receiver, receiver, value.sealedTag)
				return
			}
		}
	}
}
