package pg

import (
	"math"
	"testing"

	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

func TestQueryNumericPrecision(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	if _, err := db.conn.Exec(ctx, "CREATE TABLE aggregate_precision (i bigint, f double precision, d numeric)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(ctx, "INSERT INTO aggregate_precision VALUES (9007199254740993, 1.25, 12345678901234567890.123456789), (2, 2.5, 0.000000001)"); err != nil {
		t.Fatal(err)
	}
	q := NewQuery[testUser](db, "aggregate_precision", scanUser, nil)
	if got, err := q.Sum(ctx, "i"); err != nil || got != 9007199254740995 {
		t.Fatal(got, err)
	}
	if got, err := q.Max(ctx, "i"); err != nil || got != 9007199254740993 {
		t.Fatal(got, err)
	}
	if got, err := q.SumFloat(ctx, "f"); err != nil || got != 3.75 {
		t.Fatal(got, err)
	}
	if got, err := q.Avg(ctx, "f"); err != nil || got != 1.875 {
		t.Fatal(got, err)
	}
	if got, err := q.SumDecimal(ctx, "d"); err != nil || got.String() != "12345678901234567890.12345679" {
		t.Fatal(got, err)
	}
	checkOtherNumericAggregates(t, q)
	var integer int64
	var average float64
	var exact decimal.Decimal
	err := q.AggregateBatchTo(ctx, []AggregateDestination{{Int: &integer}, {Float: &average}, {Decimal: &exact}},
		lux.AggregateSpec{Function: lux.AggregateSum, Column: "i"},
		lux.AggregateSpec{Function: lux.AggregateAvg, Column: "f"},
		lux.AggregateSpec{Function: lux.AggregateSum, Column: "d"})
	if err != nil || integer != 9007199254740995 || average != 1.875 || exact.String() != "12345678901234567890.12345679" {
		t.Fatal(integer, average, exact, err)
	}
	if _, err := db.conn.Exec(ctx, "INSERT INTO aggregate_precision (i) VALUES (9223372036854775807)"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Sum(ctx, "i"); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err := db.conn.Exec(ctx, "TRUNCATE aggregate_precision; INSERT INTO aggregate_precision VALUES (1,'Infinity','NaN')"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SumFloat(ctx, "f"); err == nil {
		t.Fatal("infinite Float accepted")
	}
	if _, err := q.SumDecimal(ctx, "d"); err == nil {
		t.Fatal("NaN Decimal accepted")
	}
	if err := q.AggregateBatchTo(ctx, []AggregateDestination{{Float: &average}}, lux.AggregateSpec{Function: lux.AggregateSum, Column: "f"}); err == nil {
		t.Fatal("infinite batch accepted")
	}
	if _, err := q.SumFloat(ctx, "missing"); err == nil {
		t.Fatal("SQL error lost")
	}
	if _, err := q.SumDecimal(ctx, "missing"); err == nil {
		t.Fatal("SQL error lost")
	}
}

func checkOtherNumericAggregates(t *testing.T, q *Query[testUser]) {
	t.Helper()
	if got, err := q.MinFloat(t.Context(), "f"); err != nil || got != 1.25 {
		t.Fatal(got, err)
	}
	if got, err := q.MaxFloat(t.Context(), "f"); err != nil || got != 2.5 {
		t.Fatal(got, err)
	}
	if got, err := q.MinDecimal(t.Context(), "d"); err != nil || got.String() != "0.000000001" {
		t.Fatal(got, err)
	}
	if got, err := q.MaxDecimal(t.Context(), "d"); err != nil || got.String() != "12345678901234567890.123456789" {
		t.Fatal(got, err)
	}
	if got, err := q.AvgDecimal(t.Context(), "d"); err != nil || got.String() != "6172839450617283945.061728395" {
		t.Fatal(got, err)
	}
}

func TestAggregateDestinationValidation(t *testing.T) {
	var integer int64
	floating := math.NaN()
	var exact decimal.Decimal
	for _, target := range []AggregateDestination{{}, {Int: &integer, Float: &floating}} {
		if _, err := target.scanTarget(); err == nil {
			t.Fatal("ambiguous target accepted")
		}
	}
	for _, target := range []AggregateDestination{{Float: &floating}, {Decimal: &exact}} {
		if err := target.finish(); err == nil {
			t.Fatal("invalid value accepted")
		}
	}
	q := NewQuery[testUser](nil, "unused", scanUser, nil)
	if _, err := q.AggregateBatch(t.Context(), lux.AggregateSpec{Function: lux.AggregateAvg, Column: "i"}); err == nil {
		t.Fatal("integer average accepted")
	}
	if err := q.AggregateBatchTo(t.Context(), []AggregateDestination{{Int: &integer}}, lux.AggregateSpec{Function: lux.AggregateAvg, Column: "i"}); err == nil {
		t.Fatal("integer average destination accepted")
	}
	if err := q.AggregateBatchTo(t.Context(), nil, lux.AggregateSpec{Function: lux.AggregateCount}); err == nil {
		t.Fatal("mismatched lengths accepted")
	}
	if err := q.AggregateBatchTo(t.Context(), nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	if err := q.AggregateBatchTo(t.Context(), []AggregateDestination{{}}, lux.AggregateSpec{Function: lux.AggregateCount}); err == nil {
		t.Fatal("missing target accepted")
	}
}
