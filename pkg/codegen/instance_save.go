package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

type instanceSavePlan struct {
	model    *ast.ModelDecl
	fields   []*ast.FieldDecl
	setters  []*ast.FieldDecl
	snapshot []*ast.FieldDecl
	index    map[string]int
	words    int
	prefix   string
}

// Only models with an actual instance-save call pay for persistence snapshots.
func (g *GeneratorContext) prepareInstanceSaves(result *semantic.Result, enums map[string]bool) error {
	g.instanceSaves = nil
	var failure error
	visit := func(body *ast.Block) bool {
		ast.WalkExprs(body, func(expr ast.Expr) {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return
			}
			member, ok := call.Func.(*ast.MemberExpr)
			if !ok || member.Field != "save" {
				return
			}
			model := g.modelDeclarations[member.Object.GetTypeTag()]
			if model == nil || member.Object.IsListType() {
				return
			}
			if primaryKeyField(model) == nil {
				failure = fmt.Errorf("%s: save model %s requires a declared primary key", call.Pos, model.Name)
				return
			}
			if g.instanceSaves == nil {
				g.instanceSaves = make(map[string]*instanceSavePlan)
			}
			if g.instanceSaves[model.Name] == nil {
				g.instanceSaves[model.Name] = newInstanceSavePlan(model, enums)
			}
		})
		return false
	}
	for _, file := range result.Files {
		visitLoadCallBodies(file, visit)
		for _, middleware := range file.Middlewares {
			visit(middleware.Body)
		}
	}
	return failure
}

func newInstanceSavePlan(model *ast.ModelDecl, enums map[string]bool) *instanceSavePlan {
	plan := &instanceSavePlan{model: model, index: make(map[string]int), prefix: instanceSavePrefix(model)}
	fields := model.Fields
	if isSoftDelete(model) && !hasDeletedAtField(fields) {
		fields = append(append([]*ast.FieldDecl(nil), fields...), softDeleteField())
	}
	for _, field := range fields {
		if field.Computed == nil && !isRelationField(field, enums) {
			plan.index[field.Name] = len(plan.fields)
			plan.fields = append(plan.fields, field)
		}
	}
	plan.words = (len(plan.fields) + 63) / 64
	plan.setters = mutationSetterFields(plan.fields, primaryKeyField(model), true, model.Directives)
	plan.snapshot = append([]*ast.FieldDecl{primaryKeyField(model)}, plan.setters...)
	return plan
}

func (plan *instanceSavePlan) bitSet(value, kind string, index int) string {
	return fmt.Sprintf("%s.%s%s[%d] |= uint64(1) << %d", value, plan.prefix, kind, index/64, index%64)
}

func (plan *instanceSavePlan) bitTest(value, kind string, index int) string {
	return fmt.Sprintf("%s.%s%s[%d] & (uint64(1) << %d) != 0", value, plan.prefix, kind, index/64, index%64)
}

func (g *GeneratorContext) generateInstanceSaveState(b *strings.Builder, name string) {
	if plan := g.instanceSaves[name]; plan != nil {
		plan.write(b, "\t_luxoSaveOriginal struct {\n")
		for _, field := range plan.snapshot {
			typ := resolveGoType(field.Type)
			if field.Type.Nullable && !field.Type.IsList {
				inner := *field.Type
				inner.Nullable = false
				typ = "struct { value " + resolveGoType(&inner) + "; present bool }"
			}
			fmt.Fprintf(b, "\t\t%s %s\n", str.Capitalize(field.Name), typ)
		}
		plan.write(b, "\t}\n\t_luxoSaveLoaded [%d]uint64\n\t_luxoSaveExplicit [%d]uint64\n\t_luxoSaveConstructed bool\n\t_luxoSaveHasSnapshot bool\n", plan.words, plan.words)
	}
}

