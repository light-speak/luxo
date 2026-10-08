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
	generated, err := generateSourceFragments(result, packageName, driver, softModels...)
	if err != nil {
		tb.Fatal(err)
	}
	return generated
}

// Fragment tests inspect individual generated constructs, not a deployable wire
// contract. There is deliberately no equivalent public bypass.
func generateSourceFragments(result *semantic.Result, packageName string, driver DBDriver, softModels ...map[string]bool) (*GenerateResult, error) {
	generator, err := NewGenerator(GeneratorConfig{Driver: driver})
	if err != nil {
		return nil, err
	}
	if err := validateRequestContracts(result); err != nil {
		return nil, err
	}
	var soft map[string]bool
	if len(softModels) > 0 {
		soft = softModels[0]
	}
	return generator.generateSources(result, packageName, generator.allEnums(collectEnums(result)), soft)
}

func generatorWithModelFieldIDs(ids map[string]map[string]int) *GeneratorContext {
	generator := defaultGenerator()
	generator.ids.ModelFields = cloneNestedIntMap(ids)
	return generator
}

// Fixture catalogs are built explicitly before entering the public generator.
// Identifier rejection tests deliberately do not use this fixture convenience.
func completeFixtureAPIIDs(input *semantic.Result, ids StableIDs) StableIDs {
	ids = cloneStableIDs(ids)
	if ids.APIs == nil {
		ids.APIs = make(map[string]int)
	}
	if ids.APIParams == nil {
		ids.APIParams = make(map[string]map[string]int)
	}
	var nextAPI int
	for _, id := range ids.APIs {
		nextAPI = max(nextAPI, id)
	}
	for _, api := range defaultGenerator().collectSchemaAPIs(input, collectEnums(input)) {
		if ids.APIs[api.name] == 0 {
			nextAPI++
			ids.APIs[api.name] = nextAPI
		}
		if ids.APIParams[api.name] == nil {
			ids.APIParams[api.name] = make(map[string]int)
		}
		params := ids.APIParams[api.name]
		var nextParam int
		for _, id := range params {
			nextParam = max(nextParam, id)
		}
		for _, param := range api.params {
			if params[param.Name] == 0 {
				nextParam++
				params[param.Name] = nextParam
			}
		}
	}
	return ids
}
