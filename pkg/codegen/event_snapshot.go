package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type eventSnapshotGenerator struct {
	objects map[string][]*ast.FieldDecl
	enums   map[string]bool
	queued  map[string]bool
	queue   []string
}

func (generator *GeneratorContext) generateEventSnapshots(b *strings.Builder, result *semantic.Result, events []*ast.EventDecl, enums map[string]bool) {
	g := eventSnapshotGenerator{objects: make(map[string][]*ast.FieldDecl), enums: enums, queued: make(map[string]bool)}
	for _, file := range result.Files {
		for _, declaration := range file.Types {
			g.objects[declaration.Name] = declaration.Fields
		}
	}
	for _, declaration := range events {
		g.writeEvent(b, declaration)
	}
	objects := collectEventObjectTypes(result)
	for index := 0; index < len(g.queue); index++ {
		name := g.queue[index]
		g.writeObject(b, name)
		generator.generateEventObjectCodec(b, name, g.objects[name], enums, objects)
	}
}

func (g *eventSnapshotGenerator) writeEvent(b *strings.Builder, declaration *ast.EventDecl) {
	name := declaration.Name + "Event"
	fmt.Fprintf(b, "// SnapshotEvent validates and copies %s without encoding.\n", name)
	fmt.Fprintf(b, "func (e *%s) SnapshotEvent() (event.Snapshot, error) {\n", name)
	b.WriteString("\tif e == nil { return nil, event.ErrInvalidSnapshot }\n\tconst depth = 0\n\tout := *e\n")
	for _, param := range declaration.Params {
		field := str.Capitalize(param.Name)
		g.writeValue(b, param.Type, "out."+field, "e."+field, false, true, "nil", "\t")
	}
	if !g.needsClone(declaration.Params) {
		b.WriteString("\treturn event.NewImmutableSnapshot(out), nil\n}\n\n")
		return
	}
	fmt.Fprintf(b, "\treturn event.NewSnapshot(out, clone%s)\n}\n\n", name)
	fmt.Fprintf(b, "func clone%s(value %s) %s {\n\tout := value\n", name, name, name)
	for _, param := range declaration.Params {
		field := str.Capitalize(param.Name)
		g.writeValue(b, param.Type, "out."+field, "value."+field, false, false, "", "\t")
	}
	b.WriteString("\treturn out\n}\n\n")
}

func (g *eventSnapshotGenerator) needsClone(params []*ast.ParamDecl) bool {
	for _, param := range params {
		ref := param.Type
		if ref == nil {
			continue
		}
		if ref.Nullable || ref.IsList || ref.Name == "Bytes" || ref.Name == "JSON" || (!isEventBuiltin(ref.Name) && !g.enums[ref.Name]) {
			return true
		}
	}
	return false
}

func (g *eventSnapshotGenerator) writeObject(b *strings.Builder, name string) {
	for _, validate := range []bool{true, false} {
		prefix, result := "clone", name
		params := ""
		if validate {
			prefix, result = "copy", "("+name+", error)"
			params = ", depth int"
		}
		fmt.Fprintf(b, "func %sEvent%s(value %s%s) %s {\n", prefix, name, name, params, result)
		if validate {
			fmt.Fprintf(b, "\tif depth > event.MaxSnapshotDepth { return %s{}, event.ErrSnapshotDepth }\n", name)
		}
		b.WriteString("\tout := value\n")
		for _, field := range g.objects[name] {
			if field.Type == nil {
				continue
			}
			goName := str.Capitalize(field.Name)
			_, object := g.objects[field.Type.Name]
			g.writeValue(b, field.Type, "out."+goName, "value."+goName, object, validate, name+"{}", "\t")
		}
		if validate {
			b.WriteString("\treturn out, nil\n}\n\n")
		} else {
			b.WriteString("\treturn out\n}\n\n")
		}
	}
}

