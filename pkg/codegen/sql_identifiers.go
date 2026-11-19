package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/internal/inferred"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/semantic"
)

// SQL names are validated once at generation/migration boundaries, never per request.
func validateDatabaseIdentifiers(result *semantic.Result, enums map[string]bool, dialect lux.Dialect) error {
	files := inferred.ExpandedFiles(result.Files)
	v := sqlNameValidation{dialect: dialect}
	for _, file := range files {
		for _, model := range file.Models {
			if err := v.addRelation(modelTableName(model.Name), "model "+model.Name); err != nil {
				return fmt.Errorf("%s: %w", model.Pos, err)
			}
		}
	}
	for _, file := range files {
		for _, model := range file.Models {
			if err := v.validateModel(model, enums); err != nil {
				return err
			}
		}
		for _, ext := range file.Extends {
			if err := v.validateExternal(ext, enums); err != nil {
				return err
			}
		}
	}
	return nil
}

type sqlNameValidation struct {
	dialect   lux.Dialect
	relations map[string]string
	fields    fieldNameValidation
}

func (v *sqlNameValidation) validateName(name, owner string) error {
	if v.dialect == nil {
		return fmt.Errorf("database identifier validation requires an explicit dialect")
	}
	if err := v.dialect.ValidateIdentifier(name); err != nil {
		return fmt.Errorf("%s: %w", owner, err)
	}
	return nil
}

// The current PostgreSQL adapter has a shared table/index relation namespace.
func (v *sqlNameValidation) addRelation(name, owner string) error {
	if err := v.validateName(name, owner); err != nil {
		return err
	}
	if previous, ok := v.relations[name]; ok {
		return fmt.Errorf("database name %q for %s conflicts with %s", name, owner, previous)
	}
	if v.relations == nil {
		v.relations = make(map[string]string)
	}
	v.relations[name] = owner
	return nil
}

func (v *sqlNameValidation) validateColumns(owner string, fields []*ast.FieldDecl, enums map[string]bool) error {
	if v.dialect == nil {
		return fmt.Errorf("database identifier validation requires an explicit dialect")
	}
	v.fields.databaseDialect = v.dialect
	return v.fields.validateDatabaseColumnNames(owner, fields, enums)
}

func (v *sqlNameValidation) validateModel(model *ast.ModelDecl, enums map[string]bool) error {
	if err := v.validateColumns(model.Name, model.Fields, enums); err != nil {
		return err
	}
	if err := v.validateManagedColumns(model); err != nil {
		return err
	}
	table := modelTableName(model.Name)
	for _, field := range model.Fields {
		if field.Type == nil || field.Type.IsList || field.Computed != nil || isRelationField(field, enums) || !hasDirective(field.Directives, "unique") {
			continue
		}
		name := v.dialect.UniqueConstraintName(table, databaseFieldColumnName(field.Name))
		if err := v.addRelation(name, "unique constraint for "+model.Name+"."+field.Name); err != nil {
			return err
		}
	}
	for _, index := range collectIndexes(model, table, v.dialect) {
		if err := v.addRelation(index.Name, "index on "+model.Name); err != nil {
			return err
		}
		for _, column := range index.Columns {
			if _, exists := v.fields.columns[column]; !exists {
				return fmt.Errorf("index %q refers to non-stored column %q", index.Name, column)
			}
		}
	}
	return nil
}

func (v *sqlNameValidation) validateManagedColumns(model *ast.ModelDecl) error {
	noTime, soft := hasDirective(model.Directives, "noTime"), isSoftDelete(model)
	for index, field := range [3]string{"createdAt", "updatedAt", "deletedAt"} {
		if index < 2 && noTime || index == 2 && !soft {
			continue
		}
		column := databaseFieldColumnName(field)
		if previous, exists := v.fields.columns[column]; exists && previous != field {
			return fmt.Errorf("%s field %q conflicts with managed database column %q for %s", model.Name, previous, column, field)
		}
		v.fields.columns[column] = field
	}
	return nil
}

func (v *sqlNameValidation) validateExternal(ext *ast.ExtendDecl, enums map[string]bool) error {
	if err := v.validateName(modelTableName(ext.Name), "external model "+ext.Name); err != nil {
		return err
	}
	return v.validateColumns(ext.Name, ext.Fields, enums)
}
