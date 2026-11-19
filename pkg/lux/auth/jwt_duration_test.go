package auth

import (
	"math"
	"math/big"
	"strconv"
	"testing"
	"time"
)

func FuzzJWTDurationIntegerUnits(f *testing.F) {
	for _, seed := range [][3]uint64{{0, 0, 0}, {7, 0, 0}, {1, 2, 3}, {106751, 0, 23}, {100000, 100000, 100000}, {math.MaxUint64, 0, 0}} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, days, weeks, hours uint64) {
		input := strconv.FormatUint(days, 10) + "d" + strconv.FormatUint(weeks, 10) + "w" + strconv.FormatUint(hours, 10) + "h"
		var expected, component, unit big.Int
		for _, pair := range [][2]uint64{{days, uint64(24 * time.Hour)}, {weeks, uint64(7 * 24 * time.Hour)}, {hours, uint64(time.Hour)}} {
			component.SetUint64(pair[0])
			unit.SetUint64(pair[1])
			expected.Add(&expected, component.Mul(&component, &unit))
		}
		got, err := parseDuration(input)
		if !expected.IsInt64() {
			if err == nil {
				t.Fatalf("overflow accepted: %s = %v", input, got)
			}
			return
		}
		if err != nil || int64(got) != expected.Int64() {
			t.Fatalf("%s = %v, %v; want %s ns", input, got, err, expected.String())
		}
	})
}

func TestJWTDurationArithmeticBoundaries(t *testing.T) {
	for _, input := range []string{"NaN*24h", "Inf*24h", "-Inf*24h", "1e309*24h", "-106752*24h", "-2562047h47m16.854775808s+-1ns"} {
		if got, err := parseDurationWithMultiplier(input); err == nil {
			t.Errorf("accepted %q as %v", input, got)
		}
	}
	for input, expected := range map[string]time.Duration{
		"106751d23h47m16.854775807s": math.MaxInt64,
		"0.5d":                       12 * time.Hour,
		"0.25w":                      42 * time.Hour,
	} {
		if got, err := parseDuration(input); err != nil || got != expected {
			t.Errorf("%q = %v, %v; want %v", input, got, err, expected)
		}
	}
}

func BenchmarkJWTDurationConfig(b *testing.B) {
	for _, input := range []string{"7d", "1d12h", "0.5d", "30m"} {
		b.Run(input, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := parseDuration(input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestJWTDurationRejectsOverflowAndMalformedNumbers(t *testing.T) {
	for _, input := range []string{"100000d100000d100000d", "106752d", "106751d23h47m16.854775808s", "1..2d", "1.2.3w"} {
		t.Run(input, func(t *testing.T) {
			if duration, err := parseDuration(input); err == nil {
				t.Fatalf("accepted invalid duration %q as %v", input, duration)
			}
		})
	}
}

func TestParseDuration_InvalidNumber(t *testing.T) {
	// "xd" — x is not a valid number
	_, err := parseDuration("xd")
	if err == nil {
		t.Fatal("parseDuration(\"xd\") should error")
	}
}

func TestParseDuration_MultiPart(t *testing.T) {
	// "1d12h" → 36 hours
	d, err := parseDuration("1d12h")
	if err != nil {
		t.Fatalf("parseDuration(\"1d12h\") error: %v", err)
	}
	expected := 36 * time.Hour
	if d != expected {
		t.Errorf("got %v, want %v", d, expected)
	}
}

func TestParseDurationWithMultiplier_InvalidDurStr(t *testing.T) {
	// Directly test parseDurationWithMultiplier with bad duration part
	_, err := parseDurationWithMultiplier("7*badunit")
	if err == nil {
		t.Fatal("should error on invalid duration unit")
	}
}

func TestParseDurationWithMultiplier_InvalidNumber(t *testing.T) {
	// Directly test parseDurationWithMultiplier with non-numeric multiplier
	_, err := parseDurationWithMultiplier("abc*24h")
	if err == nil {
		t.Fatal("should error on invalid multiplier number")
	}
}

func TestParseDurationWithMultiplier_PlainDuration(t *testing.T) {
	// Part without multiplier — just a plain duration
	d, err := parseDurationWithMultiplier("30m")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if d != 30*time.Minute {
		t.Errorf("got %v, want 30m", d)
	}
}

func TestParseDurationWithMultiplier_PlainDurationError(t *testing.T) {
	// Invalid plain duration
	_, err := parseDurationWithMultiplier("invalid")
	if err == nil {
		t.Fatal("should error on invalid plain duration")
	}
}

func TestParseDurationWithMultiplier_MultiPart(t *testing.T) {
	// "1*24h+12h" → 36h
	d, err := parseDurationWithMultiplier("1*24h+12h")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if d != 36*time.Hour {
		t.Errorf("got %v, want 36h", d)
	}
}

func TestParseDurationWithMultiplier_EmptyParts(t *testing.T) {
	// Empty string — should return 0
	d, err := parseDurationWithMultiplier("")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if d != 0 {
		t.Errorf("got %v, want 0", d)
	}
}
