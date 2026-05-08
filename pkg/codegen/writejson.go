package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

// generateWriteJSONFile produces writejson.gen.go containing per-model
// WriteLuxo, ReadLuxo, and WriteColumnar methods for binary serialization.
// Uses ResponseBuf for zero-allocation direct append.
// Returns nil if there are no models.
func generateWriteJSONFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		models = append(models, file.Models...)
	}
	if len(models) == 0 {
		return nil
	}

	// Collect model names to skip extend stubs that are also full models
	modelNames := make(map[string]bool)
	for _, m := range models {
		modelNames[m.Name] = true
	}

	// Collect extend stubs (cross-module types, deduplicated)
	stubDone := make(map[string]bool)
	var stubs []*ast.ModelDecl
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if modelNames[ext.Name] || stubDone[ext.Name] {
				continue
			}
			stubDone[ext.Name] = true
			stubs = append(stubs, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
		}
	}

	var b strings.Builder
	writeHeader(&b, packageName, "writejson.gen.go")
	writeWriteJSONImports(&b, models, stubs)

	for _, m := range models {
		generateWriteLuxo(&b, m, enums)
		generateReadLuxo(&b, m, enums)
		generateWriteColumnar(&b, m, enums)
	}

	for _, s := range stubs {
		generateWriteLuxo(&b, s, enums)
		generateReadLuxo(&b, s, enums)
	}

	// type declarations — generate WriteLuxo for plain data types (AuthPayload, etc.)
	for _, file := range result.Files {
		for _, t := range file.Types {
			pseudo := &ast.ModelDecl{Name: t.Name, Fields: t.Fields}
			generateWriteLuxo(&b, pseudo, enums)
		}
	}

	return []byte(b.String())
}

// writeJSONImportNeeds tracks which imports writejson.gen.go requires.
type writeJSONImportNeeds struct {
	time    bool
	uuid    bool
	decimal bool
}

func scanWriteJSONImports(m *ast.ModelDecl, needs *writeJSONImportNeeds) {
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		switch f.Type.Name {
		case "DateTime", "Duration":
			needs.time = true
		case "UUID":
			needs.uuid = true
		case "Decimal":
			needs.decimal = true
		}
	}
}

func writeWriteJSONImports(b *strings.Builder, models []*ast.ModelDecl, stubs []*ast.ModelDecl) {
	var needs writeJSONImportNeeds
	for _, m := range models {
		scanWriteJSONImports(m, &needs)
	}
	for _, s := range stubs {
		scanWriteJSONImports(s, &needs)
	}

	b.WriteString("import (\n")
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	if needs.uuid {
		b.WriteString("\n\t\"github.com/google/uuid\"\n")
	}
	if needs.decimal {
		b.WriteString("\t\"github.com/shopspring/decimal\"\n")
	}
	b.WriteString(")\n\n")

	// Suppress unused import warnings
	var suppressions []string
	if needs.time {
		suppressions = append(suppressions, `var _ = time.RFC3339`)
	}
	if needs.uuid {
		suppressions = append(suppressions, `var _ uuid.UUID`)
	}
	if needs.decimal {
		suppressions = append(suppressions, `var _ decimal.Decimal`)
	}
	for _, s := range suppressions {
		fmt.Fprintf(b, "%s\n", s)
	}
	if len(suppressions) > 0 {
		b.WriteByte('\n')
	}
}

