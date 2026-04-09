package lux

// Dialect defines database-specific SQL generation for migrations.
type Dialect interface {
	// ColumnType maps a Luxo type name + directives to a SQL column type.
	// e.g., "Int" → "BIGINT" (PG) or "BIGINT" (MySQL)
	ColumnType(luxoType string, serial bool, directives []DirectiveInfo) string

	// ColumnDef produces a full column definition.
	// e.g., "id SERIAL PRIMARY KEY" or "id BIGINT AUTO_INCREMENT PRIMARY KEY"
	ColumnDef(name string, col ColumnInfo) string

	// CreateTable produces a CREATE TABLE statement.
	CreateTable(table string, columns []ColumnEntry, indexes []string) string

	// AddColumn produces ALTER TABLE ADD COLUMN.
	AddColumn(table, colDef string) string

	// DropColumn produces ALTER TABLE DROP COLUMN (commented out for safety).
	DropColumn(table, column string) string

	// AlterColumnType produces ALTER COLUMN TYPE.
	AlterColumnType(table, column, newType string) string

	// SetNotNull / DropNotNull
	SetNotNull(table, column string) string
	DropNotNull(table, column string) string

	// AddUnique / DropUnique
	AddUnique(table, column string) string
	DropUnique(table, column string) string

	// CreateIndex / DropIndex
	CreateIndex(index, table, column string) string
	DropIndex(index string) string

	// DropTable (commented out for safety)
	DropTable(table string) string

	// Name returns the dialect name ("pg", "mysql", "mongo").
	Name() string
}

// DirectiveInfo is a simplified directive for the dialect layer (no AST dependency).
type DirectiveInfo struct {
	Name string
	Arg  string // first positional arg value, if any
}

// ColumnInfo holds column properties for dialect rendering.
type ColumnInfo struct {
	Type     string
	Nullable bool
	PK       bool
	Unique   bool
	Serial   bool
}

// ColumnEntry is a name + info pair for ordered column rendering.
type ColumnEntry struct {
	Name string
	Info ColumnInfo
}
