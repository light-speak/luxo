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
	// Check if any types exist (non-DB declarations also need WriteLuxo)
	hasTypes := false
	for _, file := range result.Files {
		if len(file.Types) > 0 {
			hasTypes = true
			break
		}
	}
	if len(models) == 0 && !hasTypes {
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
	importDecls := append(append([]*ast.ModelDecl{}, models...), stubs...)
	for _, file := range result.Files {
		for _, t := range file.Types {
			importDecls = append(importDecls, &ast.ModelDecl{Name: t.Name, Fields: t.Fields})
		}
	}
	writeWriteJSONImports(&b, importDecls)

	for _, m := range models {
		generateWriteLuxo(&b, m, enums)
		generateReadLuxo(&b, m, enums)
		generateWriteColumnar(&b, m, enums)
	}

	for _, s := range stubs {
		generateWriteLuxo(&b, s, enums)
		generateReadLuxo(&b, s, enums)
	}

	// type declarations — generate WriteLuxo with VALUE receiver (can call on literal)
	// plus WriteColumnar so native list responses follow the columnar protocol
	for _, file := range result.Files {
		for _, t := range file.Types {
			pseudo := &ast.ModelDecl{Name: t.Name, Fields: t.Fields}
			generateTypeWriteLuxo(&b, pseudo, enums)
			generateTypeWriteColumnar(&b, pseudo, enums)
		}
	}

	return []byte(b.String())
}

// writeJSONImportNeeds tracks which imports writejson.gen.go requires.
type writeJSONImportNeeds struct {
	time    bool
	uuid    bool
	decimal bool
	strings bool
	str     bool
}

func scanWriteJSONImports(m *ast.ModelDecl, needs *writeJSONImportNeeds) {
	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if hasDirective(f.Directives, "mask") {
			needs.str = true
		}
		if expression := compileTransformDirectiveExpr(f, "value"); expression != "value" {
			needs.strings = needs.strings || strings.Contains(expression, "strings.")
			needs.str = needs.str || strings.Contains(expression, "str.")
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

func writeWriteJSONImports(b *strings.Builder, declarations []*ast.ModelDecl) {
	var needs writeJSONImportNeeds
	for _, m := range declarations {
		scanWriteJSONImports(m, &needs)
	}

	b.WriteString("import (\n")
	if needs.strings {
		b.WriteString("\t\"strings\"\n")
	}
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	if needs.str {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/str\"\n")
	}
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

// isArenaField returns true if the field's value is stored as a Go string and
// benefits from arena allocation during decoding. Includes String, String?, Enum, Enum?.
// Excludes UUID/Decimal (parsed to non-string types after decoding).
// Excludes fields with @transform/@mask/@visible — these directives can change the
// written string length at runtime, causing a mismatch between the pre-calculated
// totalStringLen and the actual bytes on wire.
func isArenaField(f *ast.FieldDecl, enums map[string]bool) bool {
	if f.Type == nil || f.Computed != nil {
		return false
	}
	if f.Type.IsList {
		return false
	}
	// Directives that modify the wire value or conditionally skip the field
	if hasDirective(f.Directives, "transform") || hasDirective(f.Directives, "mask") || hasDirective(f.Directives, "visible") {
		return false
	}
	if f.Type.Name == "String" {
		return true
	}
	if enums[f.Type.Name] {
		return true
	}
	return false
}

// writeArenaLenCalc generates code to calculate totalStringLen for arena allocation.
// Emits: var _arenaLen int; _arenaLen += len(...); ...
// Returns true if any arena fields exist (totalStringLen prefix should be written).
func writeArenaLenCalc(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool, masked bool, indent string) bool {
	var arenaFields []struct {
		goField  string
		nullable bool
		isEnum   bool
		fieldID  int
	}
	for _, f := range m.Fields {
		if !isArenaField(f, enums) {
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
		arenaFields = append(arenaFields, struct {
			goField  string
			nullable bool
			isEnum   bool
			fieldID  int
		}{
			goField:  recv + "." + str.Capitalize(f.Name),
			nullable: f.Type.Nullable,
			isEnum:   enums[f.Type.Name],
			fieldID:  fieldID,
		})
	}

	if len(arenaFields) == 0 {
		fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, 0)\n", indent)
		return false
	}

	fmt.Fprintf(b, "%svar _arenaLen int\n", indent)
	for _, af := range arenaFields {
		lenExpr := fmt.Sprintf("len(%s)", af.goField)
		if af.isEnum {
			lenExpr = fmt.Sprintf("len(string(%s))", af.goField)
		}
		ptrLenExpr := fmt.Sprintf("len(*%s)", af.goField)
		if af.isEnum {
			ptrLenExpr = fmt.Sprintf("len(string(*%s))", af.goField)
		}

		if masked {
			if af.nullable {
				fmt.Fprintf(b, "%sif codec.FieldMaskHas(mask, %d) && %s != nil { _arenaLen += %s }\n",
					indent, af.fieldID, af.goField, ptrLenExpr)
			} else {
				fmt.Fprintf(b, "%sif codec.FieldMaskHas(mask, %d) { _arenaLen += %s }\n",
					indent, af.fieldID, lenExpr)
			}
		} else {
			if af.nullable {
				fmt.Fprintf(b, "%sif %s != nil { _arenaLen += %s }\n", indent, af.goField, ptrLenExpr)
			} else {
				fmt.Fprintf(b, "%s_arenaLen += %s\n", indent, lenExpr)
			}
		}
	}
	fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, uint64(_arenaLen))\n", indent)
	return true
}

// generateWriteLuxo generates a WriteLuxo method for Luxo binary serialization.
// Field IDs come from luxo.lock via getModelFieldID().
// Writes all non-hidden, non-relation scalar fields.
// Prefixes field data with totalStringLen varint for arena allocation on decode.
func generateWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])

	fmt.Fprintf(b, "// WriteLuxo writes %s as Luxo binary directly to buf. Zero intermediate allocation.\n", name)
	fmt.Fprintf(b, "func (%s *%s) WriteLuxo(buf *api.ResponseBuf, mask []byte) {\n", recv, name)

	// Output directives must also run for SELECT *, so those models use the
	// directive-aware path even when the field mask is empty.
	if !hasOutputDirectives(m) {
		fmt.Fprintf(b, "\tif len(mask) == 0 {\n")
		writeArenaLenCalc(b, m, recv, enums, false, "\t\t")
		generateWriteLuxoAllFields(b, m, recv, enums)
		fmt.Fprintf(b, "\t\tbuf.B = append(buf.B, 0x00)\n")
		fmt.Fprintf(b, "\t\treturn\n")
		fmt.Fprintf(b, "\t}\n")
	}

	// Slow path: arena len with mask checks, then field encoding
	writeArenaLenCalc(b, m, recv, enums, true, "\t")

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
		writeModelFieldEncoding(b, f, fid, goField, enums, "\t\t")
		if hasVisible {
			b.WriteString("\t }\n")
		}
		b.WriteString("\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "}\n\n")
}

