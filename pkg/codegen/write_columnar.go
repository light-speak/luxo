package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

type directColumn struct {
	field       *ast.FieldDecl
	id          int
	relation    bool
	targetModel bool
	pointerList bool
	stringValue bool
}

func (g *GeneratorContext) directColumns(model *ast.ModelDecl, enums map[string]bool, modelProjection bool, modelSets []map[string]bool) []directColumn {
	var fields []directColumn
	for _, f := range model.Fields {
		if f.Type == nil || hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}
		id := g.modelFieldID(model.Name, f.Name)
		if id == 0 {
			continue
		}
		relation := isRelationField(f, enums)
		targetModel := modelProjection
		if len(modelSets) > 0 {
			targetModel = modelSets[0][f.Type.Name]
		}
		fields = append(fields, directColumn{
			field: f, id: id, relation: relation, targetModel: targetModel, pointerList: g.isModelReference(f.Type.Name),
			stringValue: !f.Type.IsList && !relation && (f.Type.Name == "String" || f.Type.Name == "Decimal" || enums[f.Type.Name]),
		})
	}
	return fields
}

func (g *GeneratorContext) generateDirectColumnar(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool, functionName, itemsType string, modelProjection bool, modelSets []map[string]bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).generateDirectColumnar(b, model, enums, functionName, itemsType, modelProjection, modelSets)
}

