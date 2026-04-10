// Package datetime provides date and time utility functions for the Luxo runtime.
// Only functions that add genuine value over Go's time.Time methods.
package datetime

import "time"

// Now returns the current time in UTC.
func Now() time.Time {
	return time.Now().UTC()
}

// Today returns the current date at midnight (00:00:00) in UTC.
func Today() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// FromUnix creates a time.Time from a Unix timestamp (seconds since epoch).
func FromUnix(sec int64) time.Time {
	return time.Unix(sec, 0).UTC()
}

// FromUnixMilli creates a time.Time from a Unix timestamp in milliseconds.
func FromUnixMilli(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// StartOfDay returns t at midnight (00:00:00).
func StartOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// EndOfDay returns t at 23:59:59.999999999.
func EndOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, t.Location())
}

// StartOfMonth returns the first day of t's month at midnight.
func StartOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// EndOfMonth returns the last day of t's month at 23:59:59.
func EndOfMonth(t time.Time) time.Time {
	return StartOfMonth(t).AddDate(0, 1, 0).Add(-time.Nanosecond)
}

// AddDays returns t + n days.
func AddDays(t time.Time, n int) time.Time {
	return t.AddDate(0, 0, n)
}

// AddMonths returns t + n months.
func AddMonths(t time.Time, n int) time.Time {
	return t.AddDate(0, n, 0)
}

// DaysBetween returns the number of days between a and b (absolute value).
func DaysBetween(a, b time.Time) int {
	days := int(b.Sub(a).Hours() / 24)
	if days < 0 {
		return -days
	}
	return days
}

// IsToday reports whether t is today (UTC).
func IsToday(t time.Time) bool {
	now := time.Now().UTC()
	return t.Year() == now.Year() && t.Month() == now.Month() && t.Day() == now.Day()
}

// IsZero reports whether t is the zero time.
func IsZero(t time.Time) bool {
	return t.IsZero()
}
