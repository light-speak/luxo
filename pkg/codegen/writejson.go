package codegen

import (
	"fmt"
	"strconv"
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
	return defaultGenerator().generateWriteJSONFile(result, packageName, enums)
}

func (g *GeneratorContext) generateWriteJSONFile(result *semantic.Result, packageName string, enums map[string]bool) []byte {
	result = g.withoutValueAliases(result)
	var models []*ast.ModelDecl
	for _, file := range result.Files {
		models = append(models, file.Models...)
	}
	// Check if any types exist (non-DB declarations also need WriteLuxo)
	hasTypes := false
	hasExtends := false
	for _, file := range result.Files {
		if len(file.Types) > 0 {
			hasTypes = true
		}
		hasExtends = hasExtends || len(file.Extends) > 0
	}
	if len(models) == 0 && !hasTypes && !hasExtends {
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
			stubs = append(stubs, g.extendStubModel(ext))
			modelNames[ext.Name] = true
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
	var helpers string
	g, helpers = g.withTransformFunctions(result, importDecls, enums)
	depthContext := *g
	depthContext.singleObjectDepth = g.valueTypes == nil
	for _, declaration := range importDecls {
		if g.hasWriteRelation(declaration, enums) {
			depthContext.singleObjectDepth = false
			break
		}
	}
	g = depthContext.withCodecDepthNames(result, importDecls)
	writers := &writeJSONContext{GeneratorContext: *g, enums: enums}
	var input strings.Builder
	identityObjects := identityObjectNames(result, importDecls)
	for _, declaration := range importDecls {
		g.generateReadJSONInput(&input, declaration, enums)
		if identityObjects[declaration.Name] {
			g.generateReadIdentityInput(&input, declaration, enums, modelNames[declaration.Name])
		}
	}
	writers.writeWriteJSONImports(&b, importDecls, generatedPackages(input.String()), helpers)
	b.WriteString(input.String())

	for _, m := range models {
		writers.generateWriteLuxo(&b, m, enums, modelNames)
		g.generateReadLuxo(&b, m, enums)
		g.generateReadColumnar(&b, m, enums)
		writers.generateWriteColumnar(&b, m, enums, modelNames)
	}

	for _, s := range stubs {
		writers.generateWriteLuxo(&b, s, enums, modelNames)
		g.generateReadLuxo(&b, s, enums)
		g.generateReadColumnar(&b, s, enums)
		writers.generateWriteColumnar(&b, s, enums, modelNames)
	}

	// type declarations — generate WriteLuxo with VALUE receiver (can call on literal)
	// plus WriteColumnar so native list responses follow the columnar protocol
	for _, file := range result.Files {
		for _, t := range file.Types {
			pseudo := &ast.ModelDecl{Name: t.Name, Fields: t.Fields}
			writers.generateTypeWriteLuxo(&b, pseudo, enums, modelNames)
			g.generateReadLuxo(&b, pseudo, enums)
			g.generateReadColumnar(&b, pseudo, enums)
			writers.generateTypeWriteColumnar(&b, pseudo, enums, modelNames)
		}
	}

	if helpers != "" {
		b.WriteString(helpers)
	}
	return []byte(b.String())
}

func extendStubModel(ext *ast.ExtendDecl) *ast.ModelDecl {
	return defaultGenerator().extendStubModel(ext)
}

func (g *GeneratorContext) extendStubModel(ext *ast.ExtendDecl) *ast.ModelDecl {
	keyName := g.externalModelIDFieldName(ext.Name)
	fields := make([]*ast.FieldDecl, 0, len(ext.Fields)+1)
	hasKey := false
	for _, field := range ext.Fields {
		if field.Name == keyName {
			hasKey = true
		}
		fields = append(fields, field)
	}
	if !hasKey {
		key := &ast.FieldDecl{
			Name:       keyName,
			Type:       &ast.TypeRef{Name: g.externalModelIDTypeName(ext.Name)},
			Directives: []*ast.Directive{{Name: "id"}},
		}
		fields = append([]*ast.FieldDecl{key}, fields...)
	}
	return &ast.ModelDecl{Name: ext.Name, Fields: fields}
}

// writeJSONImportNeeds tracks which imports writejson.gen.go requires.
type writeJSONImportNeeds struct {
	time    bool
	json    bool
	uuid    bool
	decimal bool
	strings bool
	str     bool
	math    bool
	checked bool
	inline  generatedImports
}

func (w *writeJSONContext) scanWriteJSONImports(m *ast.ModelDecl, needs *writeJSONImportNeeds) {
	for _, f := range m.Fields {
		if f.Type == nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if hasDirective(f.Directives, "mask") {
			needs.str = true
		}
		for _, directive := range f.Directives {
			if directive.Name != "visible" {
				continue
			}
			w.scanVisibleImportNeeds(f, directive, needs)
		}
		// Fallible expressions are compiled into helpers, whose imports are scanned separately.
		transform := findDirective(f.Directives, "transform")
		if transform == nil || !fieldBodyPropagatesError(transform.Body) {
			if expression := compileTransformDirectiveExpr(f, "value"); expression != "value" {
				needs.inline |= generatedPackages(expression)
			}
		}
		switch f.Type.Name {
		case "DateTime", "Duration":
			needs.time = true
		case "UUID":
			needs.uuid = true
		case "Decimal":
			needs.decimal = true
		case "JSON":
			needs.json = needs.json || f.Type.Nullable || f.Type.IsList
		}
	}
}

func (w *writeJSONContext) scanVisibleImportNeeds(field *ast.FieldDecl, directive *ast.Directive, needs *writeJSONImportNeeds) {
	typed, identity := false, false
	walkExpr(directive.SingleExpression(), func(value ast.Expr) {
		needs.checked = needs.checked || fieldExpressionMayFail(value)
		if value.GetTypeTag() == "Duration" {
			needs.time = true
		}
		switch value := value.(type) {
		case *ast.BinaryExpr:
			if _, matched := compileFloatRemainder(value, "", ""); matched {
				needs.math = true
			}
			if w.sealed != nil && (value.Op == "==" || value.Op == "!=") {
				typed = typed || w.sealedDeclaration(value.Left.GetTypeTag()) != nil || w.sealedDeclaration(value.Right.GetTypeTag()) != nil
			}
		case *ast.MemberExpr:
			typed = typed || value.SealedConstructor
			if name, ok := value.Object.(*ast.Ident); ok && name.Name == "my" {
				identity = true
			}
		case *ast.Literal:
			if value.Kind == token.Duration {
				needs.time = true
			}
		}
	})
	if typed {
		plan := w.prepareVisibleCondition(field, directive, identity)
		needs.inline |= generatedPackages(plan.code)
	}
}

func writeWriteJSONImports(b *strings.Builder, declarations []*ast.ModelDecl, inputImports generatedImports, helperBodies ...string) {
	(&writeJSONContext{}).writeWriteJSONImports(b, declarations, inputImports, helperBodies...)
}

func (w *writeJSONContext) writeWriteJSONImports(b *strings.Builder, declarations []*ast.ModelDecl, inputImports generatedImports, helperBodies ...string) {
	var needs writeJSONImportNeeds
	for _, m := range declarations {
		w.scanWriteJSONImports(m, &needs)
	}

	var helperBody string
	if len(helperBodies) != 0 {
		helperBody = helperBodies[0]
	}
	helperImports := generatedPackages(helperBody) | needs.inline
	if needs.math {
		helperImports |= generatedPackageBit("math")
	}
	if needs.decimal {
		helperImports |= generatedPackageBit("lux")
	}
	if needs.checked {
		helperImports |= generatedPackageBit("lux") | generatedPackageBit("errors")
	}
	mergeFieldFunctionImportNeeds(&needs, helperImports)
	b.WriteString("import (\n")
	writeFieldFunctionExtraImports(b, helperImports)
	for _, name := range []string{"bytes", "fmt"} {
		if inputImports.has(name) || helperImports.has(name) {
			fmt.Fprintf(b, "\t%q\n", name)
		}
	}
	if needs.json || inputImports.has("json") || helperImports.has("json") {
		b.WriteString("\t\"encoding/json\"\n")
	}
	if needs.strings {
		b.WriteString("\t\"strings\"\n")
	}
	if needs.time {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n\t\"github.com/light-speak/luxo/pkg/lux/api\"\n")
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/codec\"\n")
	if inputImports.has("jsonutil") {
		b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/jsonutil\"\n")
	}
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
		suppressions = append(suppressions, `var _ = lux.FormatDecimal`)
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
	if f.Type == nil {
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
// Always writes a totalStringLen prefix; returns whether any arena fields exist.
func writeArenaLenCalc(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool, masked bool, indent string) bool {
	return defaultGenerator().writeArenaLenCalc(b, m, recv, enums, masked, indent, false)
}

func (g *GeneratorContext) writeArenaLenCalc(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool, masked bool, indent string, compact bool) bool {
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
		fieldID := g.modelFieldID(m.Name, f.Name)
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
			condition := rowFieldSelected(af.fieldID, compact)
			if af.nullable {
				fmt.Fprintf(b, "%sif %s && %s != nil { _arenaLen += %s }\n",
					indent, condition, af.goField, ptrLenExpr)
			} else {
				fmt.Fprintf(b, "%sif %s { _arenaLen += %s }\n",
					indent, condition, lenExpr)
			}
		} else {
			if af.nullable {
				fmt.Fprintf(b, "%sif %s != nil { _arenaLen += %s }\n", indent, af.goField, ptrLenExpr)
			} else {
				fmt.Fprintf(b, "%s_arenaLen += %s\n", indent, lenExpr)
			}
		}
	}
	fmt.Fprintf(b, "%sbuf.WriteLuxoVarint(uint64(_arenaLen))\n", indent)
	return true
}

