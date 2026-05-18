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

	// type declarations — generate WriteLuxo with VALUE receiver (can call on literal)
	for _, file := range result.Files {
		for _, t := range file.Types {
			pseudo := &ast.ModelDecl{Name: t.Name, Fields: t.Fields}
			generateTypeWriteLuxo(&b, pseudo, enums)
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

	// Generate nil-mask fast path: write all fields without FieldMaskHas checks
	fmt.Fprintf(b, "\tif len(mask) == 0 {\n")
	writeArenaLenCalc(b, m, recv, enums, false, "\t\t")
	generateWriteLuxoAllFields(b, m, recv, enums)
	fmt.Fprintf(b, "\t\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "\t\treturn\n")
	fmt.Fprintf(b, "\t}\n")

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

// writeModelFieldEncoding writes the binary encoding for a single model field.
// Handles enums, all scalar types (Int/Float/String/Boolean/DateTime/Duration/UUID/Decimal/Bytes/JSON),
// both nullable and non-nullable variants.
func writeModelFieldEncoding(b *strings.Builder, f *ast.FieldDecl, fid, goField string, enums map[string]bool, indent string) {
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
		"UUID":     {"AppendString", "%s.String()", "%s.String()"},
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
		if f.Type == nil {
			continue
		}
		fieldID := getModelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}
		goField := recv + "." + str.Capitalize(f.Name)
		fid := fmt.Sprintf("%d", fieldID)

		if isRelationField(f, enums) {
			// Nested model/type reference — write inline with WriteLuxo
			if f.Type.IsList {
				fmt.Fprintf(b, "\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\tbuf.B = codec.AppendSvarint(buf.B, int64(len(%s)))\n", goField)
				fmt.Fprintf(b, "\tfor i := range %s { %s[i].WriteLuxo(buf, nil) }\n", goField, goField)
			} else if f.Type.Nullable {
				fmt.Fprintf(b, "\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\tif %s != nil { buf.B = codec.AppendPresent(buf.B); %s.WriteLuxo(buf, nil) } else { buf.B = codec.AppendNull(buf.B) }\n", goField, goField)
			} else {
				fmt.Fprintf(b, "\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
				fmt.Fprintf(b, "\t%s.WriteLuxo(buf, nil)\n", goField)
			}
			continue
		}

		// List of scalars: [String], [Int], [Enum], etc.
		if f.Type.IsList {
			writeTypeListScalarField(b, f, fid, goField, enums)
			continue
		}

		// Scalar/enum fields
		if enums[f.Type.Name] {
			fmt.Fprintf(b, "\tbuf.B = codec.AppendVarint(buf.B, %s); buf.B = codec.AppendString(buf.B, string(%s))\n", fid, goField)
			continue
		}

		writeTypeScalarField(b, f, fid, goField)
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeTypeScalarField writes a single scalar field for type WriteLuxo, handling nullable.
func writeTypeScalarField(b *strings.Builder, f *ast.FieldDecl, fid, goField string) {
	writeScalarEncoding(b, f.Type.Name, f.Type.Nullable, fid, goField, "\t")
}

// writeTypeListScalarField writes a list of scalar values for type WriteLuxo.
func writeTypeListScalarField(b *strings.Builder, f *ast.FieldDecl, fid, goField string, enums map[string]bool) {
	fmt.Fprintf(b, "\tbuf.B = codec.AppendVarint(buf.B, %s)\n", fid)
	fmt.Fprintf(b, "\tbuf.B = codec.AppendSvarint(buf.B, int64(len(%s)))\n", goField)

	elemType := f.Type.Name
	if enums[elemType] {
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendString(buf.B, string(v)) }\n", goField)
		return
	}
	switch elemType {
	case "Int":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, v) }\n", goField)
	case "Float":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendFixed64(buf.B, v) }\n", goField)
	case "String":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendString(buf.B, v) }\n", goField)
	case "DateTime":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, v.Unix()) }\n", goField)
	case "UUID":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendString(buf.B, v.String()) }\n", goField)
	case "Decimal":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendString(buf.B, v.String()) }\n", goField)
	case "Bytes":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendBytes(buf.B, v) }\n", goField)
	case "Boolean":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendBool(buf.B, v) }\n", goField)
	case "Duration":
		fmt.Fprintf(b, "\tfor _, v := range %s { buf.B = codec.AppendSvarint(buf.B, int64(v)) }\n", goField)
	default:
		// Unknown list element — write as nested model
		fmt.Fprintf(b, "\tfor i := range %s { %s[i].WriteLuxo(buf, nil) }\n", goField, goField)
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
			fmt.Fprintf(b, "\t\tcase %d:\n\t\t\tif v := dec.ReadStringPtr(); v != nil { u := uuid.MustParse(*v); %s = &u }\n", fieldID, goField)
		} else {
			fmt.Fprintf(b, "\t\tcase %d: %s = uuid.MustParse(dec.ReadString())\n", fieldID, goField)
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
	case "UUID":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s.String() }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnString(%d, vals)\n\t}\n", fieldID)
	case "Decimal":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items { vals[i] = item.%s.String() }\n", goName)
		fmt.Fprintf(b, "\t\tw.WriteColumnString(%d, vals)\n\t}\n", fieldID)
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
	case "DateTime":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
		fmt.Fprintf(b, "\t\t\tif item.%s != nil { v := item.%s.Unix(); vals[i] = &v }\n", goName, goName)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\tw.WriteColumnIntPtr(%d, vals)\n\t}\n", fieldID)
	case "Duration":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*int64, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
		fmt.Fprintf(b, "\t\t\tif item.%s != nil { v := int64(*item.%s); vals[i] = &v }\n", goName, goName)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\tw.WriteColumnIntPtr(%d, vals)\n\t}\n", fieldID)
	case "UUID":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
		fmt.Fprintf(b, "\t\t\tif item.%s != nil { s := item.%s.String(); vals[i] = &s }\n", goName, goName)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\tw.WriteColumnStringPtr(%d, vals)\n\t}\n", fieldID)
	case "Decimal":
		fmt.Fprintf(b, "\t{\n\t\tvals := make([]*string, len(items))\n")
		fmt.Fprintf(b, "\t\tfor i, item := range items {\n")
		fmt.Fprintf(b, "\t\t\tif item.%s != nil { s := item.%s.String(); vals[i] = &s }\n", goName, goName)
		fmt.Fprintf(b, "\t\t}\n")
		fmt.Fprintf(b, "\t\tw.WriteColumnStringPtr(%d, vals)\n\t}\n", fieldID)
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
