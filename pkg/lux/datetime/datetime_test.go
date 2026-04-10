package datetime

import (
	"testing"
	"time"
)

func TestNow(t *testing.T) {
	n := Now()
	if time.Since(n) > time.Second {
		t.Error("Now should be recent")
	}
}

func TestToday(t *testing.T) {
	td := Today()
	if td.Hour() != 0 || td.Minute() != 0 || td.Second() != 0 {
		t.Error("Today should be midnight")
	}
}

func TestFromUnix(t *testing.T) {
	ts := FromUnix(1000000000) // 2001-09-09
	if ts.Year() != 2001 {
		t.Errorf("year = %d", ts.Year())
	}
}

func TestFromUnixMilli(t *testing.T) {
	ts := FromUnixMilli(1000000000000)
	if ts.Year() != 2001 {
		t.Errorf("year = %d", ts.Year())
	}
}

func TestStartOfDay(t *testing.T) {
	now := time.Date(2026, 4, 11, 15, 30, 45, 0, time.UTC)
	sod := StartOfDay(now)
	if sod.Hour() != 0 || sod.Day() != 11 {
		t.Error("StartOfDay")
	}
}

func TestEndOfDay(t *testing.T) {
	now := time.Date(2026, 4, 11, 15, 30, 45, 0, time.UTC)
	eod := EndOfDay(now)
	if eod.Hour() != 23 || eod.Minute() != 59 {
		t.Error("EndOfDay")
	}
}

func TestStartOfMonth(t *testing.T) {
	d := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	som := StartOfMonth(d)
	if som.Day() != 1 || som.Month() != 4 {
		t.Error("StartOfMonth")
	}
}

func TestEndOfMonth(t *testing.T) {
	d := time.Date(2026, 2, 15, 10, 0, 0, 0, time.UTC)
	eom := EndOfMonth(d)
	if eom.Day() != 28 || eom.Month() != 2 {
		t.Errorf("EndOfMonth = %v", eom)
	}
}

func TestAddDays(t *testing.T) {
	d := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := AddDays(d, 31)
	if r.Month() != 2 || r.Day() != 1 {
		t.Errorf("AddDays = %v", r)
	}
}

func TestAddMonths(t *testing.T) {
	d := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	r := AddMonths(d, 1)
	if r.Month() != 3 { // Jan 31 + 1 month = March (Feb doesn't have 31)
		t.Errorf("AddMonths = %v", r)
	}
}

func TestDaysBetween(t *testing.T) {
	a := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	if DaysBetween(a, b) != 9 {
		t.Errorf("got %d", DaysBetween(a, b))
	}
	if DaysBetween(b, a) != 9 {
		t.Error("should be absolute")
	}
}

func TestIsToday(t *testing.T) {
	if !IsToday(time.Now().UTC()) {
		t.Error("now should be today")
	}
	if IsToday(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("2000 should not be today")
	}
}

func TestIsZero(t *testing.T) {
	if !IsZero(time.Time{}) {
		t.Error("zero time")
	}
	if IsZero(time.Now()) {
		t.Error("now is not zero")
	}
}