// generateWriteLuxo generates a WriteLuxo method for Luxo binary serialization.
// Field IDs come from luxo.lock via getModelFieldID().
// Writes persistent/default fields and explicitly selected nested relations.
// Prefixes field data with totalStringLen varint for arena allocation on decode.
func generateWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateWriteLuxo(b, m, enums)
}

func (g *GeneratorContext) generateWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateWriteLuxo(b, m, enums, modelSets...)
}

func (g *writeJSONContext) generateWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	name := m.Name
	recv := receiverName(name)

	fmt.Fprintf(b, "// WriteLuxo writes %s as Luxo binary directly to buf. Zero intermediate allocation.\n", name)
	fmt.Fprintf(b, "func (%s *%s) WriteLuxo(buf *api.ResponseBuf, mask []byte) error {\n", recv, name)
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "\treturn %s.%s(buf, mask, 1)\n}\n\n", recv, g.valueDepthMethod(name, "writeLuxoDepth"))
		fmt.Fprintf(b, "func (%s *%s) %s(buf *api.ResponseBuf, mask []byte, depth int) error {\n", recv, name, g.valueDepthMethod(name, "writeLuxoDepth"))
		writeModelDepthGuard(b, "return codec.ErrModelDepth")
	}

	// Output directives must also run for SELECT *, so those models use the
	// directive-aware path even when the field mask is empty.
	hasRelations := g.hasWriteRelation(m, enums)
	compact := !hasRelations && g.useByteRowProjection(m)
	if !hasOutputDirectives(m) && !compact {
		fmt.Fprintf(b, "\tif len(mask) == 0 {\n")
		g.writeArenaLenCalc(b, m, recv, enums, false, "\t\t", false)
		g.generateWriteLuxoAllFields(b, m, recv, enums)
		fmt.Fprintf(b, "\t\tbuf.B = append(buf.B, 0x00)\n")
		fmt.Fprintf(b, "\t\treturn nil\n")
		fmt.Fprintf(b, "\t}\n")
	}
	if hasRelations {
		b.WriteString("\tselectionMask := mask\n")
	}
	if compact {
		b.WriteString("_fieldBits := byte(0xff)\nif len(mask) != 0 {\nmask = codec.SelectionMaskFields(mask)\nif len(mask) != 0 {_fieldBits = mask[0]}\n}\n")
	} else {
		b.WriteString("\tmask = codec.SelectionMaskFields(mask)\n")
	}

	// Slow path: arena len with mask checks, then field encoding
	g.writeArenaLenCalc(b, m, recv, enums, true, "\t", compact)

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

		if isRelationField(f, enums) {
			fmt.Fprintf(b, "\tif len(selectionMask) > 0 && codec.FieldMaskHas(mask, %d) {\n", fieldID)
		} else {
			fmt.Fprintf(b, "\tif %s {\n", rowFieldSelected(fieldID, compact))
		}

		// @visible: conditional field visibility based on identity
		hasVisible := g.writeVisibleDirectiveWithEnums(b, f, enums)

		goField := recv + "." + str.Capitalize(f.Name)
		baseType := f.Type.Name

		if isRelationField(f, enums) {
			g.writeNestedRowField(b, f, fieldID, goField, "selectionMask", "\t\t", nestedModelTarget(f.Type.Name, modelSets), g.isModelReference(f.Type.Name))
		} else {
			// @transform: apply value transformation before writing
			goField = g.writeFieldTransform(b, f, goField, "		")

			// @mask: apply masking before writing string fields
			goField = writeMaskDirective(b, f, goField, baseType)

			// All fields write directly to buf.B — zero intermediate buffer
			writeModelFieldEncoding(b, f, fieldID, goField, enums, "\t\t")
		}
		if hasVisible {
			b.WriteString("\t }\n")
		}
		b.WriteString("\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")
}

