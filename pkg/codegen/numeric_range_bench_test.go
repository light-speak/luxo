package codegen

import (
	"strings"
	"testing"
)

func BenchmarkNumericRangeValidation(b *testing.B) {
	for _, sample := range []struct{ name, kind, annotation string }{
		{"none", "Int", ""}, {"positive", "Int", "@range(0, 100)"},
		{"signed_int", "Int", "@range(-010, 020)"}, {"signed_float", "Float", "@range(-1.5, 1.5)"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, "model Probe @noTime { value: "+sample.kind+" "+sample.annotation+" }")
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			field := input.Files[0].Models[0].Fields[0]
			b.ReportAllocs()
			for b.Loop() {
				var output strings.Builder
				generateNumericValidation(&output, field, "value", "")
				numericLiteralSourceSink = output.String()
			}
		})
	}
}

func BenchmarkGenerateNumericRange(b *testing.B) {
	for _, bounds := range []string{"0, 100", "-010, 020"} {
		b.Run(bounds, func(b *testing.B) {
			input := analyzePermissionFixture(b, "model Probe @noTime @crud { id: Int @id value: Int @range("+bounds+") }")
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			config := GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{
				ModelFields: map[string]map[string]int{"Probe": {"id": 1, "value": 2}},
			})}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
