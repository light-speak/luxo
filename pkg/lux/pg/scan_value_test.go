package pg

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

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
