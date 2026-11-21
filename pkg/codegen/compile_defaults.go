package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func hasObjectDefault(field *ast.FieldDecl) bool {
	return field.Type != nil && field.Default != nil && field.Computed == nil
}

// Constructor/input defaults belong to value types, not partial model projections.
func collectObjectDefaults(files []*ast.File) map[string][]*ast.FieldDecl {
	var defaults map[string][]*ast.FieldDecl
	for _, file := range files {
		for _, declaration := range file.Types {
			for _, field := range declaration.Fields {
				if !hasObjectDefault(field) {
					continue
				}
				if defaults == nil {
					defaults = make(map[string][]*ast.FieldDecl)
				}
				defaults[declaration.Name] = append(defaults[declaration.Name], field)
			}
		}
	}
	return defaults
}

func (c *compiler) compileObjectDefaults(name string, explicit []*ast.NamedArg, fields []string) []string {
	if c.generator == nil || len(c.generator.objectDefaults[name]) == 0 {
		return fields
	}
	var provided map[string]bool
	if len(explicit) > 0 {
		provided = make(map[string]bool, len(explicit))
		for _, field := range explicit {
			provided[field.Name] = true
		}
	}
	for _, field := range c.generator.objectDefaults[name] {
		if !provided[field.Name] {
			fields = append(fields, str.Capitalize(field.Name)+": "+c.objectDefaultValue(field))
		}
	}
	return fields
}

func (c *compiler) objectDefaultValue(field *ast.FieldDecl) string {
	if field.Type.Name == "JSON" && !field.Type.Nullable && !field.Type.IsList && isNullLiteral(field.Default) {
		return `[]byte("null")`
	}
	return c.compileFunctionDefault(&ast.ParamDecl{Type: field.Type, Default: field.Default})
}

func writeObjectDefaultAssignment(b *strings.Builder, field *ast.FieldDecl, target, indent string, enums map[string]bool) {
	c := &compiler{b: b, indent: indent, enums: enums}
	fmt.Fprintf(b, "%s%s = %s\n", indent, target, c.objectDefaultValue(field))
}

func (g *GeneratorContext) writeUnaddressableInputDefaults(b *strings.Builder, name string, enums map[string]bool) {
	for _, field := range g.objectDefaults[name] {
		if hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") || g.modelFieldID(name, field.Name) == 0 {
			writeObjectDefaultAssignment(b, field, "out."+str.Capitalize(field.Name), "\t", enums)
		}
	}
}

func (g *GeneratorContext) writeBinaryInputDefaults(b *strings.Builder, name, receiver, seen string, enums map[string]bool) {
	defaults := g.objectDefaults[name]
	b.WriteString("\tif dec.Err() != nil { return }\n")
	for index, field := range defaults {
		fmt.Fprintf(b, "\tif %s[%d] & (uint64(1) << %d) == 0 {\n", seen, index/64, index%64)
		writeObjectDefaultAssignment(b, field, receiver+"."+str.Capitalize(field.Name), "\t\t", enums)
		b.WriteString("\t}\n")
	}
}
