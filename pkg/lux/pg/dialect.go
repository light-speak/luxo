package pg

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/lux"
)

// Dialect implements lux.Dialect for PostgreSQL.
type Dialect struct{}

var _ lux.Dialect = Dialect{}

func (Dialect) Name() string { return "pg" }

func (Dialect) ColumnType(luxoType string, serial bool, directives []lux.DirectiveInfo) string {
	if serial {
		return "SERIAL"
	}
	if hasDir(directives, "varchar") {
		length := "255"
		for _, d := range directives {
			if d.Name == "varchar" && d.Arg != "" {
				length = d.Arg
			}
		}
		return fmt.Sprintf("VARCHAR(%s)", length)
	}

	base := pgBaseType(luxoType)
	return pgApplyDirective(base, luxoType, directives)
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
			return "DECIMAL"
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

func (Dialect) ColumnDef(name string, col lux.ColumnInfo) string {
	var parts []string
	parts = append(parts, name+" "+col.Type)
	if !col.Nullable && !col.Serial {
		parts = append(parts, "NOT NULL")
	}
	if col.PK {
		parts = append(parts, "PRIMARY KEY")
	}
	if col.Unique {
		parts = append(parts, "UNIQUE")
	}
	return strings.Join(parts, " ")
}

func (d Dialect) CreateTable(table string, columns []lux.ColumnEntry, indexes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", table)

	var cols []string
	for _, c := range columns {
		cols = append(cols, "  "+d.ColumnDef(c.Name, c.Info))
	}
	b.WriteString(strings.Join(cols, ",\n"))
	b.WriteString("\n);\n")

	for _, idx := range indexes {
		col := strings.TrimPrefix(idx, "idx_"+table+"_")
		fmt.Fprintf(&b, "CREATE INDEX %s ON %s (%s);\n", idx, table, col)
	}
	return b.String()
}

func (Dialect) AddColumn(table, colDef string) string {
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;\n", table, colDef)
}

func (Dialect) DropColumn(table, column string) string {
	return fmt.Sprintf("-- ALTER TABLE %s DROP COLUMN %s; -- DANGEROUS: uncomment to apply\n", table, column)
}

func (Dialect) AlterColumnType(table, column, newType string) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s;\n", table, column, newType)
}

func (Dialect) SetNotNull(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;\n", table, column)
}

func (Dialect) DropNotNull(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;\n", table, column)
}

func (Dialect) AddUnique(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s_%s_key UNIQUE (%s);\n", table, table, column, column)
}

func (Dialect) DropUnique(table, column string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s_%s_key;\n", table, table, column)
}

func (Dialect) CreateIndex(index, table, column string) string {
	return fmt.Sprintf("CREATE INDEX %s ON %s (%s);\n", index, table, column)
}

func (Dialect) DropIndex(index string) string {
	return fmt.Sprintf("DROP INDEX IF EXISTS %s;\n", index)
}

func (Dialect) DropTable(table string) string {
	return fmt.Sprintf("-- DROP TABLE IF EXISTS %s; -- DANGEROUS: uncomment to apply\n", table)
}

func hasDir(directives []lux.DirectiveInfo, name string) bool {
	for _, d := range directives {
		if d.Name == name {
			return true
		}
	}
	return false
}
