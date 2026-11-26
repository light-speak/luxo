package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func (g *errorDataGenerator) writeSealedObject(b *strings.Builder, owner *ast.SealedDecl) {
	fmt.Fprintf(b, "func append%s%sJSON(w *errors.DataWriter,value *%s){\n", g.objectPrefix, owner.Name, owner.Name)
	b.WriteString("if value==nil{w.Token(\"null\");return};if !w.Enter(){return};defer w.Leave();w.Token(\"{\");switch value.LuxoVariantTag(){\n")
	for _, variant := range owner.Variants {
		g.writeSealedErrorCase(b, owner, variant)
	}
	b.WriteString("default:w.Reject(\"error data: invalid sealed variant\")\n};w.Token(\"}\")\n}\n")
	for _, variant := range owner.Variants {
		tag := g.owner.modelFieldID(owner.Name, variant.Name)
		for _, field := range variant.Fields {
			id := g.owner.modelFieldID(owner.Name+"."+variant.Name, field.Name)
			fmt.Fprintf(b, "func %s(w *errors.DataWriter,value %s){if w.Err()!=nil{return};\n", g.sealedErrorField(owner.Name, tag, id), compiledFunctionGoType(field.Type, g.owner.modelDeclarations))
			g.writeValue(b, field.Type, "value", g.models[field.Type.Name], false, "", nil)
			b.WriteString("}\n")
		}
	}
}

func (g *errorDataGenerator) writeSealedErrorCase(b *strings.Builder, owner *ast.SealedDecl, variant *ast.SealedVariant) {
	tag := g.owner.modelFieldID(owner.Name, variant.Name)
	fmt.Fprintf(b, "case %d:if value.AsVariant%d()==nil{w.Reject(\"error data: invalid sealed payload\");return};\n", tag, tag)
	if len(variant.Fields) > 0 {
		fmt.Fprintf(b, "payload:=value.AsVariant%d();\n", tag)
	}
	writeErrorFieldName(b, variant.Name, 0, "")
	b.WriteString("if !w.Enter(){return};defer w.Leave();w.Token(\"{\");\n")
	for index, field := range variant.Fields {
		id := g.owner.modelFieldID(owner.Name+"."+variant.Name, field.Name)
		writeErrorFieldName(b, field.Name, index, "")
		fmt.Fprintf(b, "%s(w,payload.F%d)\n", g.sealedErrorField(owner.Name, tag, id), id)
	}
	b.WriteString("w.Token(\"}\")\n")
}

func (g *errorDataGenerator) sealedErrorField(name string, tag, id int) string {
	return fmt.Sprintf("append%s%sCase%dField%d", g.objectPrefix, name, tag, id)
}
