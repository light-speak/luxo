package auth

import (
	"math"
	"math/big"
	"strconv"
	"strings"
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

func FuzzJWTDurationScaledDecimal(f *testing.F) {
	f.Add(int64(0), uint32(29), uint8(1), int64(100))
	f.Add(int64(math.MinInt64), uint32(0), uint8(0), int64(-1))
	f.Add(int64(922337203685477580), uint32(7), uint8(0), int64(10))
	f.Add(int64(0), uint32(5), uint8(0), int64(math.MinInt64))
	f.Fuzz(func(t *testing.T, whole int64, fractional uint32, precision uint8, unit int64) {
		divisor := uint64(1)
		for range precision%9 + 1 {
			divisor *= 10
		}
		fraction := strconv.FormatUint(divisor+uint64(fractional)%divisor, 10)[1:]
		number := strconv.FormatInt(whole, 10) + "." + fraction
		input := number + "*" + strconv.FormatInt(unit, 10) + "ns"
		var reference, factor big.Rat
		reference.SetString(number)
		reference.Mul(&reference, factor.SetInt64(unit))
		var exact big.Int
		exact.Quo(reference.Num(), reference.Denom())
		got, err := parseDuration(input)
		if !exact.IsInt64() {
			if err == nil {
				t.Fatalf("overflow accepted: %s = %v", input, got)
			}
			return
		}
		if err != nil || int64(got) != exact.Int64() {
			t.Fatalf("%s = %v, %v; want %s ns", input, got, err, exact.String())
		}
	})
}

func FuzzJWTDurationScaledExponent(f *testing.F) {
	f.Add(uint64(29), int8(-2), int64(100), false)
	f.Add(uint64(1), int8(-63), int64(math.MinInt64), true)
	f.Add(uint64(math.MaxInt64), int8(-16), int64(1), true)
	f.Fuzz(func(t *testing.T, significand uint64, exponent int8, unit int64, hex bool) {
		raw := strconv.FormatUint(significand, 10) + "e" + strconv.Itoa(int(exponent))
		if hex {
			raw = "0x" + strconv.FormatUint(significand, 16) + "p" + strconv.Itoa(int(exponent))
		}
		var reference, factor big.Rat
		reference.SetString(raw)
		reference.Mul(&reference, factor.SetInt64(unit))
		var exact big.Int
		exact.Quo(reference.Num(), reference.Denom())
		got, err := scaleDuration(raw, time.Duration(unit), raw)
		if !exact.IsInt64() {
			if err == nil {
				t.Fatalf("%s scaled by %d overflowed to %v", raw, unit, got)
			}
			return
		}
		if err != nil || int64(got) != exact.Int64() {
			t.Fatalf("%s scaled by %d = %v, %v; want %s ns", raw, unit, got, err, exact.String())
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

func TestJWTDurationScaledInt64Boundaries(t *testing.T) {
	for _, raw := range []string{"9223372036854775808*1ns", "-9223372036854775809*1ns", "4611686018427387904*2ns", "-9223372036854775808*-1ns"} {
		if duration, err := parseDuration(raw); err == nil {
			t.Errorf("accepted overflow %q as %v", raw, duration)
		}
	}
	for raw, want := range map[string]time.Duration{
		"9223372036854775807*1ns":    math.MaxInt64,
		"-9223372036854775808*1ns":   math.MinInt64,
		"4611686018427387903.5*2ns":  math.MaxInt64,
		"922337203685477580.7*10ns":  math.MaxInt64,
		"-922337203685477580.8*10ns": math.MinInt64,
	} {
		duration, err := parseDuration(raw)
		if err != nil || duration != want {
			t.Errorf("%q = %v, %v; want %v", raw, duration, err, want)
		}
	}
}

func TestJWTDurationSumOfScaledTerms(t *testing.T) {
	const exactMaximum = "4611686018427387904*1ns+4611686018427387903*1ns"
	if got, err := parseDuration(exactMaximum); err != nil || got != math.MaxInt64 {
		t.Errorf("%s = %v, %v; want %v", exactMaximum, got, err, time.Duration(math.MaxInt64))
	}
	const belowMinimum = "-4611686018427387905*1ns+-4611686018427387904*1ns"
	if got, err := parseDuration(belowMinimum); err == nil {
		t.Errorf("accepted underflow %s as %v", belowMinimum, got)
	}
}

func TestJWTDurationScaledDecimalPrecision(t *testing.T) {
	for _, sample := range []struct {
		input string
		want  time.Duration
	}{
		{"0.29*100ns", 29},
		{"-0.29*100ns", -29},
		{"0.29*100ns+0.29*100ns", 58},
		{"2*1h+3d", 74 * time.Hour},
		{"3d+2*1h", 74 * time.Hour},
		{"0.999999999999999999*1ns", 0},
		{"0x1_0p-3*5ns", 10},
		{"0x1p-1*3ns", 1},
		{"-0x1p63*1ns", math.MinInt64},
		{"0x1p-63*-9223372036854775808ns", -1},
		{"0x1p-64*-9223372036854775808ns", 0},
		{"0x1.1p-60*-9223372036854775808ns", -8},
		{"0x1.ffffffffffffffp-1*1ns", 0},
		{"0x10000000000000000p-64*1ns", 1},
		{"0X0.0P-3*1ns", 0},
		{"1e-9*1s", 1},
		{"1_0e-1*1ns", 1},
		{"-2.9E-1*100ns", -29},
		{"9.223372036854775807e18*1ns", math.MaxInt64},
		{"1e-20*9223372036854775807ns", 0},
		{"1.0e-19*9223372036854775807ns", 0},
		{"1_000*2ns", 2_000},
		{"1e-1000001*1ns", 0},
		{"9223372036854774784." + strings.Repeat("0", 1_000_001) + "*1ns", 9223372036854774784},
	} {
		got, err := parseDuration(sample.input)
		if err != nil || got != sample.want {
			t.Errorf("%.30q (%d bytes) = %v, error=%t; want %v", sample.input, len(sample.input), got, err != nil, sample.want)
		}
	}
}

func TestJWTDurationExponentSignsAndHexUnits(t *testing.T) {
	for _, sample := range []struct {
		input string
		want  time.Duration
	}{
		{"1e+2*1ns", 100 * time.Nanosecond},
		{"0x1p+2*1ns", 4 * time.Nanosecond},
		{"1E+2*1ns+3ns+0x1dp+1*1ns", 161 * time.Nanosecond},
		{"0x1dp0d", 29 * 24 * time.Hour},
		{"0x1Dp0d", 29 * 24 * time.Hour},
		{"0x1dp0*1ns", 29 * time.Nanosecond},
		{"0x1Dp0*1ns", 29 * time.Nanosecond},
		{"0x1dp+0d+12h", 29*24*time.Hour + 12*time.Hour},
		{"1h0x1dp0d12h", 29*24*time.Hour + 13*time.Hour},
		{"1e+0d12h", 36 * time.Hour},
		{"0x1dp0w", 29 * 168 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"2*3h+1d", 30 * time.Hour},
		{"30m", 30 * time.Minute},
		{"1.25µs", 1250 * time.Nanosecond},
		{"-1h15m", -75 * time.Minute},
	} {
		got, err := parseDuration(sample.input)
		if err != nil || got != sample.want {
			t.Errorf("%q = %v, %v; want %v", sample.input, got, err, sample.want)
		}
	}
}

func TestJWTDurationCompactMultiplierAllocations(t *testing.T) {
	for _, input := range []string{"1e-1*10s", "0x1p-1*24h"} {
		allocations := testing.AllocsPerRun(20, func() {
			if _, err := parseDuration(input); err != nil {
				t.Fatal(err)
			}
		})
		if allocations > 1 {
			t.Errorf("%s allocated %.0f objects, want at most one", input, allocations)
		}
	}
}

func TestJWTDurationRejectsTooPreciseOverflow(t *testing.T) {
	// The float parser can round this input, but exact rational parsing rejects
	// its decimal scale. The duration still must fail instead of wrapping.
	raw := "9223372036854775808." + strings.Repeat("1", 1_000_001) + "*1ns"
	if duration, err := parseDuration(raw); err == nil {
		t.Fatalf("accepted overflow as %v", duration)
	}
}

func TestLoadConfigAcceptsMixedDurationUnitOrder(t *testing.T) {
	ResetConfig()
	t.Cleanup(ResetConfig)
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_EXPIRES", "12h1d")
	t.Setenv("JWT_REFRESH", "true")
	t.Setenv("JWT_REFRESH_EXPIRES", "30m1w15s0.5d")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Expires != 36*time.Hour || cfg.RefreshExpires != 180*time.Hour+30*time.Minute+15*time.Second {
		t.Fatalf("access expiry=%v, refresh expiry=%v", cfg.Expires, cfg.RefreshExpires)
	}
}

func BenchmarkJWTDurationConfig(b *testing.B) {
	for _, input := range []string{"7d", "1d12h", "0.5d", "30m", "1e-1*10s", "0x1p-1*24h"} {
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
	for _, input := range []string{
		"100000d100000d100000d", "106752d", "106751d23h47m16.854775808s",
		"1..2d", "1.2.3w", "d", ".*1ns", "9223372036854775808.000001*1ns",
		"1e_1*1ns", "9e19*1ns", "0X12*1ns", "0x1p_1*1ns",
		"2*1d", "1e++2*1ns", "0x1p+*1ns", "1e+2", "0x1dep+1*1ns+1e+ns", "0x1d*1ns",
	} {
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
