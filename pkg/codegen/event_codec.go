package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// Event codecs own their complete field set, independently of API projection,
// arena layout, visibility, transforms and database model relationships.
type eventWireCodec struct {
	name          string
	marshalName   string
	unmarshalName string
	fields        []*ast.ParamDecl
	ids           map[string]int
	objectFields  bool
}

func (c eventWireCodec) write(b *strings.Builder, enums, objects map[string]bool) {
	params := ""
	if c.objectFields {
		params = "dst []byte, depth int"
	}
	fmt.Fprintf(b, "func (e *%s) %s(%s) ([]byte, error) {\n", c.name, c.marshalName, params)
	b.WriteString("\tif e == nil { return nil, fmt.Errorf(\"event: nil snapshot\") }\n")
	c.writeDepthBoundary(b, "nil, ")
	if c.objectFields {
		b.WriteString("\tenc := codec.NewEncoder(dst)\n")
	} else {
		b.WriteString("\tvar enc codec.Encoder\n")
	}
	for _, field := range c.fields {
		if field.Type == nil || c.ids[field.Name] == 0 {
			continue
		}
		if c.objectFields && objects[field.Type.Name] && !field.Type.IsList && !field.Type.Nullable {
			fmt.Fprintf(b, "\tif e.%s == nil { return nil, fmt.Errorf(%q) }\n", str.Capitalize(field.Name), "event: nil required field "+c.name+"."+field.Name)
		}
		writeEventMarshalField(b, c.ids[field.Name], field, enums[field.Type.Name], objects[field.Type.Name])
	}
	b.WriteString("\tenc.WriteEnd()\n\treturn enc.Bytes(), nil\n}\n\n")
	c.writeReader(b, enums, objects)
}

func (c eventWireCodec) writeReader(b *strings.Builder, enums, objects map[string]bool) {
	params := "data []byte"
	if c.objectFields {
		params += ", depth int"
	}
	fmt.Fprintf(b, "func (e *%s) %s(%s) error {\n", c.name, c.unmarshalName, params)
	b.WriteString("\tif e == nil { return fmt.Errorf(\"event: nil snapshot\") }\n")
	c.writeDepthBoundary(b, "")
	for _, field := range c.fields {
		if c.required(field) {
			fmt.Fprintf(b, "\tvar seen%s bool\n", str.Capitalize(field.Name))
		}
	}
	b.WriteString("\tdec := codec.NewDecoder(data)\n\tfor dec.NextField() {\n\t\tswitch dec.FieldID() {\n")
	for _, field := range c.fields {
		if field.Type == nil || c.ids[field.Name] == 0 {
			continue
		}
		writeEventUnmarshalField(b, c.ids[field.Name], field, enums[field.Type.Name], objects[field.Type.Name], c.objectFields)
		if c.required(field) {
			fmt.Fprintf(b, "\t\t\tseen%s = true\n", str.Capitalize(field.Name))
		}
	}
	b.WriteString("\t\tdefault: dec.SkipField()\n\t\t}\n\t}\n\tif err := dec.Finish(); err != nil { return err }\n")
	for _, field := range c.fields {
		if c.required(field) {
			fmt.Fprintf(b, "\tif !seen%s { return fmt.Errorf(%q) }\n", str.Capitalize(field.Name), "event "+c.name+": missing required field "+field.Name)
		}
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func (c eventWireCodec) writeDepthBoundary(b *strings.Builder, zero string) {
	if c.objectFields {
		fmt.Fprintf(b, "\tif depth > event.MaxSnapshotDepth { return %sevent.ErrSnapshotDepth }\n", zero)
	} else {
		b.WriteString("\tconst depth = 0\n")
	}
}

func (c eventWireCodec) required(field *ast.ParamDecl) bool {
	return field.Type != nil && !field.Type.Nullable && c.ids[field.Name] != 0
}

func (g *GeneratorContext) generateEventObjectCodec(b *strings.Builder, name string, fields []*ast.FieldDecl, enums, objects map[string]bool) {
	params := make([]*ast.ParamDecl, 0, len(fields))
	for _, field := range fields {
		params = append(params, &ast.ParamDecl{Name: field.Name, Type: field.Type})
	}
	c := eventWireCodec{name: name, marshalName: "marshalEventSnapshot", unmarshalName: "unmarshalEventSnapshot", fields: params, ids: g.ids.ModelFields[name], objectFields: true}
	c.write(b, enums, objects)
}
