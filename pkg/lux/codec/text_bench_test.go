package codec

import (
	"fmt"
	"strings"
	"testing"
)

type textBenchmark struct {
	name  string
	count int
	read  func(*Decoder, *ColumnarReader, []byte, *int) int
}

func textBenchmarks(before bool) []textBenchmark {
	if before {
		return []textBenchmark{
			{"row", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { return len(d.readStringBeforeUTF8()) }},
			{"nullable", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { return len(*d.readStringPtrBeforeUTF8()) }},
			{"arena", 1, func(d *Decoder, _ *ColumnarReader, a []byte, off *int) int {
				return len(d.readStringArenaBeforeUTF8(a, off))
			}},
			{"skip-row", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { d.skipStringBeforeUTF8(); return 0 }},
			{"skip-column", 128, func(_ *Decoder, r *ColumnarReader, _ []byte, _ *int) int { r.skipColumnStringBeforeUTF8(); return 0 }},
			{"column-arena", 128, func(_ *Decoder, r *ColumnarReader, a []byte, off *int) int {
				return len(r.readColumnStringArenaBeforeUTF8(a, off))
			}},
		}
	}
	return []textBenchmark{
		{"row", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { return len(d.ReadString()) }},
		{"nullable", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { return len(*d.ReadStringPtr()) }},
		{"arena", 1, func(d *Decoder, _ *ColumnarReader, a []byte, off *int) int { return len(d.ReadStringArena(a, off)) }},
		{"skip-row", 1, func(d *Decoder, _ *ColumnarReader, _ []byte, _ *int) int { d.SkipString(); return 0 }},
		{"skip-column", 128, func(_ *Decoder, r *ColumnarReader, _ []byte, _ *int) int { r.SkipColumnString(); return 0 }},
		{"column-arena", 128, func(_ *Decoder, r *ColumnarReader, a []byte, off *int) int {
			return len(r.ReadColumnStringArena(a, off))
		}},
	}
}

func BenchmarkStringUTF8(b *testing.B) {
	for _, fixture := range []struct{ name, value string }{
		{"empty", ""}, {"ascii16", "1234567890abcdef"}, {"ascii1024", strings.Repeat("a", 1024)},
		{"unicode24", strings.Repeat("東京", 4)}, {"unicode1026", strings.Repeat("東京", 171)},
	} {
		for _, before := range []bool{true, false} {
			for _, tc := range textBenchmarks(before) {
				b.Run(fmt.Sprintf("%s/%s/before=%v", tc.name, fixture.name, before), func(b *testing.B) {
					benchmarkTextReader(b, tc, fixture.value)
				})
			}
		}
	}
}

func benchmarkTextReader(b *testing.B, tc textBenchmark, value string) {
	var data []byte
	if tc.name == "nullable" {
		data = append(data, 1)
	}
	for range tc.count {
		data = AppendBytes(data, []byte(value))
	}
	arena := make([]byte, len(value)*tc.count)
	d := Decoder{buf: data}
	r := ColumnarReader{buf: data, count: tc.count}
	arenaOff := 0
	want := len(value)
	if strings.HasPrefix(tc.name, "skip-") {
		want = 0
	}
	if tc.name == "column-arena" {
		want = tc.count
	}
	if got := tc.read(&d, &r, arena, &arenaOff); got != want || d.err != nil || r.err != nil || d.off+r.off != len(data) {
		b.Fatalf("invalid benchmark result: %d/%d offsets=%d/%d err=%v/%v", got, want, d.off, r.off, d.err, r.err)
	}
	b.ReportAllocs()
	for b.Loop() {
		d.off, r.off, arenaOff = 0, 0, 0
		tc.read(&d, &r, arena, &arenaOff)
	}
}
