package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

func writeNestedComputedResolve(b *strings.Builder, relation Relation, resultExpr, indent string, computedModels map[string]bool) {
	if !computedModels[relation.TargetName] {
		return
	}
	if !relation.IsList {
		fmt.Fprintf(b, "%sif err := resolve%sComputedFields(ctx, app, []*%s{%s}, f.Children); err != nil { return err }\n",
			indent, relation.TargetName, relation.TargetName, resultExpr)
		return
	}
	fmt.Fprintf(b, "%sif err := resolve%sComputedFields(ctx, app, %s, f.Children); err != nil { return err }\n",
		indent, relation.TargetName, resultExpr)
}

// generateRelationResolver generates a resolve<Model>Relations function
// that loads relation fields via DataLoader based on $select.
func generateRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, computedSets ...map[string]bool) {
	name := m.Name
	computedModels := firstBoolSet(computedSets)
	fmt.Fprintf(b, "// resolve%sRelations loads selected relations using all known keys.\n", name)
	b.WriteString("// depth limits recursive resolution to prevent infinite loops.\n")
	fmt.Fprintf(b, "func resolve%sRelations(ctx context.Context, app *App, value *%s, fields []*selection.Field, depth ...int) error {\n", name, name)
	b.WriteString("\tif value == nil { return nil }\n\td := MaxRelationDepth\n\tif len(depth) > 0 { d = depth[0] }\n")
	for _, relation := range rels {
		b.WriteString("\tfor _, f := range fields {\n")
		fmt.Fprintf(b, "\t\tif f.Name == %q && f.Children != nil {\n", relation.FieldName)
		writeSingleRelationResult(b, name, relation, modelRels, computedModels)
		b.WriteString("\t\t\tbreak\n\t\t}\n\t}\n")
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func writeSingleRelationResult(b *strings.Builder, name string, relation Relation, modelRels, computedModels map[string]bool) {
	indent := "\t\t\t"
	key := "value." + str.Capitalize(relation.LocalKey)
	field := str.Capitalize(relation.FieldName)
	if relation.FKNullable {
		fmt.Fprintf(b, "%sif %s != nil {\n", indent, key)
		indent += "\t"
		key = "*" + key
	}
	writeRelationDepthCheck(b, indent, false)
	fmt.Fprintf(b, "%skey := %s\n", indent, key)
	fmt.Fprintf(b, "%sresultMap, err := app.loaders.%s.LoadAll(ctx, []%s{key}, f.Children)\n", indent, loaderFieldName(name, relation), relation.KeyGoType)
	fmt.Fprintf(b, "%sif err != nil { return err }\n", indent)
	fmt.Fprintf(b, "%sresult := resultMap[key]\n%svalue.%s = result\n", indent, indent, field)
	writeNestedComputedResolve(b, relation, "result", indent, computedModels)
	writeNestedRelationResolve(b, relation, "result", indent, modelRels)
	if relation.FKNullable {
		b.WriteString("\t\t\t}\n")
	}
}

func writeRelationDepthCheck(b *strings.Builder, indent string, keys bool) {
	condition := "d <= 0"
	if keys {
		condition = "d <= 0 && len(keys) > 0"
	}
	fmt.Fprintf(b, "%sif %s { return errors.RelationDepthExceeded }\n", indent, condition)
}

func writeNestedRelationResolve(b *strings.Builder, relation Relation, resultExpr, indent string, modelRels map[string]bool) {
	if !modelRels[relation.TargetName] {
		return
	}
	fmt.Fprintf(b, "%sif has%sSelectedRelations(f.Children) {\n", indent, relation.TargetName)
	outerIndent := indent
	indent += "\t"
	b.WriteString(indent)
	b.WriteString("traceCtx := api.WithTraceFieldPath(ctx, f.Name)\n")
	writeTraceScope(b, indent)
	b.WriteString(indent)
	b.WriteString("if err := resolve")
	b.WriteString(relation.TargetName)
	if relation.IsList {
		b.WriteString("List")
	}
	b.WriteString("Relations(traceCtx, app, ")
	b.WriteString(resultExpr)
	b.WriteString(", f.Children, d-1); err != nil {\n")
	b.WriteString(indent)
	b.WriteString("\treturn err\n")
	b.WriteString(indent)
	b.WriteString("}\n")
	b.WriteString(outerIndent)
	b.WriteString("}\n")
}

// generateListRelationResolver generates a batch resolve function for LIST handlers.
// Uses LoadAll (direct dispatch, zero wait) instead of per-item Load.
func generateListRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, computedSets ...map[string]bool) {
	generateListRelationResolverMode(b, m, rels, modelRels, false, computedSets...)
}

func generateValueListRelationResolver(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, computedSets ...map[string]bool) {
	generateListRelationResolverMode(b, m, rels, modelRels, true, computedSets...)
}

func generateListRelationResolverMode(b *strings.Builder, m *ast.ModelDecl, rels []Relation, modelRels map[string]bool, valueSlice bool, computedSets ...map[string]bool) {
	name := m.Name
	functionName, itemsType := "resolve"+name+"ListRelations", "[]*"+name
	if valueSlice {
		functionName, itemsType = "resolve"+name+"ValueListRelations", "[]"+name
	}
	fmt.Fprintf(b, "// %s batch-loads all relation fields for a list of %s.\n", functionName, name)
	b.WriteString("// Uses LoadAll — direct dispatch, zero wait.\n")
	fmt.Fprintf(b, "func %s(ctx context.Context, app *App, items %s, fields []*selection.Field, depth ...int) error {\n", functionName, itemsType)
	b.WriteString("\td := MaxRelationDepth\n\tif len(depth) > 0 { d = depth[0] }\n")
	computedModels := firstBoolSet(computedSets)
	for _, relation := range rels {
		writeListRelation(b, name, relation, valueSlice, modelRels, computedModels)
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func writeListRelation(b *strings.Builder, name string, relation Relation, valueSlice bool, modelRels, computedModels map[string]bool) {
	b.WriteString("\tfor _, f := range fields {\n")
	fmt.Fprintf(b, "\t\tif f.Name == %q && f.Children != nil {\n", relation.FieldName)
	writeListRelationKeys(b, relation, valueSlice)
	writeRelationDepthCheck(b, "\t\t\t", true)
	fmt.Fprintf(b, "\t\t\tresultMap, err := app.loaders.%s.LoadAll(ctx, keys, f.Children)\n", loaderFieldName(name, relation))
	b.WriteString("\t\t\tif err != nil { return err }\n")
	writeListRelationAssignments(b, relation, valueSlice)
	writeNestedListResolvers(b, relation, valueSlice, modelRels, computedModels)
	b.WriteString("\t\t\tbreak\n\t\t}\n\t}\n")
}

func writeListRelationKeys(b *strings.Builder, relation Relation, valueSlice bool) {
	fmt.Fprintf(b, "\t\t\tkeys := make([]%s, 0, len(items))\n", relation.KeyGoType)
	writeRelationItemLoopStart(b, valueSlice)
	if !valueSlice {
		b.WriteString("\t\t\t\tif item == nil { return codec.ErrNullModelElement }\n")
	}
	key := "item." + str.Capitalize(relation.LocalKey)
	if relation.FKNullable {
		fmt.Fprintf(b, "\t\t\t\tif %s != nil { keys = append(keys, *%s) }\n", key, key)
	} else {
		fmt.Fprintf(b, "\t\t\t\tkeys = append(keys, %s)\n", key)
	}
	b.WriteString("\t\t\t}\n")
}

func writeListRelationAssignments(b *strings.Builder, relation Relation, valueSlice bool) {
	writeRelationItemLoopStart(b, valueSlice)
	key, field := "item."+str.Capitalize(relation.LocalKey), str.Capitalize(relation.FieldName)
	if relation.FKNullable {
		fmt.Fprintf(b, "\t\t\t\tif %s != nil { item.%s = resultMap[*%s] }\n", key, field, key)
	} else {
		fmt.Fprintf(b, "\t\t\t\titem.%s = resultMap[%s]\n", field, key)
	}
	b.WriteString("\t\t\t}\n")
}

// Child references are gathered once per selected edge, shared by computed
// fields and recursive relations. Every following depth uses a single LoadAll.
func writeNestedListResolvers(b *strings.Builder, relation Relation, valueSlice bool, modelRels, computedModels map[string]bool) {
	computed, nested := computedModels[relation.TargetName], modelRels[relation.TargetName]
	if !computed && !nested {
		return
	}
	if !computed {
		fmt.Fprintf(b, "\t\t\tif has%sSelectedRelations(f.Children) {\n", relation.TargetName)
	}
	if !valueSlice {
		b.WriteString("\t\t\tif len(items) == 1 {\n")
		resultExpr := "items[0]." + str.Capitalize(relation.FieldName)
		writeNestedComputedResolve(b, relation, resultExpr, "\t\t\t\t", computedModels)
		writeNestedRelationResolve(b, relation, resultExpr, "\t\t\t\t", modelRels)
		b.WriteString("\t\t\t} else {\n")
	}
	writeNestedRelationItems(b, relation, valueSlice)
	b.WriteString("\t\t\tif len(nestedItems) > 0 {\n")
	if computed {
		fmt.Fprintf(b, "\t\t\t\tif err := resolve%sComputedFields(ctx, app, nestedItems, f.Children); err != nil { return err }\n", relation.TargetName)
	}
	if nested {
		b.WriteString("\t\t\t\ttraceCtx := api.WithTraceFieldPath(ctx, f.Name)\n")
		writeTraceScope(b, "\t\t\t\t")
		fmt.Fprintf(b, "\t\t\t\tif err := resolve%sListRelations(traceCtx, app, nestedItems, f.Children, d-1); err != nil { return err }\n", relation.TargetName)
	}
	b.WriteString("\t\t\t}\n")
	if !valueSlice {
		b.WriteString("\t\t\t}\n")
	}
	if !computed {
		b.WriteString("\t\t\t}\n")
	}
}

func writeNestedRelationItems(b *strings.Builder, relation Relation, valueSlice bool) {
	if valueSlice {
		fmt.Fprintf(b, "\t\t\tvar nestedBuf [8]*%s\n", relation.TargetName)
		b.WriteString("\t\t\tnestedItems := nestedBuf[:0]\n")
		fmt.Fprintf(b, "\t\t\tif len(items) > len(nestedBuf) { nestedItems = make([]*%s, 0, len(items)) }\n", relation.TargetName)
	} else {
		fmt.Fprintf(b, "\t\t\tvar nestedItems []*%s\n", relation.TargetName)
	}
	writeRelationItemLoopStart(b, valueSlice)
	field := "item." + str.Capitalize(relation.FieldName)
	condition, capacity := field+" != nil", "len(items)"
	if relation.IsList {
		condition, capacity = "len("+field+") > 0", "max(len(items), len("+field+"))"
	}
	fmt.Fprintf(b, "\t\t\t\tif %s {\n", condition)
	if !valueSlice {
		fmt.Fprintf(b, "\t\t\t\t\tif nestedItems == nil { nestedItems = make([]*%s, 0, %s) }\n", relation.TargetName, capacity)
	}
	if relation.IsList {
		field += "..."
	}
	fmt.Fprintf(b, "\t\t\t\t\tnestedItems = append(nestedItems, %s)\n", field)
	b.WriteString("\t\t\t\t}\n")
	b.WriteString("\t\t\t}\n")
}

func writeRelationItemLoopStart(b *strings.Builder, valueSlice bool) {
	if valueSlice {
		b.WriteString("\t\t\tfor i := range items {\n\t\t\t\titem := &items[i]\n")
		return
	}
	b.WriteString("\t\t\tfor _, item := range items {\n")
}
