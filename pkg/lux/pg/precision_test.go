package pg

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func TestPostgresDateTimeStoragePrecision(t *testing.T) {
	db := testDB(t)
	if _, err := Exec(t.Context(), db, "CREATE TABLE datetime_precision(stamp timestamptz NOT NULL, optional_stamp timestamptz, history timestamptz[] NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		name  string
		value time.Time
	}{
		{"microsecond", time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)},
		{"nanosecond", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)},
		{"before_epoch", time.Unix(-1, 999999789).UTC()},
		{"timezone", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 9*60*60))},
		{"year_zero", time.Date(0, 1, 1, 0, 0, 0, 123456789, time.UTC)},
		{"last_year", time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, mode := range []pgx.QueryExecMode{pgx.QueryExecModeCacheStatement, pgx.QueryExecModeCacheDescribe, pgx.QueryExecModeDescribeExec, pgx.QueryExecModeExec, pgx.QueryExecModeSimpleProtocol} {
				t.Run(mode.String(), func(t *testing.T) { checkStoredDateTimePrecision(t, db, mode, sample.value) })
			}
		})
	}
}

func checkStoredDateTimePrecision(t *testing.T, db *DB, mode pgx.QueryExecMode, value time.Time) {
	t.Helper()
	want := value.Truncate(time.Microsecond)
	for _, input := range []any{value, &value} {
		for _, optional := range []*time.Time{nil, &value} {
			list := []time.Time{value, time.Unix(0, 0).UTC()}
			var got time.Time
			var gotOptional *time.Time
			var history []time.Time
			row := db.queryRowBound(t.Context(), "INSERT INTO datetime_precision VALUES($1,$2,$3) RETURNING stamp,optional_stamp,history", mode, input, optional, list)
			if err := row.Scan(&got, &gotOptional, &history); err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) || len(history) != 2 || !history[0].Equal(want) || !history[1].Equal(list[1]) {
				t.Fatal("stored DateTime precision changed", want, got, history)
			}
			if (optional == nil) != (gotOptional == nil) || gotOptional != nil && !gotOptional.Equal(want) {
				t.Fatal("nullable DateTime precision changed", gotOptional)
			}
			if !list[0].Equal(value) {
				t.Fatal("driver modified caller-owned timestamp list")
			}
		}
	}
}

func TestPostgresJSONColumnStorage(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	dialect := Dialect{}
	ddl := "CREATE TABLE json_storage(data " + dialect.ColumnType("JSON", false, nil) + ", entries " + dialect.ArrayColumnType("JSON") + ")"
	if _, err := Exec(ctx, db, ddl); err != nil {
		t.Fatal(err)
	}
	value := json.RawMessage(`{"integer":123456789012345678901234567890,"decimal":0.12345678901234567890123456789}`)
	entries := []json.RawMessage{value, json.RawMessage("null"), json.RawMessage(`"hello"`)}
	if _, err := Exec(ctx, db, "INSERT INTO json_storage VALUES($1,$2)", value, entries); err != nil {
		t.Fatal(err)
	}
	got, err := db.QueryScalar[json.RawMessage](ctx, "SELECT data FROM json_storage")
	if err != nil || !strings.Contains(string(got), "123456789012345678901234567890") || !strings.Contains(string(got), "0.12345678901234567890123456789") {
		t.Fatalf("JSON numeric precision: %s, %v", got, err)
	}
	list, err := db.QueryScalar[[]json.RawMessage](ctx, "SELECT entries FROM json_storage")
	if err != nil || len(list) != 3 {
		t.Fatalf("JSON array: %s, %v", list, err)
	}
	if string(list[0]) != string(got) || string(list[1]) != "null" || string(list[2]) != `"hello"` {
		t.Fatalf("JSON array values: %s", list)
	}
}

func TestPostgresJSONColumnTextMigration(t *testing.T) {
	for _, columnType := range []string{"JSONB", "JSONB[]"} {
		t.Run(columnType, func(t *testing.T) {
			db := testDB(t)
			ctx := t.Context()
			oldType, value := "TEXT", `'123456789012345678901234567890'`
			if columnType == "JSONB[]" {
				oldType, value = "TEXT[]", `ARRAY['123456789012345678901234567890','null']`
			}
			if _, err := Exec(ctx, db, "CREATE TABLE old_json(data "+oldType+")"); err != nil {
				t.Fatal(err)
			}
			if _, err := Exec(ctx, db, "INSERT INTO old_json VALUES ("+value+")"); err != nil {
				t.Fatal(err)
			}
			if _, err := Exec(ctx, db, (Dialect{}).AlterColumnType("old_json", "data", columnType)); err != nil {
				t.Fatal(err)
			}
			actual, err := db.QueryScalar[string](ctx, "SELECT pg_typeof(data)::text FROM old_json")
			if err != nil || actual != strings.ToLower(columnType) {
				t.Fatalf("type=%s error=%v", actual, err)
			}
			stored, err := db.QueryScalar[string](ctx, "SELECT data::text FROM old_json")
			if err != nil || !strings.Contains(stored, "123456789012345678901234567890") {
				t.Fatalf("converted data=%s error=%v", stored, err)
			}
		})
	}
}

func TestPostgresJSONMigrationRejectsInvalidText(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	if _, err := Exec(ctx, db, "CREATE TABLE invalid_json(data TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := Exec(ctx, db, "INSERT INTO invalid_json VALUES ('not-json')"); err != nil {
		t.Fatal(err)
	}
	if _, err := Exec(ctx, db, (Dialect{}).AlterColumnType("invalid_json", "data", "JSONB")); err == nil {
		t.Fatal("invalid JSON was silently migrated")
	}
	actual, err := db.QueryScalar[string](ctx, "SELECT pg_typeof(data)::text || ':' || data FROM invalid_json")
	if err != nil || actual != "text:not-json" {
		t.Fatalf("failed conversion changed column: %s, %v", actual, err)
	}
}

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
