package codegen

import (
	"fmt"
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

type compiledTemplatePart struct {
	value   string
	numeric bool
}

// Semantic analysis permits only non-null scalar and enum values here.
// Resolve the expression result, not the receiver of a method call.
func (c *compiler) prepareTemplatePart(part ast.Expr, index int) compiledTemplatePart {
	if literal, ok := part.(*ast.Literal); ok && literal.Kind == token.String {
		return compiledTemplatePart{value: fmt.Sprintf("%q", literal.Value)}
	}
	kind := part.GetTypeTag()
	if kind == "" {
		// Unanalyzed source-fragment tests can still carry explicit bindings.
		if value, ok := c.valTypeFromExpr(part); ok {
			kind = value.name
		}
	}
	value := c.compileExpr(part)
	suffix := strconv.Itoa(index)
	name := "_templatePart" + suffix
	switch kind {
	case "Int", "Float":
		buffer := "_templateNumber" + suffix
		c.write("var %s [32]byte", buffer)
		if kind == "Int" {
			c.write("%s := strconv.AppendInt(%s[:0], int64(%s), 10)", name, buffer, value)
		} else {
			c.write("%s := strconv.AppendFloat(%s[:0], float64(%s), 'g', -1, 64)", name, buffer, value)
		}
		return compiledTemplatePart{value: name, numeric: true}
	case "Boolean":
		value = "strconv.FormatBool(" + value + ")"
	case "DateTime", "Duration", "UUID", "Decimal":
		value = "(" + value + ").String()"
	case "String":
	default:
		// All remaining semantically valid values are string-backed enums.
		value = "string(" + value + ")"
	}
	c.write("%s := %s", name, value)
	return compiledTemplatePart{value: name}
}