func hasOutputDirectives(model *ast.ModelDecl) bool {
	for _, field := range model.Fields {
		if hasDirective(field.Directives, "mask") || hasDirective(field.Directives, "transform") || hasDirective(field.Directives, "visible") {
			return true
		}
	}
	return false
}

// writeModelFieldEncoding writes the binary encoding for a single model field.
// Handles enums, all scalar types (Int/Float/String/Boolean/DateTime/Duration/UUID/Decimal/Bytes/JSON),
// both nullable and non-nullable variants.
func writeModelFieldEncoding(b *strings.Builder, f *ast.FieldDecl, fid, goField string, enums map[string]bool, indent string) {
	// Scalar array field ([String], [Int], [UUID], ...). Relation lists are
	// excluded by the caller via isRelationField.
	if f.Type.IsList {
		writeListScalarField(b, f, fid, goField, enums, indent)
		return
	}
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sif %s != nil {\n", indent, goField)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendPresent(buf.B)\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendString(buf.B, string(*%s))\n", indent, goField)
			fmt.Fprintf(b, "%s} else {\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendNull(buf.B)\n", indent)
			fmt.Fprintf(b, "%s}\n", indent)
		} else {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sbuf.B = codec.AppendString(buf.B, string(%s))\n", indent, goField)
		}
		return
	}
	writeScalarEncoding(b, f.Type.Name, f.Type.Nullable, fid, goField, indent)
}

