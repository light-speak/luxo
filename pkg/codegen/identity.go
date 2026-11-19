package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// Conditions compare Duration values as nanoseconds, without runtime parsing.
func compileConditionLiteral(literal *ast.Literal) (string, bool) {
	switch literal.Kind {
	case token.Int:
		return decimalIntegerLiteral(literal.Value), true
	case token.Float:
		return compileNumericLiteral(literal), true
	case token.String:
		return fmt.Sprintf("%q", literal.Value), true
	case token.Duration:
		value, ok := compileDurationLiteral(literal.Value)
		if !ok {
			return "", false
		}
		return "int64(" + value + ")", true
	default:
		return literal.Value, true
	}
}

// Preserve the AST grouping when emitting nested condition expressions.
func groupBinaryOperand(expr ast.Expr, compiled string) string {
	if _, nested := expr.(*ast.BinaryExpr); nested {
		return "(" + compiled + ")"
	}
	return compiled
}

// compileMyField compiles my.field access to Go identity method call.
// identityExpr is the Go expression for the identity object (e.g., "identity", "buf.Identity").
func compileMyField(member *ast.MemberExpr, identityExpr string) string {
	if identityExpr == "_identity" {
		method := "Int"
		switch member.GetTypeTag() {
		case "String":
			method = "String"
		case "Boolean":
			method = "Bool"
		case "Float":
			method = "Float"
		}
		return fmt.Sprintf("_identity.%s(%q)", method, member.Field)
	}

	field := member.Field
	if field == "id" {
		return fmt.Sprintf("api.IdentityID(%s)", identityExpr)
	}
	if member.GetTypeTag() == "String" {
		return fmt.Sprintf("api.IdentityString(%s, %q)", identityExpr, field)
	}
	return fmt.Sprintf("api.IdentityInt(%s, %q)", identityExpr, field)
}

// inferMyFieldType prefers the resolved identity type. Literal inference is
// retained for untyped compiler fragments, not for analyzed field accesses.
// Returns the updated compiled string.
func inferMyFieldType(compiled string, self ast.Expr, other ast.Expr, identityExpr string) string {
	me, ok := self.(*ast.MemberExpr)
	if !ok {
		return compiled
	}
	ident, ok := me.Object.(*ast.Ident)
	if !ok || ident.Name != "my" || me.Field == "id" {
		return compiled
	}
	if identityExpr == "_identity" && (me.GetTypeTag() == "Float" || me.GetTypeTag() == "Boolean") {
		return compiled
	}
	if me.GetTypeTag() == "String" {
		return compiled
	}
	if lit, ok := other.(*ast.Literal); ok {
		if lit.Kind == token.String {
			if identityExpr == "_identity" {
				return fmt.Sprintf("_identity.String(%q)", me.Field)
			}
			return fmt.Sprintf("api.IdentityString(%s, %q)", identityExpr, me.Field)
		}
		if lit.Kind == token.Int || lit.Kind == token.Float {
			if identityExpr == "_identity" {
				return fmt.Sprintf("_identity.Int(%q)", me.Field)
			}
			return fmt.Sprintf("api.IdentityInt(%s, %q)", identityExpr, me.Field)
		}
	}
	return compiled
}

func conditionUsesIdentity(expr ast.Expr) bool {
	found := false
	walkExpr(expr, func(value ast.Expr) {
		member, ok := value.(*ast.MemberExpr)
		if !ok {
			return
		}
		ident, ok := member.Object.(*ast.Ident)
		if ok && ident.Name == "my" {
			found = true
		}
	})
	return found
}

// Emit the checked read at the expression's evaluation point. The compiler's
// existing logical/closure lowering keeps it inside the correct branch.
func (c *compiler) compileIdentityField(member *ast.MemberExpr) string {
	claim := fmt.Sprintf("identity.Get(%q)", member.Field)
	if !member.IsNullable() {
		return c.compileIdentityValue(member, claim)
	}
	c.resultTmp++
	name := fmt.Sprintf("_nullableIdentity%d", c.resultTmp)
	goType := resolveGoType(&ast.TypeRef{Name: member.GetTypeTag(), IsList: member.IsListType(), Nullable: true})
	model := c.models[member.GetTypeTag()] != nil
	if model && member.IsListType() {
		goType = "[]*" + member.GetTypeTag()
	}
	c.write("var %s %s", name, goType)
	// Only explicit null is optional. Missing or malformed claims still use
	// the checked reader, at the expression's original evaluation point.
	c.write("%sClaim := %s", name, claim)
	c.write("if %sClaim.Raw() != \"null\" {", name)
	value := c.compileIdentityValue(member, name+"Claim")
	if !member.IsListType() && !model {
		local := name + "Value"
		c.write("%s := %s", local, value)
		value = "&" + local
	}
	c.write("%s = %s", name, value)
	c.write("}")
	return name
}

