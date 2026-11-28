package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

type mutationInputField struct {
	field    *ast.FieldDecl
	optional bool
}

func isCRUDInputField(field *ast.FieldDecl, model *ast.ModelDecl, enums map[string]bool, update bool) bool {
	return field.Type != nil && !skipHandlerField(field, enums, model.Directives) &&
		(!update || (field.Name != primaryKeyFieldName(model) && !hasDirective(field.Directives, "immutable")))
}

func mutationInputFields(model *ast.ModelDecl, enums map[string]bool, update bool) []mutationInputField {
	var fields []mutationInputField
	for _, field := range model.Fields {
		if !isCRUDInputField(field, model, enums, update) {
			continue
		}
		optional := update || field.Default != nil || (field.Type != nil && field.Type.Nullable)
		fields = append(fields, mutationInputField{field, optional})
	}
	return fields
}

// Keep values in separate locals: taking the address of a nullable scalar must
// not force every input value into one heap-allocated staging struct.
func generateMutationInputRead(b *strings.Builder, input mutationInputField, indent string, enums map[string]bool) {
	field := input.field
	typ := field.Type
	if typ != nil && typ.Nullable {
		base := *typ
		base.Nullable = false
		typ = &base
	}
	fmt.Fprintf(b, "%svar %sVal %s\n", indent, field.Name, resolveGoType(typ))
	condition := ""
	if input.optional {
		condition = field.Name + "Present"
		fmt.Fprintf(b, "%s%s := req.HasParam(%q)\n", indent, condition, field.Name)
	}
	if field.Type != nil && field.Type.Nullable {
		prefix := ""
		if input.optional {
			prefix = condition + " && "
		}
		fmt.Fprintf(b, "%s%sNull := %sreq.ParamIsNull(%q)\n", indent, field.Name, prefix, field.Name)
		condition = prefix + "!" + field.Name + "Null"
	}
	if condition == "" {
		fmt.Fprintf(b, "%s{\n", indent)
	} else {
		fmt.Fprintf(b, "%sif %s {\n", indent, condition)
	}
	writeMutationValueRead(b, field, typ, indent+"\t", enums)
	generateFieldValidation(b, field, field.Name+"Val", indent+"\t")
	fmt.Fprintf(b, "%s}\n", indent)
}

func writeMutationValueRead(b *strings.Builder, field *ast.FieldDecl, typ *ast.TypeRef, indent string, enums map[string]bool) {
	name := field.Name + "Val"
	if typ != nil && enums[typ.Name] {
		writeEnumParamValue(b, field.Name, name, typ, false, indent)
		return
	}
	fmt.Fprintf(b, "%s_value, err := req.Param%s(%q)\n", indent, paramMethod(resolveGoType(typ)), field.Name)
	fmt.Fprintf(b, "%sif err != nil { return fmt.Errorf(\"param %s: %%w\", err) }\n", indent, field.Name)
	fmt.Fprintf(b, "%s%s = _value\n", indent, name)
}

// Applying fields never reads the request again; presence and null decisions
// belong to the completed validation phase.
func generateMutationInputApply(b *strings.Builder, input mutationInputField, setter, indent string, modelNames ...string) {
	field, outer := input.field, indent
	if preparedMutationField(field) && strings.HasPrefix(setter, "Set") {
		setter = "setPrepared" + strings.TrimPrefix(setter, "Set")
	}
	if input.optional {
		fmt.Fprintf(b, "%sif %sPresent {\n", indent, field.Name)
		indent += "\t"
	}
	nullable := field.Type != nil && field.Type.Nullable
	if nullable {
		fmt.Fprintf(b, "%sif %sNull {\n%s\tbuilder.%s(nil)\n%s} else {\n", indent, field.Name, indent, setter, indent)
		indent += "\t"
	}
	name := field.Name + "Val"
	if fallibleSave(field) {
		if len(modelNames) == 0 {
			panic(&InternalCompilerError{Detail: "missing save model name"})
		}
		fmt.Fprintf(b, "%[1]sif _prepared, err := %[2]s(%[3]s); err != nil {\n%[1]s\treturn err\n%[1]s} else {\n%[1]s\t%[3]s = _prepared\n%[1]s}\n",
			indent, saveBodyName(modelNames[0], field), name)
	} else {
		generateBeforeSave(b, field, name, indent)
	}
	generateMutationHash(b, field, name, indent)
	arg := name
	if nullable && !field.Type.IsList {
		// Only a present, non-null value needs an escaping setter argument.
		fmt.Fprintf(b, "%s_value := %s\n", indent, name)
		arg = "&_value"
	}
	fmt.Fprintf(b, "%sbuilder.%s(%s)\n", indent, setter, arg)
	if nullable {
		indent = indent[:len(indent)-1]
		fmt.Fprintf(b, "%s}\n", indent)
	}
	if input.optional {
		fmt.Fprintf(b, "%s}\n", outer)
	}
}