func (g *GeneratorContext) generateInstanceSaveFile(result *semantic.Result, packageName string) []byte {
	if len(g.instanceSaves) == 0 {
		return nil
	}
	var body strings.Builder
	for _, file := range result.Files {
		for _, model := range file.Models {
			if plan := g.instanceSaves[model.Name]; plan != nil {
				generateInstanceSaveCapture(&body, plan)
				g.generateInstanceSaveWriter(&body, plan)
			}
		}
	}
	var b strings.Builder
	writeHeader(&b, packageName, "save.gen.go")
	b.WriteString("import (\"context\";\"fmt\"\n")
	packages := generatedPackages(body.String())
	writeOptionalStandardImports(&b, packages, "time")
	if packages.has("json") {
		b.WriteString("\"encoding/json\"\n")
	}
	if packages.has("uuid") {
		b.WriteString("\"github.com/google/uuid\"\n")
	}
	if packages.has("decimal") {
		b.WriteString("\"github.com/shopspring/decimal\"\n")
	}
	b.WriteString(")\n\n")
	b.WriteString(body.String())
	return []byte(b.String())
}

func generateInstanceSaveCapture(b *strings.Builder, plan *instanceSavePlan) {
	name := plan.model.Name
	plan.write(b, "func (value *%s) _luxoSaveCapture() {\n original := &value._luxoSaveOriginal\n", name)
	plan.write(b, "value._luxoSaveExplicit = [%d]uint64{}\n", plan.words)
	for _, field := range plan.snapshot {
		target, source := "original."+str.Capitalize(field.Name), "value."+str.Capitalize(field.Name)
		if field.Type.Nullable && !field.Type.IsList {
			inner := *field.Type
			inner.Nullable = false
			fmt.Fprintf(b, "%s.present = %s != nil\nif %s != nil {\n%s.value = *%s\n", target, source, source, target, source)
			writeSaveClone(b, target+".value", target+".value", &inner, "")
			fmt.Fprintf(b, "} else { %s.value = *new(%s) }\n", target, resolveGoType(&inner))
			continue
		}
		fmt.Fprintf(b, "%s = %s\n", target, source)
		writeSaveClone(b, target, source, field.Type, "")
	}
	plan.write(b, "value._luxoSaveHasSnapshot = true\n}\n\n")
	for _, field := range plan.setters {
		generateInstanceSaveChanged(b, plan, field)
	}
}

// Clone mutable containers and nullable storage without reflection or wire conversion.
func writeSaveClone(b *strings.Builder, target, value string, typ *ast.TypeRef, indent string) {
	if typ.Nullable && !typ.IsList {
		fmt.Fprintf(b, "%sif %s != nil { cloned := *%s; %s = &cloned\n", indent, value, value, target)
		copy := *typ
		copy.Nullable = false
		writeSaveClone(b, "cloned", "cloned", &copy, indent+"\t")
		fmt.Fprintf(b, "%s}\n", indent)
		return
	}
	if typ.IsList || typ.Name == "Bytes" || typ.Name == "JSON" {
		fmt.Fprintf(b, "%s%s = append(%s[:0:0], %s...)\n", indent, target, value, value)
		if typ.IsList && (typ.Name == "Bytes" || typ.Name == "JSON") {
			fmt.Fprintf(b, "%sfor i := range %s { %s[i] = append(%s[i][:0:0], %s[i]...) }\n", indent, target, target, value, value)
		}
	}
}

