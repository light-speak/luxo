package pg

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// BenchmarkDurationScanTarget isolates checked conversion from driver and network costs.
func BenchmarkDurationScanTarget(b *testing.B) {
	for _, sample := range []struct {
		name  string
		value DurationValue
	}{
		{"microseconds", DurationValue{Microseconds: 1234, Valid: true}},
		{"days", DurationValue{Days: 2, Microseconds: -1, Valid: true}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			var value time.Duration
			target := (*DurationScanValue)(&value)
			b.ReportAllocs()
			for b.Loop() {
				if err := target.ScanInterval(sample.value); err != nil {
					b.Fatal(err)
				}
			}
			if value == 0 {
				b.Fatal("duration was not written")
			}
		})
	}
	for _, source := range []DurationValue{{Microseconds: 1234, Valid: true}, {}} {
		name := "nullable_value"
		if !source.Valid {
			name = "nullable_null"
		}
		b.Run(name, func(b *testing.B) {
			var value *time.Duration
			var target pgtype.IntervalScanner = DurationPointer(&value)
			b.ReportAllocs()
			for b.Loop() {
				if err := target.ScanInterval(source); err != nil {
					b.Fatal(err)
				}
			}
			if (value != nil) != source.Valid {
				b.Fatal("nullable value changed")
			}
		})
	}
}