func (c *compiler) compileIdentityValue(member *ast.MemberExpr, claim string) string {
	if c.models[member.GetTypeTag()] != nil || c.isTypeDecl(member.GetTypeTag()) {
		return c.compileIdentityObject(member, claim)
	}
	if member.IsListType() && c.enums[member.GetTypeTag()] {
		return c.compileIdentityEnumArray(member, claim)
	}
	if reader := identityValueReader(member); reader != "" {
		return c.compileCheckedIdentityRead(reader, claim)
	}

	method := "String"
	switch member.GetTypeTag() {
	case "Int", "Duration":
		method = "Int64"
	case "Float":
		method = "Float64"
	case "Boolean":
		method = "Bool"
	}
	if member.Field == "id" && member.GetTypeTag() == "" {
		method = "Int64"
	}
	c.resultTmp++
	value := fmt.Sprintf("_identityValue%d", c.resultTmp)
	valid := fmt.Sprintf("_identityValid%d", c.resultTmp)
	c.write("%s, %s := %s.%s()", value, valid, claim, method)
	if tag := member.GetTypeTag(); tag != "" && c.enums[tag] {
		c.write("if !%s || !%s(%s).IsValid() {", valid, tag, value)
	} else {
		c.write("if !%s {", valid)
	}
	c.writeErrorReturn("\t", "errors.Unauthorized")
	c.write("}")
	if tag := member.GetTypeTag(); tag != "" && c.enums[tag] {
		return tag + "(" + value + ")"
	}
	return value
}

func (c *compiler) compileCheckedIdentityRead(reader, claim string) string {
	c.resultTmp++
	value := fmt.Sprintf("_identityValue%d", c.resultTmp)
	failure := fmt.Sprintf("_identityError%d", c.resultTmp)
	c.write("%s, %s := api.%s(%s)", value, failure, reader, claim)
	c.write("if %s != nil {", failure)
	c.writeErrorReturn("\t", "errors.Unauthorized")
	c.write("}")
	return value
}

func (c *compiler) compileIdentityEnumArray(member *ast.MemberExpr, claim string) string {
	reader := "ReadIdentityEnumArray[" + member.GetTypeTag() + "]"
	value := c.compileCheckedIdentityRead(reader, claim)
	item := fmt.Sprintf("_identityItem%d", c.resultTmp)
	c.write("for _, %s := range %s {", item, value)
	c.write("\tif !%s.IsValid() {", item)
	c.writeErrorReturn("\t\t", "errors.Unauthorized")
	c.write("\t}")
	c.write("}")
	return value
}

func identityValueReader(member *ast.MemberExpr) string {
	if member.IsListType() {
		switch member.GetTypeTag() {
		case "Int", "String", "Boolean", "Float", "Duration", "DateTime", "UUID", "Decimal", "Bytes", "JSON":
			return "ReadIdentity" + member.GetTypeTag() + "Array"
		}
		return ""
	}
	switch member.GetTypeTag() {
	case "Duration", "DateTime", "UUID", "Decimal", "Bytes", "JSON":
		return "ReadIdentity" + member.GetTypeTag()
	}
	return ""
}

// Validate at the read site so short-circuited branches do not access claims.
func compileIdentityEnumCondition(member *ast.MemberExpr, enumType string) string {
	return fmt.Sprintf("func() string { value := _identity.String(%q); return _identity.ValidatedString(value, %s(value).IsValid()) }()", member.Field, enumType)
}

func (c *compiler) compileIdentityObject(member *ast.MemberExpr, claim string) string {
	c.resultTmp++
	value := fmt.Sprintf("_identityValue%d", c.resultTmp)
	failure := fmt.Sprintf("_identityError%d", c.resultTmp)
	name := member.GetTypeTag()
	if member.IsListType() {
		c.write("%s, %s := readIdentity%sList([]byte(%s.Raw()))", value, failure, name, claim)
	} else {
		c.write("var %s %s", value, name)
		c.write("%s := %s.readIdentityInput([]byte(%s.Raw()),0)", failure, value, claim)
	}
	c.write("if %s != nil {", failure)
	c.writeErrorReturn("\t", "errors.Unauthorized")
	c.write("}")
	if !member.IsListType() && c.models[name] != nil {
		return "(&" + value + ")"
	}
	return value
}
