package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// Only structured claims and their nested declarations need identity readers.
func identityObjectNames(result *semantic.Result, declarations []*ast.ModelDecl) map[string]bool {
	var roots []string
	for _, file := range result.Files {
		for _, model := range file.Models {
			for _, field := range authStoreFields(model) {
				if isRelationType(field.Type.Name) {
					roots = append(roots, field.Type.Name)
				}
			}
		}
	}
	if len(roots) == 0 {
		return nil
	}
	objects := make(map[string]*ast.ModelDecl, len(declarations))
	for _, decl := range declarations {
		objects[decl.Name] = decl
	}
	needed := make(map[string]bool)
	var visit func(string)
	visit = func(name string) {
		decl := objects[name]
		if decl == nil || needed[name] {
			return
		}
		needed[name] = true
		for _, field := range decl.Fields {
			if field.Type != nil && !hasDirective(field.Directives, "hidden") && !hasDirective(field.Directives, "internal") {
				visit(field.Type.Name)
			}
		}
	}
	for _, name := range roots {
		visit(name)
	}
	return needed
}

func (g *GeneratorContext) generateReadIdentityInput(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool, pointer bool) {
	fmt.Fprintf(b, "func (v *%s) readIdentityInput(raw []byte, depth int) error {\n", model.Name)
	fields := g.jsonInputFields(model)
	if len(fields) != len(model.Fields) {
		// A partial claim must not materialize a complete model whose omitted
		// fields can subsequently be read as legitimate zero values.
		fmt.Fprintf(b, "\treturn fmt.Errorf(%q)\n}\n\n", model.Name+": identity object omits declared fields")
	} else {
		b.WriteString("\tif depth >= 64 { return fmt.Errorf(\"identity nesting exceeds 64 levels\") }\n")
		g.writeJSONInputBody(b, model, fields, enums, true)
	}
	valueType := model.Name
	if pointer {
		valueType = "*" + valueType
	}
	fmt.Fprintf(b, "func readIdentity%sList(raw []byte) ([]%s, error) {\n", model.Name, valueType)

	b.WriteString("\tcount := 0\n")
	if pointer {
		b.WriteString("\tvalues, err := ")
	} else {
		b.WriteString("\treturn ")
	}
	fmt.Fprintf(b, "jsonutil.ParseArray(raw, func(item []byte) (%s,error) {\n", model.Name)
	fmt.Fprintf(b, "\t\tif count >= codec.MaxArrayElements { var zero %s; return zero, fmt.Errorf(\"identity array exceeds element limit\") }; count++\n", model.Name)
	fmt.Fprintf(b, "\t\tvar value %s; err := value.readIdentityInput(item,0)\n", model.Name)
	b.WriteString("\t\treturn value,err\n\t})\n")
	if pointer {
		b.WriteString("\tif err != nil { return nil,err }\n")
		fmt.Fprintf(b, "\tresult := make([]*%s,len(values))\n", model.Name)
		b.WriteString("\tfor i := range values { result[i] = &values[i] }\n\treturn result,nil\n")
	}
	b.WriteString("}\n\n")
}