func (g *eventSnapshotGenerator) writeValue(b *strings.Builder, ref *ast.TypeRef, target, source string, objectPointer, validate bool, zero, indent string) {
	if ref == nil {
		return
	}
	if ref.IsList {
		g.writeList(b, ref, target, source, validate, zero, indent)
		return
	}
	if ref.Nullable || objectPointer {
		if objectPointer && !ref.Nullable && validate {
			fmt.Fprintf(b, "%sif %s == nil { return %s, event.ErrInvalidSnapshot }\n", indent, source, zero)
		}
		fmt.Fprintf(b, "%sif %s != nil {\n%s\t_copied := *%s\n", indent, source, indent, source)
		g.writeScalar(b, ref, "_copied", "(*"+source+")", validate, zero, indent+"\t")
		fmt.Fprintf(b, "%s\t%s = &_copied\n%s}\n", indent, target, indent)
		return
	}
	g.writeScalar(b, ref, target, source, validate, zero, indent)
}

func (g *eventSnapshotGenerator) writeList(b *strings.Builder, ref *ast.TypeRef, target, source string, validate bool, zero, indent string) {
	if ref.Nullable {
		fmt.Fprintf(b, "%sif %s != nil {\n", indent, source)
		indent += "\t"
	}
	fmt.Fprintf(b, "%s%s = make(%s, len(%s))\n", indent, target, resolveGoType(ref), source)
	_, object := g.objects[ref.Name]
	// Nested snapshots, mutable byte values and normalized timestamps assign
	// every destination element below; an initial bulk copy would be discarded.
	assignsElements := object || ref.Name == "Bytes" || ref.Name == "JSON" || (validate && ref.Name == "DateTime")
	if !assignsElements {
		fmt.Fprintf(b, "%scopy(%s, %s)\n", indent, target, source)
	}
	if object || ref.Name == "Bytes" || ref.Name == "JSON" || (validate && (g.enums[ref.Name] || eventValidationMethod(ref.Name) != "")) {
		fmt.Fprintf(b, "%sfor i := range %s {\n", indent, source)
		g.writeScalar(b, ref, target+"[i]", source+"[i]", validate, zero, indent+"\t")
		fmt.Fprintf(b, "%s}\n", indent)
	}
	if ref.Nullable {
		fmt.Fprintf(b, "%s}\n", indent[:len(indent)-1])
	}
}

func (g *eventSnapshotGenerator) writeScalar(b *strings.Builder, ref *ast.TypeRef, target, source string, validate bool, zero, indent string) {
	if _, object := g.objects[ref.Name]; object {
		g.writeNested(b, ref.Name, target, source, validate, zero, indent)
		return
	}
	if validate {
		if g.enums[ref.Name] {
			fmt.Fprintf(b, "%sif !%s.IsValid() { return %s, codec.ErrInvalidEnum }\n", indent, source, zero)
		} else if method := eventValidationMethod(ref.Name); method != "" {
			fmt.Fprintf(b, "%sif err := event.%s(%s); err != nil { return %s, err }\n", indent, method, source, zero)
		}
	}
	if ref.Name == "Bytes" || ref.Name == "JSON" {
		fmt.Fprintf(b, "%s%s = bytes.Clone(%s)\n", indent, target, source)
	}
	if validate && ref.Name == "DateTime" {
		fmt.Fprintf(b, "%s%s = %s.UTC()\n", indent, target, source)
	}
}

func (g *eventSnapshotGenerator) writeNested(b *strings.Builder, name, target, source string, validate bool, zero, indent string) {
	if !g.queued[name] {
		g.queued[name] = true
		g.queue = append(g.queue, name)
	}
	if !validate {
		fmt.Fprintf(b, "%s%s = cloneEvent%s(%s)\n", indent, target, name, source)
		return
	}
	fmt.Fprintf(b, "%s{\n%s\t_nested, err := copyEvent%s(%s, depth + 1)\n", indent, indent, name, source)
	fmt.Fprintf(b, "%s\tif err != nil { return %s, err }\n%s\t%s = _nested\n%s}\n", indent, zero, indent, target, indent)
}

func eventValidationMethod(name string) string {
	switch name {
	case "String", "Float", "DateTime", "JSON":
		return "Check" + name
	default:
		return ""
	}
}
