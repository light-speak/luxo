package str

import (
	"strconv"
	"strings"
	"testing"
)

var regexBenchmarkResult bool

func BenchmarkRegexCache(b *testing.B) {
	for _, sample := range []struct {
		name, pattern, value string
		want                 bool
	}{
		{"ascii", `^[a-z]+$`, "luxo", true},
		{"mismatch", `^[a-z]+$`, "42", false},
		{"counted", `^[0-9]{1,12}$`, "1234567", true},
		{"unicode", `^\p{L}+$`, "中文框架", true},
		{"long-admitted", "^" + strings.Repeat("x", 600) + "$", strings.Repeat("x", 600), true},
		{"long-uncached", "^" + strings.Repeat("x", 5000) + "$", strings.Repeat("x", 5000), true},
		{"counted-large", `a{1000}`, strings.Repeat("a", 1000), true},
		{"invalid", "[invalid", "value", false},
	} {
		b.Run(sample.name, func(b *testing.B) {
			if Matches(sample.pattern, sample.value) != sample.want {
				b.Fatal("fixture result changed")
			}
			b.ReportAllocs()
			for b.Loop() {
				regexBenchmarkResult = Matches(sample.pattern, sample.value)
			}
		})
	}
}

func BenchmarkRegexCacheCorpus(b *testing.B) {
	for _, count := range []int{16, 64, 1024} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			patterns, values := make([]string, count), make([]string, count)
			for index := range count {
				values[index] = "corpus-" + strconv.Itoa(index)
				patterns[index] = "^" + values[index] + "$"
				if !Matches(patterns[index], values[index]) {
					b.Fatal("fixture did not match")
				}
			}
			index := 0
			b.ReportAllocs()
			for b.Loop() {
				regexBenchmarkResult = Matches(patterns[index], values[index])
				index++
				if index == count {
					index = 0
				}
			}
		})
	}
}

func BenchmarkRegexCacheParallel(b *testing.B) {
	const pattern = `^[0-9]{1,12}$`
	if !Matches(pattern, "1234567") {
		b.Fatal("fixture did not match")
	}
	b.ReportAllocs()
	b.RunParallel(func(work *testing.PB) {
		for work.Next() {
			if !Matches(pattern, "1234567") {
				b.Error("concurrent fixture did not match")
			}
		}
	})
}

var regexBenchmarkCache *regexMatcherCache

func BenchmarkRegexCacheConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		regexBenchmarkCache = newRegexMatcherCache()
	}
}

func BenchmarkRegexCacheColdMatch(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		cache := newRegexMatcherCache()
		if !cache.matches(`^[a-z]+$`, "luxo") {
			b.Fatal("cold fixture did not match")
		}
		regexBenchmarkCache = cache
	}
}
