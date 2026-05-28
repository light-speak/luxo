package lux

// Dialect defines database-specific SQL generation for migrations.
type Dialect interface {
	// ColumnType maps a Luxo type name + directives to a SQL column type.
	ColumnType(luxoType string, serial bool, directives []DirectiveInfo) string

	// ArrayColumnType maps a scalar Luxo type to the column type for a list
	// field ([T]). PG uses native arrays (e.g. TEXT[]); other backends (MySQL,
	// SQLite) may store JSON.
	ArrayColumnType(luxoType string) string

	// ColumnDef produces a full column definition.
	ColumnDef(name string, col ColumnInfo) string

	// CreateTable produces a CREATE TABLE statement with all indexes.
	CreateTable(table string, columns []ColumnEntry, indexes []IndexInfo) string

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

	// CreateIndexSQL produces CREATE INDEX for any index type.
	CreateIndexSQL(idx IndexInfo, table string) string

	// DropIndex produces DROP INDEX.
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
	Default  string
}

// ColumnEntry is a name + info pair for ordered column rendering.
type ColumnEntry struct {
	Name string
	Info ColumnInfo
}

// IndexKind distinguishes index types.
type IndexKind int

const (
	BTreeIndex     IndexKind = iota // default B-tree index
	UniqueIndex                     // UNIQUE constraint as index
	SearchIndex                     // full-text search (@search)
	BrinIndex                       // BRIN index (@brin, time-series)
	CompositeIndex                  // multi-column index (@index(fields: [...]))
)

// IndexInfo describes a database index.
type IndexInfo struct {
	Name    string    // index name, e.g., "idx_users_name"
	Kind    IndexKind // index type
	Columns []string  // column(s) in the index
	Lang    string    // language for search index (PG: "simple", "english")
}
