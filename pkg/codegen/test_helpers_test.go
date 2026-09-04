package codegen

import (
	"testing"

	"github.com/light-speak/luxo/pkg/semantic"
)

func mustNewGenerator(tb testing.TB, config GeneratorConfig) *GeneratorContext {
	tb.Helper()
	generator, err := NewGenerator(config)
	if err != nil {
		tb.Fatal(err)
	}
	return generator
}

func mustGenerate(tb testing.TB, result *semantic.Result, packageName string, driver DBDriver, softModels ...map[string]bool) *GenerateResult {
	tb.Helper()
	generated, err := Generate(result, packageName, driver, softModels...)
	if err != nil {
		tb.Fatal(err)
	}
	return generated
}

func generatorWithModelFieldIDs(ids map[string]map[string]int) *GeneratorContext {
	generator := defaultGenerator()
	generator.ids.ModelFields = cloneNestedIntMap(ids)
	return generator
}
