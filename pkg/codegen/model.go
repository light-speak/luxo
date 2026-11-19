package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
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
func generateModel(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, writers ...*errorDataGenerator) {
	defaultGenerator().generateModel(b, m, enums, writers...)
}

func (g *GeneratorContext) generateModel(b *strings.Builder, m *ast.ModelDecl, enums map[string]bool, writers ...*errorDataGenerator) {
	// Collect effective fields — @soft adds deletedAt if missing.
	effectiveFields := m.Fields
	if isSoftDelete(m) && !hasDeletedAtField(m.Fields) {
		effectiveFields = append(append([]*ast.FieldDecl{}, m.Fields...), softDeleteField())
	}

	fields, maxName, maxType := g.collectFieldInfos(effectiveFields, enums)

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
	g.generateInstanceSaveState(b, m.Name)
	b.WriteString("}\n")

	generateHashMethods(b, m)
	generateAuthMethods(b, m, enums, writers)
}

// generateHashMethods uses the same field-specific contract as semantic analysis.
func generateHashMethods(b *strings.Builder, model *ast.ModelDecl) {
	var single *ast.FieldDecl
	count := 0
	recv := receiverName(model.Name)
	for _, field := range model.Fields {
		if !hasDirective(field.Directives, "hash") {
			continue
		}
		single = field
		count++
		goField := str.Capitalize(field.Name)
		fmt.Fprintf(b, "\nfunc (%s *%s) Verify%s(plaintext string) bool {\n", recv, model.Name, goField)
		value := recv + "." + goField
		if field.Type != nil && field.Type.Nullable {
			fmt.Fprintf(b, "\tif %s == nil { return false }\n", value)
			value = "*" + value
		}
		fmt.Fprintf(b, "\treturn luxocrypto.VerifyPassword(plaintext, %s)\n}\n", value)
	}
	if count == 1 && hasDirective(model.Directives, "withAuth") {
		fmt.Fprintf(b, "\nfunc (%s *%s) Verify(plaintext string) bool {\n", recv, model.Name)
		fmt.Fprintf(b, "\treturn %s.Verify%s(plaintext)\n}\n", recv, str.Capitalize(single.Name))
	}
}

// generateTypeStruct generates a Go struct for a `type` declaration (non-DB plain data).
// No db tags, no auto timestamps, no @hash — just json fields.
func generateTypeStruct(b *strings.Builder, t *ast.TypeDecl, enums map[string]bool) {
	defaultGenerator().generateTypeStruct(b, t, enums)
}

func (g *GeneratorContext) generateTypeStruct(b *strings.Builder, t *ast.TypeDecl, enums map[string]bool) {
	fields, maxName, maxType := g.collectFieldInfos(t.Fields, enums)
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
	defaultGenerator().generateExtendStub(b, ext)
}

func (g *GeneratorContext) generateExtendStub(b *strings.Builder, ext *ast.ExtendDecl, enumSets ...map[string]bool) {
	fmt.Fprintf(b, "// %s is a stub for the external %s model (from extend).\n", ext.Name, ext.Name)
	g.generateModel(b, g.extendStubModel(ext), firstBoolSet(enumSets))
}

// collectFieldInfos collects field info from declarations and measures column widths.
func collectFieldInfos(fields []*ast.FieldDecl, enums map[string]bool) ([]fieldInfo, int, int) {
	return defaultGenerator().collectFieldInfos(fields, enums)
}

func (g *GeneratorContext) collectFieldInfos(fields []*ast.FieldDecl, enums map[string]bool) ([]fieldInfo, int, int) {
	infos := make([]fieldInfo, 0, len(fields))
	maxName := 0
	maxType := 0
	for _, f := range fields {
		relation := isRelationField(f, enums)
		goType := resolveGoType(f.Type)
		if f.Type != nil && f.Type.IsList && g.isModelReference(f.Type.Name) {
			goType = "[]*" + f.Type.Name
		}
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
		if relation || f.Computed != nil {
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
	if t.Name == "Channel" {
		if len(t.TypeArgs) != 1 || t.TypeArgs[0] == nil {
			panic(&InternalCompilerError{Detail: "Channel element metadata is missing"})
		}
		goType = "chan " + resolveGoType(t.TypeArgs[0])
	}

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
	case "JSON":
		return "json.RawMessage"
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

func primaryKeyField(model *ast.ModelDecl) *ast.FieldDecl { return inferred.PrimaryKeyField(model) }

func primaryKeyFieldName(model *ast.ModelDecl) string { return inferred.PrimaryKeyName(model) }

func primaryKeyGoName(model *ast.ModelDecl) string {
	return str.Capitalize(primaryKeyFieldName(model))
}

// A nil entry marks an external model projection whose key metadata is supplied
// by the event context. Owned declarations share this index with reference checks.
func (g *GeneratorContext) withModelReferences(result *semantic.Result) *GeneratorContext {
	bound := *g
	bound.genericExprTypes = genericExpressionRefs(result.GenericExprTypes)
	bound.modelDeclarations = make(map[string]*ast.ModelDecl)
	bound.typeDeclarations = nil
	for _, file := range result.Files {
		for _, declaration := range file.Types {
			if bound.typeDeclarations == nil {
				bound.typeDeclarations = make(map[string]*ast.TypeDecl)
			}
			bound.typeDeclarations[declaration.Name] = declaration
		}
		for _, model := range file.Models {
			bound.modelDeclarations[model.Name] = model
		}
		for _, extension := range file.Extends {
			if _, exists := bound.modelDeclarations[extension.Name]; !exists {
				bound.modelDeclarations[extension.Name] = nil
			}
		}
	}
	return &bound
}

// Resolve each shared generic instance once per generation, outside business code.
func genericExpressionRefs(types map[ast.Expr]*semantic.ResolvedType) map[ast.Expr]*ast.TypeRef {
	if len(types) == 0 {
		return nil
	}
	refs := make(map[ast.Expr]*ast.TypeRef, len(types))
	shared := make(map[*semantic.ResolvedType]*ast.TypeRef)
	for expression, resolved := range types {
		ref := shared[resolved]
		if ref == nil {
			ref = resolved.TypeRef()
			shared[resolved] = ref
		}
		refs[expression] = ref
	}
	return refs
}

func (g *GeneratorContext) isModelReference(name string) bool {
	_, exists := g.modelDeclarations[name]
	return exists
}
