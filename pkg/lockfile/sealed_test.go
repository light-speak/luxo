package lockfile

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/parser"
)

func sealedFiles(t *testing.T, source string) []*ast.File {
	t.Helper()
	tokens, errors := lexer.New(source, "sealed.luxo").Tokenize()
	if len(errors) != 0 {
		t.Fatal(errors)
	}
	file, diagnostics := parser.New(tokens).Parse("sealed.luxo")
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	return []*ast.File{file}
}

func TestSealedTagsAndPayloadIDsSurviveEvolution(t *testing.T) {
	lf := New()
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Success(value: Int, note: String) Pending }`))
	if lf.Types["Outcome"] == nil || lf.Types["Outcome.Success"] == nil {
		t.Fatal("missing independent stable tag/payload namespaces")
	}
	first, value := lf.Types["Outcome"].Fields["Success"], lf.Types["Outcome.Success"].Fields["value"]
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Pending Success(note: String, value: Int, extra: Int = 1) }`))
	if lf.Types["Outcome"].Fields["Success"] != first || lf.Types["Outcome.Success"].Fields["value"] != value {
		t.Fatal("declaration reorder renumbered wire IDs")
	}
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Pending }`))
	encoded, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	lf, err = readLock(encoded)
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Success(value: Int) Pending }`))
	if lf.Types["Outcome"].Fields["Success"] <= first || lf.Types["Outcome.Success"].Fields["value"] <= value {
		t.Fatal("removed tag or payload ID was reused")
	}
}

func TestSealedKindAndNewCasesAreBreaking(t *testing.T) {
	lf := New()
	input := sealedFiles(t, `sealed Empty {} sealed Outcome { Pending }`)
	mustUpdate(t, lf, input)
	if lf.Types["Empty"].kind() != "sealed" || lf.Types["Outcome.Pending"].kind() != "sealedVariant" {
		t.Fatal("uninhabited sum lost declaration identity")
	}
	if changes := lf.BreakingChanges(input); len(changes) != 0 {
		t.Fatal("unchanged sum changed its wire contract", changes)
	}
	for _, source := range []string{`type Empty {} sealed Outcome { Pending }`, `sealed Empty {} sealed Outcome { Pending NewCase }`} {
		if changes := lf.BreakingChanges(sealedFiles(t, source)); len(changes) == 0 {
			t.Fatal("closed sum change was hidden", source)
		}
	}
}

func TestSealedLockStandardJSONRoundTrip(t *testing.T) {
	lf := New()
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Value(value: Int = 1) }`))
	data, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	var decoded LockFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Types["Outcome.Value"].kind() != "sealedVariant" {
		t.Fatal("standard JSON decoding lost declaration metadata")
	}
	actual, err := json.Marshal(&decoded)
	if err != nil || string(actual) != string(data) {
		t.Fatal("standard JSON roundtrip lost default presence", string(actual), err)
	}
}

func TestSealedPayloadDefaultsPreserveInputCompatibility(t *testing.T) {
	lf := New()
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Value(value: Int = 1) }`))
	encoded, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	lf, err = readLock(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`sealed Outcome { Value(value: Int) }`,
		`sealed Outcome { Value(value: Int = 1, added: Int) }`,
		`sealed Outcome { Value(value: Int = 1, added: Int?) }`,
	} {
		if changes := lf.BreakingChanges(sealedFiles(t, source)); len(changes) == 0 {
			t.Fatal("old omitted payload is no longer accepted", source)
		}
	}
	optional := sealedFiles(t, `sealed Outcome { Value(value: Int = 1, added: Int? = null) }`)
	if changes := lf.BreakingChanges(optional); len(changes) != 0 {
		t.Fatal("a declared default made an input addition required", changes)
	}
	mustUpdate(t, lf, optional)
	mustUpdate(t, lf, sealedFiles(t, `sealed Outcome { Value(added: Int? = null) }`))
	if err := lf.Validate(); err != nil {
		t.Fatal("removed default field left dangling metadata", err)
	}
}

func TestSealedLockKindsRejectInvalidOwnership(t *testing.T) {
	for _, source := range []string{
		`{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"unknown","next_id":1,"fields":{}}}}`,
		`{"version":2,"models":{"Outcome":{"kind":"sealed","next_id":1,"fields":{}}},"apis":{}}`,
		`{"version":2,"models":{},"apis":{},"types":{"Outcome.Value":{"kind":"sealed","next_id":1,"fields":{}}}}`,
		`{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"sealed","next_id":2,"fields":{"Value":1}}}}`,
		`{"version":2,"models":{},"apis":{},"types":{"Value":{"kind":"sealedVariant","next_id":1,"fields":{}}}}`,
		`{"version":2,"models":{},"apis":{},"types":{"Outcome.Value":{"kind":"sealedVariant","next_id":2,"fields":{"value":1}}}}`,
		`{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"sealed","kind":"sealed","next_id":1,"fields":{}}}}`,
	} {
		t.Run(source, func(t *testing.T) {
			lock, err := readLock([]byte(source))
			if err == nil {
				err = lock.Validate()
			}
			if err == nil {
				t.Fatal("invalid sealed ownership metadata was accepted")
			}
		})
	}
}

func loadSealedLockJSON(t *testing.T, raw string) (*LockFile, error) {
	t.Helper()
	path := lockValidationPath(t)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := Load(path)
	retained, readErr := os.ReadFile(path)
	if readErr != nil || string(retained) != raw {
		t.Fatal("loading metadata changed its source file", readErr)
	}
	return lock, err
}

