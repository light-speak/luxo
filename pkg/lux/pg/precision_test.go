package pg

import (
	"math"
	"slices"
	"testing"

	"github.com/shopspring/decimal"
)

// Use the owned per-test schema and real PostgreSQL storage, not codec self-roundtrips.
func precisionDB(t *testing.T) *DB {
	t.Helper()
	db := testDB(t)
	_, err := Exec(t.Context(), db, `CREATE TABLE numeric_precision (
		id INT PRIMARY KEY,
		integer_value BIGINT,
		integer_values BIGINT[],
		float_value DOUBLE PRECISION,
		decimal_value NUMERIC
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPostgresInt64StoragePrecision(t *testing.T) {
	db := precisionDB(t)
	values := []int64{math.MinInt64, math.MinInt64 + 1, -(1 << 53) - 1, -1, 0, 1, (1 << 53) + 1, math.MaxInt64 - 1, math.MaxInt64}
	for index, value := range values {
		_, err := Exec(t.Context(), db, "INSERT INTO numeric_precision (id, integer_value) VALUES ($1, $2)", index, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.QueryScalar[int64](t.Context(), "SELECT integer_value FROM numeric_precision WHERE id=$1", index)
		if err != nil || got != value {
			t.Fatalf("stored Int64 %d became %d: %v", value, got, err)
		}
	}
	for index, input := range [][]int64{values, {}} {
		_, err := Exec(t.Context(), db, "UPDATE numeric_precision SET integer_values=$1 WHERE id=$2", input, index)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.QueryScalar[[]int64](t.Context(), "SELECT integer_values FROM numeric_precision WHERE id=$1", index)
		if err != nil || got == nil || !slices.Equal(got, input) {
			t.Fatalf("stored Int64 array %v became %v: %v", input, got, err)
		}
	}
}

func TestPostgresFiniteFloatStoragePrecision(t *testing.T) {
	db := precisionDB(t)
	values := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64,
		-math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, math.Nextafter(1, 2), 0.1}
	for index, value := range values {
		_, err := Exec(t.Context(), db, "INSERT INTO numeric_precision (id, float_value) VALUES ($1, $2)", index, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.QueryScalar[float64](t.Context(), "SELECT float_value FROM numeric_precision WHERE id=$1", index)
		if err != nil || math.Float64bits(got) != math.Float64bits(value) {
			t.Fatalf("stored Float %016x became %016x: %v", math.Float64bits(value), math.Float64bits(got), err)
		}
	}
}

func TestPostgresNullableNumericStoragePrecision(t *testing.T) {
	db := precisionDB(t)
	if _, err := Exec(t.Context(), db, "INSERT INTO numeric_precision (id) VALUES (0)"); err != nil {
		t.Fatal(err)
	}
	value, err := db.QueryScalar[*int64](t.Context(), "SELECT integer_value FROM numeric_precision WHERE id=0")
	if err != nil || value != nil {
		t.Fatalf("SQL null became a present Int64: %v / %v", value, err)
	}
	values, err := db.QueryScalar[[]int64](t.Context(), "SELECT integer_values FROM numeric_precision WHERE id=0")
	if err != nil || values != nil {
		t.Fatalf("SQL null became a present array: %v / %v", values, err)
	}
	for _, input := range []int64{0, math.MaxInt64} {
		if _, err := Exec(t.Context(), db, "UPDATE numeric_precision SET integer_value=$1 WHERE id=0", input); err != nil {
			t.Fatal(err)
		}
		value, err = db.QueryScalar[*int64](t.Context(), "SELECT integer_value FROM numeric_precision WHERE id=0")
		if err != nil || value == nil || *value != input {
			t.Fatalf("stored nullable Int64 %d became %v: %v", input, value, err)
		}
	}
}

func TestPostgresDecimalStoragePrecision(t *testing.T) {
	db := precisionDB(t)
	for index, text := range []string{"0", "-0.000000000000000000000000000000000000001",
		"123456789012345678901234567890.12345678901234567890123456789",
		"-123456789012345678901234567890.12345678901234567890123456789", "1e100", "1e-100"} {
		value, err := decimal.NewFromString(text)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Exec(t.Context(), db, "INSERT INTO numeric_precision (id, decimal_value) VALUES ($1, $2)", index, value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.QueryScalar[decimal.Decimal](t.Context(), "SELECT decimal_value FROM numeric_precision WHERE id=$1", index)
		if err != nil || !got.Equal(value) {
			t.Fatalf("stored Decimal %s became %s: %v", value, got, err)
		}
	}
}
