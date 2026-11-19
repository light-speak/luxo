package pg

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var durationBindingSink []any

func durationBindingSamples() []struct {
	name string
	args []any
} {
	values := make([]time.Duration, 128)
	ordinary := make([]any, 128)
	for i := range values {
		values[i], ordinary[i] = time.Duration(i)*time.Microsecond, int64(i)
	}
	value := time.Microsecond
	return []struct {
		name string
		args []any
	}{
		{"Empty", nil}, {"Plain1", []any{int64(42)}}, {"Plain4", []any{int64(42), "name", true, int64(7)}},
		{"Plain128", ordinary}, {"Decimal", []any{decimal.New(123, -2)}},
		{"Duration", []any{value}}, {"Pointer", []any{&value}}, {"Null", []any{(*time.Duration)(nil)}},
		{"NamedDuration", []any{pgx.NamedArgs{"elapsed": value}}}, {"List1", []any{values[:1]}}, {"List128", []any{values}}, {"ListPointer128", []any{&values}},
	}
}

func BenchmarkDatabaseBinding(b *testing.B) {
	for _, sample := range durationBindingSamples() {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				values, err := bindArguments(sample.args)
				if err != nil {
					b.Fatal(err)
				}
				durationBindingSink = values
			}
		})
	}
}

func BenchmarkBoundDatabaseQuery(b *testing.B) {
	for _, sample := range durationBindingSamples() {
		b.Run(sample.name, func(b *testing.B) {
			db := &DB{conn: &methodTestConn{rows: &methodTestRows{}}}
			ctx := b.Context()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := db.queryBound(ctx, "SELECT 1", sample.args...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDurationWriteReject(b *testing.B) {
	values := make([]time.Duration, 128)
	values[len(values)-1] = time.Nanosecond
	for _, sample := range []struct {
		name string
		args []any
	}{
		{"Scalar", []any{time.Nanosecond}}, {"List128Last", []any{values}},
		{"AfterDecimal", []any{decimal.New(123, -2), time.Nanosecond}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				values, err := bindArguments(sample.args)
				if values != nil || !errors.Is(err, ErrDurationPrecision) {
					b.Fatal(values, err)
				}
			}
		})
	}
}

func BenchmarkRewrittenDuration(b *testing.B) {
	args := []any{pgx.NamedArgs{"elapsed": time.Microsecond}}
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		bound, err := bindArguments(args)
		if err != nil {
			b.Fatal(err)
		}
		_, values, err := bound[0].(pgx.QueryRewriter).RewriteQuery(ctx, nil, "SELECT @elapsed::interval", nil)
		if err != nil {
			b.Fatal(err)
		}
		durationBindingSink = values
	}
}
