package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type errorDataGenerator struct {
	publicErrorData bool
	objectPrefix    string
	objects         map[string][]*ast.FieldDecl
	models          map[string]bool
	enums           map[string]bool
	queued          map[string]bool
	queue           []string
	imports         generatedImports
	owner           *GeneratorContext
}

type errorFieldPolicy struct {
	omit bool
	mask fieldMaskRule
}

func errorPolicyFor(field *ast.FieldDecl, public bool) errorFieldPolicy {
	if hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") ||
		(public && hasDirective(field.Directives, "visible")) {
		return errorFieldPolicy{omit: true}
	}
	if public && field.Type != nil {
		return errorFieldPolicy{mask: fieldMaskFor(field, field.Type.Name)}
	}
	return errorFieldPolicy{}
}

func (context *EventContext) recordProjectedErrorPolicies(files []*ast.File) {
	if len(context.remotePKModels) == 0 {
		return
	}
	for _, file := range files {
		for _, model := range file.Models {
			if !context.remotePKModels[model.Name] {
				continue
			}
			for _, field := range model.Fields {
				context.recordErrorFieldPolicy(model.Name, field)
			}
		}
	}
}

func (context *EventContext) recordErrorFieldPolicy(model string, field *ast.FieldDecl) {
	if len(field.Directives) == 0 {
		return
	}
	policy := errorPolicyFor(field, true)
	if policy == (errorFieldPolicy{}) {
		return
	}
	if context.modelErrorPolicies == nil {
		context.modelErrorPolicies = make(map[string]map[string]errorFieldPolicy)
	}
	fields := context.modelErrorPolicies[model]
	if fields == nil {
		fields = make(map[string]errorFieldPolicy)
		context.modelErrorPolicies[model] = fields
	}
	fields[field.Name] = policy
}

func newErrorDataGenerator(result *semantic.Result, generator *GeneratorContext) *errorDataGenerator {
	g := &errorDataGenerator{
		objectPrefix: "Error",
		objects:      make(map[string][]*ast.FieldDecl),
		models:       make(map[string]bool),
		enums:        generator.allEnums(collectEnums(result)),
		queued:       make(map[string]bool),
		owner:        generator,
	}
	for _, file := range result.Files {
		for _, model := range file.Models {
			fields := model.Fields
			if isSoftDelete(model) && !hasDeletedAtField(fields) {
				fields = append(append([]*ast.FieldDecl{}, fields...), softDeleteField())
			}
			g.objects[model.Name] = fields
			g.models[model.Name] = true
		}
		for _, item := range file.Types {
			g.objects[item.Name] = item.Fields
		}
		for _, item := range file.Extends {
			g.models[item.Name] = true
			if _, exists := g.objects[item.Name]; !exists {
				g.objects[item.Name] = generator.extendStubModel(item).Fields
			}
		}
	}
	return g
}

// Model lists retain the same pointer element ABI as generated business calls.
func errorDataGoType(ref *ast.TypeRef, models map[string]bool) string {
	if ref != nil && ref.IsList && models[ref.Name] {
		return "[]*" + ref.Name
	}
	return resolveGoType(ref)
}

