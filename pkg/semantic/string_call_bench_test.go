package semantic

import "testing"

func BenchmarkStringCallAnalysis(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"control", `fn test(value: Int): Int { return value + 1 }`},
		{"positional", `fn test(value: String): String { return value.replace("x", "y") }`},
		{"named", `fn test(value: String): String { return value.replace(new: "y", old: "x") }`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			files := numericLiteralBenchmarkInput(b, sample.source)
			b.ReportAllocs()
			for b.Loop() {
				if result := Analyze(files); len(result.Errors) != 0 {
					b.Fatal(result.Errors)
				}
			}
		})
	}
}
