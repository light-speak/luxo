package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Write the canonical LEB128 tag as a constant so the generated writer can
// append it directly to the response without a runtime varint loop.
func writeBinaryFieldID(b *strings.Builder, indent string, fieldID int) {
	var scratch [10]byte
	tag := codec.AppendVarint(scratch[:0], uint64(fieldID))
	if len(tag) == 1 {
		writeStringParts(b, indent, "buf.AppendByte(", strconv.Itoa(int(tag[0])), ")\n")
		return
	}
	writeStringParts(b, indent, "buf.AppendString(", strconv.Quote(string(tag)), ")\n")
}

func writeStringParts(b *strings.Builder, parts ...string) {
	for _, part := range parts {
		b.WriteString(part)
	}
}

// writeBinaryAppend keeps fallible scalar appends in the existing encoding pass.
// onError includes the caller's return shape and any owned-buffer cleanup.
func writeBinaryAppend(b *strings.Builder, indent, destination, expression, onError string) {
	if strings.HasPrefix(expression, "codec.AppendArrayHeader(") || strings.HasPrefix(expression, "codec.AppendString(") || strings.HasPrefix(expression, "codec.AppendFixed64(") || strings.HasPrefix(expression, "codec.AppendJSON(") || strings.HasPrefix(expression, "codec.AppendDateTime(") || strings.HasSuffix(expression, ").AppendLuxo("+destination+")") {
		writeStringParts(b, indent, "if encoded, err := ", expression, "; err != nil { ", onError, " } else { ", destination, " = encoded }\n")
		return
	}
	writeStringParts(b, indent, destination, " = ", expression, "\n")
}

func writeBinaryCall(b *strings.Builder, indent, expression, onError string) {
	writeStringParts(b, indent, "if err := ", expression, "; err != nil { ", onError, " }\n")
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

func writeBufferedBinaryAppend(b *strings.Builder, indent, expression string) {
	method, args, ok := strings.Cut(strings.TrimPrefix(expression, "codec.Append"), "(buf.B, ")
	if ok {
		switch method {
		case "Svarint":
			writeStringParts(b, indent, "buf.WriteLuxoInt(", args, "\n")
			return
		case "Bool", "UUID", "Bytes":
			writeStringParts(b, indent, "buf.WriteLuxo", method, "(", args, "\n")
			return
		case "Fixed64":
			method = "Float"
		case "ArrayHeader", "String", "DateTime", "JSON":
		default:
			writeBinaryAppend(b, indent, "buf.B", expression, "return err")
			return
		}
		writeStringParts(b, indent, "if err := buf.WriteLuxo", method, "(", args, "; err != nil { return err }\n")
		return
	}
	writeBinaryAppend(b, indent, "buf.B", expression, "return err")
}
