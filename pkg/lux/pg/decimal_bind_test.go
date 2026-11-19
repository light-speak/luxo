package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/shopspring/decimal"
)

func TestDecimalParameterRange(t *testing.T) {
	for _, sample := range []struct {
		coefficient int64
		exponent    int32
		valid       bool
	}{
		{123, -2, true}, {-123, -2, true}, {9, 131071, true}, {10, 131071, false},
		{1, 131072, false}, {1, -16383, true}, {1, -16384, false}, {10, -16384, true},
		{100, -16385, true}, {101, -16385, false}, {1, 2147483647, false},
		{1, -2147483648, false}, {0, 2147483647, true}, {0, -2147483648, true},
	} {
		value := decimal.New(sample.coefficient, sample.exponent)
		got, err := bindDecimal(value)
		if !sample.valid {
			if !errors.Is(err, ErrDecimalRange) {
				t.Fatal("expected numeric range rejection", sample, err)
			}
			continue
		}
		if err != nil || !got.Valid || lux.CompareDecimal(value, decimal.NewFromBigInt(got.Int, got.Exp)) != 0 {
			t.Fatal("numeric value changed", sample, err)
		}
		wire, err := pgtype.NewMap().Encode(pgtype.NumericOID, pgtype.BinaryFormatCode, got, nil)
		if err != nil {
			t.Fatal("numeric encoding", sample, err)
		}
		var decoded pgtype.Numeric
		if err := pgtype.NewMap().Scan(pgtype.NumericOID, pgtype.BinaryFormatCode, wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if lux.CompareDecimal(value, decimal.NewFromBigInt(decoded.Int, decoded.Exp)) != 0 {
			t.Fatal("numeric binary changed the value", sample)
		}
	}
}

func TestDecimalArgumentOwnership(t *testing.T) {
	value := decimal.New(123, -2)
	items := []decimal.Decimal{value}
	args := []any{int64(42), value, &value, items, []*decimal.Decimal{nil, &value}}
	got, err := bindArguments(args)
	if err != nil || len(got) != len(args) {
		t.Fatal(err)
	}
	if _, ok := args[1].(decimal.Decimal); !ok || items[0].Exponent() != -2 {
		t.Fatal("caller arguments changed")
	}
	if got[1].(pgtype.Numeric).Exp != -2 || len(got[3].([]pgtype.Numeric)) != 1 || got[4].([]pgtype.Numeric)[0].Valid {
		t.Fatal("typed arguments changed")
	}
	plain := []any{int64(7), "name", []int64{1}}
	unchanged, err := bindArguments(plain)
	if err != nil || &unchanged[0] != &plain[0] {
		t.Fatal("ordinary arguments copied", err)
	}
	if got, err := bindArguments([]any{value, decimal.New(1, 2147483647)}); got != nil || !errors.Is(err, ErrDecimalRange) {
		t.Fatal("partial arguments returned", err)
	}
}

var emptyDecimalBindingSink []any

func TestEmptyDecimalBindingAllocations(t *testing.T) {
	args := []any{[]decimal.Decimal{}}
	allocations := testing.AllocsPerRun(100, func() {
		var err error
		emptyDecimalBindingSink, err = bindArguments(args)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("empty Decimal bind allocations/run: %.2f", allocations)
	if allocations > 1 {
		t.Fatalf("empty Decimal binding allocated %.2f times; want at most one caller-argument copy", allocations)
	}
	if _, ok := args[0].([]decimal.Decimal); !ok {
		t.Fatal("caller argument type changed")
	}
	bound, ok := emptyDecimalBindingSink[0].([]pgtype.Numeric)
	if !ok || bound == nil || len(bound) != 0 || &emptyDecimalBindingSink[0] == &args[0] {
		t.Fatal("empty array or caller-argument ownership changed", emptyDecimalBindingSink)
	}
}

func TestDecimalQueryRejectsBeforeDriver(t *testing.T) {
	// A nil driver deliberately proves rejection happens before any I/O.
	db := &DB{}
	value := decimal.New(1, 2147483647)
	ctx := context.Background()
	if _, err := QueryRaw(ctx, db, "SELECT $1", value); !errors.Is(err, ErrDecimalRange) {
		t.Fatal(err)
	}
	if _, err := QueryScalar[int64](ctx, db, "SELECT $1", value); !errors.Is(err, ErrDecimalRange) {
		t.Fatal(err)
	}
	if _, err := Exec(ctx, db, "SELECT $1", value); !errors.Is(err, ErrDecimalRange) {
		t.Fatal(err)
	}
}

func TestDecimalArgumentShapes(t *testing.T) {
	value := decimal.New(7, -2)
	list := []decimal.Decimal{value}
	pointers := []*decimal.Decimal{nil, &value}
	for _, input := range []any{&list, &pointers, []decimal.Decimal{}, []*decimal.Decimal{}} {
		got, changed, err := bindArgument(input)
		if err != nil || !changed || got.([]pgtype.Numeric) == nil {
			t.Fatal("lost present array", err)
		}
	}
	for _, input := range []any{[]decimal.Decimal(nil), []*decimal.Decimal(nil), (*[]decimal.Decimal)(nil), (*[]*decimal.Decimal)(nil)} {
		got, changed, err := bindArgument(input)
		if err != nil || !changed || got.([]pgtype.Numeric) != nil {
			t.Fatal("lost null array", err)
		}
	}
	var pointer *decimal.Decimal
	got, _, err := bindArgument(pointer)
	if err != nil || got.(pgtype.Numeric).Valid {
		t.Fatal("lost null scalar", err)
	}
	bad := decimal.New(1, 2147483647)
	for _, input := range []any{[]decimal.Decimal{value, bad}, []*decimal.Decimal{&bad}} {
		if _, _, err := bindArgument(input); !errors.Is(err, ErrDecimalRange) {
			t.Fatal("array accepted invalid value", err)
		}
	}
}

type decimalTestConn struct{ methodTestConn }

func (c *decimalTestConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	c.ctx, c.sql, c.args = ctx, sql, args
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestDecimalQueryBindings(t *testing.T) {
	connection := &decimalTestConn{methodTestConn{rows: &methodTestRows{}}}
	db := &DB{conn: connection}
	value := decimal.New(123, -2)
	for _, call := range []func() error{
		func() error { _, err := QueryRaw(t.Context(), db, "SELECT $1", value); return err },
		func() error { _, err := QueryScalar[int64](t.Context(), db, "SELECT $1", value); return err },
		func() error { _, err := Exec(t.Context(), db, "SELECT $1", value); return err },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
		if len(connection.args) != 1 {
			t.Fatal("arguments lost")
		}
		if bound, ok := connection.args[0].(pgtype.Numeric); !ok || bound.Exp != -2 {
			t.Fatal("driver received unbound decimal")
		}
	}
}