func generateInstanceSaveChanged(b *strings.Builder, plan *instanceSavePlan, field *ast.FieldDecl) {
	name := str.Capitalize(field.Name)
	plan.write(b, "func (value *%s) _luxoSaveChanged%s() bool {\n", plan.model.Name, name)
	plan.write(b, "if !value._luxoSaveHasSnapshot && !value._luxoSaveConstructed { return true }\n")
	fmt.Fprintf(b, "if %s { return true }\n", plan.bitTest("value", "Explicit", plan.index[field.Name]))
	plan.write(b, "if !value._luxoSaveHasSnapshot { return false }\n")
	plan.write(b, "original := &value._luxoSaveOriginal\n")
	if field.Type.Nullable && !field.Type.IsList {
		fmt.Fprintf(b, "if (value.%s != nil) != original.%s.present { return true }; if value.%s != nil {\n", name, name, name)
		inner := *field.Type
		inner.Nullable = false
		writeSaveCompare(b, "(*value."+name+")", "original."+name+".value", &inner, "")
		b.WriteString("}\n")
	} else {
		writeSaveCompare(b, "value."+name, "original."+name, field.Type, "")
	}
	b.WriteString("return false\n}\n\n")
}

// A changed predicate preserves SQL-null/empty-container distinctions.
func writeSaveCompare(b *strings.Builder, value, original string, typ *ast.TypeRef, indent string) {
	if typ.IsList {
		fmt.Fprintf(b, "%sif (%s == nil) != (%s == nil) || len(%s) != len(%s) { return true }; for i := range %s {\n", indent, value, original, value, original, value)
		copy := *typ
		copy.IsList, copy.Nullable = false, false
		writeSaveCompare(b, value+"[i]", original+"[i]", &copy, indent+"\t")
		fmt.Fprintf(b, "%s}\n", indent)
		return
	}
	condition := value + " != " + original
	switch typ.Name {
	case "DateTime", "Decimal":
		condition = "!" + value + ".Equal(" + original + ")"
	case "Bytes", "JSON":
		condition = fmt.Sprintf("(%s == nil) != (%s == nil) || string(%s) != string(%s)", value, original, value, original)
	}
	fmt.Fprintf(b, "%sif %s { return true }\n", indent, condition)
}

func (g *GeneratorContext) generateInstanceSaveWriter(b *strings.Builder, plan *instanceSavePlan) {
	model, primary := plan.model, primaryKeyField(plan.model)
	name, key := model.Name, str.Capitalize(primary.Name)
	fmt.Fprintf(b, "func (client *%sClient) save(ctx context.Context, value *%s) (*%s,error) {\n", name, name, name)
	plan.write(b, "if value == nil { return nil,fmt.Errorf(%q) }; if (value._luxoSaveHasSnapshot || value._luxoSaveConstructed) && !(%s) { return nil,fmt.Errorf(%q) }\n", "save "+name+": nil model", plan.bitTest("value", "Loaded", plan.index[primary.Name]), "save "+name+": primary key was not loaded or explicitly constructed")
	plan.write(b, "if value._luxoSaveHasSnapshot && value.%s != value._luxoSaveOriginal.%s { return nil,fmt.Errorf(%q) }\n", key, key, "save "+name+": primary key cannot change")
	fmt.Fprintf(b, "builder := new%sUpdateBuilder(client.db,%q,value.%s)\n", name, g.sqlTable(name), key)
	for _, field := range plan.setters {
		fieldName := str.Capitalize(field.Name)
		plan.write(b, "if value._luxoSaveChanged%s() {\n", fieldName)
		argument := "value." + fieldName
		if hasDirective(field.Directives, "beforeSave") {
			b.WriteString("input := " + argument + "\n")
			writeSaveClone(b, "input", "input", field.Type, "")
			argument = "input"
		}
		fmt.Fprintf(b, "builder.Set%s(%s) }\n", fieldName, argument)
	}
	b.WriteString("if len(builder.Sets) == 0 {\n")
	if hasFallibleSave(plan.setters) {
		b.WriteString("if builder._prepareErr != nil { return nil,builder._prepareErr }\n")
	}
	b.WriteString("return value,nil }\n updated,err := builder.Exec(ctx)\n if err != nil { return nil,err }; if updated == nil { return nil,fmt.Errorf(\"save: record no longer exists\") }\n")
	for _, field := range plan.setters {
		fieldName := str.Capitalize(field.Name)
		plan.write(b, "if builder._setSlot%s != 0 { value.%s = updated.%s; value._luxoSaveOriginal.%s = updated._luxoSaveOriginal.%s; %s }\n", fieldName, fieldName, fieldName, fieldName, fieldName, plan.bitSet("value", "Loaded", plan.index[field.Name]))
	}
	if field := automaticUpdateField(plan.fields, model.Directives); field != nil {
		fieldName := str.Capitalize(field.Name)
		fmt.Fprintf(b, "value.%s = updated.%s; %s\n", fieldName, fieldName, plan.bitSet("value", "Loaded", plan.index[field.Name]))
	}
	fmt.Fprintf(b, "%s\n", plan.bitSet("value", "Loaded", plan.index[primary.Name]))
	plan.write(b, "value._luxoSaveOriginal.%s = value.%s; value._luxoSaveHasSnapshot = true; value._luxoSaveExplicit = [%d]uint64{}; return value,nil\n}\n\n", key, key, plan.words)
}