// generateWriteLuxo generates a WriteLuxo method for Luxo binary serialization.
// Field IDs come from luxo.lock via getModelFieldID().
// Writes all non-hidden, non-relation scalar fields.
func generateWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])

	fmt.Fprintf(b, "// WriteLuxo writes %s as Luxo binary directly to buf. Zero intermediate allocation.\n", name)
	fmt.Fprintf(b, "func (%s *%s) WriteLuxo(buf *api.ResponseBuf, mask []byte) {\n", recv, name)

	// Generate nil-mask fast path: write all fields without FieldMaskHas checks
	fmt.Fprintf(b, "\tif len(mask) == 0 {\n")
	generateWriteLuxoAllFields(b, m, recv, enums)
	fmt.Fprintf(b, "\t\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "\t\treturn\n")
	fmt.Fprintf(b, "\t}\n")

	// Slow path with mask checks
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}

		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		fmt.Fprintf(b, "\tif codec.FieldMaskHas(mask, %d) {\n", fieldID)

		// @visible: conditional field visibility based on identity
		hasVisible := writeVisibleDirective(b, f)

		goField := recv + "." + str.Capitalize(f.Name)
		baseType := f.Type.Name
		fid := fmt.Sprintf("%d", fieldID)

		// @transform: apply value transformation before writing
		goField = writeTransformDirective(b, f, goField)

		// @mask: apply masking before writing string fields
		goField = writeMaskDirective(b, f, goField, baseType)

		// All fields write directly to buf.B — zero intermediate buffer
		if enums[f.Type.Name] {
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendString(buf.B, string(*%s))\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendString(buf.B, string(%s))\n", goField)
			}
			if hasVisible {
				b.WriteString("\t }\n")
			}
			b.WriteString("\t}\n")
			continue
		}

		switch baseType {
		case "Int":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendSvarint(buf.B, *%s)\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendSvarint(buf.B, %s)\n", goField)
			}
		case "DateTime":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendSvarint(buf.B, %s.Unix())\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendSvarint(buf.B, %s.Unix())\n", goField)
			}
		case "Duration":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendSvarint(buf.B, int64(*%s))\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendSvarint(buf.B, int64(%s))\n", goField)
			}
		case "Float":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendFixed64(buf.B, *%s)\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendFixed64(buf.B, %s)\n", goField)
			}
		case "String":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendString(buf.B, *%s)\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendString(buf.B, %s)\n", goField)
			}
		case "Boolean":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil {\n", goField)
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendBool(buf.B, *%s)\n", goField)
				fmt.Fprintf(b, "\t\t} else {\n")
				fmt.Fprintf(b, "\t\t\tbuf.B = codec.AppendNull(buf.B)\n")
				fmt.Fprintf(b, "\t\t}\n")
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendBool(buf.B, %s)\n", goField)
			}
		default:
			fmt.Fprintf(b, "\t\t// TODO: binary encoding for type %s (field %s)\n", baseType, f.Name)
		}
		if hasVisible {
			b.WriteString("\t }\n")
		}
		b.WriteString("\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "}\n\n")
}

// generateWriteLuxoAllFields generates the nil-mask fast path — all fields, no checks.
func generateWriteLuxoAllFields(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool) {
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}
		fieldID := getModelFieldID(m.Name, f.Name)
		if fieldID == 0 {
			continue
		}
		goField := recv + "." + str.Capitalize(f.Name)
		fid := fmt.Sprintf("%d", fieldID)

		if enums[f.Type.Name] {
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendString(buf.B, string(*%s)) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendString(buf.B, string(%s))\n", fid, goField)
			}
			continue
		}

		switch f.Type.Name {
		case "Int":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendSvarint(buf.B, *%s) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendSvarint(buf.B, %s)\n", fid, goField)
			}
		case "DateTime":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendSvarint(buf.B, %s.Unix()) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendSvarint(buf.B, %s.Unix())\n", fid, goField)
			}
		case "Duration":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendSvarint(buf.B, int64(*%s)) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendSvarint(buf.B, int64(%s))\n", fid, goField)
			}
		case "Float":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendFixed64(buf.B, *%s) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendFixed64(buf.B, %s)\n", fid, goField)
			}
		case "String":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendString(buf.B, *%s) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendString(buf.B, %s)\n", fid, goField)
			}
		case "Boolean":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t\tif %s != nil { buf.B = codec.AppendPresent(buf.B); buf.B = codec.AppendBool(buf.B, *%s) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendBool(buf.B, %s)\n", fid, goField)
			}
		}
	}
}

