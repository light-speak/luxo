package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// Response objects are framed; input objects retain their recursive row grammar.
// The choice is made by the generator, never by a hot-path runtime mode switch.
func (g *GeneratorContext) writeReadNestedValue(b *strings.Builder, f *ast.FieldDecl, goField string, input bool, modelLists ...bool) {
	if g.sealedDeclaration(f.Type.Name) != nil {
		g.writeSealedNestedInput(b, f, goField, input, true)
		return
	}
	if f.Type.Nullable {
		fmt.Fprintf(b, "if !dec.ReadBool() { %s = nil } else {\n", goField)
	}
	if input {
		g.writeReadNestedInput(b, f, goField, modelLists...)
	} else if f.Type.IsList {
		if len(modelLists) > 0 && modelLists[0] {
			fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _items, _err := %s(_raw, depth+1); dec.Fail(_err); if _err == nil { %s = reference%sList(_items) } } }\n", g.depthColumnarFunction("ReadColumnar"+f.Type.Name), goField, f.Type.Name)
		} else {
			fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _items, _err := %s(_raw, depth+1); dec.Fail(_err); %s = _items } }\n", g.depthColumnarFunction("ReadColumnar"+f.Type.Name), goField)
		}
	} else {
		fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _child := codec.NewDecoder(_raw); %s = &%s{}; %s.%s(_child, depth+1); dec.Fail(_child.Finish()) } }\n", goField, f.Type.Name, goField, g.valueDepthMethod(f.Type.Name, "readLuxoDepth"))
	}
	if f.Type.Nullable {
		b.WriteString("}\n")
	}
}

func (g *GeneratorContext) writeReadNestedInput(b *strings.Builder, f *ast.FieldDecl, goField string, modelLists ...bool) {
	method := g.valueDepthMethod(f.Type.Name, "readLuxoInputDepth")
	if f.Type.IsList {
		if len(modelLists) > 0 && modelLists[0] {
			fmt.Fprintf(b, "{ _n := dec.ReadArrayLength(); if dec.CheckElementBytes(_n, 2) { _values := make([]%s, _n); for _index := range _values { if dec.Err() != nil { break }; _values[_index].%s(dec, depth+1) }; if dec.Err() == nil { %s = reference%sList(_values) } } }\n", f.Type.Name, method, goField, f.Type.Name)
			return
		}
		fmt.Fprintf(b, "{ _n := dec.ReadArrayLength(); if dec.CheckElementBytes(_n, 2) { %s = make([]%s, _n); for _index := range %s { if dec.Err() != nil { break }; %s[_index].%s(dec, depth+1) } } }\n", goField, f.Type.Name, goField, goField, method)
	} else {
		fmt.Fprintf(b, "%s = &%s{}\n%s.%s(dec, depth+1)\n", goField, f.Type.Name, goField, method)
	}
}

// Read directly into final rows instead of allocating a typed slice per column.
func (g *GeneratorContext) generateReadColumnar(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool) {
	if g.isModelReference(model.Name) {
		fmt.Fprintf(b, "func reference%sList(values []%s) []*%s { if values == nil { return nil }; refs := make([]*%s, len(values)); for i := range values { refs[i] = &values[i] }; return refs }\n\n", model.Name, model.Name, model.Name, model.Name)
	}
	fmt.Fprintf(b, "// ReadColumnar%s reads a complete columnar response.\n", model.Name)
	fmt.Fprintf(b, "func ReadColumnar%s(data []byte) ([]%s, error) {\n", model.Name, model.Name)
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "return %s(data, 1)\n}\n\n", g.depthColumnarFunction("ReadColumnar"+model.Name))
		fmt.Fprintf(b, "func %s(data []byte, depth int) ([]%s, error) {\n", g.depthColumnarFunction("ReadColumnar"+model.Name), model.Name)
	}
	b.WriteString("r := codec.NewColumnarReader(data)\nif r.Err() != nil { return nil, r.Err() }\n")
	if !g.singleObjectDepth {
		b.WriteString("if r.Count() > 0 && depth > codec.MaxModelDepth { return nil, codec.ErrModelDepth }\n")
	}
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
		if plan := g.instanceSaves[model.Name]; plan != nil {
			if slot, ok := plan.index[f.Name]; ok {
				fmt.Fprintf(b, "%s\n", plan.bitSet("items[_row]", "Loaded", slot))
			}
		}
		g.writeReadColumnarValue(b, f, "items[_row]."+str.Capitalize(f.Name), enums, useArena, g.isModelReference(f.Type.Name))
		b.WriteString("}\n")
	}
	b.WriteString("default: dec.SkipField()\n}\n}\nif err := dec.Finish(); err != nil { return nil, err }\n")
	if plan := g.instanceSaves[model.Name]; plan != nil {
		plan.write(b, "for i := range items { items[i]._luxoSaveCapture() }\n")
	}
	b.WriteString("return items, nil\n}\n\n")
}

func (g *GeneratorContext) writeReadColumnarValue(b *strings.Builder, f *ast.FieldDecl, goField string, enums map[string]bool, useArena bool, modelLists ...bool) {
	switch {
	case f.Type.IsList && g.sealedDeclaration(f.Type.Name) != nil:
		if f.Type.Nullable {
			fmt.Fprintf(b, "if !dec.ReadBool() { %s = nil } else {\n", goField)
		}
		fmt.Fprintf(b, "{ _raw := dec.ReadBytes(); if dec.Err() == nil { _items, _err := %s(_raw, depth+1); dec.Fail(_err); %s = _items } }\n", g.depthColumnarFunction("ReadColumnar"+f.Type.Name), goField)
		if f.Type.Nullable {
			b.WriteString("}\n")
		}
	case isRelationField(f, enums):
		g.writeReadNestedValue(b, f, goField, false, modelLists...)
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
