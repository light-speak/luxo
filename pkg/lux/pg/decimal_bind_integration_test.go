package pg

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

func TestDecimalDatabaseBindings(t *testing.T) {
	db := testDB(t)
	for _, sample := range []struct {
		value    decimal.Decimal
		expected string
	}{
		{decimal.New(123, -2), "1.23"}, {decimal.New(-123, -2), "-1.23"},
		{decimal.New(9, 131071), "9e131071"}, {decimal.New(1, -16383), "1e-16383"},
		{decimal.New(10, -16384), "1e-16383"}, {decimal.New(0, 2147483647), "0"},
	} {
		// The independent expected text stays inside PostgreSQL's lexical scale limit.
		ok, err := QueryScalar[bool](t.Context(), db, "SELECT $1::numeric = $2::numeric", sample.value, sample.expected)
		if err != nil || !ok {
			t.Fatal("database value changed", sample.value.Exponent(), err)
		}
	}
	value := decimal.New(123, -2)
	for _, sample := range []struct {
		argument any
		sql      string
	}{
		{(*decimal.Decimal)(nil), "SELECT $1::numeric IS NULL"},
		{[]decimal.Decimal(nil), "SELECT $1::numeric[] IS NULL"},
		{[]decimal.Decimal{}, "SELECT cardinality($1::numeric[]) = 0"},
		{[]decimal.Decimal{value}, "SELECT $1::numeric[] = ARRAY[1.23::numeric]"},
		{[]*decimal.Decimal{nil, &value}, "SELECT $1::numeric[] IS NOT DISTINCT FROM ARRAY[NULL,1.23::numeric]"},
	} {
		ok, err := QueryScalar[bool](t.Context(), db, sample.sql, sample.argument)
		if err != nil || !ok {
			t.Fatal("database array/null changed", sample.sql, err)
		}
	}
	if _, err := Exec(t.Context(), db, "CREATE TABLE decimal_binding (value numeric)"); err != nil {
		t.Fatal(err)
	}
	err := db.Tx(t.Context(), func(tx *DB) error {
		if _, err := Exec(t.Context(), tx, "INSERT INTO decimal_binding VALUES ($1)", value); err != nil {
			return err
		}
		_, err := Exec(t.Context(), tx, "INSERT INTO decimal_binding VALUES ($1)", decimal.New(1, 2147483647))
		return err
	})
	if !errors.Is(err, ErrDecimalRange) {
		t.Fatal("transaction accepted invalid exponent", err)
	}
	count, err := QueryScalar[int64](t.Context(), db, "SELECT count(*) FROM decimal_binding")
	if err != nil || count != 0 {
		t.Fatal("failed transaction did not roll back", count, err)
	}
}

func TestDecimalDatabaseQueryModes(t *testing.T) {
	db := testDB(t)
	value := decimal.New(123, -2)
	list := []decimal.Decimal{value}
	pointers := []*decimal.Decimal{nil, &value}
	for _, mode := range []pgx.QueryExecMode{
		pgx.QueryExecModeCacheStatement, pgx.QueryExecModeCacheDescribe,
		pgx.QueryExecModeDescribeExec, pgx.QueryExecModeExec, pgx.QueryExecModeSimpleProtocol,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			for _, sample := range []struct {
				argument any
				sql      string
			}{
				{value, "SELECT $1::numeric = 1.23::numeric"},
				{&value, "SELECT $1::numeric = 1.23::numeric"},
				{&list, "SELECT $1::numeric[] = ARRAY[1.23::numeric]"},
				{&pointers, "SELECT $1::numeric[] IS NOT DISTINCT FROM ARRAY[NULL,1.23::numeric]"},
				{(*decimal.Decimal)(nil), "SELECT $1::numeric IS NULL"},
				{(*[]decimal.Decimal)(nil), "SELECT $1::numeric[] IS NULL"},
				{decimal.New(10, -16384), "SELECT $1::numeric = '1e-16383'::numeric"},
			} {
				ok, err := QueryScalar[bool](t.Context(), db, sample.sql, mode, sample.argument)
				if err != nil || !ok {
					t.Fatal("binding changed by query mode", sample.sql, err)
				}
			}
			_, err := QueryScalar[bool](t.Context(), db, "SELECT $1::numeric = 0", mode, decimal.New(1, -2147483648))
			if !errors.Is(err, ErrDecimalRange) {
				t.Fatal("invalid exponent accepted", err)
			}
		})
	}
}

func TestDecimalDatabaseConditions(t *testing.T) {
	db := testDB(t)
	if _, err := Exec(t.Context(), db, "CREATE TABLE decimal_conditions (value numeric)"); err != nil {
		t.Fatal(err)
	}
	value := decimal.New(123, -2)
	if _, err := Exec(t.Context(), db, "INSERT INTO decimal_conditions VALUES ($1)", value); err != nil {
		t.Fatal(err)
	}
	field := lux.NewDecimalField("value")
	for _, condition := range []lux.Condition{field.Eq(value), field.In(value), field.Between(value, value)} {
		query := NewQuery[testUser](db, "decimal_conditions", nil, []lux.Condition{condition})
		count, err := query.Count(t.Context())
		if err != nil || count != 1 {
			t.Fatal("condition value changed", count, err)
		}
		got, err := query.SumDecimal(t.Context(), "value")
		if err != nil || lux.CompareDecimal(got, value) != 0 {
			t.Fatal("aggregate value changed", err)
		}
	}
	condition, err := field.FilterOp("eq", "1e2147483647")
	if err != nil {
		t.Fatal("protocol-valid Decimal rejected before backend binding", err)
	}
	query := NewQuery[testUser](db, "decimal_conditions", nil, []lux.Condition{condition})
	if _, err := query.Count(t.Context()); !errors.Is(err, ErrDecimalRange) {
		t.Fatal(err)
	}
	var result decimal.Decimal
	err = query.AggregateBatchTo(t.Context(), []AggregateDestination{{Decimal: &result}}, lux.AggregateSpec{Function: lux.AggregateSum, Column: "value"})
	if !errors.Is(err, ErrDecimalRange) {
		t.Fatal("batch skipped range check", err)
	}
}