func (g *writeJSONContext) generateDirectColumnar(b *strings.Builder, model *ast.ModelDecl, enums map[string]bool, functionName, itemsType string, modelProjection bool, modelSets []map[string]bool) {
	fields := g.directColumns(model, enums, modelProjection, modelSets)
	if len(fields) == 0 {
		if len(model.Fields) == 0 {
			g.generateEmptyValueColumnar(b, functionName, itemsType)
		}
		return
	}
	fmt.Fprintf(b, "// %s appends columnar values directly to the response buffer.\n", functionName)
	fmt.Fprintf(b, "func %s(buf *api.ResponseBuf, items %s, mask []byte) error {\n", functionName, itemsType)
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "\treturn %s(buf, items, mask, 1)\n}\n\n", g.depthColumnarFunction(functionName))
		fmt.Fprintf(b, "func %s(buf *api.ResponseBuf, items %s, mask []byte, depth int) error {\n", g.depthColumnarFunction(functionName), itemsType)
	}
	b.WriteString("\tif len(items) == 0 { buf.B = append(buf.B, 0, 0, 0); return nil }\n")
	if !g.singleObjectDepth {
		writeModelDepthGuard(b, "return codec.ErrModelDepth")
	}
	if strings.HasPrefix(itemsType, "[]*") {
		b.WriteString("\tfor _, item := range items { if item == nil { return codec.ErrNullModelElement } }\n")
	}
	if g.hasWriteRelation(model, enums) {
		b.WriteString("\tselectionMask := mask\n")
	}
	b.WriteString("\tmask = codec.SelectionMaskFields(mask)\n\tbuf.B = codec.AppendVarint(buf.B, uint64(len(items)))\n")
	hasStrings := false
	for _, field := range fields {
		hasStrings = hasStrings || field.stringValue
	}
	if hasStrings {
		b.WriteString("\t_arenaMark := buf.BeginArenaHeader()\n\tvar _arenaSize int\n")
	} else {
		b.WriteString("\tbuf.B = append(buf.B, 0)\n")
	}
	b.WriteString("\t_columnStart := len(buf.B)\n")
	for _, field := range fields {
		g.writeDirectColumn(b, field, enums, modelProjection)
	}
	b.WriteString("\tif len(buf.B) == _columnStart { return codec.ErrEmptyColumnarProjection }\n")
	b.WriteString("\tbuf.B = append(buf.B, 0)\n")
	if hasStrings {
		b.WriteString("\tbuf.EndArenaHeader(_arenaMark, _arenaSize)\n")
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func (g *GeneratorContext) generateEmptyValueColumnar(b *strings.Builder, functionName, itemsType string) {
	fmt.Fprintf(b, "func %s(buf *api.ResponseBuf, items %s, mask []byte) error {\n", functionName, itemsType)
	if !g.singleObjectDepth {
		fmt.Fprintf(b, "return %s(buf, items, mask, 1)\n}\n\n", g.depthColumnarFunction(functionName))
		fmt.Fprintf(b, "func %s(buf *api.ResponseBuf, items %s, mask []byte, depth int) error {\n", g.depthColumnarFunction(functionName), itemsType)
	}
	b.WriteString("if len(items) != 0 { return codec.ErrEmptyColumnarProjection }\nbuf.B = append(buf.B, 0, 0, 0)\nreturn nil\n}\n\n")
}

func writeDirectColumn(b *strings.Builder, column directColumn, enums map[string]bool, modelProjection bool) {
	defaultGenerator().writeDirectColumn(b, column, enums, modelProjection)
}

func (g *GeneratorContext) writeDirectColumn(b *strings.Builder, column directColumn, enums map[string]bool, modelProjection bool) {
	(&writeJSONContext{GeneratorContext: *g, enums: enums}).writeDirectColumn(b, column, enums, modelProjection)
}

func (g *writeJSONContext) writeDirectColumn(b *strings.Builder, column directColumn, enums map[string]bool, modelProjection bool) {
	if column.relation && modelProjection {
		fmt.Fprintf(b, "\tif len(selectionMask) > 0 && codec.FieldMaskHas(mask, %d) {\n", column.id)
	} else {
		fmt.Fprintf(b, "\tif len(mask) == 0 || codec.FieldMaskHas(mask, %d) {\n", column.id)
	}
	visible := g.writeVisibleConditionAt(b, column.field, enums, "\t\t")
	if visible != "" {
		fmt.Fprintf(b, "\t\tif %s {\n", visible)
	}
	fmt.Fprintf(b, "\t\tbuf.B = codec.AppendVarint(buf.B, %d)\n", column.id)
	childMask := ""
	if column.relation {
		childMask = fmt.Sprintf("_childMask%d", column.id)
		fmt.Fprintf(b, "\t\t%s, _ := codec.SelectionMaskNested(selectionMask, %d)\n", childMask, column.id)
	}
	var body strings.Builder
	g.writeDirectColumnValue(&body, column, enums, childMask)
	if strings.Contains(body.String(), "items[_row]") {
		b.WriteString("\t\tfor _row := range items {\n")
	} else {
		b.WriteString("\t\tfor range items {\n")
	}
	b.WriteString(body.String())
	b.WriteString("\t\t}\n")
	if visible != "" {
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
}

func (g *GeneratorContext) writeDirectColumnValue(b *strings.Builder, column directColumn, enums map[string]bool, childMask string) {
	f := column.field
	source := "items[_row]." + str.Capitalize(f.Name)
	if f.Type.IsList && !column.relation {
		g.writeDirectArrayColumn(b, f, source, enums)
		return
	}
	if f.Type.Nullable {
		fmt.Fprintf(b, "\t\t\tif %s == nil { buf.B = codec.AppendNull(buf.B); continue }\n", source)
		if f.Type.Name != "JSON" {
			b.WriteString("\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
		}
	}
	if column.relation {
		b.WriteString("\t\t\t_frame := buf.BeginMessage()\n")
		if f.Type.IsList {
			writeBinaryCall(b, "\t\t\t", fmt.Sprintf("%s(buf, %s, %s, depth+1)", g.depthColumnarFunction(nestedColumnarWriter(f.Type.Name, column.targetModel, column.pointerList)), source, childMask), "return err")
		} else {
			writeBinaryCall(b, "\t\t\t", fmt.Sprintf("%s.%s(buf, %s, depth+1)", source, g.valueDepthMethod(f.Type.Name, "writeLuxoDepth"), childMask), "return err")
		}
		b.WriteString("\t\t\tbuf.EndMessage(_frame)\n")
		return
	}
	if f.Type.Nullable {
		source = "*" + source
	}
	value := source
	if helper := g.fieldTransforms[f]; helper != "" {
		writeFieldTransformCall(b, helper, "_transformed", source, "\t\t\t")
		value = "_transformed"
	} else {
		value = compileTransformDirectiveExpr(f, source)
	}
	value = compileMaskDirectiveExpr(f, value, f.Type.Name)
	writeDirectScalarColumn(b, column, value, enums[f.Type.Name])
}

func writeDirectScalarColumn(b *strings.Builder, column directColumn, value string, isEnum bool) {
	if column.stringValue {
		expression := "codec.AppendString(buf.B, v)"
		switch {
		case isEnum:
			value = column.field.Type.Name + "(" + value + ")"
			expression = enumAppendExpression(column.field.Type.Name, "buf.B", "v")
		case column.field.Type.Name == "Decimal":
			value = "lux.FormatDecimal(" + value + ")"
		}
		fmt.Fprintf(b, "\t\t\tv := %s\n", value)
		writeBinaryAppend(b, "\t\t\t", "buf.B", expression, "return err")
		b.WriteString("\t\t\t_arenaSize += len(v)\n")
		return
	}
	declaration := fieldValueBinding("v", value, column.field.Type)
	fmt.Fprintf(b, "\t\t\t%s\n", declaration)
	if column.field.Type.Name == "JSON" && column.field.Type.Nullable {
		b.WriteString("\t\t\tif codec.IsJSONNull(v) { buf.B = codec.AppendNull(buf.B); continue }\n\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
	}
	writeBinaryAppend(b, "\t\t\t", "buf.B", scalarArrayItemEncoding(column.field.Type.Name, isEnum, "buf.B"), "return err")
}

func writeDirectArrayColumn(b *strings.Builder, f *ast.FieldDecl, source string, enums map[string]bool) {
	defaultGenerator().writeDirectArrayColumn(b, f, source, enums)
}

func (g *GeneratorContext) writeDirectArrayColumn(b *strings.Builder, f *ast.FieldDecl, source string, enums map[string]bool) {
	value := source
	if helper := g.fieldTransforms[f]; helper != "" {
		if f.Type.Nullable {
			fmt.Fprintf(b, "\t\t\tif %s == nil { buf.B = codec.AppendNull(buf.B); continue }\n", source)
		}
		writeFieldTransformCall(b, helper, "_transformed", source, "\t\t\t")
		value = "_transformed"
	} else {
		value = compileTransformDirectiveExpr(f, source)
	}
	fmt.Fprintf(b, "\t\t\tvalues := %s\n", value)
	if f.Type.Nullable {
		b.WriteString("\t\t\tif values == nil { buf.B = codec.AppendNull(buf.B); continue }\n\t\t\tbuf.B = codec.AppendPresent(buf.B)\n")
	}
	b.WriteString("\t\t\t_frame := buf.BeginMessage()\n")
	writeScalarArrayItems(b, f.Type.Name, enums[f.Type.Name], "values", "\t\t\t")
	b.WriteString("\t\t\tbuf.EndMessage(_frame)\n")
}