// writeScalarEncoding writes binary encoding for a scalar type, handling nullable.
func writeScalarEncoding(b *strings.Builder, typeName string, nullable bool, fid, goField, indent string) {
	type encSpec struct {
		appendFn string // e.g. "AppendSvarint", "AppendFixed64"
		valExpr  string // e.g. "%s", "%s.Unix()", "int64(%s)"
		ptrExpr  string // e.g. "*%s", "%s.Unix()", "int64(*%s)"
	}
	specs := map[string]encSpec{
		"Int":      {"AppendSvarint", "%s", "*%s"},
		"DateTime": {"AppendSvarint", "%s.Unix()", "%s.Unix()"},
		"Duration": {"AppendSvarint", "int64(%s)", "int64(*%s)"},
		"Float":    {"AppendFixed64", "%s", "*%s"},
		"String":   {"AppendString", "%s", "*%s"},
		"Boolean":  {"AppendBool", "%s", "*%s"},
		"UUID":     {"AppendUUID", "[16]byte(%s)", "[16]byte(*%s)"},
		"Decimal":  {"AppendString", "%s.String()", "%s.String()"},
	}

	if spec, ok := specs[typeName]; ok {
		if nullable {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sif %s != nil {\n", indent, goField)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendPresent(buf.B)\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.%s(buf.B, %s)\n", indent, spec.appendFn, fmt.Sprintf(spec.ptrExpr, goField))
			fmt.Fprintf(b, "%s} else {\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendNull(buf.B)\n", indent)
			fmt.Fprintf(b, "%s}\n", indent)
		} else {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sbuf.B = codec.%s(buf.B, %s)\n", indent, spec.appendFn, fmt.Sprintf(spec.valExpr, goField))
		}
		return
	}
	// Bytes/JSON: length-prefixed binary
	if typeName == "Bytes" || typeName == "JSON" {
		if nullable {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sif %s != nil {\n", indent, goField)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendPresent(buf.B)\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendBytes(buf.B, %s)\n", indent, goField)
			fmt.Fprintf(b, "%s} else {\n", indent)
			fmt.Fprintf(b, "%s\tbuf.B = codec.AppendNull(buf.B)\n", indent)
			fmt.Fprintf(b, "%s}\n", indent)
		} else {
			fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
			fmt.Fprintf(b, "%sbuf.B = codec.AppendBytes(buf.B, %s)\n", indent, goField)
		}
	}
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

		writeModelFieldEncoding(b, f, fid, goField, enums, "\t\t")
	}
}

// generateReadLuxo generates a ReadLuxo method that decodes a model from Luxo binary.
// This is the inverse of WriteLuxo — used by remote DataLoaders to decode RPC responses.
// generateTypeWriteLuxo generates WriteLuxo with value receiver for type declarations.
// Value receiver allows calling on literals: AuthPayload{...}.WriteLuxo(...)
// Type fields write ALL fields unconditionally — including nested model references.
// Unlike model WriteLuxo, type fields don't skip "relations" since types have no DB semantics.
func generateTypeWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])
	fmt.Fprintf(b, "// WriteLuxo writes %s as Luxo binary directly to buf.\n", name)
	fmt.Fprintf(b, "func (%s %s) WriteLuxo(buf *api.ResponseBuf, mask []byte) {\n", recv, name)

	// Arena header for type declarations (always write all fields, no mask)
	writeArenaLenCalc(b, m, recv, enums, false, "\t")

	for _, f := range m.Fields {
		if f.Type == nil || hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}
		goField := recv + "." + str.Capitalize(f.Name)
		fid := fmt.Sprintf("%d", fieldID)
		indent := "\t"
		visibleExpr := compileVisibleDirectiveExpr(f)
		if visibleExpr != "" {
			fmt.Fprintf(b, "\tif %s {\n", visibleExpr)
			indent = "\t\t"
		}
		goField = writeTransformDirectiveAt(b, f, goField, indent)
		goField = writeMaskDirectiveAt(b, f, goField, f.Type.Name, indent)

		if isRelationField(f, enums) {
			// Nested model/type reference — write inline with WriteLuxo
			if f.Type.IsList {
				fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
				fmt.Fprintf(b, "%sbuf.B = codec.AppendSvarint(buf.B, int64(len(%s)))\n", indent, goField)
				fmt.Fprintf(b, "%sfor i := range %s { %s[i].WriteLuxo(buf, nil) }\n", indent, goField, goField)
			} else if f.Type.Nullable {
				fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
				fmt.Fprintf(b, "%sif %s != nil { buf.B = codec.AppendPresent(buf.B); %s.WriteLuxo(buf, nil) } else { buf.B = codec.AppendNull(buf.B) }\n", indent, goField, goField)
			} else {
				fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
				fmt.Fprintf(b, "%s%s.WriteLuxo(buf, nil)\n", indent, goField)
			}
		} else {
			writeModelFieldEncoding(b, f, fid, goField, enums, indent)
		}
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t}\n")
		}
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeListScalarField writes a list of scalar values (count + items) for
// WriteLuxo, per the protocol array encoding: [varint count][items...].
// Shared by model and type declarations; indent positions the emitted code.
func writeListScalarField(b *strings.Builder, f *ast.FieldDecl, fid, goField string, enums map[string]bool, indent string) {
	fmt.Fprintf(b, "%sbuf.B = codec.AppendVarint(buf.B, %s)\n", indent, fid)
	fmt.Fprintf(b, "%sbuf.B = codec.AppendArrayHeader(buf.B, len(%s))\n", indent, goField)

	elemType := f.Type.Name
	if enums[elemType] {
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendString(buf.B, string(v)) }\n", indent, goField)
		return
	}
	switch elemType {
	case "Int":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, v) }\n", indent, goField)
	case "Float":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendFixed64(buf.B, v) }\n", indent, goField)
	case "String":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendString(buf.B, v) }\n", indent, goField)
	case "DateTime":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, v.Unix()) }\n", indent, goField)
	case "UUID":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendUUID(buf.B, [16]byte(v)) }\n", indent, goField)
	case "Decimal":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendString(buf.B, v.String()) }\n", indent, goField)
	case "Bytes", "JSON":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendBytes(buf.B, v) }\n", indent, goField)
	case "Boolean":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendBool(buf.B, v) }\n", indent, goField)
	case "Duration":
		fmt.Fprintf(b, "%sfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, int64(v)) }\n", indent, goField)
	default:
		// Unknown list element — write as nested model
		fmt.Fprintf(b, "%sfor i := range %s { %s[i].WriteLuxo(buf, nil) }\n", indent, goField, goField)
	}
}

