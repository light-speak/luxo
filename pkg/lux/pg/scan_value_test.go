package pg

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestJSONListValue(t *testing.T) {
	for _, oid := range []uint32{pgtype.JSONArrayOID, pgtype.JSONBArrayOID} {
		for _, format := range []int16{pgtype.TextFormatCode, pgtype.BinaryFormatCode} {
			mapping := pgtype.NewMap()
			for _, source := range [][]json.RawMessage{nil, {}, {json.RawMessage("null"), json.RawMessage("123456789012345678901234567890")}, {nil}} {
				raw, err := mapping.Encode(oid, format, source, nil)
				if err != nil {
					t.Fatal(err)
				}
				var value JSONListValue
				err = mapping.Scan(oid, format, raw, &value)
				if len(source) == 1 && source[0] == nil {
					if err == nil {
						t.Fatalf("oid=%d format=%d accepted SQL NULL element", oid, format)
					}
					continue
				}
				if err != nil || (value == nil) != (source == nil) || len(value) != len(source) {
					t.Fatalf("oid=%d format=%d value=%q err=%v", oid, format, value, err)
				}
				clear(raw)
				for i := range source {
					if string(value[i]) != string(source[i]) {
						t.Fatalf("oid=%d format=%d lost JSON bytes or ownership", oid, format)
					}
				}
			}
		}
	}
}

func TestRequiredJSONValue(t *testing.T) {
	for _, oid := range []uint32{pgtype.JSONOID, pgtype.JSONBOID} {
		for _, format := range []int16{pgtype.TextFormatCode, pgtype.BinaryFormatCode} {
			mapping := pgtype.NewMap()
			for _, source := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("{\"id\":9223372036854775807}")} {
				raw, err := mapping.Encode(oid, format, source, nil)
				if err != nil {
					t.Fatal(err)
				}
				var value json.RawMessage
				err = mapping.Scan(oid, format, raw, (*JSONValue)(&value))
				if source == nil {
					if err == nil {
						t.Fatalf("oid=%d format=%d accepted SQL NULL", oid, format)
					}
					continue
				}
				clear(raw)
				if err != nil || string(value) != string(source) {
					t.Fatalf("oid=%d format=%d value=%q error=%v", oid, format, value, err)
				}
			}
		}
	}
}

func TestNullableJSONValue(t *testing.T) {
	var value NullableJSONValue
	if err := value.ScanBytes(nil); err != nil || value.Pointer() != nil {
		t.Fatalf("SQL NULL: %v", err)
	}
	source := []byte("null")
	if err := value.ScanBytes(source); err != nil {
		t.Fatal(err)
	}
	source[0] = 'x'
	if got := value.Pointer(); got == nil || string(*got) != "null" {
		t.Fatalf("JSON null or ownership lost: %v", got)
	}
	if err := value.ScanBytes([]byte{}); err != nil || value.Pointer() == nil {
		t.Fatalf("present empty bytes became NULL: %v", err)
	}
	if err := value.ScanBytes(nil); err != nil || value.Pointer() != nil {
		t.Fatalf("reset SQL NULL: %v", err)
	}
}

func TestDurationFromValue(t *testing.T) {
	for _, tc := range []struct {
		value   DurationValue
		want    time.Duration
		invalid bool
	}{
		{DurationValue{Valid: true}, 0, false},
		{DurationValue{Valid: true, Days: 2, Microseconds: -1}, 48*time.Hour - time.Microsecond, false},
		{DurationValue{Valid: true, Microseconds: -999999}, -999999 * time.Microsecond, false},
		{DurationValue{Valid: true, Microseconds: math.MaxInt64 / 1000}, time.Duration(math.MaxInt64 / 1000 * 1000), false},
		{DurationValue{Valid: true, Microseconds: math.MinInt64 / 1000}, time.Duration(math.MinInt64 / 1000 * 1000), false},
		{DurationValue{}, 0, true},
		{DurationValue{Valid: true, Months: 1}, 0, true},
		{DurationValue{Valid: true, Microseconds: math.MaxInt64}, 0, true},
		{DurationValue{Valid: true, Microseconds: math.MinInt64}, 0, true},
		{DurationValue{Valid: true, Days: math.MaxInt32}, 0, true},
		{DurationValue{Valid: true, Days: math.MinInt32}, 0, true},
		{DurationValue{Valid: true, Days: 106751992, Microseconds: math.MinInt64}, time.Duration((106751992*86400000000 - 9223372036854775808) * 1000), false},
	} {
		got, err := DurationFromValue(tc.value)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Fatalf("%+v => %v, %v; want %v invalid=%v", tc.value, got, err, tc.want, tc.invalid)
		}
	}
}

