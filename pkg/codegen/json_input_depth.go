package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Discarded values are checked statically for resources, not decoded as business
// values. Missing fields, unknown names and mismatched shapes retain last-wins.
func (g *GeneratorContext) generateJSONInputDepthCheck(b *strings.Builder, model *ast.ModelDecl, fields []*ast.FieldDecl, enums map[string]bool) {
	if g.singleObjectDepth {
		return
	}
	g.writeJSONInputDepthCheck(b, model.Name, fields, enums)
}

func (g *GeneratorContext) writeJSONInputDepthCheck(b *strings.Builder, name string, fields []*ast.FieldDecl, enums map[string]bool) {
	fmt.Fprintf(b, "func (*%s) %s(reader *jsonutil.DepthReader, depth int) error {\n", name, g.valueDepthMethod(name, "checkJSONInputDepth"))
	b.WriteString("if reader.Kind() != '{' { return reader.JSON() }\n")
	writeModelDepthGuard(b, "return codec.ErrModelDepth")
	b.WriteString("return reader.Object(func(name []byte) error {\nswitch string(name) {\n")
	for _, field := range fields {
		if !field.Type.IsList && !isRelationField(field, enums) {
			continue
		}
		fmt.Fprintf(b, "case %q:\nreturn %s\n", field.Name, g.jsonInputDepthExpression(field, enums, "reader", "depth+1"))
	}
	b.WriteString("default: return reader.JSON()\n}\n})\n}\n\n")
}

func (g *GeneratorContext) writeJSONInputPreviousDepth(b *strings.Builder, field *ast.FieldDecl, enums map[string]bool, index int, identity bool) {
	fmt.Fprintf(b, "previous := fields[%d]; fields[%d] = value\n", index, index)
	depth := "depth+1"
	if identity {
		depth = "depth+2"
	}
	if field.Type.IsList || isRelationField(field, enums) {
		check := g.jsonInputDepthExpression(field, enums, "reader", depth)
		fmt.Fprintf(b, "if previous == nil { return nil }; return jsonutil.CheckJSONValueDepth(previous, func(reader *jsonutil.DepthReader) error { return %s })\n", check)
	} else {
		b.WriteString("return jsonutil.CheckJSONDepth(previous)\n")
	}
}

func (g *GeneratorContext) jsonInputDepthExpression(field *ast.FieldDecl, enums map[string]bool, reader, depth string) string {
	check := "(*jsonutil.DepthReader).JSON"
	if isRelationField(field, enums) {
		check = fmt.Sprintf("func(reader *jsonutil.DepthReader) error { return (*%s)(nil).%s(reader, %s) }", field.Type.Name, g.valueDepthMethod(field.Type.Name, "checkJSONInputDepth"), depth)
	}
	if field.Type.IsList {
		return fmt.Sprintf("%s.List(codec.MaxArrayElements, %s)", reader, check)
	}
	return fmt.Sprintf("(*%s)(nil).%s(%s, %s)", field.Type.Name, g.valueDepthMethod(field.Type.Name, "checkJSONInputDepth"), reader, depth)
}