// hasArenaFields returns true if the model has any arena-eligible fields.
func hasArenaFields(m *ast.ModelDecl, enums map[string]bool) bool {
	for _, f := range m.Fields {
		if isArenaField(f, enums) {
			if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
				continue
			}
			if isRelationField(f, enums) {
				continue
			}
			if getModelFieldID(m.Name, f.Name) == 0 {
				continue
			}
			return true
		}
	}
	return false
}

func generateReadLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])
	useArena := hasArenaFields(m, enums)

	fmt.Fprintf(b, "// ReadLuxo decodes %s from Luxo binary format.\n", name)
	fmt.Fprintf(b, "func (%s *%s) ReadLuxo(dec *codec.Decoder) {\n", recv, name)

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

		if useArena && isArenaField(f, enums) {
			writeReadLuxoFieldArena(b, f, fieldID, goField, enums)
		} else {
			writeReadLuxoField(b, f, fieldID, goField, enums)
		}
	}

	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t}\n")   // end for
	fmt.Fprintf(b, "}\n\n")
}

// writeReadLuxoFieldArena writes a single field's decode case using arena allocation.
// Only called for String and Enum fields (isArenaField == true).
func writeReadLuxoFieldArena(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool) {
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringArenaPtr(_arena, &_arenaOff); v != nil { tmp := %s(*v); %s = &tmp }\n", fieldID, f.Type.Name, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = %s(dec.ReadStringArena(_arena, &_arenaOff))\n", fieldID, goField, f.Type.Name)
		}
		return
	}
	// String / String?
	if f.Type.Nullable {
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadStringArenaPtr(_arena, &_arenaOff)\n", fieldID, goField)
	} else {
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadStringArena(_arena, &_arenaOff)\n", fieldID, goField)
	}
}

// writeReadLuxoField writes a single field's decode case for ReadLuxo.
func writeReadLuxoField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool) {
	if f.Type.IsList {
		writeReadLuxoListField(b, f, fieldID, goField, enums)
		return
	}
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringPtr(); v != nil { tmp := %s(*v); %s = &tmp }\n", fieldID, f.Type.Name, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = %s(dec.ReadString())\n", fieldID, goField, f.Type.Name)
		}
		return
	}
	type decSpec struct {
		read    string // non-nullable read expression
		readPtr string // nullable: read expression returning pointer
		convert string // optional conversion wrapper for non-nullable
		ptrWrap string // nullable: conversion wrapper "if v := readPtr; v != nil { varName := convert(*v); goField = &varName }"
	}
	simple := map[string]decSpec{
		"Int":     {read: "dec.ReadInt()", readPtr: "dec.ReadIntPtr()"},
		"Float":   {read: "dec.ReadFloat()", readPtr: "dec.ReadFloatPtr()"},
		"String":  {read: "dec.ReadString()", readPtr: "dec.ReadStringPtr()"},
		"Boolean": {read: "dec.ReadBool()", readPtr: "dec.ReadBoolPtr()"},
	}
	if spec, ok := simple[f.Type.Name]; ok {
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\tcase %d: %s = %s\n", fieldID, goField, spec.readPtr)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = %s\n", fieldID, goField, spec.read)
		}
		return
	}
	writeReadLuxoTypedField(b, f, fieldID, goField)
}

// writeReadLuxoTypedField decodes scalar types that need a conversion wrapper
// (DateTime/Duration/UUID/Decimal/Bytes/JSON) for ReadLuxo.
func writeReadLuxoTypedField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string) {
	switch f.Type.Name {
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
			fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadUUIDPtr(); v != nil { u := uuid.UUID(*v); %s = &u }\n", fieldID, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = uuid.UUID(dec.ReadUUID())\n", fieldID, goField)
		}
	case "Decimal":
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringPtr(); v != nil { d := decimal.RequireFromString(*v); %s = &d }\n", fieldID, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = decimal.RequireFromString(dec.ReadString())\n", fieldID, goField)
		}
	case "Bytes", "JSON":
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBytesPtr()\n", fieldID, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBytes()\n", fieldID, goField)
		}
	}
}

