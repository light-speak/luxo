package luxolog

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

var benchmarkLevel int
var benchmarkMessage string
var benchmarkRecord *Record

// The factory case isolates the actual cold allocation callback; it excludes
// sync.Pool.Get misses, GC and process initialization. Borrow includes release.
func BenchmarkRecordOwnership(b *testing.B) {
	if err := SetLevel(LevelError); err != nil {
		b.Fatal(err)
	}
	b.Run("cold_factory", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			benchmarkRecord = records.New().(*Record)
		}
	})
	b.Run("warm_borrow_release", func(b *testing.B) {
		r := NewRecord()
		r.WriteDebug()
		b.ReportAllocs()
		for b.Loop() {
			r := NewRecord()
			r.WriteDebug()
		}
	})
}

func BenchmarkGetLevel(b *testing.B) {
	if err := SetLevel(LevelInfo); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkLevel = GetLevel()
	}
}

func BenchmarkFilteredDebug(b *testing.B) {
	if err := SetLevel(LevelInfo); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkMessage = Debug("not printed")
	}
}

func BenchmarkFirstLevel(b *testing.B) {
	b.Setenv("LOG_LEVEL", "info")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		levelState.Store(0)
		benchmarkLevel = GetLevel()
	}
}

func BenchmarkSetLevel(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := SetLevel(LevelInfo); err != nil {
			b.Fatal(err)
		}
	}
}

// These samples include the real file write to /dev/null, not a network or
// asynchronous request logger. Both revisions use the same call site and input.
func BenchmarkDiagnosticMessages(b *testing.B) {
	file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	oldFile, oldLevel := os.Stderr, GetLevel()
	os.Stderr = file
	b.Cleanup(func() { os.Stderr = oldFile; _ = SetLevel(oldLevel); _ = file.Close() })
	if err := SetLevel(LevelInfo); err != nil {
		b.Fatal(err)
	}
	for _, sample := range []struct{ name, text string }{{"short", "diagnostic message"}, {"4KiB", strings.Repeat("x", 4096)}} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkMessage = Info(sample.text)
			}
		})
	}
}

// Compare the bounded formatter with a direct static standard-library quote.
// All inputs fit including their quotes, so output is equivalent and complete.
func BenchmarkRecordString(b *testing.B) {
	for _, sample := range []struct{ name, text string }{
		{"short", "diagnostic \"message\""},
		{"unicode", strings.Repeat("中文", 32)},
		{"4KiB", strings.Repeat("x", MaxValueBytes-2)},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.Run("reference", func(b *testing.B) {
				buf := make([]byte, 0, MaxValueBytes+16)
				b.ReportAllocs()
				for b.Loop() {
					buf = strconv.AppendQuote(buf[:0], sample.text)
					benchmarkLevel = len(buf)
				}
			})
			b.Run("record", func(b *testing.B) {
				r := new(Record)
				r.String(sample.text)
				want := strconv.Quote(sample.text)
				if r.truncated || string(r.data[logPrefixSize:logPrefixSize+r.size]) != want {
					b.Fatal("formatter mismatch")
				}
				b.ReportAllocs()
				for b.Loop() {
					r.size, r.truncated = 0, false
					r.String(sample.text)
					benchmarkLevel = r.size
				}
			})
		})
	}
}