// Share the mask byte across fields. A single field keeps its direct all-fields path.
func (g *GeneratorContext) useByteRowProjection(m *ast.ModelDecl) bool {
	fieldCount := 0
	for _, field := range m.Fields {
		if field.Type == nil || hasDirective(field.Directives, "hidden") || hasDirective(field.Directives, "internal") {
			continue
		}
		id := g.modelFieldID(m.Name, field.Name)
		if id > 8 {
			return false
		}
		if id > 0 {
			fieldCount++
		}
	}
	return fieldCount > 1
}

func rowFieldSelected(id int, compact bool) string {
	if compact {
		return fmt.Sprintf("_fieldBits&%d != 0", 1<<(id-1))
	}
	return fmt.Sprintf("codec.FieldMaskHas(mask, %d)", id)
}

func writeModelDepthGuard(b *strings.Builder, failure string) {
	b.WriteString("\tif depth > codec.MaxModelDepth { ")
	b.WriteString(failure)
	b.WriteString(" }\n")
}

func (g *GeneratorContext) depthColumnarFunction(name string) string {
	if g.valueTypes != nil && g.valueTypes.columnar[name] != "" {
		return g.valueTypes.columnar[name]
	}
	initial := "r"
	if name[0] == 'W' {
		initial = "w"
	}
	name = initial + name[1:] + "Depth"
	for g.codecDepthNames[name] {
		name += "_"
	}
	return name
}