// generateReadLuxo generates a ReadLuxo method that decodes a model from Luxo binary.
// This is the inverse of WriteLuxo — used by remote DataLoaders to decode RPC responses.
func generateReadLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])

	fmt.Fprintf(b, "// ReadLuxo decodes %s from Luxo binary format.\n", name)
	fmt.Fprintf(b, "func (%s *%s) ReadLuxo(dec *codec.Decoder) {\n", recv, name)
	fmt.Fprintf(b, "\tfor dec.NextField() {\n")
	fmt.Fprintf(b, "\t\tswitch dec.FieldID() {\n")

	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}

		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}

		goField := recv + "." + str.Capitalize(f.Name)

		if enums[f.Type.Name] {
			if f.Type.Nullable {
				typeName := f.Type.Name
				fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringPtr(); v != nil { tmp := %s(*v); %s = &tmp }\n", fieldID, typeName, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = %s(dec.ReadString())\n", fieldID, goField, f.Type.Name)
			}
			continue
		}

		switch f.Type.Name {
		case "Int":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadIntPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadInt()\n", fieldID, goField)
			}
		case "Float":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadFloatPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadFloat()\n", fieldID, goField)
			}
		case "String":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadStringPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadString()\n", fieldID, goField)
			}
		case "Boolean":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBoolPtr()\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBool()\n", fieldID, goField)
			}
		case "DateTime":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadIntPtr(); v != nil { t := time.Unix(*v, 0); %s = &t }\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = time.Unix(dec.ReadInt(), 0)\n", fieldID, goField)
			}
		case "Duration":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadIntPtr(); v != nil { d := time.Duration(*v); %s = &d }\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = time.Duration(dec.ReadInt())\n", fieldID, goField)
			}
		case "UUID":
			if f.Type.Nullable {
				fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringPtr(); v != nil { u := uuid.MustParse(*v); %s = &u }\n", fieldID, goField)
			} else {
				fmt.Fprintf(b, "\t\tcase %d: %s = uuid.MustParse(dec.ReadString())\n", fieldID, goField)
			}
		case "Bytes":
			fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBytes()\n", fieldID, goField)
		}
	}

	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t}\n")   // end for
	fmt.Fprintf(b, "}\n\n")
}

// generateWriteColumnar generates a WriteColumnar function for list encoding.
// Writes all items in columnar format: [count][col1: fieldID + all values][col2: ...]...[0x00]
// 2.75x faster, 19% smaller than row-by-row WriteLuxo for lists.
func generateWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name

	// Collect encodable fields
	type fieldMeta struct {
		name     string
		goName   string
		fieldID  int
		typeName string
		nullable bool
		isEnum   bool
	}
	var fields []fieldMeta
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}
		fid := getModelFieldID(name, f.Name)
		if fid == 0 {
			continue
		}
		fields = append(fields, fieldMeta{
			name:     f.Name,
			goName:   str.Capitalize(f.Name),
			fieldID:  fid,
			typeName: f.Type.Name,
			nullable: f.Type.Nullable,
			isEnum:   enums[f.Type.Name],
		})
	}

	if len(fields) == 0 {
		return
	}

	fmt.Fprintf(b, "// WriteColumnar%s writes a list of %s in columnar format.\n", name, name)
	fmt.Fprintf(b, "// Column-by-column encoding: fieldID once per column, values packed.\n")
	fmt.Fprintf(b, "func WriteColumnar%s(buf *api.ResponseBuf, items []*%s, mask []byte) {\n", name, name)
	fmt.Fprintf(b, "\tw := &codec.ColumnarWriter{}\n")
	fmt.Fprintf(b, "\tw.SetCount(len(items))\n")

	for _, f := range fields {
		fmt.Fprintf(b, "\tif len(mask) == 0 || codec.FieldMaskHas(mask, %d) {\n", f.fieldID)
		if f.nullable || f.isEnum {
			writeColumnarNullableField(b, f.goName, f.fieldID, f.typeName, f.isEnum, f.nullable)
		} else {
			writeColumnarField(b, f.goName, f.fieldID, f.typeName)
		}
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, w.Bytes()...)\n")
	fmt.Fprintf(b, "}\n\n")
}

func writeColumnarField(b *strings.Builder, goName string, fieldID int, typeName string) {
	switch typeName {
	case "Int":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnInt(%d, vals)\n\t}\n", fieldID)
	case "Float":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]float64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnFloat(%d, vals)\n\t}\n", fieldID)
	case "String":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnString(%d, vals)\n\t}\n", fieldID)
	case "Boolean":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]bool, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnBool(%d, vals)\n\t}\n", fieldID)
	case "DateTime":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s.Unix() }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnInt(%d, vals)\n\t}\n", fieldID)
	case "Duration":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = int64(item.%s) }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnInt(%d, vals)\n\t}\n", fieldID)
	}
}

