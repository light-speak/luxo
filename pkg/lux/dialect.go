package lux

// Dialect defines database-specific SQL generation for migrations.
type Dialect interface {
	// GeneratedIdentifier bounds framework-owned names; raw table/column names are never rewritten.
	GeneratedIdentifier(name string) string

	// ValidateIdentifier rejects names the backend cannot preserve exactly.
	ValidateIdentifier(name string) error

	// UniqueConstraintName is the name used by generated unique migrations.
	UniqueConstraintName(table, column string) string

	// QuoteIdentifier quotes one raw name, without parsing SQL expressions or qualified paths.
	QuoteIdentifier(name string) string

	// ColumnType maps a Luxo type name + directives to a SQL column type.
	ColumnType(luxoType string, serial bool, directives []DirectiveInfo) string

	// ArrayColumnType maps a scalar Luxo type to the column type for a list
	// field ([T]). PG uses native arrays (e.g. TEXT[]); other backends (MySQL,
	// SQLite) may store JSON.
	ArrayColumnType(luxoType string) string

	// DefaultSQL renders a typed constant without changing its value.
	DefaultSQL(value ColumnDefault) (string, error)

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
	RenameUnique(table, oldColumn, newColumn string) string

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

// DefaultKind identifies the scalar domain of a migration default.
type DefaultKind uint8

const (
	DefaultNull DefaultKind = iota
	DefaultString
	DefaultInt
	DefaultFloat
	DefaultBoolean
	DefaultDuration
	DefaultJSONNull
)

// ColumnDefault keeps SQL NULL separate from string and JSON null values.
// Int stores nanoseconds for DefaultDuration; Text is unescaped string content.
type ColumnDefault struct {
	Kind  DefaultKind
	Text  string
	Int   int64
	Float float64
	Bool  bool
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
