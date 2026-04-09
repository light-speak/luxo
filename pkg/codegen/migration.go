package codegen

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// MigrationFile represents a generated SQL migration.
type MigrationFile struct {
	Name string // e.g., "20260409_001_create_users.sql"
	Up   string // SQL to apply
	Down string // SQL to rollback
}

// GenerateMigrations produces SQL migration files from the schema.
// For initial setup, generates CREATE TABLE statements.
func GenerateMigrations(result *semantic.Result) []MigrationFile {
	enums := collectEnums(result)
	var migrations []MigrationFile
	seq := 1

	for _, file := range result.Files {
		for _, m := range file.Models {
			up, down := generateCreateTable(m, enums)
			name := fmt.Sprintf("%s_%03d_create_%s.sql",
				time.Now().Format("20060102"), seq, toSnakeCase(m.Name)+"s")
			migrations = append(migrations, MigrationFile{
				Name: name,
				Up:   up,
				Down: down,
			})
			seq++
		}
	}

	return migrations
}

// generateCreateTable produces CREATE TABLE / DROP TABLE SQL for a model.
func generateCreateTable(m *ast.ModelDecl, enums map[string]bool) (up, down string) {
	tableName := toSnakeCase(m.Name) + "s"

	var upB strings.Builder
	fmt.Fprintf(&upB, "CREATE TABLE %s (\n", tableName)

	columns := collectColumns(m, enums)
	columns = appendAutoColumns(columns, m)

	upB.WriteString(strings.Join(columns, ",\n"))
	upB.WriteString("\n);\n")

	appendIndexes(&upB, m.Fields, tableName)

	up = upB.String()
	down = fmt.Sprintf("DROP TABLE IF EXISTS %s;\n", tableName)
	return
}

// collectColumns generates column definitions from model fields.
func collectColumns(m *ast.ModelDecl, enums map[string]bool) []string {
	var columns []string
	for _, f := range m.Fields {
		if f.Computed != nil || isRelationField(f, enums) {
			continue
		}
		if col := generateColumn(f); col != "" {
			columns = append(columns, col)
		}
	}
	return columns
}

// appendAutoColumns adds auto timestamp and soft delete columns.
func appendAutoColumns(columns []string, m *ast.ModelDecl) []string {
	if !hasDirective(m.Directives, "noTime") {
		if !hasFieldNamed(m.Fields, "createdAt") {
			columns = append(columns, "  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
		}
		if !hasFieldNamed(m.Fields, "updatedAt") {
			columns = append(columns, "  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
		}
	}
	if isSoftDelete(m) && !hasFieldNamed(m.Fields, "deletedAt") {
		columns = append(columns, "  deleted_at TIMESTAMPTZ")
	}
	return columns
}

// hasFieldNamed checks if a field with the given name exists.
func hasFieldNamed(fields []*ast.FieldDecl, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// appendIndexes adds CREATE INDEX statements for @index fields.
func appendIndexes(b *strings.Builder, fields []*ast.FieldDecl, tableName string) {
	for _, f := range fields {
		if hasDirective(f.Directives, "index") {
			fmt.Fprintf(b, "CREATE INDEX idx_%s_%s ON %s (%s);\n",
				tableName, toSnakeCase(f.Name), tableName, toSnakeCase(f.Name))
		}
	}
}

// generateColumn produces a single column definition.
func generateColumn(f *ast.FieldDecl) string {
	colName := toSnakeCase(f.Name)
	isSerial := hasDirective(f.Directives, "serial")
	nullable := f.Type != nil && f.Type.Nullable

	sqlType := resolveColumnType(f)
	if sqlType == "" {
		return ""
	}
	if isSerial {
		sqlType = "SERIAL"
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("  %s %s", colName, sqlType))

	if !nullable && !isSerial && f.Default == nil {
		parts = append(parts, "NOT NULL")
	}
	if isSerial || hasDirective(f.Directives, "id") {
		parts = append(parts, "PRIMARY KEY")
	}
	if hasDirective(f.Directives, "unique") {
		parts = append(parts, "UNIQUE")
	}

	return strings.Join(parts, " ")
}

// resolveColumnType maps a Luxo type to a PostgreSQL column type.
func resolveColumnType(f *ast.FieldDecl) string {
	if f.Type == nil || f.Type.IsList {
		return ""
	}
	if hasDirective(f.Directives, "varchar") {
		return resolveVarcharType(f)
	}
	base := baseColumnType(f.Type.Name)
	return applyTypeDirective(base, f)
}

// resolveVarcharType extracts VARCHAR length from @varchar directive.
func resolveVarcharType(f *ast.FieldDecl) string {
	length := "255"
	for _, d := range f.Directives {
		if d.Name == "varchar" && len(d.Args) > 0 {
			if lit, ok := d.Args[0].Value.(*ast.Literal); ok {
				length = lit.Value
			}
		}
	}
	return fmt.Sprintf("VARCHAR(%s)", length)
}

// baseColumnType returns the default SQL type for a Luxo type name.
func baseColumnType(name string) string {
	switch name {
	case "Int":
		return "BIGINT"
	case "Float":
		return "DOUBLE PRECISION"
	case "String":
		return "TEXT"
	case "Boolean":
		return "BOOLEAN"
	case "DateTime":
		return "TIMESTAMPTZ"
	case "Duration":
		return "INTERVAL"
	case "UUID":
		return "UUID"
	case "Decimal":
		return "DECIMAL"
	case "Bytes":
		return "BYTEA"
	default:
		return "TEXT"
	}
}

// applyTypeDirective overrides the base SQL type based on directives.
func applyTypeDirective(base string, f *ast.FieldDecl) string {
	switch f.Type.Name {
	case "Int":
		if hasDirective(f.Directives, "bigint") {
			return "BIGINT"
		}
		if hasDirective(f.Directives, "smallint") {
			return "SMALLINT"
		}
	case "Float":
		if hasDirective(f.Directives, "decimal") {
			return "DECIMAL"
		}
	case "DateTime":
		if hasDirective(f.Directives, "date") {
			return "DATE"
		}
		if hasDirective(f.Directives, "time") {
			return "TIME"
		}
	}
	return base
}

// SchemaHash returns a hash of the current schema for change detection.
func SchemaHash(result *semantic.Result) string {
	var b strings.Builder
	for _, file := range result.Files {
		for _, m := range file.Models {
			fmt.Fprintf(&b, "model:%s\n", m.Name)
			for _, f := range m.Fields {
				if f.Computed != nil {
					continue
				}
				fmt.Fprintf(&b, "  field:%s:%s\n", f.Name, resolveColumnType(f))
			}
		}
	}
	h := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", h[:8])
}
