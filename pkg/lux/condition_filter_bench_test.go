package lux

import (
	"github.com/light-speak/luxo/pkg/lux/datetime"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Frozen pre-contract implementation, retained only for non-equivalent benchmarks.
func (f TimeField) filterOpBefore(op, val string) Condition {
	t, err := datetime.ParseRFC3339(val)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(t)
	case "ne":
		return f.Neq(t)
	case "gt", "after":
		return f.After(t)
	case "gte":
		return f.Gte(t)
	case "lt", "before":
		return f.Before(t)
	case "lte":
		return f.Lte(t)
	default:
		return f.Eq(t)
	}
}

func (f IntField) filterOpBefore(op, val string) Condition {
	v, err := parseInt64(val)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(v)
	case "ne":
		return f.Neq(v)
	case "gt":
		return f.Gt(v)
	case "gte":
		return f.Gte(v)
	case "lt":
		return f.Lt(v)
	case "lte":
		return f.Lte(v)
	default:
		return f.Eq(v)
	}
}

func (f StringField) filterOpBefore(op, val string) Condition {
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(val)
	case "ne":
		return f.Neq(val)
	case "contains":
		return f.Like("%" + EscapeLike(val) + "%")
	case "startswith":
		return f.Like(EscapeLike(val) + "%")
	case "endswith":
		return f.Like("%" + EscapeLike(val))
	default:
		return f.Eq(val)
	}
}

func (f FloatField) filterOpBefore(op, val string) Condition {
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return falseCond{}
	}
	switch strings.ToLower(op) {
	case "eq":
		return f.Eq(v)
	case "ne":
		return f.Neq(v)
	case "gt":
		return f.Gt(v)
	case "gte":
		return f.Gte(v)
	case "lt":
		return f.Lt(v)
	case "lte":
		return f.Lte(v)
	default:
		return f.Eq(v)
	}
}

func (f BoolField) filterOpBefore(op, val string) Condition {
	v := strings.EqualFold(val, "true") || val == "1"
	if strings.EqualFold(op, "ne") {
		return f.Eq(!v)
	}
	return f.Eq(v)
}

func (f SearchField) filterOpBefore(op, val string) Condition {
	if strings.EqualFold(op, "match") || strings.EqualFold(op, "search") {
		return f.Match(val)
	}
	return f.StringField.filterOpBefore(op, val)
}
func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

type falseCond struct{}

func (falseCond) ToSQL(int) (string, []any) { return "FALSE", nil }

var filterBenchmarkSink Condition

func BenchmarkStrictFilterContract(b *testing.B) {
	for _, sample := range []struct {
		name, op, value string
		before          func(string, string) Condition
		after           func(string, string) (Condition, error)
	}{
		{"Int", "gte", "42", NewIntField("v").filterOpBefore, NewIntField("v").FilterOp},
		{"Float", "gte", "1.25", NewFloatField("v").filterOpBefore, NewFloatField("v").FilterOp},
		{"Bool", "eq", "true", NewBoolField("v").filterOpBefore, NewBoolField("v").FilterOp},
		{"Time", "gte", "2026-09-18T00:00:00.123456789Z", NewTimeField("v").filterOpBefore, NewTimeField("v").FilterOp},
		{"String", "eq", "luxo", NewStringField("v").filterOpBefore, NewStringField("v").FilterOp},
		{"String1KiB", "eq", strings.Repeat("中", 342), NewStringField("v").filterOpBefore, NewStringField("v").FilterOp},
		{"Contains", "contains", "100%_off", NewStringField("v").filterOpBefore, NewStringField("v").FilterOp},
		{"Search", "match", "luxo protocol", NewSearchField("v").filterOpBefore, NewSearchField("v").FilterOp},
	} {
		previous := sample.before(sample.op, sample.value)
		current, err := sample.after(sample.op, sample.value)
		if err != nil {
			b.Fatal(err)
		}
		oldSQL, oldArgs := previous.ToSQL(1)
		newSQL, newArgs := current.ToSQL(1)
		if oldSQL != newSQL || !reflect.DeepEqual(oldArgs, newArgs) {
			b.Fatal("legal SQL changed")
		}
		b.Run(sample.name+"/Before", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				filterBenchmarkSink = sample.before(sample.op, sample.value)
			}
		})
		b.Run(sample.name+"/After", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				filterBenchmarkSink, err = sample.after(sample.op, sample.value)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
