package pg

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDurationDatabaseWriteBindings(t *testing.T) {
	db := testDB(t)
	if _, err := Exec(t.Context(), db, "CREATE TABLE duration_writes(id bigint PRIMARY KEY, elapsed interval, laps interval[])"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement, pgx.QueryExecModeCacheDescribe, pgx.QueryExecModeDescribeExec, pgx.QueryExecModeExec, pgx.QueryExecModeSimpleProtocol} {
		t.Run(mode.String(), func(t *testing.T) { checkDurationDatabaseMode(t, db, mode) })
	}
}

func checkDurationDatabaseMode(t *testing.T, db *DB, mode pgx.QueryExecMode) {
	t.Helper()
	for _, value := range []time.Duration{0, time.Microsecond, -time.Microsecond, math.MaxInt64 / 1000 * 1000, math.MinInt64 / 1000 * 1000} {
		for _, input := range []any{value, &value} {
			got, err := QueryScalar[time.Duration](t.Context(), db, "SELECT $1::interval", mode, input)
			if err != nil || got != value {
				t.Fatal("exact duration changed", got, value, err)
			}
		}
	}
	values := []time.Duration{time.Microsecond, -time.Microsecond, 0, 2 * time.Microsecond, 0}
	for _, input := range []any{values, &values} {
		got, err := QueryScalar[[]time.Duration](t.Context(), db, "SELECT $1::interval[]", mode, input)
		if err != nil || !slices.Equal(got, values) {
			t.Fatal("exact list changed", got, err)
		}
	}
	for _, sample := range []struct {
		input any
		sql   string
	}{
		{(*time.Duration)(nil), "SELECT $1::interval IS NULL"}, {[]time.Duration(nil), "SELECT $1::interval[] IS NULL"},
		{(*[]time.Duration)(nil), "SELECT $1::interval[] IS NULL"}, {[]time.Duration{}, "SELECT cardinality($1::interval[]) = 0"},
	} {
		ok, err := QueryScalar[bool](t.Context(), db, sample.sql, mode, sample.input)
		if err != nil || !ok {
			t.Fatal("null/empty duration changed", sample.sql, err)
		}
	}
	checkDurationDatabaseRejection(t, db, mode)
	checkDurationNamedArguments(t, db, mode)
}

func checkDurationDatabaseRejection(t *testing.T, db *DB, mode pgx.QueryExecMode) {
	t.Helper()
	bad := time.Duration(1001)
	list := []time.Duration{time.Microsecond, time.Microsecond, -time.Microsecond, 0, bad}
	pointers := []*time.Duration{nil, &bad}
	for _, sample := range []struct {
		value  any
		column string
	}{{bad, "elapsed"}, {&bad, "elapsed"}, {list, "laps"}, {&list, "laps"}, {pointers, "laps"}, {&pointers, "laps"}} {
		_, err := Exec(t.Context(), db, "INSERT INTO duration_writes(id,"+sample.column+") VALUES(1,$1)", mode, sample.value)
		if !errors.Is(err, ErrDurationPrecision) {
			t.Fatal("inexact duration was not explicitly rejected", err)
		}
	}
	err := db.Tx(t.Context(), func(tx *DB) error {
		if _, err := Exec(t.Context(), tx, "INSERT INTO duration_writes(id,elapsed) VALUES(2,$1)", mode, time.Microsecond); err != nil {
			return err
		}
		_, err := Exec(t.Context(), tx, "UPDATE duration_writes SET elapsed=$1 WHERE id=2", mode, bad)
		return err
	})
	if !errors.Is(err, ErrDurationPrecision) {
		t.Fatal("transaction lost precision error", err)
	}
	count, err := QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM duration_writes")
	if err != nil || count != 0 {
		t.Fatal("failed write or transaction changed stored data", count, err)
	}
}

func checkDurationNamedArguments(t *testing.T, db *DB, mode pgx.QueryExecMode) {
	t.Helper()
	for _, strict := range []bool{false, true} {
		for _, value := range []time.Duration{time.Microsecond, 1001} {
			var args pgx.QueryRewriter = pgx.NamedArgs{"elapsed": value}
			if strict {
				args = pgx.StrictNamedArgs{"elapsed": value}
			}
			got, err := QueryScalar[time.Duration](t.Context(), db, "SELECT @elapsed::interval", mode, args)
			if value == 1001 {
				if !errors.Is(err, ErrDurationPrecision) {
					t.Fatal("named binding bypassed precision check", got, err)
				}
			} else if err != nil || got != value {
				t.Fatal("exact named binding changed", got, err)
			}
		}
	}
}