// Reserve only possible private-codec collisions; ordinary schemas need no map.
func (g *GeneratorContext) withCodecDepthNames(result *semantic.Result, declarations []*ast.ModelDecl) *GeneratorContext {
	if g.singleObjectDepth {
		return g
	}
	var names map[string]bool
	reserve := func(name string) {
		if strings.HasPrefix(name, "readColumnar") || strings.HasPrefix(name, "writeColumnar") {
			if names == nil {
				names = make(map[string]bool)
			}
			names[name] = true
		}
	}
	for _, declaration := range declarations {
		reserve(declaration.Name)
	}
	for _, file := range result.Files {
		for _, declaration := range file.Enums {
			name := declaration.Name
			if !strings.HasPrefix(name, "readColumnar") && !strings.HasPrefix("readColumnar", name) &&
				!strings.HasPrefix(name, "writeColumnar") && !strings.HasPrefix("writeColumnar", name) {
				continue
			}
			reserve(name)
			for _, value := range declaration.Values {
				reserve(name + value)
			}
		}
	}
	if names == nil {
		return g
	}
	copy := *g
	copy.codecDepthNames = names
	return &copy
}

func (g *GeneratorContext) writeNestedRowField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField, selectionMask, indent string, targetIsModel bool, pointerLists ...bool) {
	fid := strconv.Itoa(fieldID)
	childMask := "nil"
	if selectionMask != "" {
		childMask = "_childMask" + fid
		fmt.Fprintf(b, "%s%s, _ := codec.SelectionMaskNested(%s, %s)\n", indent, childMask, selectionMask, fid)
	}
	writeBinaryFieldID(b, indent, fieldID)
	if f.Type.Nullable {
		fmt.Fprintf(b, "%sif %s == nil { buf.WriteLuxoNull() } else {\n", indent, goField)
		fmt.Fprintf(b, "%sbuf.WriteLuxoPresent()\n", indent)
	}
	sealed := g.sealedDeclaration(f.Type.Name) != nil
	if sealed && !f.Type.IsList && !f.Type.Nullable {
		fmt.Fprintf(b, "%sif %s == nil { return fmt.Errorf(%q) }\n", indent, goField, "missing sealed field "+f.Name)
	}
	framed := !sealed || !f.Type.IsList
	if framed {
		fmt.Fprintf(b, "%s_frame%s := buf.BeginMessage()\n", indent, fid)
	}
	if f.Type.IsList {
		writeBinaryCall(b, indent, fmt.Sprintf("%s(buf, %s, %s, depth+1)", g.depthColumnarFunction(nestedColumnarWriter(f.Type.Name, targetIsModel, pointerLists...)), goField, childMask), "return err")
	} else {
		writeBinaryCall(b, indent, fmt.Sprintf("%s.%s(buf, %s, depth+1)", goField, g.valueDepthMethod(f.Type.Name, "writeLuxoDepth"), childMask), "return err")
	}
	if framed {
		fmt.Fprintf(b, "%sbuf.EndMessage(_frame%s)\n", indent, fid)
	}
	if f.Type.Nullable {
		fmt.Fprintf(b, "%s}\n", indent)
	}
}

func nestedColumnarWriter(name string, targetIsModel bool, pointerLists ...bool) string {
	if targetIsModel && (len(pointerLists) == 0 || !pointerLists[0]) {
		return "WriteColumnar" + name + "Values"
	}
	return "WriteColumnar" + name
}

func nestedModelTarget(name string, modelSets []map[string]bool) bool {
	return len(modelSets) == 0 || modelSets[0][name]
}

func hasOutputDirectives(model *ast.ModelDecl) bool {
	for _, field := range model.Fields {
		if hasDirective(field.Directives, "mask") || hasDirective(field.Directives, "transform") || hasDirective(field.Directives, "visible") {
			return true
		}
	}
	return false
}

func hasWriteRelation(model *ast.ModelDecl, enums map[string]bool) bool {
	return defaultGenerator().hasWriteRelation(model, enums)
}

func (g *GeneratorContext) hasWriteRelation(model *ast.ModelDecl, enums map[string]bool) bool {
	for _, field := range model.Fields {
		if field.Type != nil && !hasDirective(field.Directives, "hidden") &&
			!hasDirective(field.Directives, "internal") && isRelationField(field, enums) &&
			g.modelFieldID(model.Name, field.Name) != 0 {
			return true
		}
	}
	return false
}