func generateMutationHash(b *strings.Builder, field *ast.FieldDecl, value, indent string) {
	if !hasDirective(field.Directives, "hash") {
		return
	}
	fmt.Fprintf(b, "%s{\n%s\t_hashed, err := luxocrypto.HashPassword(%s)\n", indent, indent, value)
	fmt.Fprintf(b, "%s\tif err != nil { return fmt.Errorf(\"hash %s: %%w\", err) }\n", indent, field.Name)
	fmt.Fprintf(b, "%s\t%s = _hashed\n%s}\n", indent, value, indent)
}

// Save preparation owns nullable scalar storage before applying field transforms.
func generateSavePreparation(b *strings.Builder, model string, field *ast.FieldDecl) {
	if field.Type == nil || !hasDirective(field.Directives, "beforeSave") {
		return
	}
	if fallibleSave(field) {
		generateFallibleSavePreparation(b, model, field)
		return
	}
	typ := resolveGoType(field.Type)
	fmt.Fprintf(b, "func %s(value %s) %s {\n", savePreparationName(model, field.Name), typ, typ)
	value := "value"
	if field.Type.Nullable && !field.Type.IsList {
		b.WriteString("\tif value == nil { return nil }\n\tprepared := *value\n")
		value = "prepared"
	}
	generateBeforeSave(b, field, value, "\t")
	if value == "prepared" {
		value = "&prepared"
	}
	fmt.Fprintf(b, "\treturn %s\n}\n\n", value)
}

func savePreparationName(model, field string) string {
	return fmt.Sprintf("prepareSave%d%s%s", len(model), model, str.Capitalize(field))
}

// Compiled mutations apply conversion before hashing and preserve nullable storage.
func (c *compiler) compileMutationValue(model *ast.ModelDecl, argument *ast.NamedArg) (string, bool) {
	field := modelField(model, argument.Name)
	if field == nil || !hasDirective(field.Directives, "beforeSave") {
		if field != nil && field.Type != nil && (field.Type.IsList || field.Type.Nullable && hasDirective(field.Directives, "hash")) {
			return c.compileAssignedValue(argument.Value, field.Type), false
		}
		return c.compileExpr(argument.Value), false
	}
	value := c.compileAssignedValue(argument.Value, field.Type)
	c.resultTmp++
	name := fmt.Sprintf("_saveValue%d", c.resultTmp)
	if fallibleSave(field) {
		c.write("%s, err := %s(%s)", name, savePreparationName(model.Name, field.Name), value)
		c.writeErrorGuard()
	} else {
		c.write("%s := %s(%s)", name, savePreparationName(model.Name, field.Name), value)
	}
	return name, true
}

// Slot zero means absent; all stored indices are one-based.
func generateMutationSlots(b *strings.Builder, fields []*ast.FieldDecl) {
	for _, field := range fields {
		fmt.Fprintf(b, "\t_setSlot%s int\n", str.Capitalize(field.Name))
	}
}

func mutationSetterFields(fields []*ast.FieldDecl, primaryKey *ast.FieldDecl, update bool, modelDirectives ...[]*ast.Directive) []*ast.FieldDecl {
	var result []*ast.FieldDecl
	for _, field := range fields {
		if field.Computed != nil || hasDirective(field.Directives, "internal") || isAutoManaged(field, modelDirectives...) {
			continue
		}
		if update && ((primaryKey != nil && field.Name == primaryKey.Name) || hasDirective(field.Directives, "immutable")) {
			continue
		}
		result = append(result, field)
	}
	return result
}

func (g *GeneratorContext) generateMutationSetter(b *strings.Builder, model, kind string, field *ast.FieldDecl, fallible bool) {
	builder := model + kind + "Builder"
	name, typ := str.Capitalize(field.Name), resolveGoType(field.Type)
	fmt.Fprintf(b, "func (b *%s) Set%s(v %s) *%s {\n", builder, name, typ, builder)
	if fallible {
		b.WriteString("\tif b._prepareErr != nil { return b }\n")
	}
	if preparedMutationField(field) {
		writeSetterPreparation(b, model, field)
		fmt.Fprintf(b, "\treturn b.setPrepared%s(v)\n}\n\n", name)
		fmt.Fprintf(b, "func (b *%s) setPrepared%s(v %s) *%s {\n", builder, name, typ, builder)
	}
	if kind == "Create" {
		fmt.Fprintf(b, "\tpg.SetCreateSlot(&b.CreateBase, &b._setSlot%s, %q, v)\n", name, g.sqlColumn(field.Name))
	} else {
		fmt.Fprintf(b, "\tpg.SetUpdateSlot(&b.Sets, &b._setSlot%s, %q, v)\n", name, g.sqlColumn(field.Name))
	}
	b.WriteString("\treturn b\n}\n\n")
}

