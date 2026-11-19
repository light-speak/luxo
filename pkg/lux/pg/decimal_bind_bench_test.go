package pg

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

var decimalBindingSink []any
var decimalWireSink []byte

func BenchmarkDecimalBinding(b *testing.B) {
	for _, sample := range []struct {
		name string
		args []any
	}{
		{"Plain1", []any{int64(42)}},
		{"Plain4", []any{int64(42), "name", true, int64(7)}},
		{"Decimal", []any{decimal.New(123, -2)}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				bound, err := bindArguments(sample.args)
				if err != nil {
					b.Fatal(err)
				}
				decimalBindingSink = bound
			}
		})
	}
}

func BenchmarkDecimalDriverEncoding(b *testing.B) {
	for _, bound := range []bool{false, true} {
		name := "Before"
		if bound {
			name = "Bound"
		}
		b.Run(name, func(b *testing.B) {
			mapper := pgtype.NewMap()
			args := []any{decimal.New(123, -2)}
			buffer := make([]byte, 0, 64)
			b.ReportAllocs()
			for b.Loop() {
				values := args
				var err error
				if bound {
					values, err = bindArguments(args)
				}
				if err != nil {
					b.Fatal(err)
				}
				buffer, err = mapper.Encode(pgtype.NumericOID, pgtype.BinaryFormatCode, values[0], buffer[:0])
				if err != nil {
					b.Fatal(err)
				}
				decimalWireSink = buffer
			}
		})
	}
}

func BenchmarkDecimalQueryControl(b *testing.B) {
	for _, sample := range []struct {
		name string
		args []any
	}{
		{"Empty", nil},
		{"One", []any{int64(42)}},
		{"Four", []any{int64(42), "name", true, int64(7)}},
	} {
		for _, bound := range []bool{false, true} {
			name := sample.name + "/Before"
			if bound {
				name = sample.name + "/Bound"
			}
			b.Run(name, func(b *testing.B) {
				db := &DB{conn: &methodTestConn{rows: &methodTestRows{}}}
				ctx := b.Context()
				b.ReportAllocs()
				for b.Loop() {
					var err error
					if bound {
						_, err = db.queryBound(ctx, "SELECT 1", sample.args...)
					} else {
						_, err = db.conn.Query(ctx, "SELECT 1", sample.args...)
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
