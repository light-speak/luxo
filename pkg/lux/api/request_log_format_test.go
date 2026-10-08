package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAppendRequestLogMatchesFormat(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 34, 56, 0, time.UTC)
	for _, duration := range []time.Duration{0, 999 * time.Nanosecond, 1250 * time.Microsecond, 100 * time.Millisecond, 101 * time.Millisecond, 500 * time.Millisecond, 501 * time.Millisecond} {
		for _, failure := range []string{"", "InvalidCredentials"} {
			ms := float64(duration.Microseconds()) / 1000
			color := colorDim
			if ms > 500 {
				color = colorRed
			} else if ms > 100 {
				color = "\033[33m"
			}
			status := colorGreen + "✓" + colorReset
			if failure != "" {
				status = colorRed + "✗ " + failure + colorReset
			}
			want := fmt.Sprintf("%s12:34:56%s %s[user]%s getUser %s%.1fms%s %s\n", colorDim, colorReset, colorGreen, colorReset, color, ms, colorReset, status)
			got := appendRequestLog([]byte("prefix:"), now, "user", colorGreen, "getUser", duration, failure)
			if string(got) != "prefix:"+want {
				t.Fatalf("duration=%s failure=%q: got %q want %q", duration, failure, got, want)
			}
		}
	}
}

func TestAppendRequestLogLongName(t *testing.T) {
	name := strings.Repeat("operation", 1024)
	got := appendRequestLog(nil, time.Time{}, "api", colorGreen, name, time.Millisecond, "")
	if !strings.Contains(string(got), name) || got[len(got)-1] != '\n' {
		t.Fatal("log was truncated")
	}
}

func BenchmarkAppendRequestLog(b *testing.B) {
	buf := make([]byte, 0, 256)
	now := time.Date(2026, 9, 27, 12, 34, 56, 0, time.UTC)
	b.ReportAllocs()
	for b.Loop() {
		buf = appendRequestLog(buf[:0], now, "user", colorGreen, "getUser", time.Millisecond, "")
	}
}

func BenchmarkRequestLogFormatBaseline(b *testing.B) {
	var buf bytes.Buffer
	buf.Grow(256)
	now := time.Date(2026, 9, 27, 12, 34, 56, 0, time.UTC)
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		fmt.Fprintf(&buf, "%s %s[%s]%s %s %s%.1fms%s %s✓%s\n",
			colorDim+now.Format("15:04:05")+colorReset,
			colorGreen, "user", colorReset, "getUser", colorDim, 1.0,
			colorReset, colorGreen, colorReset)
	}
}
