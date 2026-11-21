package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type debugLogGenerator struct {
	owner   *GeneratorContext
	source  *semantic.Result
	enums   map[string]bool
	objects map[string][]*ast.FieldDecl
	events  map[string]bool
	queue   []string
}

func (g *debugLogGenerator) requireObject(name string) {
	if _, known := g.objects[name]; known {
		return
	}
	fields, found := g.findObject(name)
	if !found {
		panic(&InternalCompilerError{Detail: "debug receiver has no static declaration: " + name})
	}
	if g.objects == nil {
		g.objects = make(map[string][]*ast.FieldDecl)
	}
	g.objects[name] = fields
	g.queue = append(g.queue, name)
}

func (g *debugLogGenerator) findObject(name string) ([]*ast.FieldDecl, bool) {
	if g.source == nil {
		return nil, false
	}
	for _, file := range g.source.Files {
		for _, declaration := range file.Models {
			if declaration.Name == name {
				return declaration.Fields, true
			}
		}
		for _, declaration := range file.Types {
			if declaration.Name == name {
				return declaration.Fields, true
			}
		}
		for _, declaration := range file.Extends {
			if declaration.Name == name {
				return g.owner.extendStubModel(declaration).Fields, true
			}
		}
		for _, declaration := range file.Events {
			if declaration.Name+"Event" == name {
				return g.eventFields(declaration), true
			}
		}
	}
	return nil, false
}

func (g *debugLogGenerator) eventFields(declaration *ast.EventDecl) []*ast.FieldDecl {
	fields := make([]*ast.FieldDecl, 0, len(declaration.Params))
	for _, param := range declaration.Params {
		fields = append(fields, &ast.FieldDecl{Name: param.Name, Type: param.Type})
	}
	if g.events == nil {
		g.events = make(map[string]bool)
	}
	g.events[declaration.Name+"Event"] = true
	return fields
}

func (g *GeneratorContext) generateDebugFile(packageName string) []byte {
	if g.debugLogs == nil || len(g.debugLogs.queue) == 0 {
		return nil
	}
	var b strings.Builder
	for index := 0; index < len(g.debugLogs.queue); index++ {
		g.debugLogs.writeObject(&b, g.debugLogs.queue[index])
	}
	var out strings.Builder
	writeHeader(&out, packageName, "debug.gen.go")
	out.WriteString("import (\n\"github.com/light-speak/luxo/pkg/lux/luxolog\"\n")
	if generatedPackages(b.String()).has("lux") {
		out.WriteString("\"github.com/light-speak/luxo/pkg/lux\"\n")
	}
	out.WriteString(")\n")
	out.WriteString(b.String())
	return []byte(out.String())
}

func (g *debugLogGenerator) writeObject(b *strings.Builder, name string) {
	fmt.Fprintf(b, "func (value *%s) luxoAppendDebug(record *luxolog.Record, depth int) {\n", name)
	b.WriteString("if record.Full() { record.Truncate(); return }\n")
	b.WriteString("if value == nil { record.Text(\"null\"); return }\n")
	b.WriteString("if depth > luxolog.MaxValueDepth { record.Text(\"<depth-limit>\"); return }\n")
	fmt.Fprintf(b, "record.Text(%q)\n", name+"{")
	for index, field := range g.objects[name] {
		b.WriteString("if record.Full() { record.Truncate(); return }\n")
		prefix := field.Name + ": "
		if index != 0 {
			prefix = ", " + prefix
		}
		fmt.Fprintf(b, "record.Text(%q)\n", prefix)
		if hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") || hasDirective(field.Directives, "hash") {
			b.WriteString("record.Text(\"<redacted>\")\n")
			continue
		}
		pointer := !g.events[name] && field.Type != nil && !field.Type.IsList && isRelationField(field, g.enums)
		g.writeValue(b, field.Type, "value."+str.Capitalize(field.Name), "record", "depth + 1", "", pointer, false)
	}
	b.WriteString("record.Text(\"}\")\n}\n\n")
}

func (g *debugLogGenerator) writeValue(b *strings.Builder, ref *ast.TypeRef, value, record, depth, indent string, pointer, root bool) {
	if ref == nil {
		panic(&InternalCompilerError{Detail: "debug value has no static type"})
	}
	if ref.IsList {
		g.writeList(b, ref, value, record, depth, indent)
		return
	}
	if ref.Nullable || pointer {
		fmt.Fprintf(b, "%sif %s == nil { %s.Text(\"null\") } else {\n", indent, value, record)
		if !pointer {
			value = "(*" + value + ")"
		}
		g.writeScalar(b, ref, value, record, depth, indent+"\t", root)
		fmt.Fprintf(b, "%s}\n", indent)
		return
	}
	g.writeScalar(b, ref, value, record, depth, indent, root)
}

func (g *debugLogGenerator) writeList(b *strings.Builder, ref *ast.TypeRef, value, record, depth, indent string) {
	if ref.Nullable {
		fmt.Fprintf(b, "%sif %s == nil { %s.Text(\"null\") } else {\n", indent, value, record)
		indent += "\t"
	}
	fmt.Fprintf(b, "%s%s.Text(\"[\")\n%sfor _debugIndex, _debugItem := range %s {\n", indent, record, indent, value)
	fmt.Fprintf(b, "%s\tif %s.Full() { %s.Truncate(); break }\n", indent, record, record)
	fmt.Fprintf(b, "%s\tif _debugIndex != 0 { %s.Text(\", \") }\n", indent, record)
	element := *ref
	element.IsList, element.Nullable = false, false
	g.writeValue(b, &element, "_debugItem", record, depth, indent+"\t", g.owner.isModelReference(ref.Name), false)
	fmt.Fprintf(b, "%s}\n%s%s.Text(\"]\")\n", indent, indent, record)
	if ref.Nullable {
		fmt.Fprintf(b, "%s}\n", strings.TrimSuffix(indent, "\t"))
	}
}

func (g *debugLogGenerator) writeScalar(b *strings.Builder, ref *ast.TypeRef, value, record, depth, indent string, root bool) {
	method, argument := "", value
	switch ref.Name {
	case "Int":
		method = "Int"
	case "Float":
		method = "Float"
	case "Boolean":
		method = "Boolean"
	case "String":
		method = "String"
		if root {
			method = "Text"
		}
	case "Bytes":
		method = "Hex"
	case "JSON":
		method = "Raw"
	case "DateTime", "Duration", "UUID":
		method, argument = "Text", "("+value+").String()"
	case "Decimal":
		method, argument = "Text", "lux.FormatDecimal("+value+")"
	case "Channel":
		fmt.Fprintf(b, "%s%s.Text(\"Channel(len=\")\n%s%s.Int(int64(len(%s)))\n", indent, record, indent, record, value)
		fmt.Fprintf(b, "%s%s.Text(\", cap=\")\n%s%s.Int(int64(cap(%s)))\n%s%s.Text(\")\")\n", indent, record, indent, record, value, indent, record)
		return
	default:
		if g.enums[ref.Name] {
			method, argument = "String", "string("+value+")"
		} else {
			g.requireObject(ref.Name)
			fmt.Fprintf(b, "%s(%s).luxoAppendDebug(%s, %s)\n", indent, value, record, depth)
			return
		}
	}
	fmt.Fprintf(b, "%s%s.%s(%s)\n", indent, record, method, argument)
}
