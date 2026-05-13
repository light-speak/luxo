package auth

import (
	"testing"
	"time"
)

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