// writeReadLuxoListField writes the decode case for a scalar array field ([T]),
// the inverse of writeListScalarField. Types that aren't a direct Go slice
// (DateTime/Duration/UUID/Decimal/Enum) decode into a temp slice and convert.
func writeReadLuxoListField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool) {
	elemType := f.Type.Name
	if enums[elemType] {
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\t{ _a := dec.ReadStringArray(); %s = make([]%s, len(_a)); for i, v := range _a { %s[i] = %s(v) } }\n",
			fieldID, goField, elemType, goField, elemType)
		return
	}
	switch elemType {
	case "Int":
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadIntArray()\n", fieldID, goField)
	case "Float":
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadFloatArray()\n", fieldID, goField)
	case "String":
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadStringArray()\n", fieldID, goField)
	case "Boolean":
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBoolArray()\n", fieldID, goField)
	case "Bytes", "JSON":
		fmt.Fprintf(b, "\t\tcase %d: %s = dec.ReadBytesArray()\n", fieldID, goField)
	case "DateTime":
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\t{ _a := dec.ReadIntArray(); %s = make([]time.Time, len(_a)); for i, v := range _a { %s[i] = time.Unix(v, 0) } }\n", fieldID, goField, goField)
	case "Duration":
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\t{ _a := dec.ReadIntArray(); %s = make([]time.Duration, len(_a)); for i, v := range _a { %s[i] = time.Duration(v) } }\n", fieldID, goField, goField)
	case "UUID":
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\t{ _a := dec.ReadUUIDArray(); %s = make([]uuid.UUID, len(_a)); for i, v := range _a { %s[i] = uuid.UUID(v) } }\n", fieldID, goField, goField)
	case "Decimal":
		fmt.Fprintf(b, "\t\tcase %d:\n\t\t\t{ _a := dec.ReadStringArray(); %s = make([]decimal.Decimal, len(_a)); for i, v := range _a { %s[i] = decimal.RequireFromString(v) } }\n", fieldID, goField, goField)
	}
}

// generateWriteColumnar generates a WriteColumnar function for list encoding.
// Writes all items in columnar format: [count][col1: fieldID + all values][col2: ...]...[0x00]
// 2.75x faster, 19% smaller than row-by-row WriteLuxo for lists.
func generateWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name

	// Collect encodable fields
	type fieldMeta struct {
		decl     *ast.FieldDecl
		name     string
		goName   string
		fieldID  int
		typeName string
		nullable bool
		isEnum   bool
		isList   bool
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
			decl:     f,
			name:     f.Name,
			goName:   str.Capitalize(f.Name),
			fieldID:  fid,
			typeName: f.Type.Name,
			nullable: f.Type.Nullable,
			isEnum:   enums[f.Type.Name],
			isList:   f.Type.IsList,
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
		visibleExpr := compileVisibleDirectiveExpr(f.decl)
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t\tif %s {\n", visibleExpr)
		}
		sourceExpr := "item." + f.goName
		if f.nullable && !f.isList {
			valueExpr := compileTransformDirectiveExpr(f.decl, "*"+sourceExpr)
			valueExpr = compileMaskDirectiveExpr(f.decl, valueExpr, f.typeName)
			writeColumnarNullableValueField(b, sourceExpr, valueExpr, f.fieldID, f.typeName, f.isEnum)
		} else {
			valueExpr := compileTransformDirectiveExpr(f.decl, sourceExpr)
			valueExpr = compileMaskDirectiveExpr(f.decl, valueExpr, f.typeName)
			if f.isList {
				writeColumnarArrayField(b, valueExpr, f.fieldID, f.typeName, f.isEnum)
			} else {
				writeColumnarValueField(b, valueExpr, f.fieldID, f.typeName, f.isEnum)
			}
		}
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t\t}\n")
		}
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, w.Bytes()...)\n")
	fmt.Fprintf(b, "}\n\n")
}

func writeColumnarMaskedStringField(b *strings.Builder, field *ast.FieldDecl, goName string, fieldID int) {
	expression := compileMaskDirectiveExpr(field, "item."+goName, "String")
	writeColumnarValueField(b, expression, fieldID, "String", false)
}

