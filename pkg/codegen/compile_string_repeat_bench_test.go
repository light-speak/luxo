package codegen

import "testing"

func BenchmarkCheckedRepeatGeneration(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"function", `fn repeatValue(value: String, count: Int): String { return value.repeat(count) }`},
		{"api", `api repeatValue(value: String, count: Int): String { return value.repeat(count) }`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{})}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
