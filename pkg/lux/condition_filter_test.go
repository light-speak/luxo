package lux

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestFilterOpRejectsInvalidInput(t *testing.T) {
	for _, field := range []struct {
		name    string
		parse   func(string, string) (Condition, error)
		valid   string
		invalid []string
	}{
		{"int", NewIntField("v").FilterOp, "42", []string{"", "null", "1.5", "9223372036854775808", "bad"}},
		{"float", NewFloatField("v").FilterOp, "1.5", []string{"", "NaN", "Inf", "-Infinity", "1e309", "1e-9999", "0x1p2", "1_000"}},
		{"bool", NewBoolField("v").FilterOp, "true", []string{"", "truue", "1", "0", "TRUE", "null"}},
		{"time", NewTimeField("v").FilterOp, "2026-09-18T12:00:00.123456789Z", []string{"", "bad", "2026-09-18", "2026-09-18T00:00:00.1234567890Z"}},
		{"string", NewStringField("v").FilterOp, "valid", []string{string([]byte{0xff})}},
		{"search", NewSearchField("v").FilterOp, "valid", []string{string([]byte{0xff})}},
		{"uuid", NewUUIDField("v").FilterOp, "531f2d00-20e5-47f2-b874-22b6fc76328b", []string{"bad", "531f2d0020e547f2b87422b6fc76328b", "531f2d00_20e5-47f2-b874-22b6fc76328b"}},
		{"decimal", NewDecimalField("v").FilterOp, "12345678901234567890.123456789", []string{"", "bad", "NaN", "Infinity"}},
	} {
		t.Run(field.name, func(t *testing.T) {
			for _, value := range field.invalid {
				condition, err := field.parse("eq", value)
				if condition != nil || !errors.Is(err, ErrInvalidFilterValue) {
					t.Errorf("value %q: condition=%v error=%v", value, condition, err)
				}
			}
			for _, op := range []string{"", "unknown", "in", "isnull", "eq OR 1=1"} {
				condition, err := field.parse(op, field.valid)
				if condition != nil || !errors.Is(err, ErrInvalidFilterOperator) {
					t.Errorf("operator %q: condition=%v error=%v", op, condition, err)
				}
			}
		})
	}
}

func TestFilterOpRejectsInapplicableOperators(t *testing.T) {
	for _, parse := range []func(string, string) (Condition, error){
		NewBoolField("v").FilterOp, NewIntField("v").FilterOp,
		NewFloatField("v").FilterOp, NewTimeField("v").FilterOp,
	} {
		if condition, err := parse("contains", "1"); condition != nil || err == nil {
			t.Fatalf("inapplicable pattern filter: %v, %v", condition, err)
		}
	}
	for _, op := range []string{"gt", "gte", "lt", "lte", "match"} {
		if condition, err := NewStringField("v").FilterOp(op, "x"); condition != nil || err == nil {
			t.Fatalf("inapplicable string filter %s: %v, %v", op, condition, err)
		}
	}
	if condition, err := NewSearchField("v").FilterOp("match", string([]byte{0xff})); condition != nil || err == nil {
		t.Fatalf("invalid search text: %v, %v", condition, err)
	}
}

func TestFilterOpPreservesFloatBoundaries(t *testing.T) {
	for _, value := range []string{"-0", "5e-324", "1.7976931348623157e308"} {
		condition, err := NewFloatField("v").FilterOp("eq", value)
		if err != nil {
			t.Fatal(err)
		}
		_, args := condition.ToSQL(1)
		actual := args[0].(float64)
		if value == "-0" && !math.Signbit(actual) {
			t.Fatal("lost negative zero")
		}
		if value != "-0" && actual == 0 {
			t.Fatal("lost finite nonzero value")
		}
	}
}

func requireFilter(condition Condition, err error) Condition {
	if err != nil {
		panic(err)
	}
	return condition
}

func TestFilterOpPreservesTypedValues(t *testing.T) {
	value := "531f2d00-20e5-47f2-b874-22b6fc76328b"
	for _, op := range []string{"eq", "ne"} {
		condition, err := NewUUIDField("v").FilterOp(op, value)
		if err != nil {
			t.Fatal(err)
		}
		_, args := condition.ToSQL(1)
		if got, ok := args[0].(uuid.UUID); !ok || got.String() != value {
			t.Fatalf("UUID lost type/value: %v", args)
		}
	}
	if condition, err := NewUUIDField("v").FilterOp("eq", "z31f2d00-20e5-47f2-b874-22b6fc76328b"); condition != nil || err == nil {
		t.Fatal("accepted invalid UUID hex")
	}
	condition, err := NewDecimalField("v").FilterOp("gte", "12345678901234567890.123456789")
	if err != nil {
		t.Fatal(err)
	}
	_, args := condition.ToSQL(1)
	if got, ok := args[0].(decimal.Decimal); !ok || got.String() != "12345678901234567890.123456789" {
		t.Fatalf("Decimal lost precision: %v", args)
	}
	condition, err = NewTimeField("v").FilterOp("eq", "2026-09-18T12:00:00.123456789+09:00")
	if err != nil {
		t.Fatal(err)
	}
	_, args = condition.ToSQL(1)
	if got, ok := args[0].(time.Time); !ok || got.Nanosecond() != 123456789 {
		t.Fatalf("DateTime lost nanos: %v", args)
	}
}