// generateTypeWriteColumnar generates WriteColumnar for a type declaration.
// Lists are always columnar on the wire (protocol.md), so native APIs that
// return [Type] need this writer. Unlike model columnar (which skips DB
// relations — federation resolves those), type declarations own their nested
// values, so nested type/model references become blob columns:
//   - single nested value → cell holds its row-wise WriteLuxo bytes
//   - nested list → cell holds the columnar encoding of that record's items
//
// Value-slice signature — native resolvers return []Type, not []*Type.
func generateTypeWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name

	hasFields := false
	for _, f := range m.Fields {
		if f.Type != nil && !hasDirective(f.Directives, "hidden") && !hasDirective(f.Directives, "internal") && getModelFieldID(name, f.Name) != 0 {
			hasFields = true
			break
		}
	}
	if !hasFields {
		return
	}

	fmt.Fprintf(b, "// WriteColumnar%s writes a list of %s in columnar format.\n", name, name)
	fmt.Fprintf(b, "// Nested type/model fields are encoded as blob columns.\n")
	fmt.Fprintf(b, "func WriteColumnar%s(buf *api.ResponseBuf, items []%s, mask []byte) {\n", name, name)
	fmt.Fprintf(b, "\tw := &codec.ColumnarWriter{}\n")
	fmt.Fprintf(b, "\tw.SetCount(len(items))\n")

	for _, f := range m.Fields {
		if f.Type == nil || hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		fid := getModelFieldID(name, f.Name)
		if fid == 0 {
			continue
		}
		goName := str.Capitalize(f.Name)
		fmt.Fprintf(b, "\tif len(mask) == 0 || codec.FieldMaskHas(mask, %d) {\n", fid)
		visibleExpr := compileVisibleDirectiveExpr(f)
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t\tif %s {\n", visibleExpr)
		}
		switch {
		case isRelationField(f, enums):
			writeColumnarNestedBlobField(b, goName, fid, f.Type)
		case f.Type.IsList:
			valueExpr := compileTransformDirectiveExpr(f, "item."+goName)
			writeColumnarArrayField(b, valueExpr, fid, f.Type.Name, enums[f.Type.Name])
		case f.Type.Nullable && !f.Type.IsList:
			sourceExpr := "item." + goName
			valueExpr := compileTransformDirectiveExpr(f, "*"+sourceExpr)
			valueExpr = compileMaskDirectiveExpr(f, valueExpr, f.Type.Name)
			writeColumnarNullableValueField(b, sourceExpr, valueExpr, fid, f.Type.Name, enums[f.Type.Name])
		default:
			valueExpr := compileTransformDirectiveExpr(f, "item."+goName)
			valueExpr = compileMaskDirectiveExpr(f, valueExpr, f.Type.Name)
			writeColumnarValueField(b, valueExpr, fid, f.Type.Name, enums[f.Type.Name])
		}
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t\t}\n")
		}
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, w.Bytes()...)\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeColumnarNestedBlobField encodes a nested type/model field as a blob column.
func writeColumnarNestedBlobField(b *strings.Builder, goName string, fieldID int, t *ast.TypeRef) {
	fmt.Fprintf(b, "\t{\n\t\tvals := make([][]byte, len(items))\n")
	switch {
	case t.IsList:
		// Cell = columnar encoding of this record's nested items
		fmt.Fprintf(b, "\t\tfor i := range items {\n")
		fmt.Fprintf(b, "\t\t\tvar nb api.ResponseBuf\n")
		fmt.Fprintf(b, "\t\t\tWriteColumnar%s(&nb, items[i].%s, nil)\n", t.Name, goName)
		fmt.Fprintf(b, "\t\t\tvals[i] = nb.B\n")
		fmt.Fprintf(b, "\t\t}\n")
	case t.Nullable:
		// Empty cell → converter renders null
		fmt.Fprintf(b, "\t\tfor i := range items {\n")
		fmt.Fprintf(b, "\t\t\tif items[i].%s != nil {\n", goName)
		fmt.Fprintf(b, "\t\t\t\tvar nb api.ResponseBuf\n")
		fmt.Fprintf(b, "\t\t\t\titems[i].%s.WriteLuxo(&nb, nil)\n", goName)
		fmt.Fprintf(b, "\t\t\t\tvals[i] = nb.B\n")
		fmt.Fprintf(b, "\t\t\t}\n")
		fmt.Fprintf(b, "\t\t}\n")
	default:
		fmt.Fprintf(b, "\t\tfor i := range items {\n")
		fmt.Fprintf(b, "\t\t\tvar nb api.ResponseBuf\n")
		fmt.Fprintf(b, "\t\t\titems[i].%s.WriteLuxo(&nb, nil)\n", goName)
		fmt.Fprintf(b, "\t\t\tvals[i] = nb.B\n")
		fmt.Fprintf(b, "\t\t}\n")
	}
	fmt.Fprintf(b, "\t\tw.WriteColumnBytes(%d, vals)\n\t}\n", fieldID)
}

