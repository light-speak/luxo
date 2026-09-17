package codegen

import (
	"fmt"
	"strings"
)

// writeBinaryAppend keeps fallible scalar appends in the existing encoding pass.
// onError includes the caller's return shape and any owned-buffer cleanup.
func writeBinaryAppend(b *strings.Builder, indent, destination, expression, onError string) {
	if strings.HasPrefix(expression, "codec.AppendString(") || strings.HasPrefix(expression, "codec.AppendFixed64(") || strings.HasPrefix(expression, "codec.AppendJSON(") || strings.HasPrefix(expression, "codec.AppendDateTime(") {
		fmt.Fprintf(b, "%sif encoded, err := %s; err != nil { %s } else { %s = encoded }\n", indent, expression, onError, destination)
		return
	}
	fmt.Fprintf(b, "%s%s = %s\n", indent, destination, expression)
}

func writeBinaryCall(b *strings.Builder, indent, expression, onError string) {
	fmt.Fprintf(b, "%s%s\n", indent, binaryCallStatement(expression, onError))
}

func binaryCallStatement(expression, onError string) string {
	return fmt.Sprintf("if err := %s; err != nil { %s }", expression, onError)
}

func (c *compiler) writeBinaryCall(format string, args ...any) {
	// Parentheses disambiguate a composite literal receiver inside an if initializer.
	writeBinaryCall(c.b, c.indent, "("+fmt.Sprintf(format, args...)+")", "return err")
}

func (c *compiler) writeBinaryAppend(expression string) {
	writeBinaryAppend(c.b, c.indent, "req.Buf.B", expression, "return err")
}