func (g *errorDataGenerator) writeData(b *strings.Builder, e *ast.ErrorDecl) {
	if len(e.Fields) == 0 {
		return
	}
	name := e.Name + "ErrorData"
	fmt.Fprintf(b, "type %s struct {\n", name)
	for i, field := range e.Fields {
		fmt.Fprintf(b, "\tfield%d %s\n", i, errorDataGoType(field.Type, g.models))
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "func (data *%s) AppendJSON(dst []byte) ([]byte, error) {\n", name)
	b.WriteString("\tif data == nil { return append(dst, \"null\"...), nil }\n")
	b.WriteString("\tw := errors.NewDataWriter(dst)\n\tw.Token(\"{\")\n")
	for i, field := range e.Fields {
		writeErrorFieldName(b, field.Name, i, "\t")
		fmt.Fprintf(b, "\tdata.appendField%d(&w, false)\n", i)
	}
	b.WriteString("\tw.Token(\"}\")\n\treturn w.Result()\n}\n\n")
	fmt.Fprintf(b, "func (data *%s) AppendText(dst []byte, field string) ([]byte, bool) {\n", name)
	b.WriteString("\tif data == nil { return dst, false }\n")
	b.WriteString("\tw := errors.NewDataWriter(dst)\n\tswitch field {\n")
	for i, field := range e.Fields {
		fmt.Fprintf(b, "\tcase %q: data.appendField%d(&w, true)\n", field.Name, i)
	}
	b.WriteString("\tdefault: return dst, false\n\t}\n\tresult, err := w.Result()\n\treturn result, err == nil\n}\n\n")
	for i, field := range e.Fields {
		fmt.Fprintf(b, "func (data *%s) appendField%d(w *errors.DataWriter, text bool) {\n", name, i)
		b.WriteString("\tif w.Err() != nil { return }\n")
		g.writeValue(b, field.Type, fmt.Sprintf("data.field%d", i), field.Type.IsList && g.models[field.Type.Name], true, "\t", nil)
		b.WriteString("}\n\n")
	}
}

func writeErrorFieldName(b *strings.Builder, name string, index int, indent string) {
	prefix := ""
	if index > 0 {
		prefix = ","
	}
	fmt.Fprintf(b, "%sw.Token(%q)\n", indent, fmt.Sprintf("%s%q:", prefix, name))
}

func (g *errorDataGenerator) writeValue(b *strings.Builder, ref *ast.TypeRef, value string, pointer, text bool, indent string, mask *fieldMaskRule) {
	if ref.IsList {
		empty := "[]"
		if ref.Nullable {
			empty = "null"
		}
		fmt.Fprintf(b, "%sif %s == nil { w.Token(%q); return }\n", indent, value, empty)
		fmt.Fprintf(b, "%sw.Token(\"[\")\n%sfor i := range %s {\n%s\tif i > 0 { w.Token(\",\") }\n", indent, indent, value, indent)
		base := *ref
		base.IsList, base.Nullable = false, false
		fmt.Fprintf(b, "%s\tif w.Err() != nil { return }\n", indent)
		element := value + "[i]"
		if pointer && g.models[ref.Name] {
			fmt.Fprintf(b, "%s\tif %s == nil { w.Reject(%q); return }\n", indent, element, "model list element is nil")
			element = "(*" + element + ")"
		}
		if mask != nil {
			element = mask.expression(element)
		}
		g.writeScalar(b, &base, element, false, indent+"\t")
		fmt.Fprintf(b, "%s}\n%sw.Token(\"]\")\n", indent, indent)
		return
	}
	if ref.Nullable || pointer {
		if ref.Nullable {
			fmt.Fprintf(b, "%sif %s == nil { w.Token(\"null\"); return }\n", indent, value)
		} else {
			fmt.Fprintf(b, "%sif %s == nil { w.Reject(%q); return }\n", indent, value, "required object is nil")
		}
		value = "(*" + value + ")"
	}
	if mask != nil {
		value = mask.expression(value)
	}
	g.writeScalar(b, ref, value, text, indent)
}

