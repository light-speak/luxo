package pg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux"
)

// Dialect implements lux.Dialect for PostgreSQL.
type Dialect struct{}

var _ lux.Dialect = Dialect{}

// MaxIdentifierBytes is the PostgreSQL identifier limit used by this dialect.
const MaxIdentifierBytes = 63

// ValidateIdentifier uses the standard PostgreSQL 63-byte identifier boundary.
func (Dialect) ValidateIdentifier(name string) error {
	if name == "" || len(name) > MaxIdentifierBytes || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("PostgreSQL identifier %q must be valid UTF-8 without NUL and contain 1..63 bytes", name)
	}
	return nil
}

// GeneratedIdentifier retains short names and uses a stable hash of the full long name.
func (Dialect) GeneratedIdentifier(name string) string {
	if len(name) <= MaxIdentifierBytes {
		return name
	}
	hash := sha256.Sum256([]byte(name))
	var suffix [16]byte
	hex.Encode(suffix[:], hash[:8])
	prefix := MaxIdentifierBytes - 1 - len(suffix)
	for !utf8.RuneStart(name[prefix]) {
		prefix--
	}
	return name[:prefix] + "_" + string(suffix[:])
}

// UniqueConstraintName matches the name used by create/add/drop migrations.
func (d Dialect) UniqueConstraintName(table, column string) string {
	return d.GeneratedIdentifier(table + "_" + column + "_key")
}

// QuoteIdentifier preserves an atomic identifier, including embedded quotes.
func (Dialect) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (Dialect) Name() string { return "pg" }

func (Dialect) ColumnType(luxoType string, serial bool, directives []lux.DirectiveInfo) string {
	if serial {
		return "SERIAL"
	}
	if hasDir(directives, "varchar") {
		return "VARCHAR(" + dirArg(directives, "varchar", "255") + ")"
	}
	base := pgBaseType(luxoType)
	return pgApplyDirective(base, luxoType, directives)
}

// ArrayColumnType maps a [T] field to a native PostgreSQL array column,
// e.g. [String] → TEXT[], [Int] → BIGINT[], [UUID] → UUID[].
func (Dialect) ArrayColumnType(luxoType string) string {
	return pgBaseType(luxoType) + "[]"
}

func pgBaseType(name string) string {
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
	case "JSON":
		return "JSONB"
	default:
		return "TEXT" // enum → TEXT
	}
}

func pgApplyDirective(base, luxoType string, directives []lux.DirectiveInfo) string {
	switch luxoType {
	case "Int":
		if hasDir(directives, "bigint") {
			return "BIGINT"
		}
		if hasDir(directives, "smallint") {
			return "SMALLINT"
		}
	case "Float":
		if hasDir(directives, "decimal") {
			p := dirArg(directives, "decimal", "")
			if p != "" {
				return "DECIMAL(" + p + ")"
			}
			return "DECIMAL"
		}
	case "String":
		if hasDir(directives, "inet") {
			return "INET"
		}
		if hasDir(directives, "point") {
			return "POINT"
		}
		if hasDir(directives, "vector") {
			dim := dirArg(directives, "vector", "1536")
			return "vector(" + dim + ")"
		}
	case "DateTime":
		if hasDir(directives, "date") {
			return "DATE"
		}
		if hasDir(directives, "time") {
			return "TIME"
		}
	}
	return base
}

func (d Dialect) ColumnDef(name string, col lux.ColumnInfo) string {
	var parts []string
	parts = append(parts, d.QuoteIdentifier(name)+" "+col.Type)
	if !col.Nullable && !col.Serial {
		parts = append(parts, "NOT NULL")
	}
	if col.Default != "" {
		parts = append(parts, "DEFAULT "+col.Default)
	}
	if col.PK {
		parts = append(parts, "PRIMARY KEY")
	}
	if col.Unique {
		parts = append(parts, "UNIQUE")
	}
	return strings.Join(parts, " ")
}

func (d Dialect) CreateTable(table string, columns []lux.ColumnEntry, indexes []lux.IndexInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", d.QuoteIdentifier(table))

	var cols []string
	for _, c := range columns {
		info := c.Info
		info.Unique = false
		definition := d.ColumnDef(c.Name, info)
		if c.Info.Unique {
			definition += " CONSTRAINT " + d.QuoteIdentifier(d.UniqueConstraintName(table, c.Name)) + " UNIQUE"
		}
		cols = append(cols, "  "+definition)
	}
	b.WriteString(strings.Join(cols, ",\n"))
	b.WriteString("\n);\n")

	for _, idx := range indexes {
		b.WriteString(d.CreateIndexSQL(idx, table))
	}
	return b.String()
}