// writeModelFieldEncoding writes the binary encoding for a single model field.
// Handles enums, all scalar types (Int/Float/String/Boolean/DateTime/Duration/UUID/Decimal/Bytes/JSON),
// both nullable and non-nullable variants.
func writeModelFieldEncoding(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool, indent string) {
	// Scalar array field ([String], [Int], [UUID], ...). Relation lists are
	// excluded by the caller via isRelationField.
	if f.Type.IsList {
		writeListScalarField(b, f, fieldID, goField, enums, indent)
		return
	}
	if enums[f.Type.Name] {
		if f.Type.Nullable {
			writeBinaryFieldID(b, indent, fieldID)
			fmt.Fprintf(b, "%sif %s != nil {\n", indent, goField)
			fmt.Fprintf(b, "%s\tbuf.WriteLuxoPresent()\n", indent)
			writeBufferedBinaryAppend(b, indent+"\t", enumAppendExpression(f.Type.Name, "buf.B", "*"+goField))
			fmt.Fprintf(b, "%s} else {\n", indent)
			fmt.Fprintf(b, "%s\tbuf.WriteLuxoNull()\n", indent)
			fmt.Fprintf(b, "%s}\n", indent)
		} else {
			writeBinaryFieldID(b, indent, fieldID)
			writeBufferedBinaryAppend(b, indent, enumAppendExpression(f.Type.Name, "buf.B", goField))
		}
		return
	}
	writeScalarEncoding(b, f.Type.Name, f.Type.Nullable, fieldID, goField, indent)
}

// writeScalarEncoding writes binary encoding for a scalar type, handling nullable.
func writeScalarEncoding(b *strings.Builder, typeName string, nullable bool, fieldID int, goField, indent string) {
	value := goField
	if nullable {
		value = "*" + value
	}
	if typeName == "UUID" {
		value = "[16]byte(" + value + ")"
	}
	expression, ok := binaryScalarAppend(typeName, "buf.B", value, false)
	if !ok {
		return
	}
	writeBinaryFieldID(b, indent, fieldID)
	if !nullable {
		writeBufferedBinaryAppend(b, indent, expression)
		return
	}
	condition := goField + " != nil"
	if typeName == "JSON" {
		condition = "!(" + nullableNullCondition(&ast.TypeRef{Name: typeName}, goField) + ")"
	}
	fmt.Fprintf(b, "%sif %s {\n", indent, condition)
	fmt.Fprintf(b, "%s\tbuf.WriteLuxoPresent()\n", indent)
	writeBufferedBinaryAppend(b, indent+"\t", expression)
	fmt.Fprintf(b, "%s} else {\n", indent)
	fmt.Fprintf(b, "%s\tbuf.WriteLuxoNull()\n", indent)
	fmt.Fprintf(b, "%s}\n", indent)
}

// generateWriteLuxoAllFields generates the nil-mask fast path for the default
// model projection. Relations are loaded only through an explicit selection.
func generateWriteLuxoAllFields(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool) {
	defaultGenerator().generateWriteLuxoAllFields(b, m, recv, enums)
}

func (g *GeneratorContext) generateWriteLuxoAllFields(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool) {
	for _, f := range m.Fields {
		if f.Type == nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		if isRelationField(f, enums) {
			continue
		}
		fieldID := g.modelFieldID(m.Name, f.Name)
		if fieldID == 0 {
			continue
		}
		goField := recv + "." + str.Capitalize(f.Name)

		writeModelFieldEncoding(b, f, fieldID, goField, enums, "\t\t")
	}
}

// generateReadLuxo generates a ReadLuxo method that decodes a model from Luxo binary.
// generateTypeWriteLuxo generates WriteLuxo with value receiver for type declarations.
// Value receiver allows calling on literals: AuthPayload{...}.WriteLuxo(...)
// Type fields write ALL fields unconditionally — including nested model references.
// Unlike model WriteLuxo, type fields don't skip "relations" since types have no DB semantics.
func generateTypeWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateTypeWriteLuxo(b, m, enums)
}

func (g *GeneratorContext) generateTypeWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateTypeWriteLuxo(b, m, enums, modelSets...)
}

