package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// Response objects are framed; input objects retain their recursive row grammar.
// The choice is made by the generator, never by a hot-path runtime mode switch.
func writeReadNestedValue(b *strings.Builder, f *ast.FieldDecl, goField string, input bool) {
	if f.Type.Nullable {
		fmt.Fprintf(b, "if !dec.ReadBool() { %s = nil } else {\n", goField)
	}
	if input {
		writeReadNestedInput(b, f, goField)
	} else if f.Type.IsList {
		fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _items, _err := ReadColumnar%s(_raw); dec.Fail(_err); %s = _items } }\n", f.Type.Name, goField)
	} else {
		fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _child := codec.NewDecoder(_raw); %s = &%s{}; %s.ReadLuxo(_child); dec.Fail(_child.Finish()) } }\n", goField, f.Type.Name, goField)
	}
	if f.Type.Nullable {
		b.WriteString("}\n")
	}
}

func writeReadNestedInput(b *strings.Builder, f *ast.FieldDecl, goField string) {
	if f.Type.IsList {
		fmt.Fprintf(b, "{ _n := dec.ReadArrayLength(); if dec.CheckElementBytes(_n, 2) { %s = make([]%s, _n); for _index := range %s { if dec.Err() != nil { break }; %s[_index].ReadLuxoInput(dec) } } }\n", goField, f.Type.Name, goField, goField)
	} else {
		fmt.Fprintf(b, "%s = &%s{}\n%s.ReadLuxoInput(dec)\n", goField, f.Type.Name, goField)
	}
}

// Read directly into final rows instead of allocating a typed slice per column.
func (g *GeneratorContext) generateReadColumnar(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool) {
	fmt.Fprintf(b, "// ReadColumnar%s reads a complete columnar response.\n", model.Name)
	fmt.Fprintf(b, "func ReadColumnar%s(data []byte) ([]%s, error) {\n", model.Name, model.Name)
	b.WriteString("r := codec.NewColumnarReader(data)\nif r.Err() != nil { return nil, r.Err() }\n")
	b.WriteString("dec := codec.NewDecoder(data[r.Offset():])\n")
	fmt.Fprintf(b, "items := make([]%s, r.Count())\n", model.Name)
	useArena := g.hasArenaFields(model, enums)
	if useArena {
		b.WriteString("var _arena []byte\nvar _arenaOff int\nif r.ArenaSize() > 0 { _arena = make([]byte, r.ArenaSize()) }\n")
	}
	b.WriteString("for dec.NextField() {\nswitch dec.FieldID() {\n")
	for _, f := range model.Fields {
		if f.Type == nil || hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		fid := g.modelFieldID(model.Name, f.Name)
		if fid == 0 {
			continue
		}
		fmt.Fprintf(b, "case %d:\nfor _row := range items {\nif dec.Err() != nil { break }\n", fid)
		writeReadColumnarValue(b, f, "items[_row]."+str.Capitalize(f.Name), enums, useArena)
		b.WriteString("}\n")
	}
	b.WriteString("default: dec.SkipField()\n}\n}\nif err := dec.Finish(); err != nil { return nil, err }\nreturn items, nil\n}\n\n")
}

func writeReadColumnarValue(b *strings.Builder, f *ast.FieldDecl, goField string, enums map[string]bool, useArena bool) {
	switch {
	case isRelationField(f, enums):
		writeReadNestedValue(b, f, goField, false)
	case f.Type.IsList:
		if f.Type.Nullable {
			fmt.Fprintf(b, "if !dec.ReadBool() { %s = nil; continue }\n", goField)
		}
		b.WriteString("{ _parent := dec; dec := codec.NewDecoder(_parent.ReadBytes()); if _parent.Err() == nil {\n")
		writeReadScalarArrayValue(b, f.Type.Name, goField, enums[f.Type.Name])
		b.WriteString("\n_parent.Fail(dec.Finish())\n} }\n")
	case useArena && isArenaField(f, enums):
		writeReadLuxoValueArena(b, f, goField, enums)
	default:
		writeReadLuxoValue(b, f, goField, enums)
	}
}