type columnarScalarSpec struct {
	goType   string
	write    string
	valueFmt string
}

func columnarSpec(typeName string, isEnum bool) (columnarScalarSpec, bool) {
	if isEnum {
		return columnarScalarSpec{goType: "string", write: "String", valueFmt: "string(%s)"}, true
	}
	specs := map[string]columnarScalarSpec{
		"Int":      {goType: "int64", write: "Int", valueFmt: "%s"},
		"Float":    {goType: "float64", write: "Float", valueFmt: "%s"},
		"String":   {goType: "string", write: "String", valueFmt: "%s"},
		"Boolean":  {goType: "bool", write: "Bool", valueFmt: "%s"},
		"DateTime": {goType: "int64", write: "Int", valueFmt: "(%s).Unix()"},
		"Duration": {goType: "int64", write: "Int", valueFmt: "int64(%s)"},
		"UUID":     {goType: "[16]byte", write: "UUID", valueFmt: "[16]byte(%s)"},
		"Decimal":  {goType: "string", write: "String", valueFmt: "(%s).String()"},
		"Bytes":    {goType: "[]byte", write: "Bytes", valueFmt: "%s"},
		"JSON":     {goType: "[]byte", write: "Bytes", valueFmt: "%s"},
	}
	spec, ok := specs[typeName]
	return spec, ok
}

func writeColumnarValueField(b *strings.Builder, valueExpr string, fieldID int, typeName string, isEnum bool) {
	spec, ok := columnarSpec(typeName, isEnum)
	if !ok {
		return
	}
	encodedExpr := fmt.Sprintf(spec.valueFmt, valueExpr)
	fmt.Fprintf(b, "\t{\n\t\tvals := make([]%s, len(items))\n", spec.goType)
	fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = %s }\n", encodedExpr)
	fmt.Fprintf(b, "\t\tw.WriteColumn%s(%d, vals)\n\t}\n", spec.write, fieldID)
}

func writeColumnarNullableValueField(b *strings.Builder, pointerExpr, valueExpr string, fieldID int, typeName string, isEnum bool) {
	spec, ok := columnarSpec(typeName, isEnum)
	if !ok {
		return
	}
	encodedExpr := fmt.Sprintf(spec.valueFmt, valueExpr)
	fmt.Fprintf(b, "\t{\n\t\tvals := make([]*%s, len(items))\n", spec.goType)
	fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
	fmt.Fprintf(b, "\t\t\tif %s != nil { v := %s; vals[i] = &v }\n", pointerExpr, encodedExpr)
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tw.WriteColumn%sPtr(%d, vals)\n\t}\n", spec.write, fieldID)
}

// writeColumnarArrayField encodes a scalar array field ([T]) as a Bytes column:
// each record's cell is an inline array encoding [count][items...], per the
// protocol's columnar array-field definition.
func writeColumnarArrayField(b *strings.Builder, valueExpr string, fieldID int, elemType string, isEnum bool) {
	var itemExpr string
	switch {
	case isEnum:
		itemExpr = "codec.AppendString(cb, string(v))"
	case elemType == "Int":
		itemExpr = "codec.AppendSvarint(cb, v)"
	case elemType == "Float":
		itemExpr = "codec.AppendFixed64(cb, v)"
	case elemType == "String":
		itemExpr = "codec.AppendString(cb, v)"
	case elemType == "Boolean":
		itemExpr = "codec.AppendBool(cb, v)"
	case elemType == "DateTime":
		itemExpr = "codec.AppendSvarint(cb, v.Unix())"
	case elemType == "Duration":
		itemExpr = "codec.AppendSvarint(cb, int64(v))"
	case elemType == "UUID":
		itemExpr = "codec.AppendUUID(cb, [16]byte(v))"
	case elemType == "Decimal":
		itemExpr = "codec.AppendString(cb, v.String())"
	case elemType == "Bytes", elemType == "JSON":
		itemExpr = "codec.AppendBytes(cb, v)"
	default:
		return // unknown element type — relation lists are excluded by the caller
	}
	fmt.Fprintf(b, "\t{\n\t\tcells := make([][]byte, len(items))\n")
	fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
	fmt.Fprintf(b, "\t\t\tvalues := %s\n", valueExpr)
	fmt.Fprintf(b, "\t\t\tvar cb []byte\n")
	fmt.Fprintf(b, "\t\t\tcb = codec.AppendArrayHeader(cb, len(values))\n")
	fmt.Fprintf(b, "\t\t\tfor _, v := range values { cb = %s }\n", itemExpr)
	fmt.Fprintf(b, "\t\t\tcells[i] = cb\n")
	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t\tw.WriteColumnBytes(%d, cells)\n\t}\n", fieldID)
}

