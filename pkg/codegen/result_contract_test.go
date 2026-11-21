package codegen

import (
	"strings"
	"testing"
)

func TestGenerateRejectsResultDataTypes(t *testing.T) {
	for _, source := range []string{
		`fn read(): Result<Result<Int>> @service @native`,
		`fn read(): Result<[Result<Int>]> @native`,
		`type Packet { value: Result<Int> }`,
		`model Item @noTime { id: Int @id value: Result<Int> }`,
		`fn read(value: Result<Int>): Result<Int> @native`,
	} {
		input := analyzePermissionFixture(t, source)
		if len(input.Errors) == 0 {
			t.Fatal("Result data type passed semantic validation", source)
		}
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
		if err == nil || output != nil || !strings.Contains(err.Error(), "Result<T>") {
			t.Fatalf("invalid Result emitted source: %v", err)
		}
	}
}

func TestGenerateRejectsLocalAPICalls(t *testing.T) {
	for _, declaration := range []string{
		`api inner(value: Int = 7): Int { return value }`,
		`api inner(value: Int = 7): Int @native`,
	} {
		input := analyzePermissionFixture(t, declaration+` api outer(): Int { return inner() }`)
		output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(input, StableIDs{})})
		if err == nil || output != nil || !strings.Contains(err.Error(), "cannot be called locally; extract a fn") {
			t.Fatalf("local API call emitted source: %v", err)
		}
	}
}

func TestPublicGenerationRejectsIdentityDataReferences(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"stored_claim", `model User @noTime @withAuth(stores: [id, peers]) { id: Int @id peers: [Identity] @by(id, id) } api probe(): Int @auth { return my.peers.size }`},
		{"type", `type Packet { value: [Identity]? }`},
		{"api", `api probe(value: Identity): Int { return 1 }`},
		{"native", `fn probe(): Result<Identity> @native`},
		{"local", `fn probe() { val value: Identity? = null }`},
		{"event", `event Ready(value: Identity)`},
		{"sealed", `sealed Outcome { Value(value: Identity) }`},
		{"error", `error Rejected(value: Identity) { code: 400 message: "rejected" }`},
		{"parent", `model Child: Identity @noTime { id: Int @id }`},
		{"extend_target", `extend Identity { id: Int }`},
		{"object_literal", `api probe(): Int { val value = Identity { id: 1, role: "admin" } return value.id }`},
		{"type_name_value", `api probe(): Int { val value = Identity return value.id }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			input := analyzePermissionFixture(t, sample.source)
			if len(input.Errors) == 0 {
				t.Fatal("Identity data reference passed semantic validation")
			}
			count := len(input.Errors)
			output, err := Generate(input, "generated", GeneratorConfig{})
			if err == nil || output != nil || !strings.Contains(err.Error(), "Identity is a runtime authentication context") {
				t.Fatalf("Identity data reference emitted source: output=%v error=%v", output != nil, err)
			}
			schema, err := BuildSchemaJSON(input, nil, GeneratorConfig{})
			if err == nil || schema != nil || !strings.Contains(err.Error(), "Identity is a runtime authentication context") {
				t.Fatalf("Identity data reference emitted schema: %v", err)
			}
			if len(input.Errors) != count {
				t.Fatal("a generation boundary modified semantic diagnostics")
			}
		})
	}
}