func (g *writeJSONContext) generateTypeWriteLuxo(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	var modelNames map[string]bool
	if len(modelSets) > 0 {
		modelNames = modelSets[0]
	}
	name := m.Name
	recv := receiverName(name)
	fmt.Fprintf(b, "// WriteLuxo writes %s as Luxo binary directly to buf.\n", name)
	fmt.Fprintf(b, "func (%s %s) WriteLuxo(buf *api.ResponseBuf, mask []byte) error {\n", recv, name)
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "\treturn %s.%s(buf, mask, 1)\n}\n\n", recv, g.valueDepthMethod(name, "writeLuxoDepth"))
		fmt.Fprintf(b, "func (%s %s) %s(buf *api.ResponseBuf, mask []byte, depth int) error {\n", recv, name, g.valueDepthMethod(name, "writeLuxoDepth"))
		writeModelDepthGuard(b, "return codec.ErrModelDepth")
	}

	if g.hasWriteRelation(m, enums) {
		b.WriteString("\tselectionMask := mask\n")
	}
	b.WriteString("\tmask = codec.SelectionMaskFields(mask)\n")
	g.writeArenaLenCalc(b, m, recv, enums, true, "\t", false)

	for _, f := range m.Fields {
		if f.Type == nil || hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		fieldID := g.modelFieldID(name, f.Name)
		if fieldID == 0 {
			continue
		}
		goField := recv + "." + str.Capitalize(f.Name)
		fmt.Fprintf(b, "\tif codec.FieldMaskHas(mask, %d) {\n", fieldID)
		indent := "\t\t"
		visibleExpr := g.writeVisibleConditionAt(b, f, enums, indent)
		if visibleExpr != "" {
			fmt.Fprintf(b, "%sif %s {\n", indent, visibleExpr)
			indent = "\t\t\t"
		}
		goField = g.writeFieldTransform(b, f, goField, indent)
		goField = writeMaskDirectiveAt(b, f, goField, f.Type.Name, indent)

		if isRelationField(f, enums) {
			g.writeNestedRowField(b, f, fieldID, goField, "selectionMask", indent, modelNames[f.Type.Name], g.isModelReference(f.Type.Name))
		} else {
			writeModelFieldEncoding(b, f, fieldID, goField, enums, indent)
		}
		if visibleExpr != "" {
			fmt.Fprintf(b, "\t\t}\n")
		}
		fmt.Fprintf(b, "\t}\n")
	}

	fmt.Fprintf(b, "\tbuf.B = append(buf.B, 0x00)\n")
	fmt.Fprintf(b, "\treturn nil\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeListScalarField writes a list of scalar values (count + items) for
// WriteLuxo, per the protocol array encoding: [varint count][items...].
// Shared by model and type declarations; indent positions the emitted code.
func writeListScalarField(b *strings.Builder, f *ast.FieldDecl, fieldID int, goField string, enums map[string]bool, indent string) {
	writeBinaryFieldID(b, indent, fieldID)
	if f.Type.Nullable {
		fmt.Fprintf(b, "%sif %s == nil {\n%s\tbuf.WriteLuxoNull()\n%s} else {\n", indent, goField, indent, indent)
		fmt.Fprintf(b, "%s\tbuf.WriteLuxoPresent()\n", indent)
		writeScalarArrayItems(b, f.Type.Name, enums[f.Type.Name], goField, indent+"\t")
		fmt.Fprintf(b, "%s}\n", indent)
		return
	}
	writeScalarArrayItems(b, f.Type.Name, enums[f.Type.Name], goField, indent)
}

func writeScalarArrayItems(b *strings.Builder, elemType string, isEnum bool, goField, indent string) {
	writeBufferedBinaryAppend(b, indent, fmt.Sprintf("codec.AppendArrayHeader(buf.B, len(%s))", goField))
	if expression := scalarArrayItemEncoding(elemType, isEnum, "buf.B"); expression != "" {
		fmt.Fprintf(b, "%sfor _, v := range %s {\n", indent, goField)
		writeBufferedBinaryAppend(b, indent+"\t", expression)
		fmt.Fprintf(b, "%s}\n", indent)
	} else {
		fmt.Fprintf(b, "%sfor i := range %s {\n", indent, goField)
		writeBinaryCall(b, indent+"\t", fmt.Sprintf("%s[i].WriteLuxo(buf, nil)", goField), "return err")
		fmt.Fprintf(b, "%s}\n", indent)
	}
}

// Share the scalar conversion for row arrays and columnar array cells.
func scalarArrayItemEncoding(elemType string, isEnum bool, buffer string) string {
	if isEnum {
		return enumAppendExpression(elemType, buffer, "v")
	}
	var method, value string
	switch elemType {
	case "Int":
		method, value = "Svarint", "v"
	case "Float":
		method, value = "Fixed64", "v"
	case "String":
		method, value = "String", "v"
	case "DateTime":
		method, value = "DateTime", "v"
	case "UUID":
		method, value = "UUID", "[16]byte(v)"
	case "Decimal":
		method, value = "String", "lux.FormatDecimal(v)"
	case "Bytes":
		method, value = "Bytes", "v"
	case "JSON":
		method, value = "JSON", "v"
	case "Boolean":
		method, value = "Bool", "v"
	case "Duration":
		method, value = "Svarint", "int64(v)"
	default:
		return ""
	}
	return fmt.Sprintf("codec.Append%s(%s, %s)", method, buffer, value)
}

// hasArenaFields returns true if the model has any arena-eligible fields.
func hasArenaFields(m *ast.ModelDecl, enums map[string]bool) bool {
	return defaultGenerator().hasArenaFields(m, enums)
}

func (g *GeneratorContext) hasArenaFields(m *ast.ModelDecl, enums map[string]bool) bool {
	for _, f := range m.Fields {
		if isArenaField(f, enums) {
			if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
				continue
			}
			if g.modelFieldID(m.Name, f.Name) == 0 {
				continue
			}
			return true
		}
	}
	return false
}