// writeMaskDirective generates @mask logic for a field. Returns the (possibly modified) goField name.
func writeMaskDirective(b *strings.Builder, f *ast.FieldDecl, goField, baseType string) string {
	return writeMaskDirectiveAt(b, f, goField, baseType, "\t\t")
}

func writeMaskDirectiveAt(b *strings.Builder, f *ast.FieldDecl, goField, baseType, indent string) string {
	if f.Type != nil && f.Type.Nullable && !f.Type.IsList {
		maskedExpr := compileMaskDirectiveExpr(f, "*"+goField, baseType)
		if maskedExpr == "*"+goField {
			return goField
		}
		maskedVar := f.Name + "Masked"
		fmt.Fprintf(b, "%svar %s *string\n", indent, maskedVar)
		fmt.Fprintf(b, "%sif %s != nil { %sValue := %s; %s = &%sValue }\n",
			indent, goField, maskedVar, maskedExpr, maskedVar, maskedVar)
		return maskedVar
	}
	maskedExpr := compileMaskDirectiveExpr(f, goField, baseType)
	if maskedExpr == goField {
		return goField
	}
	maskedVar := f.Name + "Masked"
	fmt.Fprintf(b, "%s%s := %s\n", indent, maskedVar, maskedExpr)
	return maskedVar
}

func compileMaskDirectiveExpr(f *ast.FieldDecl, valueExpr, baseType string) string {
	maskDir := findDirective(f.Directives, "mask")
	if maskDir == nil || baseType != "String" {
		return valueExpr
	}
	if hasDirective(f.Directives, "email") {
		return fmt.Sprintf("str.MaskEmail(%s)", valueExpr)
	}
	if len(maskDir.Args) == 0 {
		return fmt.Sprintf("str.Mask(%s, 3, 4)", valueExpr)
	}
	if len(maskDir.Args) != 1 {
		return fmt.Sprintf("str.MaskPattern(%s, %q)", valueExpr, "")
	}
	pattern, ok := maskDir.Args[0].Value.(*ast.Literal)
	if !ok || pattern.Kind != token.String {
		return fmt.Sprintf("str.MaskPattern(%s, %q)", valueExpr, "")
	}
	return fmt.Sprintf("str.MaskPattern(%s, %q)", valueExpr, pattern.Value)
}

// writeVisibleDirective generates @visible condition check.
// @visible { my.role == "admin" } → skip field if identity doesn't match.
func writeVisibleDirective(b *strings.Builder, f *ast.FieldDecl) bool {
	cond := compileVisibleDirectiveExpr(f)
	if cond == "" {
		return false
	}
	fmt.Fprintf(b, "\t if %s {\n", cond)
	return true
}

func compileVisibleDirectiveExpr(f *ast.FieldDecl) string {
	visibleDir := findDirective(f.Directives, "visible")
	if visibleDir == nil || visibleDir.Body == nil || len(visibleDir.Body.Stmts) == 0 {
		return ""
	}
	es, ok := visibleDir.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		return ""
	}
	return compileVisibleExpr(es.Expr)
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
	return writeTransformDirectiveAt(b, f, goField, "\t\t")
}

func writeTransformDirectiveAt(b *strings.Builder, f *ast.FieldDecl, goField, indent string) string {
	valueField := goField
	if f.Type != nil && f.Type.Nullable && !f.Type.IsList {
		valueField = "*" + goField
	}
	code := compileTransformDirectiveExpr(f, valueField)
	if code == valueField {
		return goField
	}
	transformedVar := f.Name + "Transformed"
	if f.Type != nil && f.Type.Nullable && !f.Type.IsList {
		baseType := *f.Type
		baseType.Nullable = false
		fmt.Fprintf(b, "%svar %s *%s\n", indent, transformedVar, resolveGoType(&baseType))
		fmt.Fprintf(b, "%sif %s != nil { %sValue := %s; %s = &%sValue }\n",
			indent, goField, transformedVar, code, transformedVar, transformedVar)
		return transformedVar
	}
	fmt.Fprintf(b, "%s%s := %s\n", indent, transformedVar, code)
	return transformedVar
}

func compileTransformDirectiveExpr(f *ast.FieldDecl, valueExpr string) string {
	transformDir := findDirective(f.Directives, "transform")
	if transformDir == nil || transformDir.Body == nil || len(transformDir.Body.Stmts) == 0 {
		return valueExpr
	}
	es, ok := transformDir.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		return valueExpr
	}
	code := compileFieldExpr(es.Expr, valueExpr)
	if code == "" {
		return valueExpr
	}
	return code
}

// Removed: inferVisibleIdentityType — now uses shared inferMyFieldType from identity.go
