package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// Local JSON reads the generated type directly; binary/RPC keeps wire readers.
func writeStructuredParamExtraction(b *strings.Builder, param *ast.ParamDecl, enums map[string]bool, indent string) {
	writeStructuredParamDeclaration(b, param, enums, indent)
	fmt.Fprintf(b, "%sif req.Params != nil {\n", indent)
	writeStructuredJSONParamDecode(b, param, indent+"\t")
	fmt.Fprintf(b, "%s} else {\n", indent)
	if param.Type.IsList {
		writeStructuredListParamDecode(b, param, indent+"\t")
	} else {
		writeStructuredScalarParamDecode(b, param, indent+"\t")
	}
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeStructuredJSONParamDecode(b *strings.Builder, param *ast.ParamDecl, indent string) {
	name := param.Name
	fmt.Fprintf(b, "%s_%sInput, _%sPresent, err := req.ParamInput(%q, %t, %t)\n", indent, name, name, name, param.Default != nil, param.Type.Nullable)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif _%sPresent && _%sInput != nil {\n", indent, name, name)
	if param.Type.IsList {
		fmt.Fprintf(b, "%s\t%s, err = ReadJSONInput%sList(_%sInput)\n", indent, name, param.Type.Name, name)
	} else {
		if param.Type.Nullable {
			fmt.Fprintf(b, "%s\t%s = &%s{}\n", indent, name, param.Type.Name)
		}
		fmt.Fprintf(b, "%s\terr = %s.ReadJSONInput(_%sInput)\n", indent, name, name)
	}
	fmt.Fprintf(b, "%s\tif err != nil { return api.InvalidParam(%q, err) }\n", indent, name)
	writeStructuredNullBranch(b, param, indent)
}

func writeStructuredScalarParamDecode(b *strings.Builder, param *ast.ParamDecl, indent string) {
	name := param.Name
	fmt.Fprintf(b, "%s_%sMessage, _%sPresent, err := req.ParamMessage(%q, %t, %t)\n", indent, name, name, name, param.Default != nil, param.Type.Nullable)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif _%sPresent && _%sMessage != nil {\n", indent, name, name)
	fmt.Fprintf(b, "%s\t_%sDecoder := codec.NewDecoder(_%sMessage)\n", indent, name, name)
	if param.Type.Nullable {
		fmt.Fprintf(b, "%s\t%s = &%s{}\n", indent, name, param.Type.Name)
	}
	fmt.Fprintf(b, "%s\t%s.ReadLuxoInput(_%sDecoder)\n", indent, name, name)
	fmt.Fprintf(b, "%s\tif err := _%sDecoder.Finish(); err != nil { return api.InvalidParam(%q, err) }\n", indent, name, name)
	writeStructuredNullBranch(b, param, indent)
}

func writeStructuredListParamDecode(b *strings.Builder, param *ast.ParamDecl, indent string) {
	name := param.Name
	fmt.Fprintf(b, "%s_%sMessages, _%sPresent, err := req.ParamMessageArray(%q, %t, %t)\n", indent, name, name, name, param.Default != nil, param.Type.Nullable)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sif _%sPresent && _%sMessages != nil {\n", indent, name, name)
	fmt.Fprintf(b, "%s\t%s = make([]%s, len(_%sMessages))\n", indent, name, param.Type.Name, name)
	fmt.Fprintf(b, "%s\tfor _index := range _%sMessages {\n", indent, name)
	fmt.Fprintf(b, "%s\t\t_%sDecoder := codec.NewDecoder(_%sMessages[_index])\n", indent, name, name)
	fmt.Fprintf(b, "%s\t\t%s[_index].ReadLuxoInput(_%sDecoder)\n", indent, name, name)
	fmt.Fprintf(b, "%s\t\tif err := _%sDecoder.Finish(); err != nil { return api.InvalidParam(%q, err) }\n", indent, name, name)
	fmt.Fprintf(b, "%s\t}\n", indent)
	writeStructuredNullBranch(b, param, indent)
}

func writeStructuredNullBranch(b *strings.Builder, param *ast.ParamDecl, indent string) {
	if param.Type.Nullable {
		fmt.Fprintf(b, "%s} else if _%sPresent {\n%s\t%s = nil\n", indent, param.Name, indent, param.Name)
	}
	fmt.Fprintf(b, "%s}\n", indent)
}
