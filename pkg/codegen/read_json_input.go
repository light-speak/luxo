package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// Fixed slots preserve last-name-wins without a map or intermediate wire data.
// Decode into an independent value and publish only after all fields succeed.
func (g *GeneratorContext) generateReadJSONInput(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool) {
	fields := g.jsonInputFields(model)
	fmt.Fprintf(b, "// ReadJSONInput decodes a complete input into independently owned fields.\nfunc (v *%s) ReadJSONInput(raw []byte) error {\n", model.Name)
	if len(fields) > 0 {
		fmt.Fprintf(b, "\tvar fields [%d][]byte\n", len(fields))
	}
	b.WriteString("\tif err := jsonutil.VisitObject(raw, func(name, value []byte) error {\n\t\tswitch string(name) {\n")
	for i, field := range fields {
		fmt.Fprintf(b, "\t\tcase %q: fields[%d] = value; return nil\n", field.Name, i)
	}
	fmt.Fprintf(b, "\t\tdefault: return fmt.Errorf(%q, name)\n\t\t}\n\t}); err != nil { return err }\n", model.Name+": unknown field %q")
	fmt.Fprintf(b, "\tvar out %s\n", model.Name)
	for i, field := range fields {
		writeJSONInputField(b, model.Name, field, enums, i)
	}
	b.WriteString("\t*v = out\n\treturn nil\n}\n\n")
	fmt.Fprintf(b, "// ReadJSONInput%sList decodes an owned, bounded input list.\nfunc ReadJSONInput%sList(raw []byte) ([]%s, error) {\n", model.Name, model.Name, model.Name)
	writeJSONInputList(b, model.Name, false)
	b.WriteString("}\n\n")
}

func (g *GeneratorContext) jsonInputFields(model *ast.ModelDecl) []*ast.FieldDecl {
	fields := make([]*ast.FieldDecl, 0, len(model.Fields))
	for _, field := range model.Fields {
		if field.Type != nil && !hasDirective(field.Directives, "hidden") && !hasDirective(field.Directives, "internal") && g.modelFieldID(model.Name, field.Name) != 0 {
			fields = append(fields, field)
		}
	}
	return fields
}

func writeJSONInputField(b *strings.Builder, model string, field *ast.FieldDecl, enums map[string]bool, index int) {
	fmt.Fprintf(b, "\tif raw := fields[%d]; raw != nil {\n", index)
	if field.Type.Nullable {
		b.WriteString("\t\tif string(raw) != \"null\" {\n")
	}
	goType := mapBaseType(field.Type.Name)
	if field.Type.IsList {
		goType = "[]" + goType
	}
	fmt.Fprintf(b, "\t\tvalue, err := func() (%s, error) {\n", goType)
	if field.Type.IsList {
		writeJSONInputList(b, field.Type.Name, enums[field.Type.Name])
	} else {
		writeJSONInputScalar(b, field.Type.Name, enums[field.Type.Name], "raw", "\t\t\t")
	}
	b.WriteString("\t\t}()\n")
	fmt.Fprintf(b, "\t\tif err != nil { return fmt.Errorf(%q, err) }\n", model+"."+field.Name+": %w")
	value := "value"
	if !field.Type.IsList && (field.Type.Nullable || isRelationField(field, enums)) {
		value = "&value"
	}
	fmt.Fprintf(b, "\t\tout.%s = %s\n", str.Capitalize(field.Name), value)
	if field.Type.Nullable {
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
}

func writeJSONInputList(b *strings.Builder, name string, isEnum bool) {
	b.WriteString("\t\t\tcount := 0\n")
	fmt.Fprintf(b, "\t\t\treturn jsonutil.ParseArray(raw, func(item []byte) (%s, error) {\n", mapBaseType(name))
	fmt.Fprintf(b, "\t\t\t\tif count >= codec.MaxArrayElements { var zero %s; return zero, fmt.Errorf(\"array size exceeds limit %%d\", codec.MaxArrayElements) }\n", mapBaseType(name))
	b.WriteString("\t\t\t\tcount++\n")
	writeJSONInputScalar(b, name, isEnum, "item", "\t\t\t\t")
	b.WriteString("\t\t\t})\n")
}

func writeJSONInputScalar(b *strings.Builder, name string, isEnum bool, raw, indent string) {
	readers := map[string]string{
		"Int": "ParseInt64", "Float": "ParseFloat64", "Boolean": "ParseBool", "String": "ParseString", "DateTime": "ParseDateTime", "Bytes": "ParseBase64",
	}
	if reader, ok := readers[name]; ok && !isEnum {
		fmt.Fprintf(b, "%sreturn jsonutil.%s(%s)\n", indent, reader, raw)
		return
	}
	switch {
	case name == "Duration":
		fmt.Fprintf(b, "%svalue, err := jsonutil.ParseInt64(%s); return time.Duration(value), err\n", indent, raw)
	case name == "JSON":
		// The strict object/array reader already validated the entire token.
		fmt.Fprintf(b, "%sreturn bytes.Clone(%s), nil\n", indent, raw)
	case isEnum || name == "UUID" || name == "Decimal":
		writeJSONInputTextConversion(b, name, isEnum, raw, indent)
	default:
		fmt.Fprintf(b, "%svar value %s; err := value.ReadJSONInput(%s); return value, err\n", indent, name, raw)
	}
}

func writeJSONInputTextConversion(b *strings.Builder, name string, isEnum bool, raw, indent string) {
	fmt.Fprintf(b, "%stext, err := jsonutil.ParseString(%s); if err != nil { var zero %s; return zero, err }\n", indent, raw, mapBaseType(name))
	switch {
	case isEnum:
		fmt.Fprintf(b, "%svalue := %s(text); if !value.IsValid() { return %s(\"\"), codec.ErrInvalidEnum }; return value, nil\n", indent, name, name)
	case name == "UUID":
		fmt.Fprintf(b, "%sreturn uuid.Parse(text)\n", indent)
	default:
		fmt.Fprintf(b, "%sreturn decimal.NewFromString(text)\n", indent)
	}
}
