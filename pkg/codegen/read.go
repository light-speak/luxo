package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// ReadLuxo is the inverse of WriteLuxo, including remote DataLoader responses.
func generateReadLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateReadLuxo(b, m, enums)
}

func (g *GeneratorContext) generateReadLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	g.generateRowReader(b, m, enums, false)
	if g.hasWriteRelation(m, enums) {
		g.generateRowReader(b, m, enums, true)
	} else {
		fmt.Fprintf(b, "// ReadLuxoInput reads an input with the same scalar-only layout.\nfunc (v *%s) ReadLuxoInput(dec *codec.Decoder) { v.ReadLuxo(dec) }\n\n", m.Name)
	}
}

func (g *GeneratorContext) generateRowReader(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, input bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])
	useArena := g.hasArenaFields(m, enums)
	method := "ReadLuxo"
	if input {
		method = "ReadLuxoInput"
	}

	fmt.Fprintf(b, "// %s decodes %s from Luxo binary format.\n", method, name)
	fmt.Fprintf(b, "func (%s *%s) %s(dec *codec.Decoder) {\n", recv, name, method)

	if useArena {
		fmt.Fprintf(b, "\t_arenaSize := dec.ReadArenaSize()\n")
		fmt.Fprintf(b, "\tvar _arena []byte\n")
		fmt.Fprintf(b, "\tvar _arenaOff int\n")
		fmt.Fprintf(b, "\tif _arenaSize > 0 {\n")
		fmt.Fprintf(b, "\t\t_arena = make([]byte, _arenaSize)\n")
		fmt.Fprintf(b, "\t}\n")
	} else {
		fmt.Fprintf(b, "\tdec.SkipArenaHeader()\n")
	}

	fmt.Fprintf(b, "\tfor dec.NextField() {\n")
	fmt.Fprintf(b, "\t\tswitch dec.FieldID() {\n")

	for _, f := range m.Fields {
		if f.Type == nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		fieldID := g.modelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		goField := recv + "." + str.Capitalize(f.Name)

		if isRelationField(f, enums) {
			fmt.Fprintf(b, "\t\tcase %d:\n", fieldID)
			writeReadNestedValue(b, f, goField, input)
		} else if useArena && isArenaField(f, enums) {
			writeReadLuxoFieldArena(b, f, fieldID, goField, enums)
		} else {
			writeReadLuxoField(b, f, fieldID, goField, enums)
		}
	}

	fmt.Fprintf(b, "\t\tdefault: dec.SkipField()\n")
	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t}\n")   // end for
	fmt.Fprintf(b, "}\n\n")
}

func writeReadLuxoFieldArena(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool) {
	fmt.Fprintf(b, "\t\tcase %d:", fieldID)
	writeReadLuxoValueArena(b, f, goField, enums)
}

func writeReadLuxoField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool) {
	fmt.Fprintf(b, "\t\tcase %d:", fieldID)
	writeReadLuxoValue(b, f, goField, enums)
}

// writeReadLuxoValueArena writes a single field's decode case using arena allocation.
// Only called for String and Enum fields (isArenaField == true).
func writeReadLuxoValueArena(b *strings.Builder, f *ast.FieldDecl, goField string, enums map[string]bool) {
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			writeReadNullableConversion(b, "dec.ReadStringArenaPtr(_arena, &_arenaOff)", f.Type.Name+"(*_raw)", goField)
		} else {
			fmt.Fprintf(b, " %s = %s(dec.ReadStringArena(_arena, &_arenaOff))\n", goField, f.Type.Name)
		}
		writeReadEnumCheck(b, f, goField)
		return
	}
	// String / String?
	if f.Type.Nullable {
		fmt.Fprintf(b, " %s = dec.ReadStringArenaPtr(_arena, &_arenaOff)\n", goField)
	} else {
		fmt.Fprintf(b, " %s = dec.ReadStringArena(_arena, &_arenaOff)\n", goField)
	}
}

// writeReadLuxoValue writes a single field's decode case for ReadLuxo.
func writeReadLuxoValue(b *strings.Builder, f *ast.FieldDecl, goField string, enums map[string]bool) {
	if f.Type.IsList {
		writeReadLuxoListValue(b, f, goField, enums)
		return
	}
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			writeReadNullableConversion(b, "dec.ReadStringPtr()", f.Type.Name+"(*_raw)", goField)
		} else {
			fmt.Fprintf(b, " %s = %s(dec.ReadString())\n", goField, f.Type.Name)
		}
		writeReadEnumCheck(b, f, goField)
		return
	}
	type decSpec struct {
		read    string // non-nullable read expression
		readPtr string // nullable: read expression returning pointer
	}
	simple := map[string]decSpec{
		"Int":     {read: "dec.ReadInt()", readPtr: "dec.ReadIntPtr()"},
		"Float":   {read: "dec.ReadFloat()", readPtr: "dec.ReadFloatPtr()"},
		"String":  {read: "dec.ReadString()", readPtr: "dec.ReadStringPtr()"},
		"Boolean": {read: "dec.ReadBool()", readPtr: "dec.ReadBoolPtr()"},
	}
	if spec, ok := simple[f.Type.Name]; ok {
		if f.Type.Nullable {
			fmt.Fprintf(b, " %s = %s\n", goField, spec.readPtr)
		} else {
			fmt.Fprintf(b, " %s = %s\n", goField, spec.read)
		}
		return
	}
	writeReadLuxoTypedValue(b, f, goField)
}