func writeColumnarNullableField(b *strings.Builder, goName string, fieldID int, typeName string, isEnum, nullable bool) {
	if isEnum {
		if nullable {
			fmt.Fprintf(b, "\t{\n\t\tvals := make([]*string, len(items))\n")
			fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
			fmt.Fprintf(b, "\t\t\tif item.%s != nil { s := string(*item.%s); vals[i] = &s }\n", goName, goName)
			fmt.Fprintf(b, "\t\t}\n")
			fmt.Fprintf(b, "\t\tw.WriteColumnStringPtr(%d, vals)\n\t}\n", fieldID)
		} else {
			fmt.Fprintf(b, "\t{\n\t\tvals := make([]string, len(items))\n")
			fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = string(item.%s) }\n", goName)
			fmt.Fprintf(b, "\t\tw.WriteColumnString(%d, vals)\n\t}\n", fieldID)
		}
		return
	}
	// Nullable scalar
	switch typeName {
	case "Int":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnIntPtr(%d, vals)\n\t}\n", fieldID)
	case "Float":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*float64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnFloatPtr(%d, vals)\n\t}\n", fieldID)
	case "String":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnStringPtr(%d, vals)\n\t}\n", fieldID)
	case "Boolean":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*bool, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnBoolPtr(%d, vals)\n\t}\n", fieldID)
	}
}

// writeMaskDirective generates @mask logic for a field. Returns the (possibly modified) goField name.
func writeMaskDirective(b *strings.Builder, f *ast.FieldDecl, goField, baseType string) string {
	maskDir := findDirective(f.Directives, "mask")
	if maskDir == nil || baseType != "String" || f.Type.Nullable {
		return goField
	}
	maskedVar := f.Name + "Masked"
	if hasDirective(f.Directives, "email") {
		fmt.Fprintf(b, "\t\t%s := str.MaskEmail(%s)\n", maskedVar, goField)
	} else if len(maskDir.Args) >= 2 {
		prefixLit, _ := maskDir.Args[0].Value.(*ast.Literal)
		suffixLit, _ := maskDir.Args[1].Value.(*ast.Literal)
		if prefixLit != nil && suffixLit != nil {
			fmt.Fprintf(b, "\t\t%s := str.Mask(%s, %s, %s)\n", maskedVar, goField, prefixLit.Value, suffixLit.Value)
		} else {
			return goField
		}
	} else {
		fmt.Fprintf(b, "\t\t%s := str.Mask(%s, 3, 4)\n", maskedVar, goField)
	}
	return maskedVar
}

// writeVisibleDirective generates @visible condition check.
// @visible { my.role == "admin" } → skip field if identity doesn't match.
func writeVisibleDirective(b *strings.Builder, f *ast.FieldDecl) bool {
	visibleDir := findDirective(f.Directives, "visible")
	if visibleDir == nil || visibleDir.Body == nil || len(visibleDir.Body.Stmts) == 0 {
		return false
	}
	es, ok := visibleDir.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		return false
	}
	cond := compileVisibleExpr(es.Expr)
	if cond == "" {
		return false
	}
	fmt.Fprintf(b, "\t if %s {\n", cond)
	return true
}

// compileVisibleExpr compiles a @visible body expression to Go.
// Supports: my.role == "admin", my.id == someField
func compileVisibleExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		left := compileVisibleExpr(e.Left)
		right := compileVisibleExpr(e.Right)
		if left == "" || right == "" {
			return ""
		}
		left = inferMyFieldType(left, e.Left, e.Right, "buf.Identity")
		right = inferMyFieldType(right, e.Right, e.Left, "buf.Identity")
		return fmt.Sprintf("%s %s %s", left, e.Op, right)
	case *ast.MemberExpr:
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "my" {
			return compileMyField(e.Field, "buf.Identity")
		}
	case *ast.Literal:
		if e.Kind == token.String {
			return fmt.Sprintf("%q", e.Value)
		}
		return e.Value
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// writeTransformDirective generates @transform { body } value transformation.
func writeTransformDirective(b *strings.Builder, f *ast.FieldDecl, goField string) string {
	transformDir := findDirective(f.Directives, "transform")
	if transformDir == nil || transformDir.Body == nil || len(transformDir.Body.Stmts) == 0 {
		return goField
	}
	es, ok := transformDir.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		return goField
	}
	code := compileFieldExpr(es.Expr, goField)
	if code == "" {
		return goField
	}
	transformedVar := f.Name + "Transformed"
	fmt.Fprintf(b, "\t\t%s := %s\n", transformedVar, code)
	return transformedVar
}

// Removed: inferVisibleIdentityType — now uses shared inferMyFieldType from identity.go