func TestLoadSealedDefaultsRejectsMalformedMetadata(t *testing.T) {
	const prefix = `{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"sealed","next_id":2,"fields":{"Value":1}},"Outcome.Value":{"kind":"sealedVariant","next_id":2,"fields":{"value":1},`
	for _, sample := range []struct{ name, metadata, message string }{
		{"not-array", `"default_fields":null`, "expected ["},
		{"non-integer", `"default_fields":[null]`, "expected number"},
		{"fraction", `"default_fields":[1.5]`, "invalid syntax"},
		{"too-many", `"default_fields":[` + strings.Repeat("1,", codec.MaxSelectionFieldID) + `1]`, "too many default field IDs"},
		{"duplicate-member", `"default_fields":[],"default_fields":[]`, "duplicate lock member"},
		{"escaped-duplicate-member", `"default_fields":[],"default_\u0066ields":[]`, "duplicate lock member"},
		{"zero", `"default_fields":[0]`, "invalid or duplicate default field ID"},
		{"negative", `"default_fields":[-1]`, "invalid or duplicate default field ID"},
		{"duplicate-ID", `"default_fields":[1,1]`, "invalid or duplicate default field ID"},
		{"inactive-ID", `"default_fields":[2]`, "is not active"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			lock, err := loadSealedLockJSON(t, prefix+sample.metadata+`}}}`)
			if lock != nil || err == nil || !strings.Contains(err.Error(), sample.message) {
				t.Fatalf("invalid default metadata: lock=%v err=%v, want %q", lock, err, sample.message)
			}
		})
	}
	for _, metadata := range []string{`"default_fields":[]`, `"kind":"","default_fields":[1]`} {
		lock, err := loadSealedLockJSON(t, `{"version":2,"models":{},"apis":{},"types":{"Plain":{"next_id":2,"fields":{"value":1},`+metadata+`}}}`)
		if lock != nil || err == nil || !strings.Contains(err.Error(), "requires a sealedVariant") {
			t.Fatal("ordinary type accepted sealed default metadata", lock, err)
		}
	}
}

func TestLoadSealedMetadataPreservesLegacyAndCanonicalOrder(t *testing.T) {
	ordinary, err := loadSealedLockJSON(t, `{"version":2,"models":{},"apis":{},"types":{"Plain":{"kind":"","next_id":1,"fields":{}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := lockJSON(t, ordinary); strings.Contains(encoded, `"kind"`) || strings.Contains(encoded, `"default_fields"`) {
		t.Fatal("an explicit empty kind changed the ordinary lock format", encoded)
	}
	lock, err := loadSealedLockJSON(t, `{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"sealed","next_id":2,"fields":{"Value":1}},"Outcome.Value":{"kind":"sealedVariant","next_id":3,"fields":{"value":1,"note":2},"default_fields":[2,1]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	path := lockValidationPath(t)
	if err := lock.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(path)
	if err != nil || lockJSON(t, lock) != lockJSON(t, restored) || !strings.Contains(lockJSON(t, restored), `"default_fields":[1,2]`) {
		t.Fatal("save/load lost sorted default field IDs", restored, err)
	}
}

func TestSealedUpdateOwnsDeclarationMetadata(t *testing.T) {
	lock := New()
	mustUpdate(t, lock, sealedFiles(t, `sealed Outcome { Value(value: Int = 1, note: String = "old") }`))
	previous := *lock
	before := lockJSON(t, &previous)
	mustUpdate(t, lock, sealedFiles(t, `sealed Outcome { Value(value: Int, note: String = "new") }`))
	if lockJSON(t, &previous) != before || lockJSON(t, lock) == before {
		t.Fatal("successful update shared mutable kind/default metadata with the previous snapshot")
	}
	exhausted, err := loadSealedLockJSON(t, `{"version":2,"models":{},"apis":{},"types":{"Outcome":{"kind":"sealed","next_id":2,"fields":{"Value":1}},"Outcome.Value":{"kind":"sealedVariant","next_id":65536,"fields":{"value":1},"reserved":[65535],"default_fields":[1]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	before, payload := lockJSON(t, exhausted), exhausted.Types["Outcome.Value"]
	err = exhausted.Update(sealedFiles(t, `sealed Outcome { Value(value: Int, added: Int = 7) }`))
	if err == nil || !strings.Contains(err.Error(), "outside 1..65535") || lockJSON(t, exhausted) != before || exhausted.Types["Outcome.Value"] != payload {
		t.Fatal("failed update changed default metadata or its published declaration", err)
	}
}

func TestSealedMetadataCompatibilityReportsPreciseChanges(t *testing.T) {
	for _, sample := range []struct {
		name, before, after string
		change              BreakingChange
	}{
		{"kind-added", `type Outcome {}`, `sealed Outcome {}`, BreakingChange{Path: "type Outcome kind", Before: "type", After: "sealed"}},
		{"kind-removed", `sealed Outcome {}`, `type Outcome {}`, BreakingChange{Path: "type Outcome kind", Before: "sealed", After: "type"}},
		{"variant-added", `sealed Outcome { Pending }`, `sealed Outcome { Pending Added }`, BreakingChange{Path: "type Outcome.Added", After: "sealedVariant"}},
		{"default-removed", `sealed Outcome { Value(value: Int = 1) }`, `sealed Outcome { Value(value: Int) }`, BreakingChange{Path: "type Outcome.Value.value default", Before: "optional", After: "required"}},
		{"required-added", `sealed Outcome { Value(value: Int = 1) }`, `sealed Outcome { Value(value: Int = 1, added: String) }`, BreakingChange{Path: "type Outcome.Value.added", After: "String"}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			lock := New()
			mustUpdate(t, lock, sealedFiles(t, sample.before))
			changes := lock.BreakingChanges(sealedFiles(t, sample.after))
			if !slices.Equal(changes, []BreakingChange{sample.change}) {
				t.Fatal("sealed compatibility reported the wrong wire change", changes, sample.change)
			}
		})
	}
}
