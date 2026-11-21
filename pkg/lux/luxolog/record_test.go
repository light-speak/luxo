package luxolog

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestRecordStaticValues(t *testing.T) {
	setTestLevel(t, LevelDebug)
	output := captureStderr(func() {
		r := NewRecord()
		r.Text("values=")
		r.Int(math.MinInt64)
		r.Text("|")
		r.Float(math.Copysign(0, -1))
		r.Text("|")
		r.Boolean(true)
		r.Text("|")
		r.Boolean(false)
		r.Text("|")
		r.String("中文\n\"x")
		r.Text("|")
		r.Hex([]byte{0, 255, 7})
		r.Text("|")
		r.Raw([]byte(`{"id":7}`))
		r.WriteDebug()
	})
	if !strings.Contains(output, `values=-9223372036854775808|-0|true|false|"中文\n\"x"|0x00ff07|{"id":7}`) {
		t.Fatal(output)
	}
}

func TestRecordBounded(t *testing.T) {
	setTestLevel(t, LevelDebug)
	for _, write := range []func(*Record){
		func(r *Record) { r.Text(strings.Repeat("中", MaxValueBytes)) },
		func(r *Record) { r.String(strings.Repeat("中\n\"", MaxValueBytes)) },
		func(r *Record) { r.Hex(make([]byte, MaxValueBytes)) },
		func(r *Record) { r.Raw(make([]byte, MaxValueBytes*2)) },
	} {
		output := captureStderr(func() {
			r := NewRecord()
			write(r)
			if !r.Full() {
				t.Error("oversized record did not fill")
			}
			r.Text("must-not-append")
			r.Raw([]byte("must-not-append"))
			r.String("must-not-append")
			r.WriteInfo()
		})
		if !strings.Contains(output, "<truncated>") || strings.Contains(output, "must-not-append") || len(output) > MaxValueBytes+128 || !utf8.ValidString(output) {
			t.Fatalf("unbounded or invalid record: %d", len(output))
		}
	}
}

func TestRecordFiltersAndResets(t *testing.T) {
	setTestLevel(t, LevelError)
	output := captureStderr(func() {
		for _, write := range []func(*Record){(*Record).WriteDebug, (*Record).WriteInfo, (*Record).WriteWarn} {
			r := NewRecord()
			r.Text("hidden")
			write(r)
		}
	})
	if output != "" {
		t.Fatal(output)
	}
	output = captureStderr(func() {
		r := NewRecord()
		r.Text("error-message")
		r.WriteError()
	})
	if !strings.Contains(output, "ERROR") || !strings.Contains(output, "error-message") || strings.Contains(output, "hidden") {
		t.Fatal(output)
	}
}

func TestConcurrentRecordOwnership(t *testing.T) {
	setTestLevel(t, LevelInfo)
	output := captureStderr(func() {
		var workers sync.WaitGroup
		for range 16 {
			workers.Go(func() {
				r := NewRecord()
				r.Text("concurrent-record")
				r.WriteInfo()
			})
		}
		workers.Wait()
	})
	if strings.Count(output, "concurrent-record\n") != 16 {
		t.Fatal("records lost or interleaved", output)
	}
}

func TestRecordInvalidTextAndExactBoundary(t *testing.T) {
	setTestLevel(t, LevelDebug)
	output := captureStderr(func() {
		r := NewRecord()
		r.String("\xff")
		r.WriteDebug()
	})
	if !strings.Contains(output, `"\xff"`) || !utf8.ValidString(output) {
		t.Fatal(output)
	}
	output = captureStderr(func() {
		r := NewRecord()
		r.Text(strings.Repeat("x", MaxValueBytes))
		if !r.Full() {
			t.Error("exact boundary must be full")
		}
		r.WriteDebug()
	})
	if strings.Contains(output, "<truncated>") || strings.Count(output, "x") != MaxValueBytes {
		t.Fatal("exact value was truncated")
	}
}

func TestRecordQuoteMatchesStandardAtBoundaries(t *testing.T) {
	for _, value := range []string{"", "hello", "\x00\n\t\"\\", "中文🙂", "\xff\xc0\x80", strings.Repeat("中文\n\"", 1024)} {
		for available := 0; available <= MaxValueBytes; available += 31 {
			r := new(Record)
			r.size = MaxValueBytes - available
			start := logPrefixSize + r.size
			r.String(value)
			got := string(r.data[start : logPrefixSize+r.size])
			want := strconv.Quote(value)
			if len(want) <= available {
				if r.truncated || got != want {
					t.Fatalf("available=%d, complete quote mismatch", available)
				}
			} else if !r.truncated || !utf8.ValidString(got) || r.size > MaxValueBytes {
				t.Fatalf("available=%d, unsafe truncated quote", available)
			}
		}
	}
}