func TestDurationScanTargets(t *testing.T) {
	initial := time.Second
	value := initial
	target := (*DurationScanValue)(&value)
	if err := target.ScanInterval(DurationValue{Microseconds: 1234, Valid: true}); err != nil || value != 1234*time.Microsecond {
		t.Fatal(value, err)
	}
	for _, invalid := range []DurationValue{{}, {Months: 1, Valid: true}, {Microseconds: math.MaxInt64, Valid: true}} {
		value = initial
		if err := target.ScanInterval(invalid); err == nil || value != initial {
			t.Fatal("invalid scalar changed destination", value, err)
		}
	}
	if err := (*DurationScanValue)(nil).ScanInterval(DurationValue{Valid: true}); err == nil {
		t.Fatal("nil scalar destination accepted")
	}
	pointer := &initial
	optional := DurationPointer(&pointer)
	if err := optional.ScanInterval(DurationValue{Microseconds: -1, Valid: true}); err != nil || pointer == nil || *pointer != -time.Microsecond || initial != time.Second {
		t.Fatal("optional scan changed value or previous owner", pointer, err)
	}
	previous := pointer
	if err := optional.ScanInterval(DurationValue{Months: 1, Valid: true}); err == nil || pointer != previous {
		t.Fatal("failed nullable scan changed destination", pointer, err)
	}
	if err := optional.ScanInterval(DurationValue{}); err != nil || pointer != nil {
		t.Fatal("nullable SQL NULL was lost", pointer, err)
	}
	if err := DurationPointer(nil).ScanInterval(DurationValue{}); err == nil {
		t.Fatal("nil pointer destination accepted")
	}
}

func TestDurationListScanTarget(t *testing.T) {
	for _, format := range []int16{pgtype.TextFormatCode, pgtype.BinaryFormatCode} {
		mapping := pgtype.NewMap()
		for _, sample := range []struct {
			name    string
			values  []DurationValue
			invalid bool
		}{
			{"null", nil, false}, {"empty", []DurationValue{}, false},
			{"values", []DurationValue{{Microseconds: 7, Valid: true}, {Microseconds: -7, Valid: true}}, false},
			{"null_element", []DurationValue{{}}, true},
			{"months", []DurationValue{{Months: 1, Valid: true}}, true},
			{"overflow", []DurationValue{{Microseconds: math.MaxInt64, Valid: true}}, true},
		} {
			t.Run(fmt.Sprintf("%d/%s", format, sample.name), func(t *testing.T) {
				raw, err := mapping.Encode(pgtype.IntervalArrayOID, format, sample.values, nil)
				if err != nil {
					t.Fatal(err)
				}
				value := []time.Duration{time.Hour}
				err = mapping.Scan(pgtype.IntervalArrayOID, format, raw, (*DurationListValue)(&value))
				if sample.invalid {
					if err == nil {
						t.Fatal("invalid array accepted", value)
					}
					return
				}
				if err != nil || (value == nil) != (sample.values == nil) || len(value) != len(sample.values) {
					t.Fatal(value, err)
				}
				if len(sample.values) > 0 && !slices.Equal(value, []time.Duration{7 * time.Microsecond, -7 * time.Microsecond}) {
					t.Fatal(value)
				}
				previous := slices.Clone(value)
				clear(raw)
				if !slices.Equal(value, previous) {
					t.Fatal("driver buffer aliases duration storage")
				}
			})
		}
	}
}

var durationTargetSink pgtype.IntervalScanner

func TestDurationPointerTargetDoesNotAllocate(t *testing.T) {
	var value *time.Duration
	if allocs := testing.AllocsPerRun(1000, func() { durationTargetSink = DurationPointer(&value) }); allocs != 0 {
		t.Fatalf("nullable target wrapper allocated: %g", allocs)
	}
}

func FuzzDurationFromValueExactArithmetic(f *testing.F) {
	for _, seed := range []struct {
		days   int32
		micros int64
	}{
		{0, 0}, {1, -86400000000}, {-1, 86400000000},
		{106751992, math.MinInt64}, {-106751992, math.MaxInt64},
		{math.MaxInt32, math.MinInt64}, {math.MinInt32, math.MaxInt64},
		{0, math.MaxInt64 / 1000}, {0, math.MaxInt64/1000 + 1},
		{0, math.MinInt64 / 1000}, {0, math.MinInt64/1000 - 1},
	} {
		f.Add(seed.days, seed.micros)
	}
	f.Fuzz(func(t *testing.T, days int32, micros int64) {
		// The independent oracle computes nanoseconds without intermediate overflow.
		var expected, component big.Int
		expected.SetInt64(int64(days))
		component.SetInt64(86400000000)
		expected.Mul(&expected, &component)
		component.SetInt64(micros)
		expected.Add(&expected, &component)
		component.SetInt64(1000)
		expected.Mul(&expected, &component)
		got, err := DurationFromValue(DurationValue{Valid: true, Days: days, Microseconds: micros})
		if !expected.IsInt64() {
			if err == nil || got != 0 {
				t.Fatalf("overflow accepted: days=%d micros=%d got=%d err=%v", days, micros, got, err)
			}
			return
		}
		if err != nil || int64(got) != expected.Int64() {
			t.Fatalf("days=%d micros=%d got=%d err=%v want=%s", days, micros, got, err, expected.String())
		}
	})
}

