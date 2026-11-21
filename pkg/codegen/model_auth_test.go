package codegen

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedAuthDecimalImportsFollowClaimsProjection(t *testing.T) {
	for _, sample := range []struct {
		name, source string
		needsRuntime bool
	}{
		{"scalar", authDecimalModel("Decimal"), true},
		{"nullable", authDecimalModel("Decimal?"), true},
		{"list", authDecimalModel("[Decimal]"), true},
		{"nullable-list", authDecimalModel("[Decimal]?"), true},
		{"nested", authNestedDecimalModel(""), true},
		{"hidden-nested", authNestedDecimalModel("@hidden"), false},
		{"unrelated", `model Price @noTime { id: Int @id amount: Decimal }
model User @noTime @withAuth(stores: [id]) { id: Int @id }`, false},
		{"plain", `model Price @noTime { id: Int @id amount: Decimal }`, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyzePermissionFixture(t, sample.source)
			ids := completeFixtureAPIIDs(result, StableIDs{ModelFields: map[string]map[string]int{
				"User":    {"id": 1, "amount": 2, "profileId": 3, "profile": 4},
				"Profile": {"id": 1, "amount": 2}, "Price": {"id": 1, "amount": 2},
			}})
			generated, err := Generate(result, "generated", GeneratorConfig{IDs: ids})
			if err != nil {
				t.Fatal(err)
			}
			if got := modelImportsRuntime(t, generated.Files["model.gen.go"]); got != sample.needsRuntime {
				t.Fatalf("runtime import=%v, want %v", got, sample.needsRuntime)
			}
			runGeneratedCompilation(t, generated, "package generated\nimport \"testing\"\nfunc TestAuthImportsCompile(t *testing.T){}\n")
		})
	}
}

func authDecimalModel(kind string) string {
	return "model User @noTime @withAuth(stores: [id, amount]) { id: Int @id amount: " + kind + " }"
}

func authNestedDecimalModel(directive string) string {
	return `model Profile @noTime { id: Int @id amount: Decimal ` + directive + ` }
model User @noTime @withAuth(stores: [id, profile]) { id: Int @id profileId: Int profile: Profile? }`
}

func modelImportsRuntime(t *testing.T, source []byte) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "model.gen.go", source, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Imports {
		name, err := strconv.Unquote(declaration.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if name == "github.com/light-speak/luxo/pkg/lux" {
			return true
		}
	}
	return false
}

func TestAuthStoreFragmentRetainsDuplicateArgumentDiagnostics(t *testing.T) {
	input := rejectedFragmentInput(t, `model User @noTime @withAuth(stores: ["id"], stores: [id]) { id: Int @id }`, "duplicate")
	model := input.Files[0].Models[0]
	directive := findDirective(model.Directives, "withAuth")
	if directive == nil || len(directive.Args) != 2 {
		t.Fatal("original duplicate stores arguments were not retained")
	}
	// This local validator checks the last stores list; Analyze rejects the full source.
	if err := validateRequestContracts(input); err != nil {
		t.Fatalf("fragment request precondition changed: %v", err)
	}
	fields := authStoreFields(model)
	if len(fields) != 1 || fields[0] != model.Fields[0] {
		t.Fatalf("fragment did not skip the earlier non-identifier: %v", fields)
	}
	generated, err := generateSourceFragments(input, "generated", DriverPG)
	if err != nil || generated == nil || !strings.Contains(string(generated.Files["model.gen.go"]), "CreateToken") {
		t.Fatalf("private model fragment failed: %v", err)
	}
	// Successful fragment formatting is not approval of this invalid auth configuration.
}
