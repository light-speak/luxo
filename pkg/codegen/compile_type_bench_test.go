package codegen

import "testing"

func BenchmarkOrdinaryIsGeneration(b *testing.B) {
	input := analyzePermissionFixture(b, `model User @noTime { id: Int @id }
fn modelType(value: User?): Boolean { return value is User }
fn dateType(value: DateTime?): Boolean { return value is DateTime }
fn decimalType(value: Decimal): Boolean { return value is Decimal }
fn differentType(value: Int): Boolean { return value is String }
`)
	if len(input.Errors) != 0 {
		b.Fatal(input.Errors)
	}
	generator := mustNewGenerator(b, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"User": {"id": 1}}}})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := generator.Generate(input, "generated", nil); err != nil {
			b.Fatal(err)
		}
	}
}