func TestDecimalFromValue(t *testing.T) {
	input := DecimalValue{Int: big.NewInt(1234567890123456789), Exp: -9, Valid: true}
	value, err := DecimalFromValue(input)
	if err != nil || value.String() != "1234567890.123456789" {
		t.Fatal(value, err)
	}
	input.Int.SetInt64(0)
	if value.String() != "1234567890.123456789" {
		t.Fatal("decimal retained mutable scanner storage")
	}
	for _, invalid := range []DecimalValue{{}, {Valid: true}, {Valid: true, NaN: true}, {Valid: true, InfinityModifier: pgtype.Infinity}} {
		if _, err := DecimalFromValue(invalid); err == nil {
			t.Fatalf("invalid decimal accepted: %+v", invalid)
		}
	}
}

func TestDateTimeFromValue(t *testing.T) {
	input := time.Date(0, 1, 1, 1, 0, 0, 123456789, time.FixedZone("offset", 3600))
	got, err := DateTimeFromValue(TimeValue{Time: input, Valid: true})
	if err != nil || got.Location() != time.UTC || got.Year() != 0 || got.Nanosecond() != 123456789 {
		t.Fatal(got, err)
	}
	for _, invalid := range []TimeValue{{}, {Valid: true, InfinityModifier: pgtype.Infinity}, {Valid: true, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, {Valid: true, Time: time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		if _, err := DateTimeFromValue(invalid); err == nil {
			t.Fatalf("invalid time accepted: %+v", invalid)
		}
	}
}

func TestNullableJSONDatabaseRoundTrip(t *testing.T) {
	for _, oid := range []uint32{pgtype.JSONOID, pgtype.JSONBOID} {
		for _, format := range []int16{pgtype.TextFormatCode, pgtype.BinaryFormatCode} {
			for _, raw := range []string{"", "null", `{"number":9007199254740993,"decimal":0.1234567890123456789}`} {
				var original NullableJSONValue
				var input []byte
				if raw != "" {
					input = []byte(raw)
				}
				if err := original.ScanBytes(input); err != nil {
					t.Fatal(err)
				}
				driver := pgtype.NewMap()
				encoded, err := driver.Encode(oid, format, original.Pointer(), nil)
				if err != nil {
					t.Fatalf("oid=%d format=%d encode: %v", oid, format, err)
				}
				if (encoded == nil) != (raw == "") {
					t.Fatalf("oid=%d format=%d collapsed SQL/JSON null: %q", oid, format, encoded)
				}
				var restored NullableJSONValue
				if err := driver.Scan(oid, format, encoded, &restored); err != nil {
					t.Fatal(err)
				}
				for i := range encoded {
					encoded[i] = 'x'
				}
				got := restored.Pointer()
				if raw == "" {
					if got != nil {
						t.Fatal("SQL NULL became JSON value")
					}
					continue
				}
				if got == nil || string(*got) != raw {
					t.Fatalf("oid=%d format=%d round trip lost raw JSON: %v", oid, format, got)
				}
			}
		}
	}
}

func TestFloatFromValue(t *testing.T) {
	for _, value := range []float64{0, math.Copysign(0, -1), math.MaxFloat64, math.SmallestNonzeroFloat64, -42.5} {
		got, err := FloatFromValue(Float64Value{Float64: value, Valid: true})
		if err != nil || math.Float64bits(got) != math.Float64bits(value) {
			t.Fatalf("value=%v got=%v err=%v", value, got, err)
		}
	}
	for _, value := range []Float64Value{{}, {Float64: math.NaN(), Valid: true}, {Float64: math.Inf(1), Valid: true}, {Float64: math.Inf(-1), Valid: true}} {
		if _, err := FloatFromValue(value); err == nil {
			t.Fatalf("invalid accepted: %+v", value)
		}
	}
}

func TestValidateDateTime(t *testing.T) {
	for _, sample := range []struct {
		value time.Time
		valid bool
	}{
		{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), true},
		{time.Date(0, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600)), false},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600)), true},
	} {
		if err := ValidateDateTime(sample.value); (err == nil) != sample.valid {
			t.Fatalf("value=%v err=%v", sample.value, err)
		}
	}
}

func FuzzValidateFloatDomain(f *testing.F) {
	for _, bits := range []uint64{0, 1, 1 << 63, 0x7fefffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff0000000000001, 0x7fffffffffffffff} {
		f.Add(bits)
	}
	f.Fuzz(func(t *testing.T, bits uint64) {
		value := math.Float64frombits(bits)
		valid := !math.IsNaN(value) && !math.IsInf(value, 0)
		if (ValidateFloat(value) == nil) != valid {
			t.Fatalf("invalid classification for bits %x", bits)
		}
	})
}
