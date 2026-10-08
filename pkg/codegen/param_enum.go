package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

func writeEnumParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	writeStructuredParamDeclaration(b, param, enums, indent)
	writeEnumParamAssignment(b, param, false, indent)
}

// Inferred queries pass string values directly to typed database conditions.
func writeInferredEnumParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	textType := *param.Type
	textType.Name = "String"
	if param.Default == nil || isNullLiteral(param.Default) {
		fmt.Fprintf(b, "%svar %s %s\n", indent, param.Name, resolveGoType(&textType))
	} else {
		value := compileDefaultValue(param.Default, param.Type.Name, enums)
		if param.Type.Nullable {
			fmt.Fprintf(b, "%s_%sDefault := string(%s)\n", indent, param.Name, value)
			fmt.Fprintf(b, "%svar %s *string = &_%sDefault\n", indent, param.Name, param.Name)
		} else {
			fmt.Fprintf(b, "%svar %s string = string(%s)\n", indent, param.Name, value)
		}
	}
	writeEnumParamAssignment(b, param, true, indent)
}

func writeEnumParamAssignment(b *strings.Builder, param *ast.ParamDecl, text bool, indent string) {
	outer := indent
	if param.Default != nil {
		fmt.Fprintf(b, "%sif req.HasParam(%q) {\n", indent, param.Name)
		indent += "\t"
	}
	if param.Type.Nullable {
		fmt.Fprintf(b, "%sif req.ParamIsNull(%q) {\n", indent, param.Name)
		fmt.Fprintf(b, "%s\t%s = nil\n%s} else {\n", indent, param.Name, indent)
		writeEnumParamValue(b, param.Name, param.Name, param.Type, text, indent+"\t")
		fmt.Fprintf(b, "%s}\n", indent)
	} else {
		writeEnumParamValue(b, param.Name, param.Name, param.Type, text, indent)
	}
	if param.Default != nil {
		fmt.Fprintf(b, "%s}\n", outer)
	}
}

func writeEnumParamValue(b *strings.Builder, key, name string, typ *ast.TypeRef, text bool, indent string) {
	if typ.IsList {
		writeEnumParamArrayValue(b, key, name, typ.Name, text, indent)
		return
	}
	fmt.Fprintf(b, "%s_%sText, err := req.ParamString(%q)\n", indent, name, key)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%s_%sEnum := %s(_%sText)\n", indent, name, typ.Name, name)
	writeEnumParamCheck(b, key, "_"+name+"Enum", indent)
	value := "_" + name + "Enum"
	if text {
		value = "_" + name + "Text"
	}
	if typ.Nullable {
		value = "&" + value
	}
	fmt.Fprintf(b, "%s%s = %s\n", indent, name, value)
}

func writeEnumParamArrayValue(b *strings.Builder, key, name, typ string, text bool, indent string) {
	getter := fmt.Sprintf("api.ParamEnumArray[%s](req, %q)", typ, key)
	if text {
		getter = fmt.Sprintf("req.ParamStringArray(%q)", key)
	}
	fmt.Fprintf(b, "%s_%sValues, err := %s\n", indent, name, getter)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sfor _, _value := range _%sValues {\n", indent, name)
	value := "_value"
	if text {
		value = typ + "(_value)"
	}
	writeEnumParamCheck(b, key, value, indent+"\t")
	fmt.Fprintf(b, "%s}\n%s%s = _%sValues\n", indent, indent, name, name)
}

func writeEnumParamCheck(b *strings.Builder, key, value, indent string) {
	fmt.Fprintf(b, "%sif !%s.IsValid() { return api.InvalidParam(%q, codec.ErrInvalidEnum) }\n", indent, value, key)
}
