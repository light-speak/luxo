package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
)

// generateScanner emits statically selected scan destinations; driver conversion stays at the PG boundary.
//
// For model User { id: Int, name: String, email: String }
// generates:
//
//	func scanUser(rows pg.Rows) (*User, error) {
//	    var value User
//	    fds := rows.FieldDescriptions()
//	    dests := make([]any, len(fds))
//	    for i, fd := range fds {
//	        switch string(fd.Name) {
//	        case "id":    dests[i] = &value.Id
//	        case "name":  dests[i] = &value.Name
//	        case "email": dests[i] = &value.Email
//	        }
//	    }
//	    if err := rows.Scan(dests...); err != nil { return nil, err }
//	    return &value, nil
//	}
func generateScanner(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	defaultGenerator().generateScanner(b, m, enums)
}

func (g *GeneratorContext) generateScanner(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	name := m.Name
	const receiver = "value"
	fnName := "scan" + name

	fmt.Fprintf(b, "// %s scans a row into a %s using statically selected destinations.\n", fnName, name)
	fmt.Fprintf(b, "func %s(rows pg.Rows) (*%s, error) {\n", fnName, name)
	fmt.Fprintf(b, "\tvar %s %s\n", receiver, name)
	jsonFields, checkedFields := writeScannerLocals(b, m, enums)
	fmt.Fprintf(b, "\tfds := rows.FieldDescriptions()\n")
	fmt.Fprintf(b, "\tdests := make([]any, len(fds))\n")
	fmt.Fprintf(b, "\tfor i, fd := range fds {\n")
	fmt.Fprintf(b, "\t\tswitch string(fd.Name) {\n")

	for _, f := range m.Fields {
		if f.Computed != nil {
			continue
		}
		col := str.ToSnakeCase(f.Name)
		goField := str.Capitalize(f.Name)
		fmt.Fprintf(b, "\t\tcase %q:\n", col)
		if plan := g.instanceSaves[m.Name]; plan != nil {
			fmt.Fprintf(b, "\t\t\t%s\n", plan.bitSet(receiver, "Loaded", plan.index[f.Name]))
		}
		if checkedScanField(f, enums) {
			fmt.Fprintf(b, "\t\t\t_check%s = true\n", goField)
		}
		if nullableJSONScanField(f) {
			fmt.Fprintf(b, "\t\t\t_json%s = new(pg.NullableJSONValue)\n", goField)
			fmt.Fprintf(b, "\t\t\tdests[i] = _json%s\n", goField)
		} else if f.Type != nil && f.Type.Name == "JSON" && f.Type.IsList {
			fmt.Fprintf(b, "\t\t\tdests[i] = (*pg.JSONListValue)(&%s.%s)\n", receiver, goField)
		} else if f.Type != nil && f.Type.Name == "JSON" {
			fmt.Fprintf(b, "\t\t\tdests[i] = (*pg.JSONValue)(&%s.%s)\n", receiver, goField)
		} else if f.Type != nil && f.Type.Name == "Duration" {
			fmt.Fprintf(b, durationScanFormat(f.Type), receiver, goField)
		} else {
			fmt.Fprintf(b, "\t\t\tdests[i] = &%s.%s\n", receiver, goField)
		}
	}

	// pgx treats nil as an explicit skip; do not decode discarded values into any.
	fmt.Fprintf(b, "\t\tdefault:\n")
	fmt.Fprintf(b, "\t\t\tdests[i] = nil\n")

	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t}\n")
	tracking := ""
	if plan := g.instanceSaves[m.Name]; plan != nil {
		tracking = plan.prefix
	}
	writeScannerResult(b, receiver, jsonFields, checkedFields, enums, tracking)
	fmt.Fprintf(b, "}\n\n")
}

func durationScanFormat(typ *ast.TypeRef) string {
	if typ.IsList {
		return "\t\t\tdests[i] = (*pg.DurationListValue)(&%s.%s)\n"
	}
	if typ.Nullable {
		return "\t\t\tdests[i] = pg.DurationPointer(&%s.%s)\n"
	}
	return "\t\t\tdests[i] = (*pg.DurationScanValue)(&%s.%s)\n"
}

func writeScannerLocals(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) ([]string, []*ast.FieldDecl) {
	var jsonFields []string
	var checkedFields []*ast.FieldDecl
	for _, f := range m.Fields {
		if checkedScanField(f, enums) {
			checkedFields = append(checkedFields, f)
			fmt.Fprintf(b, "\tvar _check%s bool\n", str.Capitalize(f.Name))
		}
		if nullableJSONScanField(f) {
			field := str.Capitalize(f.Name)
			jsonFields = append(jsonFields, field)
			fmt.Fprintf(b, "\tvar _json%s *pg.NullableJSONValue\n", field)
		}
	}
	return jsonFields, checkedFields
}

func nullableJSONScanField(f *ast.FieldDecl) bool {
	return f.Computed == nil && f.Type != nil && f.Type.Name == "JSON" && f.Type.Nullable && !f.Type.IsList
}

func writeScannerResult(b *strings.Builder, receiver string, jsonFields []string, checkedFields []*ast.FieldDecl, enums map[string]bool, tracked ...string) {
	b.WriteString("\tif err := rows.Scan(dests...); err != nil { return nil, err }\n")
	for _, field := range jsonFields {
		fmt.Fprintf(b, "\tif _json%s != nil { %s.%s = _json%s.Pointer() }\n", field, receiver, field, field)
	}
	for _, f := range checkedFields {
		writeScanFieldValidation(b, receiver, f, enums)
	}
	if len(tracked) > 0 && tracked[0] != "" {
		fmt.Fprintf(b, "\t%s.%sCapture()\n", receiver, tracked[0])
	}
	fmt.Fprintf(b, "\treturn &%s, nil\n", receiver)
}

func checkedScanField(f *ast.FieldDecl, enums map[string]bool) bool {
	return f.Computed == nil && f.Type != nil && (f.Type.Name == "Float" || f.Type.Name == "DateTime" || enums[f.Type.Name])
}

func writeScanFieldValidation(b *strings.Builder, receiver string, f *ast.FieldDecl, enums map[string]bool) {
	field := str.Capitalize(f.Name)
	value := receiver + "." + field
	fmt.Fprintf(b, "\tif _check%s {\n", field)
	if f.Type.IsList {
		fmt.Fprintf(b, "\tfor _, element := range %s {\n", value)
		value = "element"
	} else if f.Type.Nullable {
		fmt.Fprintf(b, "\tif %s != nil {\n", value)
		value = "*" + value
	}
	if enums[f.Type.Name] {
		fmt.Fprintf(b, "\tif !(%s).IsValid() { return nil, codec.ErrInvalidEnum }\n", value)
	} else {
		fmt.Fprintf(b, "\tif err := pg.Validate%s(%s); err != nil { return nil, err }\n", f.Type.Name, value)
	}
	if f.Type.IsList || f.Type.Nullable {
		b.WriteString("\t}\n")
	}
	b.WriteString("\t}\n")
}

func storedEnumField(f *ast.FieldDecl, enums map[string]bool) bool {
	return f.Computed == nil && f.Type != nil && enums[f.Type.Name]
}
