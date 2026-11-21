package pg

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"math"
	"testing"
	"time"
)

func TestDurationArgumentPrecision(t *testing.T) {
	for _, value := range []time.Duration{1, 999, 1001, -1, -999, -1001, math.MinInt64, math.MaxInt64} {
		list := []time.Duration{time.Microsecond, value}
		pointers := []*time.Duration{nil, &value}
		for _, input := range []any{value, &value, list, pointers, &list, &pointers} {
			if got, err := bindArguments([]any{int64(7), input}); !errors.Is(err, ErrDurationPrecision) || got != nil {
				t.Fatalf("inexact duration reached driver: input=%v result=%v err=%v", input, got, err)
			}
		}
	}
}

func TestDurationArgumentsPreserveExactValuesAndOwnership(t *testing.T) {
	for _, value := range []time.Duration{0, time.Microsecond, -time.Microsecond, math.MaxInt64 / 1000 * 1000, math.MinInt64 / 1000 * 1000} {
		list := []time.Duration{value}
		pointers := []*time.Duration{nil, &value}
		for _, input := range []any{value, &value, list, pointers, &list, &pointers} {
			args := []any{input}
			got, err := bindArguments(args)
			if err != nil || &got[0] != &args[0] || list[0] != value {
				t.Fatal("exact values copied or changed", got, err)
			}
		}
	}
	for _, input := range []any{(*time.Duration)(nil), []time.Duration(nil), []time.Duration{}, []*time.Duration(nil), []*time.Duration{}, (*[]time.Duration)(nil), (*[]*time.Duration)(nil)} {
		args := []any{input}
		if got, err := bindArguments(args); err != nil || &got[0] != &args[0] {
			t.Fatal("null or empty value changed", got, err)
		}
	}
	bad := time.Duration(1)
	args := []any{decimal.New(123, -2), bad}
	if got, err := bindArguments(args); got != nil || !errors.Is(err, ErrDurationPrecision) {
		t.Fatal("partial arguments escaped", got, err)
	}
	if _, ok := args[0].(decimal.Decimal); !ok {
		t.Fatal("caller decimal changed")
	}
}

func TestDurationWriteRejectsBeforeDriver(t *testing.T) {
	db := &DB{}
	for _, call := range []func() error{
		func() error {
			_, err := Exec(t.Context(), db, "INSERT INTO readings(elapsed) VALUES($1)", time.Nanosecond)
			return err
		},
		func() error { _, err := QueryRaw(t.Context(), db, "SELECT $1::interval", time.Nanosecond); return err },
		func() error {
			_, err := QueryScalar[time.Duration](t.Context(), db, "SELECT $1::interval", time.Nanosecond)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrDurationPrecision) {
			t.Fatal("driver reached or cause lost", err)
		}
	}
}

func TestDurationBindingDoesNotAllocate(t *testing.T) {
	args := []any{[]time.Duration{time.Microsecond, -time.Microsecond}}
	if allocs := testing.AllocsPerRun(1000, func() {
		got, err := bindArguments(args)
		if err != nil || &got[0] != &args[0] {
			t.Fatal("invalid binding", err)
		}
	}); allocs != 0 {
		t.Fatal("duration validation allocated", allocs)
	}
}

type durationQueryRewriter struct {
	values []any
	err    error
}

func (r durationQueryRewriter) RewriteQuery(context.Context, *pgx.Conn, string, []any) (string, []any, error) {
	return "SELECT $1::interval", r.values, r.err
}
func TestDurationQueryRewriterCannotBypassBinding(t *testing.T) {
	for _, input := range []pgx.QueryRewriter{
		pgx.NamedArgs{"elapsed": time.Duration(1001)},
		pgx.StrictNamedArgs{"elapsed": time.Duration(1001)},
		durationQueryRewriter{values: []any{time.Duration(1001)}},
	} {
		got, err := bindArguments([]any{input})
		if err != nil {
			t.Fatal(err)
		}
		rewritten, ok := got[0].(pgx.QueryRewriter)
		if !ok {
			t.Fatal("query rewriter lost")
		}
		if _, values, err := rewritten.RewriteQuery(t.Context(), nil, "SELECT @elapsed::interval", nil); !errors.Is(err, ErrDurationPrecision) || values != nil {
			t.Fatal("rewriter bypassed duration boundary", values, err)
		}
	}
}

func TestDurationQueryRewriterPreservesErrorsAndOwnership(t *testing.T) {
	cause := errors.New("rewrite failed")
	exact := time.Microsecond
	original := decimal.New(123, -2)
	for _, sample := range []durationQueryRewriter{{err: cause}, {values: []any{exact, original}}} {
		got, err := bindArguments([]any{sample})
		if err != nil {
			t.Fatal(err)
		}
		sql, values, err := got[0].(pgx.QueryRewriter).RewriteQuery(t.Context(), nil, "SELECT 1", nil)
		if sample.err != nil {
			if !errors.Is(err, cause) || sql != "" || values != nil {
				t.Fatal("rewrite error lost", sql, values, err)
			}
			continue
		}
		if err != nil || sql != "SELECT $1::interval" || values[0] != exact || values[1].(pgtype.Numeric).Exp != -2 {
			t.Fatal("rewritten values changed", sql, values, err)
		}
		if _, ok := sample.values[1].(decimal.Decimal); !ok {
			t.Fatal("rewriter-owned arguments changed")
		}
	}
}

func TestDurationListUnrolledBoundaries(t *testing.T) {
	for length := 0; length <= 13; length++ {
		values := make([]time.Duration, length)
		for i := range values {
			if i%2 == 0 {
				values[i] = time.Duration(math.MaxInt64 / 1000 * 1000)
			} else {
				values[i] = time.Duration(math.MinInt64 / 1000 * 1000)
			}
		}
		if err := validateDurationArgument(values); err != nil {
			t.Fatal(length, err)
		}
		for i := range values {
			old := values[i]
			values[i]++
			if err := validateDurationArgument(values); !errors.Is(err, ErrDurationPrecision) {
				t.Fatal(length, i, err)
			}
			values[i] = old
		}
	}
	for _, values := range [][]time.Duration{{1001, -1001, 0, 0}, {-1, 1, 0, 0}, {0, 0, 1, -1}} {
		if err := validateDurationArgument(values); !errors.Is(err, ErrDurationPrecision) {
			t.Fatal("opposite signs cancelled", values, err)
		}
	}
}
