package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

// generateWriteJSONFile produces writejson.gen.go containing per-model
// WriteJSON methods and list wrapper types for single-pass JSON serialization.
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

	// Collect extend stubs that need WriteJSON (cross-module types)
	var stubs []*ast.ModelDecl
	for _, file := range result.Files {
		for _, ext := range file.Extends {
			if modelNames[ext.Name] {
				continue
			}
			stubs = append(stubs, &ast.ModelDecl{Name: ext.Name, Fields: ext.Fields})
		}
	}

	var b strings.Builder
	writeHeader(&b, packageName, "writejson.gen.go")
	writeWriteJSONImports(&b, models, stubs)

	for _, m := range models {
		generateWriteJSON(&b, m, enums)
		generateListJSONWrapper(&b, m)
	}

	// Extend stubs: WriteJSON with direct field append (same as full models)
	for _, s := range stubs {
		generateWriteJSON(&b, s, enums)
		generateListJSONWrapper(&b, s)
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
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/selection\"\n")
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

// generateWriteJSON generates a WriteJSON method for a single model.
// Uses ResponseBuf for zero-allocation direct append.
func generateWriteJSON(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	recv := strings.ToLower(name[:1])

	fmt.Fprintf(b, "// WriteJSON writes %s as filtered JSON. Single pass, zero marshal.\n", name)
	fmt.Fprintf(b, "func (%s *%s) WriteJSON(buf *api.ResponseBuf, fields []*selection.Field) {\n", recv, name)

	// nil-select fallback: write all visible fields directly (zero marshal)
	writeAllFieldsFallback(b, m, recv, enums)

	fmt.Fprintf(b, "\tbuf.AppendByte('{')\n")
	fmt.Fprintf(b, "\tfirst := true\n")
	fmt.Fprintf(b, "\tfor _, f := range fields {\n")
	fmt.Fprintf(b, "\t\tswitch f.Name {\n")

	for _, f := range m.Fields {
		if f.Type == nil || f.Computed != nil {
			continue
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			continue
		}

		jsonName := f.Name
		goField := str.Capitalize(f.Name)
		relation := isRelationField(f, enums)

		fmt.Fprintf(b, "\t\tcase %q:\n", jsonName)
		fmt.Fprintf(b, "\t\t\tif !first { buf.AppendByte(',') }\n")
		fmt.Fprintf(b, "\t\t\tbuf.AppendString(`%q:`)\n", jsonName)

		fieldExpr := recv + "." + goField

		if relation {
			writeRelationJSON(b, f, fieldExpr)
		} else if f.Type.Nullable {
			writeNullableFieldJSON(b, f, fieldExpr)
		} else if f.Type.IsList {
			writeListFieldJSON(b, f, fieldExpr)
		} else {
			writeScalarFieldJSON(b, f, fieldExpr)
		}

		fmt.Fprintf(b, "\t\t\tfirst = false\n")
	}

	fmt.Fprintf(b, "\t\t}\n") // end switch
	fmt.Fprintf(b, "\t}\n")   // end for
	fmt.Fprintf(b, "\tbuf.AppendByte('}')\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeScalarFieldJSON writes a non-nullable scalar field value using ResponseBuf.
func writeScalarFieldJSON(b *strings.Builder, f *ast.FieldDecl, expr string) {
	switch f.Type.Name {
	case "Int":
		fmt.Fprintf(b, "\t\t\tbuf.AppendInt(%s)\n", expr)
	case "Float":
		fmt.Fprintf(b, "\t\t\tbuf.AppendFloat(%s)\n", expr)
	case "String":
		fmt.Fprintf(b, "\t\t\tbuf.AppendJSONString(%s)\n", expr)
	case "Boolean":
		fmt.Fprintf(b, "\t\t\tbuf.AppendBool(%s)\n", expr)
	case "DateTime":
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
		fmt.Fprintf(b, "\t\t\tbuf.AppendString(%s.Format(time.RFC3339Nano))\n", expr)
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
	case "Duration":
		fmt.Fprintf(b, "\t\t\tbuf.AppendInt(int64(%s))\n", expr)
	case "UUID":
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
		fmt.Fprintf(b, "\t\t\tbuf.AppendString(%s.String())\n", expr)
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
	case "Decimal":
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
		fmt.Fprintf(b, "\t\t\tbuf.AppendString(%s.String())\n", expr)
		fmt.Fprintf(b, "\t\t\tbuf.AppendByte('\"')\n")
	default:
		// Enum or unknown — write as JSON string
		fmt.Fprintf(b, "\t\t\tbuf.AppendJSONString(string(%s))\n", expr)
	}
}

// writeNullableFieldJSON writes a nullable scalar field (*T).
func writeNullableFieldJSON(b *strings.Builder, f *ast.FieldDecl, expr string) {
	fmt.Fprintf(b, "\t\t\tif %s == nil {\n", expr)
	fmt.Fprintf(b, "\t\t\t\tbuf.AppendString(\"null\")\n")
	fmt.Fprintf(b, "\t\t\t} else {\n")
	deref := "*" + expr
	inner := &ast.FieldDecl{Name: f.Name, Type: &ast.TypeRef{Name: f.Type.Name}}
	writeScalarFieldJSON(b, inner, deref)
	fmt.Fprintf(b, "\t\t\t}\n")
}

// writeRelationJSON writes a relation field (single or list model reference).
func writeRelationJSON(b *strings.Builder, f *ast.FieldDecl, expr string) {
	if f.Type.IsList {
		fmt.Fprintf(b, "\t\t\tif %s == nil {\n", expr)
		fmt.Fprintf(b, "\t\t\t\tbuf.AppendString(\"null\")\n")
		fmt.Fprintf(b, "\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\tbuf.AppendByte('[')\n")
		fmt.Fprintf(b, "\t\t\t\tfor i, item := range %s {\n", expr)
		fmt.Fprintf(b, "\t\t\t\t\tif i > 0 { buf.AppendByte(',') }\n")
		fmt.Fprintf(b, "\t\t\t\t\titem.WriteJSON(buf, f.Children)\n")
		fmt.Fprintf(b, "\t\t\t\t}\n")
		fmt.Fprintf(b, "\t\t\t\tbuf.AppendByte(']')\n")
		fmt.Fprintf(b, "\t\t\t}\n")
	} else {
		fmt.Fprintf(b, "\t\t\tif %s == nil {\n", expr)
		fmt.Fprintf(b, "\t\t\t\tbuf.AppendString(\"null\")\n")
		fmt.Fprintf(b, "\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\t%s.WriteJSON(buf, f.Children)\n", expr)
		fmt.Fprintf(b, "\t\t\t}\n")
	}
}

// writeListFieldJSON writes a non-relation list field (e.g., [String], [Int]).
func writeListFieldJSON(b *strings.Builder, f *ast.FieldDecl, expr string) {
	fmt.Fprintf(b, "\t\t\tif %s == nil {\n", expr)
	fmt.Fprintf(b, "\t\t\t\tbuf.AppendString(\"null\")\n")
	fmt.Fprintf(b, "\t\t\t} else {\n")
	fmt.Fprintf(b, "\t\t\t\tbuf.AppendByte('[')\n")
	fmt.Fprintf(b, "\t\t\t\tfor i, v := range %s {\n", expr)
	fmt.Fprintf(b, "\t\t\t\t\tif i > 0 { buf.AppendByte(',') }\n")

	inner := &ast.FieldDecl{Name: f.Name, Type: &ast.TypeRef{Name: f.Type.Name}}
	writeScalarFieldJSON(b, inner, "v")

	fmt.Fprintf(b, "\t\t\t\t}\n")
	fmt.Fprintf(b, "\t\t\t\tbuf.AppendByte(']')\n")
	fmt.Fprintf(b, "\t\t\t}\n")
}

// generateListJSONWrapper generates a list type with WriteJSON for list handlers.
func generateListJSONWrapper(b *strings.Builder, m *ast.ModelDecl) {
	lower := str.LowerFirst(m.Name)
	fmt.Fprintf(b, "// %sListJSON wraps []*%s to implement api.JSONWriter for list responses.\n", lower, m.Name)
	fmt.Fprintf(b, "type %sListJSON []*%s\n\n", lower, m.Name)
	fmt.Fprintf(b, "func (l %sListJSON) WriteJSON(buf *api.ResponseBuf, fields []*selection.Field) {\n", lower)
	fmt.Fprintf(b, "\tbuf.AppendByte('[')\n")
	fmt.Fprintf(b, "\tfor i, item := range l {\n")
	fmt.Fprintf(b, "\t\tif i > 0 { buf.AppendByte(',') }\n")
	fmt.Fprintf(b, "\t\titem.WriteJSON(buf, fields)\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\tbuf.AppendByte(']')\n")
	fmt.Fprintf(b, "}\n\n")
}

// writeAllFieldsFallback generates the nil-select path that writes all visible fields directly.
func writeAllFieldsFallback(b *strings.Builder, m *ast.ModelDecl, recv string, enums map[string]bool) {
	fmt.Fprintf(b, "\tif fields == nil {\n")
	fmt.Fprintf(b, "\t\tbuf.AppendByte('{')\n")
	first := true
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
		jsonName := f.Name
		goField := recv + "." + str.Capitalize(f.Name)
		if !first {
			fmt.Fprintf(b, "\t\tbuf.AppendByte(',')\n")
		}
		fmt.Fprintf(b, "\t\tbuf.AppendString(`%q:`)\n", jsonName)
		if f.Type.Nullable {
			writeNullableFieldJSON(b, f, goField)
		} else if f.Type.IsList {
			writeListFieldJSON(b, f, goField)
		} else {
			writeScalarFieldJSON(b, f, goField)
		}
		first = false
	}
	fmt.Fprintf(b, "\t\tbuf.AppendByte('}')\n")
	fmt.Fprintf(b, "\t\treturn\n")
	fmt.Fprintf(b, "\t}\n")
}
