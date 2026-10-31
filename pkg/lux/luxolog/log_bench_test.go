package luxolog

import "testing"

var benchmarkLevel int
var benchmarkMessage string

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
