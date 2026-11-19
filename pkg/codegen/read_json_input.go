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
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "\treturn v.%s(raw, 1)\n}\n\n", g.valueDepthMethod(model.Name, "readJSONInputDepth"))
		fmt.Fprintf(b, "func (v *%s) %s(raw []byte, depth int) error {\n", model.Name, g.valueDepthMethod(model.Name, "readJSONInputDepth"))
		writeModelDepthGuard(b, "return codec.ErrModelDepth")
	}
	g.writeJSONInputBody(b, model, fields, enums, false)
	fmt.Fprintf(b, "// ReadJSONInput%sList decodes an owned, bounded input list.\nfunc ReadJSONInput%sList(raw []byte) ([]%s, error) {\n", model.Name, model.Name, model.Name)
	g.writeJSONInputList(b, model.Name, false)
	b.WriteString("}\n\n")
	g.generateJSONInputDepthCheck(b, model, fields, enums)
}

func (g *GeneratorContext) writeJSONInputBody(b *strings.Builder, model *ast.ModelDecl, fields []*ast.FieldDecl, enums map[string]bool, identity bool) {
	if len(fields) > 0 {
		fmt.Fprintf(b, "\tvar fields [%d][]byte\n", len(fields))
	}
	b.WriteString("\tif err := jsonutil.VisitObject(raw, func(name, value []byte) error {\n\t\tswitch string(name) {\n")
	for i, field := range fields {
		fmt.Fprintf(b, "\t\tcase %q:\n", field.Name)
		g.writeJSONInputPreviousDepth(b, field, enums, i, identity)
	}
	fmt.Fprintf(b, "\t\tdefault: return fmt.Errorf(%q, name)\n\t\t}\n\t}); err != nil { return err }\n", model.Name+": unknown field %q")
	fmt.Fprintf(b, "\tvar out %s\n", model.Name)
	if plan := g.instanceSaves[model.Name]; plan != nil && !identity {
		plan.write(b, "out._luxoSaveConstructed = true\n")
	}
	for i, field := range fields {
		g.writeJSONInputField(b, model.Name, field, enums, i, identity, g.isModelReference(field.Type.Name))
	}
	if !identity {
		g.writeUnaddressableInputDefaults(b, model.Name, enums)
	}
	b.WriteString("\t*v = out\n\treturn nil\n}\n\n")
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

func (g *GeneratorContext) writeJSONInputField(b *strings.Builder, model string, field *ast.FieldDecl, enums map[string]bool, index int, modes ...bool) {
	identity := len(modes) > 0 && modes[0]
	if identity {
		fmt.Fprintf(b, "\tif fields[%d] == nil { return fmt.Errorf(%q) }\n", index, model+"."+field.Name+": missing identity field")
	}
	fmt.Fprintf(b, "\tif raw := fields[%d]; raw != nil {\n", index)
	if plan := g.instanceSaves[model]; plan != nil && !identity {
		if slot, ok := plan.index[field.Name]; ok {
			fmt.Fprintf(b, "%s\n%s\n", plan.bitSet("out", "Loaded", slot), plan.bitSet("out", "Explicit", slot))
		}
	}
	if field.Type.Nullable {
		b.WriteString("\t\tif string(raw) != \"null\" {\n")
	}
	g.writeJSONInputValue(b, field, enums, identity)
	fmt.Fprintf(b, "\t\tif err != nil { return fmt.Errorf(%q, err) }\n", model+"."+field.Name+": %w")
	value := "value"
	if field.Type.IsList && len(modes) > 1 && modes[1] {
		value = "reference" + field.Type.Name + "List(value)"
	}
	if !field.Type.IsList && (field.Type.Nullable || isRelationField(field, enums)) {
		value = "&value"
	}
	fmt.Fprintf(b, "\t\tout.%s = %s\n", str.Capitalize(field.Name), value)
	if field.Type.Nullable {
		b.WriteString("\t\t}\n")
	}
	if !identity && len(g.objectDefaults[model]) > 0 && hasObjectDefault(field) {
		b.WriteString("\t} else {\n")
		writeObjectDefaultAssignment(b, field, "out."+str.Capitalize(field.Name), "\t\t", enums)
	}
	b.WriteString("\t}\n")
}

func (g *GeneratorContext) writeJSONInputValue(b *strings.Builder, field *ast.FieldDecl, enums map[string]bool, identity bool) {
	if reader := directJSONInputReader(field.Type.Name, enums[field.Type.Name]); !field.Type.IsList && reader != "" {
		fmt.Fprintf(b, "\t\tvalue, err := jsonutil.%s(raw)\n", reader)
		return
	}
	goType := mapBaseType(field.Type.Name)
	if field.Type.IsList {
		goType = "[]" + goType
	}
	fmt.Fprintf(b, "\t\tvalue, err := func() (%s, error) {\n", goType)
	if field.Type.IsList {
		g.writeJSONInputList(b, field.Type.Name, enums[field.Type.Name], identity)
	} else {
		g.writeJSONInputScalar(b, field.Type.Name, enums[field.Type.Name], "raw", "\t\t\t", identity)
	}
	b.WriteString("\t\t}()\n")
}

func (g *GeneratorContext) writeJSONInputList(b *strings.Builder, name string, isEnum bool, modes ...bool) {
	b.WriteString("\t\t\tcount := 0\n")
	fmt.Fprintf(b, "\t\t\treturn jsonutil.ParseArray(raw, func(item []byte) (%s, error) {\n", mapBaseType(name))
	fmt.Fprintf(b, "\t\t\t\tif count >= codec.MaxArrayElements { var zero %s; return zero, fmt.Errorf(\"array size exceeds limit %%d\", codec.MaxArrayElements) }\n", mapBaseType(name))
	b.WriteString("\t\t\t\tcount++\n")
	g.writeJSONInputScalar(b, name, isEnum, "item", "\t\t\t\t", modes...)
	b.WriteString("\t\t\t})\n")
}

func directJSONInputReader(name string, isEnum bool) string {
	if isEnum {
		return ""
	}
	switch name {
	case "Int":
		return "ParseInt64"
	case "Float":
		return "ParseFloat64"
	case "Boolean":
		return "ParseBool"
	case "String":
		return "ParseString"
	case "DateTime":
		return "ParseDateTime"
	case "Bytes":
		return "ParseBase64"
	}
	return ""
}

func (g *GeneratorContext) writeJSONInputScalar(b *strings.Builder, name string, isEnum bool, raw, indent string, modes ...bool) {
	if reader := directJSONInputReader(name, isEnum); reader != "" {
		fmt.Fprintf(b, "%sreturn jsonutil.%s(%s)\n", indent, reader, raw)
		return
	}
	switch {
	case name == "Duration":
		fmt.Fprintf(b, "%svalue, err := jsonutil.ParseInt64(%s); return time.Duration(value), err\n", indent, raw)
	case name == "JSON":
		// The strict reader already checked syntax; this field owns its depth budget.
		fmt.Fprintf(b, "%sreturn jsonutil.CloneJSONValue(%s)\n", indent, raw)
	case isEnum || name == "UUID" || name == "Decimal":
		writeJSONInputTextConversion(b, name, isEnum, raw, indent)
	default:
		if len(modes) > 0 && modes[0] {
			fmt.Fprintf(b, "%svar value %s; err := value.readIdentityInput(%s, depth+1); return value, err\n", indent, name, raw)
		} else if len(modes) > 0 {
			fmt.Fprintf(b, "%svar value %s; err := value.%s(%s, depth+1); return value, err\n", indent, name, g.valueDepthMethod(name, "readJSONInputDepth"), raw)
		} else {
			fmt.Fprintf(b, "%svar value %s; err := value.ReadJSONInput(%s); return value, err\n", indent, name, raw)
		}
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