func (g *errorDataGenerator) writeScalar(b *strings.Builder, ref *ast.TypeRef, value string, text bool, indent string) {
	if g.enums[ref.Name] {
		fmt.Fprintf(b, "%sif !%s.IsValid() { w.Reject(%q); return }\n", indent, value, "error data: undeclared enum value for "+ref.Name)
		value = "string(" + value + ")"
		g.writeString(b, value, text, indent)
		return
	}
	if _, ok := g.objects[ref.Name]; ok || g.owner.sealedDeclaration(ref.Name) != nil {
		if !g.queued[ref.Name] {
			g.queued[ref.Name] = true
			g.queue = append(g.queue, ref.Name)
		}
		value = "&" + value
		fmt.Fprintf(b, "%sappend%s%sJSON(w, %s)\n", indent, g.objectPrefix, ref.Name, value)
		return
	}
	method := map[string]string{"Int": "Int", "Duration": "Int", "Float": "Float", "Boolean": "Bool", "DateTime": "Time", "Bytes": "Bytes", "JSON": "JSON"}[ref.Name]
	switch ref.Name {
	case "Duration":
		value = "int64(" + value + ")"
	case "String":
		g.writeString(b, value, text, indent)
		return
	case "UUID":
		g.writeString(b, value+".String()", text, indent)
		return
	case "Decimal":
		g.imports |= generatedPackageBit("lux")
		g.writeString(b, "lux.FormatDecimal("+value+")", text, indent)
		return
	}
	if method == "" {
		fmt.Fprintf(b, "%sw.Reject(%q)\n", indent, "unsupported error data type: "+ref.Name)
		return
	}
	fmt.Fprintf(b, "%sw.%s(%s)\n", indent, method, value)
}

func (g *errorDataGenerator) writeString(b *strings.Builder, value string, text bool, indent string) {
	if text {
		fmt.Fprintf(b, "%sif text { w.Token(%s) } else { w.String(%s) }\n", indent, value, value)
		return
	}
	fmt.Fprintf(b, "%sw.String(%s)\n", indent, value)
}

func (g *errorDataGenerator) writeObjects(b *strings.Builder) {
	for next := 0; next < len(g.queue); next++ {
		name := g.queue[next]
		if declaration := g.owner.sealedDeclaration(name); declaration != nil {
			g.writeSealedObject(b, declaration)
			continue
		}
		fmt.Fprintf(b, "func append%s%sJSON(w *errors.DataWriter, value *%s) {\n", g.objectPrefix, name, name)
		b.WriteString("\tif value == nil { w.Token(\"null\"); return }\n\tif !w.Enter() { return }\n\tw.Token(\"{\")\n")
		index := 0
		for _, field := range g.objects[name] {
			if g.objectFieldPolicy(name, field).omit {
				continue
			}
			writeErrorFieldName(b, field.Name, index, "\t")
			fmt.Fprintf(b, "\tappend%s%sField%d(w, value)\n", g.objectPrefix, name, index)
			index++
		}
		b.WriteString("\tw.Token(\"}\")\n\tw.Leave()\n}\n\n")
		index = 0
		for _, field := range g.objects[name] {
			policy := g.objectFieldPolicy(name, field)
			if policy.omit {
				continue
			}
			fmt.Fprintf(b, "func append%s%sField%d(w *errors.DataWriter, value *%s) {\n", g.objectPrefix, name, index, name)
			b.WriteString("\tif w.Err() != nil { return }\n")
			var mask *fieldMaskRule
			if policy.mask.kind != fieldMaskNone {
				mask = &policy.mask
				g.imports |= generatedPackageBit("str")
			}
			g.writeValue(b, field.Type, "value."+str.Capitalize(field.Name), isRelationField(field, g.enums), false, "\t", mask)
			b.WriteString("}\n\n")
			index++
		}
	}
}

func (g *errorDataGenerator) objectFieldPolicy(model string, field *ast.FieldDecl) errorFieldPolicy {
	local := errorPolicyFor(field, g.publicErrorData)
	if local.omit || !g.publicErrorData || g.owner.events == nil || !g.models[model] ||
		g.owner.modelDeclarations[model] != nil {
		return local
	}
	owner := g.owner.events.modelErrorPolicies[model][field.Name]
	switch {
	case owner.omit:
		return owner
	case owner.mask.kind == fieldMaskNone:
		return local
	case local.mask.kind == fieldMaskNone, local.mask == owner.mask:
		return owner
	default:
		// A context-free error cannot choose between incompatible masks.
		return errorFieldPolicy{omit: true}
	}
}
