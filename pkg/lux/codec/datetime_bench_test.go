package codec

import (
	"testing"
	"time"
)

var dateTimeBenchValue time.Time
var dateTimeBenchOffset int
var dateTimeBenchBytes []byte
var dateTimeBenchError error

// The old seconds-only baseline lacks fractional precision. This measures the
// local representation change, not equivalent semantics or an RPC round trip.
func BenchmarkDateTimePair(b *testing.B) {
	for _, sample := range []struct {
		name    string
		seconds int64
		nanos   int64
	}{
		{"epoch", 0, 0}, {"current", 1789646400, 123456789},
		{"pre_epoch", -1, 999999999}, {"year_9999", 253402300799, 999999999},
	} {
		b.Run(sample.name, func(b *testing.B) {
			value := time.Unix(sample.seconds, sample.nanos).UTC()
			old := AppendSvarint(nil, value.Unix())
			current, err := AppendDateTime(nil, value)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("seconds_read", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					seconds, n := ReadSvarint(old, 0)
					dateTimeBenchValue, dateTimeBenchOffset = time.Unix(seconds, 0).UTC(), n
				}
			})
			b.Run("pair_read", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					dateTimeBenchValue, dateTimeBenchOffset = ReadDateTime(current, 0)
				}
			})
			b.Run("seconds_write", func(b *testing.B) {
				buf := make([]byte, 0, 16)
				b.ReportAllocs()
				for b.Loop() {
					dateTimeBenchBytes = AppendSvarint(buf[:0], value.Unix())
				}
			})
			b.Run("pair_write", func(b *testing.B) {
				buf := make([]byte, 0, 16)
				b.ReportAllocs()
				for b.Loop() {
					dateTimeBenchBytes, dateTimeBenchError = AppendDateTime(buf[:0], value)
				}
			})
			b.Run("seconds_skip", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					dec := NewDecoder(old)
					dec.SkipVarint()
					dateTimeBenchOffset = dec.Offset()
				}
			})
			b.Run("pair_skip", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					dec := NewDecoder(current)
					dec.SkipDateTime()
					dateTimeBenchOffset = dec.Offset()
				}
			})
		})
	}
}