func (c *compiler) compileSaveAssignmentTarget(expr ast.Expr) (string, string) {
	member, ok := expr.(*ast.MemberExpr)
	if !ok || c.generator == nil {
		return c.compileExpr(expr), ""
	}
	plan := c.generator.instanceSaves[member.Object.GetTypeTag()]
	if plan == nil {
		return c.compileExpr(expr), ""
	}
	index, stored := plan.index[member.Field]
	if !stored {
		return c.compileExpr(expr), ""
	}
	value := c.compileMaterializedValue(member.Object, &ast.TypeRef{Name: plan.model.Name})
	value = c.captureExpressionValue(member.Object, value, "*"+plan.model.Name)
	return value + "." + str.Capitalize(member.Field), plan.bitSet(value, "Explicit", index)
}

// Semantic checking rejects bare model/query save receivers before instance-state lowering.
// Retain the query-root guard when inconsistent receiver metadata bypasses that boundary.
func (c *compiler) instanceSaveCall(call *ast.CallExpr) *instanceSavePlan {
	member, ok := call.Func.(*ast.MemberExpr)
	if !ok || member.SafeCall || member.Field != "save" || member.Object.IsListType() || c.generator == nil {
		return nil
	}
	if ident, ok := member.Object.(*ast.Ident); ok && c.queryRootModel(ident.Name) != "" {
		return nil
	}
	return c.generator.instanceSaves[member.Object.GetTypeTag()]
}

func (c *compiler) compileSaveObjectState(name string, explicit []*ast.NamedArg, fields []string) []string {
	if c.generator == nil {
		return fields
	}
	plan := c.generator.instanceSaves[name]
	if plan == nil {
		return fields
	}
	words := make([]uint64, plan.words)
	for _, field := range explicit {
		if index, ok := plan.index[field.Name]; ok {
			words[index/64] |= uint64(1) << (index % 64)
		}
	}
	var mask strings.Builder
	fmt.Fprintf(&mask, "[%d]uint64{", plan.words)
	for _, word := range words {
		fmt.Fprintf(&mask, "%d,", word)
	}
	mask.WriteByte('}')
	return append(fields, plan.prefix+"Loaded: "+mask.String(), plan.prefix+"Explicit: "+mask.String(), plan.prefix+"Constructed: true")
}

// Reserve an independent private namespace without rejecting accepted DSL fields.
func instanceSavePrefix(model *ast.ModelDecl) string {
	prefix := "_luxoSave"
	for {
		conflict := false
		for _, field := range model.Fields {
			if strings.HasPrefix(str.Capitalize(field.Name), prefix) {
				conflict = true
				break
			}
		}
		if !conflict {
			return prefix
		}
		prefix += "_"
	}
}

func (plan *instanceSavePlan) write(b *strings.Builder, format string, values ...any) {
	fmt.Fprintf(b, strings.ReplaceAll(format, "_luxoSave", plan.prefix), values...)
}
