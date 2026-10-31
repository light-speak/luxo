package lux

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkMembershipSQL(b *testing.B) {
	type state string
	for _, count := range []int{0, 1, 128} {
		text := make([]string, count)
		enums := make([]state, count)
		flags := make([]bool, count)
		stamps := make([]time.Time, count)
		for i := range count {
			text[i], enums[i], flags[i], stamps[i] = "OPEN", "OPEN", true, time.Unix(int64(i), 0)
		}
		b.Run(fmt.Sprintf("%d/string", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NewStringField("value").In(text...).ToSQL(1)
			}
		})
		b.Run(fmt.Sprintf("%d/enum", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NewStringField("value").InEnum(enums...).ToSQL(1)
			}
		})
		b.Run(fmt.Sprintf("%d/bool", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NewBoolField("value").In(flags...).ToSQL(1)
			}
		})
		b.Run(fmt.Sprintf("%d/time", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				NewTimeField("value").In(stamps...).ToSQL(1)
			}
		})
	}
}

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

// The two predicates select the same rows, including nullable Boolean columns.
// This measures SQL rendering only, not construction or database execution.
func BenchmarkBooleanConditionToSQL(b *testing.B) {
	field := NewBoolField("enabled")
	for _, test := range []struct {
		name      string
		condition Condition
	}{
		{"equal_false", field.Eq(false)},
		{"not_equal_true", field.Neq(true)},
		{"is_null", field.IsNull()},
		{"is_not_null", field.IsNotNull()},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				test.condition.ToSQL(1)
			}
		})
	}
}

func BenchmarkQueryBooleanNodes(b *testing.B) {
	for _, sample := range []struct {
		name      string
		condition Condition
	}{
		{"constant", BoolCondition(true)},
		{"negated_constant", Not(BoolCondition(true))},
		{"negated_comparison", Not(NewIntField("id").Eq(42))},
		{"negated_group", Not(AllOf(NewBoolField("enabled").Eq(true), NewIntField("id").Gt(42)))},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if sql, _ := sample.condition.ToSQL(1); sql == "" {
					b.Fatal("empty predicate")
				}
			}
		})
	}
}

func BenchmarkBuildGroupBySQL(b *testing.B) {
	keys := []string{"status"}
	columns := []GroupAgg{{Col: "status"}, {Fn: "COUNT", Alias: "count"}, {Fn: "SUM", Col: "points", Alias: "total"}}
	where := []Condition{NewIntField("owner_id").Eq(7)}
	for _, test := range []struct {
		name   string
		having []Condition
	}{
		{"none", nil},
		{"count", []Condition{RawCondition("COUNT(*) > $1", int64(1))}},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				BuildGroupBySQL("orders", keys, columns, where, nil, 0, 0, test.having...)
			}
		})
	}
}
