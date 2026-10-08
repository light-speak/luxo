package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

type mutationInputField struct {
	field    *ast.FieldDecl
	optional bool
}

func isCRUDInputField(field *ast.FieldDecl, model *ast.ModelDecl, enums map[string]bool, update bool) bool {
	return field.Type != nil && !skipHandlerField(field, enums, model.Directives) &&
		(!update || (field.Name != primaryKeyFieldName(model) && !hasDirective(field.Directives, "immutable")))
}

func mutationInputFields(model *ast.ModelDecl, enums map[string]bool, update bool) []mutationInputField {
	var fields []mutationInputField
	for _, field := range model.Fields {
		if !isCRUDInputField(field, model, enums, update) {
			continue
		}
		optional := update || field.Default != nil || (field.Type != nil && field.Type.Nullable)
		fields = append(fields, mutationInputField{field, optional})
	}
	return fields
}

// Keep values in separate locals: taking the address of a nullable scalar must
// not force every input value into one heap-allocated staging struct.
func generateMutationInputRead(b *strings.Builder, input mutationInputField, indent string, enums map[string]bool) {
	field := input.field
	typ := field.Type
	if typ != nil && typ.Nullable {
		base := *typ
		base.Nullable = false
		typ = &base
	}
	fmt.Fprintf(b, "%svar %sVal %s\n", indent, field.Name, resolveGoType(typ))
	condition := ""
	if input.optional {
		condition = field.Name + "Present"
		fmt.Fprintf(b, "%s%s := req.HasParam(%q)\n", indent, condition, field.Name)
	}
	if field.Type != nil && field.Type.Nullable {
		prefix := ""
		if input.optional {
			prefix = condition + " && "
		}
		fmt.Fprintf(b, "%s%sNull := %sreq.ParamIsNull(%q)\n", indent, field.Name, prefix, field.Name)
		condition = prefix + "!" + field.Name + "Null"
	}
	if condition == "" {
		fmt.Fprintf(b, "%s{\n", indent)
	} else {
		fmt.Fprintf(b, "%sif %s {\n", indent, condition)
	}
	writeMutationValueRead(b, field, typ, indent+"\t", enums)
	generateFieldValidation(b, field, field.Name+"Val", indent+"\t")
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeMutationValueRead(b *strings.Builder, field *ast.FieldDecl, typ *ast.TypeRef, indent string, enums map[string]bool) {
	name := field.Name + "Val"
	if typ != nil && enums[typ.Name] {
		writeEnumParamValue(b, field.Name, name, typ, false, indent)
		return
	}
	fmt.Fprintf(b, "%s_value, err := req.Param%s(%q)\n", indent, paramMethod(resolveGoType(typ)), field.Name)
	fmt.Fprintf(b, "%sif err != nil { return fmt.Errorf(\"param %s: %%w\", err) }\n", indent, field.Name)
	fmt.Fprintf(b, "%s%s = _value\n", indent, name)
}

// Applying fields never reads the request again; presence and null decisions
// belong to the completed validation phase.
func generateMutationInputApply(b *strings.Builder, input mutationInputField, setter, indent string) {
	field, outer := input.field, indent
	if input.optional {
		fmt.Fprintf(b, "%sif %sPresent {\n", indent, field.Name)
		indent += "\t"
	}
	nullable := field.Type != nil && field.Type.Nullable
	if nullable {
		fmt.Fprintf(b, "%sif %sNull {\n%s\tbuilder.%s(nil)\n%s} else {\n", indent, field.Name, indent, setter, indent)
		indent += "\t"
	}
	name := field.Name + "Val"
	generateBeforeSave(b, field, name, indent)
	generateMutationHash(b, field, name, indent)
	arg := name
	if nullable && !field.Type.IsList {
		// Only a present, non-null value needs an escaping setter argument.
		fmt.Fprintf(b, "%s_value := %s\n", indent, name)
		arg = "&_value"
	}
	fmt.Fprintf(b, "%sbuilder.%s(%s)\n", indent, setter, arg)
	if nullable {
		indent = indent[:len(indent)-1]
		fmt.Fprintf(b, "%s}\n", indent)
	}
	if input.optional {
		fmt.Fprintf(b, "%s}\n", outer)
	}
}

func generateMutationHash(b *strings.Builder, field *ast.FieldDecl, value, indent string) {
	if !hasDirective(field.Directives, "hash") {
		return
	}
	fmt.Fprintf(b, "%s{\n%s\t_hashed, err := luxocrypto.HashPassword(%s)\n", indent, indent, value)
	fmt.Fprintf(b, "%s\tif err != nil { return fmt.Errorf(\"hash %s: %%w\", err) }\n", indent, field.Name)
	fmt.Fprintf(b, "%s\t%s = _hashed\n%s}\n", indent, value, indent)
}
