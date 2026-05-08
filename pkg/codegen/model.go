package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
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

	fields, maxName, maxType := collectFieldInfos(effectiveFields, enums)

	// Write aligned fields.
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

	// Generate VerifyPassword methods for @hash fields
	for _, f := range m.Fields {
		if hasDirective(f.Directives, "hash") {
			goField := str.Capitalize(f.Name)
			methodName := "Verify" + goField
			recv := strings.ToLower(m.Name[:1])
			fmt.Fprintf(b, "\nfunc (%s *%s) %s(plaintext string) bool {\n", recv, m.Name, methodName)
			fmt.Fprintf(b, "\treturn luxocrypto.VerifyPassword(plaintext, %s.%s)\n", recv, goField)
			fmt.Fprintf(b, "}\n")
		}
	}
	// Generate CreateToken/RefreshToken for @withAuth models
	for _, d := range m.Directives {
		if d.Name != "withAuth" {
			continue
		}
		recv := strings.ToLower(m.Name[:1])
		// Extract stores fields
		var storeFields []string
		for _, arg := range d.Args {
			if arg.Name == "stores" {
				if list, ok := arg.Value.(*ast.ListExpr); ok {
					for _, item := range list.Items {
						if ident, ok := item.(*ast.Ident); ok {
							storeFields = append(storeFields, ident.Name)
						}
					}
				}
			}
		}
		// CreateToken: builds data map from stores fields, calls auth.Sign
		fmt.Fprintf(b, "\nfunc (%s *%s) CreateToken() string {\n", recv, m.Name)
		fmt.Fprintf(b, "\tdata := map[string]any{\n")
		for _, sf := range storeFields {
			fmt.Fprintf(b, "\t\t%q: %s.%s,\n", sf, recv, str.Capitalize(sf))
		}
		fmt.Fprintf(b, "\t}\n")
		fmt.Fprintf(b, "\tcfg, err := auth.LoadConfig()\n")
		fmt.Fprintf(b, "\tif err != nil {\n\t\treturn \"\"\n\t}\n")
		fmt.Fprintf(b, "\ttoken, err := auth.Sign(cfg, data)\n")
		fmt.Fprintf(b, "\tif err != nil {\n\t\treturn \"\"\n\t}\n")
		fmt.Fprintf(b, "\treturn token\n")
		fmt.Fprintf(b, "}\n")
		// RefreshToken
		fmt.Fprintf(b, "\nfunc (%s *%s) RefreshToken(oldToken string) string {\n", recv, m.Name)
		fmt.Fprintf(b, "\tcfg, err := auth.LoadConfig()\n")
		fmt.Fprintf(b, "\tif err != nil {\n\t\treturn \"\"\n\t}\n")
		fmt.Fprintf(b, "\tdata, err := auth.Verify(cfg, oldToken)\n")
		fmt.Fprintf(b, "\tif err != nil {\n\t\treturn \"\"\n\t}\n")
		fmt.Fprintf(b, "\ttoken, err := auth.Sign(cfg, data)\n")
		fmt.Fprintf(b, "\tif err != nil {\n\t\treturn \"\"\n\t}\n")
		fmt.Fprintf(b, "\treturn token\n")
		fmt.Fprintf(b, "}\n")
		break // only generate once even if @withAuth appears multiple times
	}
}

// generateTypeStruct generates a Go struct for a `type` declaration (non-DB plain data).
// No db tags, no auto timestamps, no @hash — just json fields.
func generateTypeStruct(b *strings.Builder, t *ast.TypeDecl, enums map[string]bool) {
	fields, maxName, maxType := collectFieldInfos(t.Fields, enums)
	fmt.Fprintf(b, "type %s struct {\n", t.Name)
	for _, fi := range fields {
		fmt.Fprintf(b, "\t%-*s %-*s `json:%q`\n",
			maxName, fi.goName,
			maxType, fi.goType,
			fi.jsonTag)
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
			goName:  str.Capitalize(f.Name),
			goType:  resolveGoType(f.Type),
			dbTag:   str.ToSnakeCase(f.Name),
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
	// Always include Id field — extend stubs need it for foreign key references and dataloader
	hasId := false
	for _, fi := range fields {
		if fi.goName == "Id" {
			hasId = true
		}
	}
	if !hasId {
		idName := "Id"
		idType := "int64"
		if len(idName) > maxName {
			maxName = len(idName)
		}
		if len(idType) > maxType {
			maxType = len(idType)
		}
		fmt.Fprintf(b, "\t%-*s %-*s `db:%q json:%q`\n", maxName, idName, maxType, idType, "id", "id")
	}
	for _, fi := range fields {
		fmt.Fprintf(b, "\t%-*s %-*s `db:%q json:%q`\n",
			maxName, fi.goName,
			maxType, fi.goType,
			fi.dbTag, fi.jsonTag)
	}
	b.WriteString("}\n")
}

// collectFieldInfos collects field info from declarations and measures column widths.
func collectFieldInfos(fields []*ast.FieldDecl, enums map[string]bool) ([]fieldInfo, int, int) {
	var infos []fieldInfo
	maxName := 0
	maxType := 0
	for _, f := range fields {
		if f.Computed != nil {
			continue
		}
		relation := isRelationField(f, enums)
		goType := resolveGoType(f.Type)
		// Single model references use pointer — but if already nullable (*Type),
		// don't double-pointer. We always want *Model, never **Model.
		if relation && f.Type != nil && !f.Type.IsList && !f.Type.Nullable {
			goType = "*" + goType
		}
		fi := fieldInfo{
			goName:  str.Capitalize(f.Name),
			goType:  goType,
			dbTag:   str.ToSnakeCase(f.Name),
			jsonTag: f.Name,
		}
		if relation {
			fi.dbTag = "-"
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
		infos = append(infos, fi)
	}
	return infos, maxName, maxType
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
// Pass modelDirectives to check for @noTime (model-level directive).
func isAutoManaged(f *ast.FieldDecl, modelDirectives ...[]*ast.Directive) bool {
	if hasDirective(f.Directives, "serial") {
		return true
	}
	if hasDirective(f.Directives, "auto") {
		return true
	}
	// createdAt/updatedAt — auto time.Now() unless @noTime on model
	if f.Type != nil && f.Type.Name == "DateTime" {
		if f.Name == "createdAt" || f.Name == "updatedAt" {
			// Check if model has @noTime
			if len(modelDirectives) > 0 {
				for _, d := range modelDirectives[0] {
					if d.Name == "noTime" {
						return false
					}
				}
			}
			return true
		}
	}
	return false
}

// isAutoOnUpdate returns true if the field is auto-filled on UPDATE.
func isAutoOnUpdate(f *ast.FieldDecl, modelDirectives ...[]*ast.Directive) bool {
	if f.Name != "updatedAt" || f.Type == nil || f.Type.Name != "DateTime" {
		return false
	}
	if len(modelDirectives) > 0 {
		for _, d := range modelDirectives[0] {
			if d.Name == "noTime" {
				return false
			}
		}
	}
	return true
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
