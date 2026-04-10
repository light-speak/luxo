package codegen

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/light-speak/luxo/pkg/ast"
)

// fieldInfo holds pre-computed field data for aligned struct generation.
type fieldInfo struct {
	goName  string
	goType  string
	dbTag   string
	jsonTag string
}

// generateModel generates a Go struct for a Luxo model declaration.
// Pre-computes field widths and aligns output without needing gofmt.
func generateModel(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool) {
	// Collect effective fields — @soft adds deletedAt if missing.
	effectiveFields := m.Fields
	if isSoftDelete(m) && !hasDeletedAtField(m.Fields) {
		effectiveFields = append(append([]*ast.FieldDecl{}, m.Fields...), softDeleteField())
	}

	// First pass: collect field info and measure widths.
	var fields []fieldInfo
	maxName := 0
	maxType := 0
	for _, f := range effectiveFields {
		if f.Computed != nil {
			continue
		}
		relation := isRelationField(f, enums)
		goType := resolveGoType(f.Type)
		// Single model references use pointer to avoid circular struct embedding
		if relation && f.Type != nil && !f.Type.IsList {
			goType = "*" + goType
		}
		fi := fieldInfo{
			goName:  capitalize(f.Name),
			goType:  goType,
			dbTag:   toSnakeCase(f.Name),
			jsonTag: f.Name,
		}
		if relation {
			fi.dbTag = "-" // relation fields are not DB columns
		}
		if hasDirective(f.Directives, "hidden") || hasDirective(f.Directives, "internal") {
			fi.jsonTag = "-"
		}
		if len(fi.goName) > maxName {
			maxName = len(fi.goName)
		}
		if len(fi.goType) > maxType {
			maxType = len(fi.goType)
		}
		fields = append(fields, fi)
	}

	// Second pass: write aligned fields.
	fmt.Fprintf(b, "type %s struct {\n", m.Name)
	for _, fi := range fields {
		if fi.dbTag == "-" {
			// Relation fields: json only, no db tag
			fmt.Fprintf(b, "\t%-*s %-*s `json:%q`\n",
				maxName, fi.goName,
				maxType, fi.goType,
				fi.jsonTag)
		} else {
			fmt.Fprintf(b, "\t%-*s %-*s `db:%q json:%q`\n",
				maxName, fi.goName,
				maxType, fi.goType,
				fi.dbTag, fi.jsonTag)
		}
	}
	b.WriteString("}\n")
}

// generateExtendStub generates a minimal Go struct for an extend declaration.
// This provides type information for cross-module references.
func generateExtendStub(b *strings.Builder, ext *ast.ExtendDecl) {
	var fields []fieldInfo
	maxName := 0
	maxType := 0
	for _, f := range ext.Fields {
		if f.Computed != nil {
			continue
		}
		fi := fieldInfo{
			goName:  capitalize(f.Name),
			goType:  resolveGoType(f.Type),
			dbTag:   toSnakeCase(f.Name),
			jsonTag: f.Name,
		}
		if len(fi.goName) > maxName {
			maxName = len(fi.goName)
		}
		if len(fi.goType) > maxType {
			maxType = len(fi.goType)
		}
		fields = append(fields, fi)
	}

	fmt.Fprintf(b, "// %s is a stub for the external %s model (from extend).\n", ext.Name, ext.Name)
	fmt.Fprintf(b, "type %s struct {\n", ext.Name)
	for _, fi := range fields {
		fmt.Fprintf(b, "\t%-*s %-*s `db:%q json:%q`\n",
			maxName, fi.goName,
			maxType, fi.goType,
			fi.dbTag, fi.jsonTag)
	}
	b.WriteString("}\n")
}

// resolveGoType maps a Luxo TypeRef to a Go type string.
func resolveGoType(t *ast.TypeRef) string {
	if t == nil {
		return "any"
	}

	// list type: [String] → []string
	if t.IsList {
		inner := &ast.TypeRef{Name: t.Name, TypeArgs: t.TypeArgs}
		return "[]" + resolveGoType(inner)
	}

	// base type mapping
	goType := mapBaseType(t.Name)

	// nullable → pointer
	if t.Nullable {
		goType = "*" + goType
	}

	return goType
}

func mapBaseType(name string) string {
	switch name {
	case "Int":
		return "int64"
	case "Float":
		return "float64"
	case "String":
		return "string"
	case "Boolean":
		return "bool"
	case "DateTime":
		return "time.Time"
	case "Duration":
		return "time.Duration"
	case "UUID":
		return "uuid.UUID"
	case "Decimal":
		return "decimal.Decimal"
	case "Bytes":
		return "[]byte"
	default:
		return name // enum or other model/type reference
	}
}

// isAutoManaged returns true if the field value is auto-generated, not user-provided.
// These fields are skipped in Create/Update builders.
func isAutoManaged(f *ast.FieldDecl) bool {
	// @serial — database auto-increment
	if hasDirective(f.Directives, "serial") {
		return true
	}
	// @auto — code-level auto-generation (UUID, etc.)
	if hasDirective(f.Directives, "auto") {
		return true
	}
	// createdAt/updatedAt — auto time.Now() by convention
	if f.Type != nil && f.Type.Name == "DateTime" {
		if f.Name == "createdAt" || f.Name == "updatedAt" {
			return true
		}
	}
	return false
}

// isAutoOnUpdate returns true if the field is auto-filled on UPDATE.
func isAutoOnUpdate(f *ast.FieldDecl) bool {
	return f.Name == "updatedAt" && f.Type != nil && f.Type.Name == "DateTime"
}

// isSoftDelete returns true if the model has @soft directive.
// IsSoftDelete checks if a model has @soft directive (exported for CLI).
func IsSoftDelete(m *ast.ModelDecl) bool {
	return isSoftDelete(m)
}

func isSoftDelete(m *ast.ModelDecl) bool {
	return hasDirective(m.Directives, "soft")
}

// hasDeletedAtField returns true if the model already has a deletedAt field.
func hasDeletedAtField(fields []*ast.FieldDecl) bool {
	for _, f := range fields {
		if f.Name == "deletedAt" {
			return true
		}
	}
	return false
}

// softDeleteField returns a synthetic deletedAt field for @soft models.
func softDeleteField() *ast.FieldDecl {
	return &ast.FieldDecl{
		Name: "deletedAt",
		Type: &ast.TypeRef{Name: "DateTime", Nullable: true},
	}
}

func hasDirective(directives []*ast.Directive, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

// capitalize returns the string with the first letter uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(unicode.ToUpper(rune(s[0]))) + s[1:]
}

// toSnakeCase converts camelCase to snake_case.
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