// writeReadLuxoTypedValue decodes scalar types that need a conversion wrapper
// (DateTime/Duration/UUID/Decimal/Bytes/JSON) for ReadLuxo.
func writeReadLuxoTypedValue(b *strings.Builder, f *ast.FieldDecl, goField string) {
	switch f.Type.Name {
	case "DateTime":
		if f.Type.Nullable {
			fmt.Fprintf(b, " %s = dec.ReadDateTimePtr()\n", goField)
		} else {
			fmt.Fprintf(b, " %s = dec.ReadDateTime()\n", goField)
		}
	case "Duration":
		if f.Type.Nullable {
			writeReadNullableConversion(b, "dec.ReadIntPtr()", "time.Duration(*_raw)", goField)
		} else {
			fmt.Fprintf(b, " %s = time.Duration(dec.ReadInt())\n", goField)
		}
	case "UUID":
		if f.Type.Nullable {
			writeReadNullableConversion(b, "dec.ReadUUIDPtr()", "uuid.UUID(*_raw)", goField)
		} else {
			fmt.Fprintf(b, " %s = uuid.UUID(dec.ReadUUID())\n", goField)
		}
	case "Decimal":
		if f.Type.Nullable {
			fmt.Fprintf(b, "\n\t\t\tif raw := dec.ReadStringPtr(); raw != nil { value, err := decimal.NewFromString(*raw); if err != nil { dec.Fail(err) } else { %s = &value } } else { %s = nil }\n", goField, goField)
		} else {
			fmt.Fprintf(b, "\n\t\t\t{ raw := dec.ReadString(); if dec.Err() == nil { value, err := decimal.NewFromString(raw); if err != nil { dec.Fail(err) } else { %s = value } } }\n", goField)
		}
	case "Bytes", "JSON":
		if f.Type.Nullable {
			if f.Type.Name == "JSON" {
				writeReadNullableConversion(b, "dec.ReadJSONPtr()", "json.RawMessage(*_raw)", goField)
			} else {
				fmt.Fprintf(b, " %s = dec.ReadBytesValuePtr()\n", goField)
			}
		} else if f.Type.Name == "JSON" {
			fmt.Fprintf(b, " %s = dec.ReadJSON()\n", goField)
		} else {
			fmt.Fprintf(b, " %s = dec.ReadBytes()\n", goField)
		}
	}
}

// Converted nullable scalars must clear reused fields when the wire says null.
func writeReadNullableConversion(b *strings.Builder, read, convert, goField string) {
	fmt.Fprintf(b, "\n\t\t\tif _raw := %s; _raw != nil { _value := %s; %s = &_value } else { %s = nil }\n", read, convert, goField, goField)
}

// writeReadLuxoListValue writes the decode case for a scalar array field ([T]),
// the inverse of writeListScalarField. Types that aren't a direct Go slice
// Duration/UUID/Decimal/Enum decode into a temp slice and convert.
func writeReadLuxoListValue(b *strings.Builder, f *ast.FieldDecl, goField string, enums map[string]bool) {
	if f.Type.Nullable {
		fmt.Fprintf(b, "\n\t\t\tif !dec.ReadBool() { %s = nil; break }\n\t\t\t", goField)
	} else {
		b.WriteByte(' ')
	}
	writeReadScalarArrayValue(b, f.Type.Name, goField, enums[f.Type.Name])
	b.WriteByte('\n')
}

func writeReadScalarArrayValue(b *strings.Builder, elemType, goField string, isEnum bool) {
	if isEnum {
		fmt.Fprintf(b, "{ _a := dec.ReadStringArray(); %s = make([]%s, len(_a)); for _index, _item := range _a { _value := %s(_item); if !_value.IsValid() { dec.Fail(codec.ErrInvalidEnum); break }; %s[_index] = _value } }",
			goField, elemType, elemType, goField)
		return
	}
	switch elemType {
	case "Int", "Float", "String":
		fmt.Fprintf(b, "%s = dec.Read%sArray()", goField, elemType)
	case "Boolean":
		fmt.Fprintf(b, "%s = dec.ReadBoolArray()", goField)
	case "Bytes":
		fmt.Fprintf(b, "%s = dec.ReadBytesArray()", goField)
	case "JSON":
		fmt.Fprintf(b, "{ _n := dec.ReadArrayLength(); if dec.CheckElementBytes(_n, 1) { %s = make([]json.RawMessage, _n); for _index := range %s { if dec.Err() != nil { break }; %s[_index] = dec.ReadJSON() } } }", goField, goField, goField)
	case "DateTime":
		fmt.Fprintf(b, "%s = dec.ReadDateTimeArray()", goField)
	case "Duration":
		fmt.Fprintf(b, "{ _a := dec.ReadIntArray(); %s = make([]time.Duration, len(_a)); for _index, _item := range _a { %s[_index] = time.Duration(_item) } }", goField, goField)
	case "UUID":
		fmt.Fprintf(b, "{ _a := dec.ReadUUIDArray(); %s = make([]uuid.UUID, len(_a)); for _index, _item := range _a { %s[_index] = uuid.UUID(_item) } }", goField, goField)
	case "Decimal":
		fmt.Fprintf(b, "{ _a := dec.ReadStringArray(); %s = make([]decimal.Decimal, len(_a)); for _index, _item := range _a { value, err := decimal.NewFromString(_item); if err != nil { dec.Fail(err); break }; %s[_index] = value } }", goField, goField)
	}
}

func writeReadEnumCheck(b *strings.Builder, f *ast.FieldDecl, value string) {
	writeEnumReadCheck(b, value, f.Type.Nullable, "\t\t\t")
}

func writeEnumReadCheck(b *strings.Builder, value string, nullable bool, indent string) {
	guard := ""
	if nullable {
		guard = value + " != nil && "
	}
	fmt.Fprintf(b, "%sif %s!%s.IsValid() { dec.Fail(codec.ErrInvalidEnum) }\n", indent, guard, value)
}
