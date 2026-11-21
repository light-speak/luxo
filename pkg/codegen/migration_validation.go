package codegen

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux"
)

func validateMigrationSQL(ops []DiffOp, desired *SchemaState, dialect lux.Dialect) error {
	v := sqlNameValidation{dialect: dialect}
	for index, op := range ops {
		if err := v.validateName(op.Table, "migration table"); err != nil {
			return fmt.Errorf("migration operation %d: %w", index, err)
		}
		if err := validateMigrationOperation(op, desired, &v); err != nil {
			return fmt.Errorf("migration operation %d on %q: %w", index, op.Table, err)
		}
	}
	return nil
}

func validateMigrationOperation(op DiffOp, desired *SchemaState, v *sqlNameValidation) error {
	switch op.Kind {
	case CreateTable:
		return validateMigrationCreate(op, desired, v)
	case DropTable:
		return nil
	case AddColumn:
		return validateMigrationColumn(op.Table, op.Column, op.NewColumn, v)
	case DropColumn:
		return validateMigrationColumn(op.Table, op.Column, op.OldColumn, v)
	case AlterColumn, RenameColumn:
		oldName := op.Column
		if op.Kind == RenameColumn {
			oldName = op.OldName
		}
		if err := validateMigrationColumn(op.Table, oldName, op.OldColumn, v); err != nil {
			return err
		}
		if err := validateMigrationColumn(op.Table, op.Column, op.NewColumn, v); err != nil {
			return err
		}
		return validateColumnTransition(op.Model, op.Column, op.OldColumn, op.NewColumn)
	case AddIndex, DropIndex:
		return validateMigrationIndex(op.Index, v)
	default:
		return fmt.Errorf("unknown migration operation %d", op.Kind)
	}
}

func validateMigrationColumn(table, name string, column *ColumnState, v *sqlNameValidation) error {
	if err := v.validateName(name, "migration column"); err != nil {
		return err
	}
	if column == nil || column.Type == "" {
		return fmt.Errorf("migration column %q requires a type definition", name)
	}
	if column.Unique {
		return v.validateName(v.dialect.UniqueConstraintName(table, name), "unique constraint")
	}
	return nil
}

func validateMigrationIndex(index *lux.IndexInfo, v *sqlNameValidation) error {
	if index == nil {
		return fmt.Errorf("migration index requires a definition")
	}
	if err := v.validateName(index.Name, "migration index"); err != nil {
		return err
	}
	if index.Kind < lux.BTreeIndex || index.Kind > lux.CompositeIndex {
		return fmt.Errorf("unknown index kind %d", index.Kind)
	}
	if len(index.Columns) == 0 || index.Kind == lux.SearchIndex && len(index.Columns) != 1 {
		return fmt.Errorf("index %q has an invalid column count", index.Name)
	}
	for _, column := range index.Columns {
		if err := v.validateName(column, "index column"); err != nil {
			return err
		}
	}
	return nil
}

func validateMigrationCreate(op DiffOp, desired *SchemaState, v *sqlNameValidation) error {
	if desired == nil || desired.Models[op.Model] == nil {
		return fmt.Errorf("CREATE TABLE requires desired model %q", op.Model)
	}
	model := desired.Models[op.Model]
	if model.Table != op.Table {
		return fmt.Errorf("CREATE TABLE %q differs from desired table %q", op.Table, model.Table)
	}
	if err := v.addRelation(model.Table, "CREATE TABLE "+op.Model); err != nil {
		return err
	}
	for _, name := range sortedMigrationKeys(model.Columns) {
		column := model.Columns[name]
		if err := validateMigrationColumn(model.Table, name, column, v); err != nil {
			return err
		}
		if column.Unique {
			if err := v.addRelation(v.dialect.UniqueConstraintName(model.Table, name), "unique constraint on "+op.Model); err != nil {
				return err
			}
		}
	}
	for _, index := range model.Indexes {
		if err := validateMigrationIndex(&index, v); err != nil {
			return err
		}
		if err := v.addRelation(index.Name, "index on "+op.Model); err != nil {
			return err
		}
		for _, column := range index.Columns {
			if _, exists := model.Columns[column]; !exists {
				return fmt.Errorf("index %q refers to missing column %q", index.Name, column)
			}
		}
	}
	return nil
}