func (d Dialect) AddColumn(table, colDef string) string {
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;\n", d.QuoteIdentifier(table), colDef)
}

func (d Dialect) DropColumn(table, column string) string {
	return fmt.Sprintf("-- ALTER TABLE %s DROP COLUMN %s; -- DANGEROUS: uncomment to apply\n", d.QuoteIdentifier(table), d.QuoteIdentifier(column))
}

func (d Dialect) AlterColumnType(table, column, newType string) string {
	table, column = d.QuoteIdentifier(table), d.QuoteIdentifier(column)
	using := pgUsingClause(column, newType)
	if using != "" {
		return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s;\n", table, column, newType, using)
	}
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;\n", table, column, newType)
}

// pgUsingClause returns a USING expression for type conversions that need explicit casting.
func pgUsingClause(column, newType string) string {
	switch newType {
	case "BIGINT", "SMALLINT", "SERIAL":
		return column + "::BIGINT"
	case "DOUBLE PRECISION":
		return column + "::DOUBLE PRECISION"
	case "BOOLEAN":
		return column + "::BOOLEAN"
	case "TIMESTAMPTZ":
		return column + "::TIMESTAMPTZ"
	case "UUID":
		return column + "::UUID"
	case "JSONB", "JSONB[]":
		return column + "::" + newType
	}
	if strings.HasPrefix(newType, "DECIMAL") {
		return column + "::DECIMAL"
	}
	return "" // TEXT, VARCHAR — implicit cast
}

func (d Dialect) SetNotNull(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;\n", d.QuoteIdentifier(table), d.QuoteIdentifier(column))
}

func (d Dialect) DropNotNull(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;\n", d.QuoteIdentifier(table), d.QuoteIdentifier(column))
}

func (d Dialect) AddUnique(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);\n", d.QuoteIdentifier(table), d.QuoteIdentifier(d.UniqueConstraintName(table, column)), d.QuoteIdentifier(column))
}

func (d Dialect) DropUnique(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;\n", d.QuoteIdentifier(table), d.QuoteIdentifier(d.UniqueConstraintName(table, column)))
}

// RenameUnique keeps generated constraint and backing-index identity in sync.
func (d Dialect) RenameUnique(table, oldColumn, newColumn string) string {
	return fmt.Sprintf("ALTER TABLE %s RENAME CONSTRAINT %s TO %s;\n", d.QuoteIdentifier(table),
		d.QuoteIdentifier(d.UniqueConstraintName(table, oldColumn)), d.QuoteIdentifier(d.UniqueConstraintName(table, newColumn)))
}

func (d Dialect) CreateIndexSQL(idx lux.IndexInfo, table string) string {
	columns := make([]string, len(idx.Columns))
	for i, column := range idx.Columns {
		columns[i] = d.QuoteIdentifier(column)
	}
	cols := strings.Join(columns, ", ")
	table, index := d.QuoteIdentifier(table), d.QuoteIdentifier(idx.Name)
	switch idx.Kind {
	case lux.SearchIndex:
		lang := idx.Lang
		if lang == "" {
			lang = "simple"
		}
		return fmt.Sprintf("CREATE INDEX %s ON %s USING gin(to_tsvector(%s, %s));\n", index, table, quoteDefaultString(lang), cols)
	case lux.BrinIndex:
		return fmt.Sprintf("CREATE INDEX %s ON %s USING brin(%s);\n", index, table, cols)
	case lux.UniqueIndex:
		return fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s);\n", index, table, cols)
	default: // BTreeIndex, CompositeIndex
		return fmt.Sprintf("CREATE INDEX %s ON %s (%s);\n", index, table, cols)
	}
}

func (d Dialect) DropIndex(index string) string {
	return fmt.Sprintf("DROP INDEX IF EXISTS %s;\n", d.QuoteIdentifier(index))
}

func (d Dialect) DropTable(table string) string {
	return fmt.Sprintf("-- DROP TABLE IF EXISTS %s; -- DANGEROUS: uncomment to apply\n", d.QuoteIdentifier(table))
}

func hasDir(directives []lux.DirectiveInfo, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}

func dirArg(directives []lux.DirectiveInfo, name, fallback string) string {
	for _, d := range directives {
		if d.Name == name && d.Arg != "" {
			return d.Arg
		}
	}
	return fallback
}