// generateWriteColumnar generates a WriteColumnar function for list encoding.
// Writes all items in columnar format: [count][col1: fieldID + all values][col2: ...]...[0x00]
// 2.75x faster, 19% smaller than row-by-row WriteLuxo for lists.
func generateWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateWriteColumnar(b, m, enums)
}

func (g *GeneratorContext) generateWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateWriteColumnar(b, m, enums, modelSets...)
}

func (g *writeJSONContext) generateWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	g.generateModelWriteColumnar(b, m, enums, "WriteColumnar"+m.Name, "[]*"+m.Name, modelSets...)
	g.generateModelWriteColumnar(b, m, enums, "WriteColumnar"+m.Name+"Values", "[]"+m.Name, modelSets...)
}

func generateModelWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, functionName, itemsType string) {
	defaultGenerator().generateModelWriteColumnar(b, m, enums, functionName, itemsType)
}

func (g *GeneratorContext) generateModelWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, functionName, itemsType string, modelSets ...map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateModelWriteColumnar(b, m, enums, functionName, itemsType, modelSets...)
}

func (g *writeJSONContext) generateModelWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, functionName, itemsType string, modelSets ...map[string]bool) {
	g.generateDirectColumnar(b, m, enums, functionName, itemsType, true, modelSets)
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
func generateTypeWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	defaultGenerator().generateTypeWriteColumnar(b, m, enums, modelSets...)
}

func (g *GeneratorContext) generateTypeWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateTypeWriteColumnar(b, m, enums, modelSets...)
}

