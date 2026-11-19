package codegen

import "testing"

func BenchmarkAppRuntimeGeneration(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"functions", `fn identity(value: Int): Int { return value }`},
		{"api", `api identity(value: Int): Int { return value }`},
		{"model", `model Record @noTime { id: Int @id value: Int }`},
		{"native", `fn read(): Result<Int> @native`},
		{"events", statelessEventSource},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			stable := StableIDs{ModelFields: map[string]map[string]int{"Record": {"id": 1, "value": 2}}}
			if sample.name == "events" {
				stable.EventFields = statelessEventIDs().EventFields
			}
			ids := completeFixtureAPIIDs(input, stable)
			config := GeneratorConfig{Driver: DriverPG, IDs: ids}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Generate(input, "generated", config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAppRuntimeEntry(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"functions", statelessServiceSource},
		{"model", `model Record @noTime { id: Int @id value: Int }`},
		{"native", `fn read(): Result<Int> @native`},
		{"events", statelessEventSource},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := analyzePermissionFixture(b, sample.source)
			input.Files[0].Name = "origin/logic/module.luxo"
			var options []EntryBackends
			if sample.name == "events" {
				options = []EntryBackends{{Events: "memory"}}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := GenerateEntryFileChecked(input, "example.test/entry", options...); err != nil {
					b.Fatal(err)
				}
				if _, err := GenerateModuleEntryFilesChecked(input, "example.test/entry", options...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
