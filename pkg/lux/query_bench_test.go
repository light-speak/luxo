package lux

import "testing"

// BenchmarkBuildSelectSimple benchmarks SELECT * FROM table.
func BenchmarkBuildSelectSimple(b *testing.B) {
	for b.Loop() {
		BuildSelectSQL("users", nil, nil, nil, 0, 0)
	}
}

// BenchmarkBuildSelectWithConditions benchmarks SELECT with WHERE conditions.
func BenchmarkBuildSelectWithConditions(b *testing.B) {
	conds := []Condition{
		NewIntField("id").Gt(10),
		NewStringField("role").Eq("ADMIN"),
	}
	b.ResetTimer()
	for b.Loop() {
		BuildSelectSQL("users", nil, conds, nil, 0, 0)
	}
}

// BenchmarkBuildSelectFull benchmarks SELECT with fields, conditions, order, limit, offset.
func BenchmarkBuildSelectFull(b *testing.B) {
	fields := []string{"id", "name", "email"}
	conds := []Condition{
		NewStringField("role").Eq("ADMIN"),
		NewIntField("age").Gt(18),
	}
	orderBy := []string{"created_at DESC"}
	b.ResetTimer()
	for b.Loop() {
		BuildSelectSQL("users", fields, conds, orderBy, 10, 20)
	}
}

// BenchmarkBuildUpdateSQL benchmarks UPDATE query building.
func BenchmarkBuildUpdateSQL(b *testing.B) {
	sets := []SetField{
		{Col: "name", Val: "alice"},
		{Col: "email", Val: "alice@example.com"},
	}
	conds := []Condition{NewIntField("id").Eq(1)}
	b.ResetTimer()
	for b.Loop() {
		BuildUpdateSQL("users", sets, conds)
	}
}

// BenchmarkConditionToSQL benchmarks individual condition SQL generation.
func BenchmarkConditionToSQL(b *testing.B) {
	cond := NewIntField("id").Eq(42)
	b.ResetTimer()
	for b.Loop() {
		cond.ToSQL(1)
	}
}

// BenchmarkInConditionToSQL benchmarks IN condition SQL generation.
func BenchmarkInConditionToSQL(b *testing.B) {
	cond := NewIntField("id").In(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	b.ResetTimer()
	for b.Loop() {
		cond.ToSQL(1)
	}
}