func generateCreateManyAdd(b *strings.Builder, model string, fields []*ast.FieldDecl) {
	fmt.Fprintf(b, "// Add prepares and stores one input without changing the caller's scalar values.\nfunc (b *%sCreateManyBuilder) Add(input %sCreateInput) *%sCreateManyBuilder {\n", model, model, model)
	if hasFallibleSave(fields) {
		b.WriteString("\tif b._prepareErr != nil { return b }\n")
	}
	for _, field := range fields {
		if fallibleSave(field) {
			name := str.Capitalize(field.Name)
			fmt.Fprintf(b, "\t{ prepared, err := %s(input.%s); if err != nil { b._prepareErr = err; return b }; input.%s = prepared }\n", savePreparationName(model, field.Name), name, name)
		} else if hasDirective(field.Directives, "beforeSave") {
			name := str.Capitalize(field.Name)
			fmt.Fprintf(b, "\tinput.%s = %s(input.%s)\n", name, savePreparationName(model, field.Name), name)
		}
		if hasDirective(field.Directives, "hash") {
			name := str.Capitalize(field.Name)
			fmt.Fprintf(b, "\t{ hashed, err := %s(input.%s); if err != nil { b._prepareErr = err; return b }; input.%s = hashed }\n", hashPreparationName(model, field), name, name)
		}
	}
	b.WriteString("\tb.rows = append(b.rows, input)\n\treturn b\n}\n\n")
}

// Unique temporaries keep repeated writes valid in the same generated Go scope.
func (c *compiler) compileMutationHash(model *ast.ModelDecl, field, value string) (string, bool) {
	if model == nil || !isHashField(model, field) {
		return value, false
	}
	c.resultTmp++
	name := fmt.Sprintf("hashed%s%d", str.Capitalize(field), c.resultTmp)
	declaration := modelField(model, field)
	if declaration != nil && declaration.Type != nil && declaration.Type.Nullable {
		c.write("%s, err := %s(%s)", name, hashPreparationName(model.Name, declaration), value)
	} else {
		c.write("%s, err := luxocrypto.HashPassword(%s)", name, value)
	}
	c.writeErrorGuard()
	return name, true
}

// Retain ordinary values before later arguments can modify their source storage.
func (c *compiler) compileMutationArgument(model *ast.ModelDecl, argument *ast.NamedArg, capture bool) (string, bool, bool) {
	value, prepared := c.compileMutationValue(model, argument)
	value, hashed := c.compileMutationHash(model, argument.Name, value)
	if capture && !prepared && !hashed {
		typ := c.goTypeForExpr(argument.Value)
		if field := modelField(model, argument.Name); field != nil && field.Type != nil {
			declared := *field.Type
			declared.Nullable = argument.Value.IsNullable()
			typ = resolveGoType(&declared)
		}
		value = c.captureExpressionValue(argument.Value, value, typ)
	}
	return value, prepared, hashed
}

func preparedMutationField(field *ast.FieldDecl) bool {
	return hasDirective(field.Directives, "beforeSave") || hasDirective(field.Directives, "hash")
}

func writeSetterPreparation(b *strings.Builder, model string, field *ast.FieldDecl) {
	writeMutationPreparation(b, model, field, "v", "\t", "b._prepareErr = err; return b")
}

// Reuse static preparation steps while preserving each caller's error boundary.
func writeMutationPreparation(b *strings.Builder, model string, field *ast.FieldDecl, value, indent, onError string) {
	if fallibleSave(field) {
		fmt.Fprintf(b, "%sprepared, err := %s(%s)\n%sif err != nil { %s }\n%s%s = prepared\n", indent, savePreparationName(model, field.Name), value, indent, onError, indent, value)
	} else if hasDirective(field.Directives, "beforeSave") {
		fmt.Fprintf(b, "%s%s = %s(%s)\n", indent, value, savePreparationName(model, field.Name), value)
	}
	if hasDirective(field.Directives, "hash") {
		fmt.Fprintf(b, "%shashed, err := %s(%s)\n%sif err != nil { %s }\n%s%s = hashed\n", indent, hashPreparationName(model, field), value, indent, onError, indent, value)
	}
}
