package inferred

import (
	"github.com/light-speak/luxo/pkg/ast"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PrimaryKeyField reads effective fields after inheritance expansion.
func PrimaryKeyField(model *ast.ModelDecl) *ast.FieldDecl {
	if model == nil {
		return nil
	}
	for _, field := range model.Fields {
		if field.Type == nil {
			continue
		}
		for _, directive := range field.Directives {
			if directive.Name == "id" {
				return field
			}
		}
	}
	for _, field := range model.Fields {
		if field.Name == "id" && field.Type != nil {
			return field
		}
	}
	return nil
}

// PrimaryKeyName retains the conventional name for incomplete internal stubs.
func PrimaryKeyName(model *ast.ModelDecl) string {
	if field := PrimaryKeyField(model); field != nil {
		return field.Name
	}
	return "id"
}

// ForeignKeyName is shared by local and federation relation inference.
func ForeignKeyName(modelName, primaryKey string) string {
	if modelName == "" && primaryKey == "" {
		return ""
	}
	var name strings.Builder
	name.Grow(len(modelName) + len(primaryKey) + 2)
	if modelName != "" {
		first, size := utf8.DecodeRuneInString(modelName)
		name.WriteRune(unicode.ToLower(first))
		name.WriteString(modelName[size:])
	}
	if primaryKey != "" {
		first, size := utf8.DecodeRuneInString(primaryKey)
		name.WriteRune(unicode.ToUpper(first))
		name.WriteString(primaryKey[size:])
	}
	return name.String()
}

// ImplicitRelationKeys prefers a declared local FK for a single relation.
// Otherwise the target stores the owner's effective primary key.
func ImplicitRelationKeys(model *ast.ModelDecl, field *ast.FieldDecl, targetPrimaryKey string) (local, remote string, belongsTo bool) {
	if model == nil || field == nil || field.Type == nil {
		return "", "", false
	}
	if !field.Type.IsList {
		candidate := ForeignKeyName(field.Type.Name, targetPrimaryKey)
		for _, localField := range model.Fields {
			if localField.Name == candidate {
				return candidate, targetPrimaryKey, true
			}
		}
	}
	local = PrimaryKeyName(model)
	return local, ForeignKeyName(model.Name, local), false
}
