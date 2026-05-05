package lux

import "fmt"

// PlaceholderFn generates a parameter placeholder for the given index.
// Default: PostgreSQL ($1, $2...). Override for MySQL (?).
var PlaceholderFn = func(index int) string {
	return fmt.Sprintf("$%d", index)
}

// AggregateSQL generates a SQL aggregate query for computed fields.
// Uses PlaceholderFn for database-agnostic parameter binding.
func AggregateSQL(fn, table, fkCol, targetCol string) string {
	ph := PlaceholderFn(1)
	col := "*"
	if targetCol != "" {
		col = targetCol
	}
	if fn == "COUNT" {
		return fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = %s", table, fkCol, ph)
	}
	return fmt.Sprintf("SELECT COALESCE(%s(%s), 0) FROM %s WHERE %s = %s", fn, col, table, fkCol, ph)
}