func (g *writeJSONContext) generateTypeWriteColumnar(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, modelSets ...map[string]bool) {
	g.generateDirectColumnar(b, m, enums, "WriteColumnar"+m.Name, "[]"+m.Name, false, modelSets)
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

type fieldMaskKind uint8

const (
	fieldMaskNone fieldMaskKind = iota
	fieldMaskEmail
	fieldMaskPartial
	fieldMaskPattern
)

type fieldMaskRule struct {
	kind    fieldMaskKind
	pattern string
}

// Mask rules retain only values so cross-module codegen need not retain directives.
func fieldMaskFor(field *ast.FieldDecl, baseType string) fieldMaskRule {
	directive := findDirective(field.Directives, "mask")
	if directive == nil || baseType != "String" {
		return fieldMaskRule{}
	}
	if hasDirective(field.Directives, "email") {
		return fieldMaskRule{kind: fieldMaskEmail}
	}
	if len(directive.Args) == 0 {
		return fieldMaskRule{kind: fieldMaskPartial}
	}
	if len(directive.Args) == 1 {
		if pattern, ok := directive.Args[0].Value.(*ast.Literal); ok && pattern.Kind == token.String {
			return fieldMaskRule{kind: fieldMaskPattern, pattern: pattern.Value}
		}
	}
	return fieldMaskRule{kind: fieldMaskPattern}
}

func (mask fieldMaskRule) expression(value string) string {
	switch mask.kind {
	case fieldMaskEmail:
		return fmt.Sprintf("str.MaskEmail(%s)", value)
	case fieldMaskPartial:
		return fmt.Sprintf("str.Mask(%s, 3, 4)", value)
	case fieldMaskPattern:
		return fmt.Sprintf("str.MaskPattern(%s, %q)", value, mask.pattern)
	default:
		return value
	}
}

func compileMaskDirectiveExpr(field *ast.FieldDecl, value, baseType string) string {
	return fieldMaskFor(field, baseType).expression(value)
}

// writeVisibleDirective generates @visible condition check.
// @visible { my.role == "admin" } → skip field if identity doesn't match.
func writeVisibleDirective(b *strings.Builder, f *ast.FieldDecl) bool {
	return writeVisibleDirectiveWithEnums(b, f, nil)
}

func writeVisibleDirectiveWithEnums(b *strings.Builder, f *ast.FieldDecl, enums map[string]bool) bool {
	cond := writeVisibleConditionAt(b, f, enums, "\t")
	if cond == "" {
		return false
	}
	fmt.Fprintf(b, "\t if %s {\n", cond)
	return true
}

func (w *writeJSONContext) writeVisibleDirectiveWithEnums(b *strings.Builder, f *ast.FieldDecl, enums map[string]bool) bool {
	cond := w.writeVisibleConditionAt(b, f, enums, "\t")
	if cond == "" {
		return false
	}
	fmt.Fprintf(b, "\t if %s {\n", cond)
	return true
}

func compileVisibleDirectiveExprWithEnums(f *ast.FieldDecl, enums map[string]bool) string {
	visible := findDirective(f.Directives, "visible")
	if visible == nil {
		return ""
	}
	expression := visible.SingleExpression()
	if expression == nil {
		panic(&InternalCompilerError{Detail: "invalid @visible expression for field " + f.Name})
	}
	identity, reader := "buf.Identity", ""
	if conditionUsesIdentity(expression) {
		identity, reader = "_identity", "api.NewIdentityReader(buf.Identity)"
		if visible.AuthSubject != "" {
			reader = fmt.Sprintf("api.NewIdentityReaderForSubject(buf.Identity, %q)", visible.AuthSubject)
		}
	}
	if conditionHasCheckedArithmetic(expression) {
		return compileCheckedVisibleCondition(expression, enums, reader)
	}
	compiled := compileVisibleCondition(expression, enums, identity)
	if compiled == "" {
		panic(&InternalCompilerError{Detail: "unsupported @visible expression for field " + f.Name})
	}
	if reader != "" {
		return "func() bool { _identity := " + reader + "; return _identity.Allowed(" + compiled + ") }()"
	}
	return compiled
}

// compileVisibleExpr compiles a @visible body expression to Go.
// Supports: my.role == "admin", my.id == someField
func compileVisibleExpr(expr ast.Expr) string {
	return compileVisibleExprWithEnums(expr, nil)
}

func compileVisibleExprWithEnums(expr ast.Expr, enums map[string]bool) string {
	return compileVisibleCondition(expr, enums, "buf.Identity")
}

func compileVisibleCondition(expr ast.Expr, enums map[string]bool, identityExpr string) string {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		operand := compileVisibleCondition(e.Value, enums, identityExpr)
		if operand != "" && (e.Op == "!" || e.Op == "-") {
			return compileUnaryOperation(e, "("+operand+")")
		}
	case *ast.BinaryExpr:
		left := compileVisibleCondition(e.Left, enums, identityExpr)
		right := compileVisibleCondition(e.Right, enums, identityExpr)
		if left == "" || right == "" {
			return ""
		}
		left = inferMyFieldType(left, e.Left, e.Right, identityExpr)
		right = inferMyFieldType(right, e.Right, e.Left, identityExpr)
		if expression, ok := compileFloatArithmetic(e, left, right); ok {
			return expression
		}
		return fmt.Sprintf("%s %s %s", groupBinaryOperand(e.Left, left), e.Op, groupBinaryOperand(e.Right, right))
	case *ast.MemberExpr:
		if e.EnumConstant {
			return fmt.Sprintf("%q", e.Field)
		}
		if ident, ok := e.Object.(*ast.Ident); ok && enums[ident.Name] {
			return "string(" + ident.Name + e.Field + ")"
		}
		if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == "my" {
			if identityExpr == "_identity" && (e.IsEnumType() || enums[e.GetTypeTag()]) {
				return compileIdentityEnumCondition(e, e.GetTypeTag())
			}
			return compileMyField(e, identityExpr)
		}
	case *ast.Literal:
		value, _ := compileConditionLiteral(e)
		return value
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
		fmt.Fprintf(b, "%sif %s != nil { %s; %s = &%sValue }\n",
			indent, goField, fieldValueBinding(transformedVar+"Value", code, f.Type), transformedVar, transformedVar)
		return transformedVar
	}
	fmt.Fprintf(b, "%s%s\n", indent, fieldValueBinding(transformedVar, code, f.Type))
	return transformedVar
}

// Numeric transforms retain the schema type even for an untyped Go constant.
func fieldValueBinding(name, value string, t *ast.TypeRef) string {
	if t != nil && !t.IsList && (t.Name == "Int" || t.Name == "Float") {
		return "var " + name + " " + mapBaseType(t.Name) + " = " + value
	}
	return name + " := " + value
}

func compileTransformDirectiveExpr(f *ast.FieldDecl, valueExpr string) string {
	transformDir := findDirective(f.Directives, "transform")
	if transformDir == nil {
		return valueExpr
	}
	if transformDir.Body == nil || len(transformDir.Body.Stmts) != 1 || len(transformDir.Args) != 0 {
		panic(&InternalCompilerError{Detail: "invalid @transform expression for field " + f.Name})
	}
	es, ok := transformDir.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		panic(&InternalCompilerError{Detail: "invalid @transform expression for field " + f.Name})
	}
	code := compileFieldExpr(es.Expr, valueExpr)
	if code == "" {
		panic(&InternalCompilerError{Detail: "unsupported @transform expression for field " + f.Name})
	}
	return code
}

// Removed: inferVisibleIdentityType — now uses shared inferMyFieldType from identity.go
