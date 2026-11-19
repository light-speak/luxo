package codegen

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestGenericMetadataIsBoundToOneCompilation(t *testing.T) {
	base := defaultGenerator()
	left, right := &ast.Ident{Name: "left"}, &ast.Ident{Name: "right"}
	channel := &semantic.ResolvedType{Name: "Channel", TypeArgs: []*semantic.ResolvedType{{Name: "Int", Nullable: true}}}
	bound := base.withModelReferences(&semantic.Result{GenericExprTypes: map[ast.Expr]*semantic.ResolvedType{left: channel, right: channel}})
	if base.genericExprTypes != nil || len(bound.genericExprTypes) != 2 || bound.genericExprTypes[left] != bound.genericExprTypes[right] {
		t.Fatal("generic metadata must share one converted type within its compilation")
	}
	bound.genericExprTypes[left].TypeArgs[0].Name = "String"
	if channel.TypeArgs[0].Name != "Int" {
		t.Fatal("generator modified semantic metadata")
	}
	if next := bound.withModelReferences(&semantic.Result{}); next.genericExprTypes != nil {
		t.Fatal("generic metadata leaked into a later compilation")
	}
}

func TestGeneratorRejectsUnsupportedDriver(t *testing.T) {
	tests := []struct {
		name   string
		driver DBDriver
		want   string
	}{
		{name: "planned backend", driver: DriverMySQL, want: "not implemented"},
		{name: "unknown backend", driver: DBDriver("oracle"), want: "unknown database driver"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewGenerator(GeneratorConfig{Driver: test.driver})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewGenerator(%q) error = %v, want %q", test.driver, err, test.want)
			}
		})
	}
}

func TestGenerateRejectsUnsupportedDriver(t *testing.T) {
	_, err := Generate(&semantic.Result{}, "luxo", GeneratorConfig{Driver: DBDriver("oracle")})
	if err == nil || !strings.Contains(err.Error(), "unknown database driver") {
		t.Fatalf("Generate error = %v, want unknown database driver", err)
	}
}

func TestGeneratorReportsInvalidGeneratedSource(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{})
	_, err := generator.Generate(&semantic.Result{}, "invalid-package", nil)
	if err == nil || !strings.Contains(err.Error(), "format model.gen.go") {
		t.Fatalf("Generator.Generate error = %v, want model formatting failure", err)
	}
}

func TestGeneratorsKeepStableIDsIsolatedConcurrently(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "user.luxo",
		Models: []*ast.ModelDecl{{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{{
				Name: "name",
				Type: &ast.TypeRef{Name: "String"},
			}},
		}},
	}}}

	const workers = 64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		fieldID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			generator, err := NewGenerator(GeneratorConfig{
				Driver: DriverPG,
				IDs: completeFixtureAPIIDs(result, StableIDs{ModelFields: map[string]map[string]int{
					"User": {"name": fieldID},
				}}),
			})
			if err != nil {
				errs <- err
				return
			}
			generated, err := generator.Generate(result, "luxo", nil)
			if err != nil {
				errs <- err
				return
			}
			want := fmt.Sprintf("AppendVarint(buf.B, %d)", fieldID)
			if !strings.Contains(string(generated.Files["writejson.gen.go"]), want) {
				errs <- fmt.Errorf("field ID %d missing from generated writer", fieldID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestGeneratorTakesConfigurationSnapshot(t *testing.T) {
	ids := StableIDs{
		ModelFields: map[string]map[string]int{"User": {"name": 3}},
		APIParams:   map[string]map[string]int{"getUser": {"id": 4}},
	}
	events := &EventContext{
		ModelModule: map[string]string{"User": "user"},
		ModelFields: map[string]map[string]bool{"User": {"name": true}},
		remoteLoadCalls: map[string][]loadCallInfo{"user": {{
			modelName:    "User",
			argNames:     []string{"id"},
			argTypes:     []string{"int64"},
			argTypeNames: []string{"Int"},
		}}},
	}
	generator := mustNewGenerator(t, GeneratorConfig{IDs: ids, Events: events})

	ids.ModelFields["User"]["name"] = 30
	ids.APIParams["getUser"]["id"] = 40
	events.ModelModule["User"] = "changed"
	events.ModelFields["User"]["name"] = false
	events.remoteLoadCalls["user"][0].argNames[0] = "changed"
	events.remoteLoadCalls["user"][0].argTypes[0] = "string"
	events.remoteLoadCalls["user"][0].argTypeNames[0] = "String"

	if got := generator.modelFieldID("User", "name"); got != 3 {
		t.Fatalf("model field snapshot changed to %d", got)
	}
	if got := generator.apiParamID("getUser", "id"); got != 4 {
		t.Fatalf("API param snapshot changed to %d", got)
	}
	if got := generator.events.ModelModule["User"]; got != "user" {
		t.Fatalf("event context snapshot changed to %q", got)
	}
	if !generator.ownerModelHasField("User", "name") {
		t.Fatal("nested event context map was not copied")
	}
	call := generator.events.remoteLoadCalls["user"][0]
	if call.argNames[0] != "id" || call.argTypes[0] != "int64" || call.argTypeNames[0] != "Int" {
		t.Fatalf("nested load metadata was not copied: %+v", call)
	}
}

func TestGeneratorDoesNotRetainOwnedModelMetadata(t *testing.T) {
	ids := StableIDs{ModelFields: map[string]map[string]int{
		"Target": {"key": 1, "uuid": 2},
		"Source": {"id": 1, "targetKey": 2, "targetUuid": 3, "target": 4},
	}}
	first := analyzePermissionFixture(t, `model Target @noTime { key: String @id } model Source @noTime { id: Int @id targetKey: String target: Target }`)
	generator := mustNewGenerator(t, GeneratorConfig{IDs: completeFixtureAPIIDs(first, ids)})
	for _, sample := range []struct{ key, keyType, local string }{{"key", "String", "targetKey"}, {"uuid", "UUID", "targetUuid"}, {"key", "String", "targetKey"}} {
		input := analyzePermissionFixture(t, "model Target @noTime { "+sample.key+": "+sample.keyType+" @id } model Source @noTime { id: Int @id "+sample.local+": "+sample.keyType+" target: Target }")
		if len(input.Errors) != 0 {
			t.Fatal(input.Errors)
		}
		generated, err := generator.Generate(input, "generated", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "result[row." + strings.ToUpper(sample.key[:1]) + sample.key[1:] + "] = row"
		if !strings.Contains(string(generated.Files["dataloader.gen.go"]), want) {
			t.Fatal("generator retained a previous primary key", sample, string(generated.Files["dataloader.gen.go"]))
		}
	}
}

func TestOwnedModelKeysAndExternalProjectionMetadata(t *testing.T) {
	base := defaultGenerator()
	base.events = &EventContext{ModelIDField: map[string]string{"External": "uuid", "Local": "stale"}, ModelIDType: map[string]string{"External": "UUID", "Local": "Int"}}
	bound := base.withModelReferences(&semantic.Result{Files: []*ast.File{{
		Models:  []*ast.ModelDecl{{Name: "Local", Fields: []*ast.FieldDecl{{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}}}}, {Name: "NoKey"}},
		Extends: []*ast.ExtendDecl{{Name: "External"}, {Name: "Local"}},
	}}})
	for _, sample := range []struct {
		name, key, keyType string
		reference          bool
	}{
		{"Local", "sku", "String", true}, {"NoKey", "id", "Int", true},
		{"External", "uuid", "UUID", true}, {"Missing", "id", "Int", false},
	} {
		if bound.externalModelIDFieldName(sample.name) != sample.key || bound.externalModelIDTypeName(sample.name) != sample.keyType || bound.isModelReference(sample.name) != sample.reference {
			t.Fatalf("wrong model metadata for %+v", sample)
		}
	}
	if base.isModelReference("Local") {
		t.Fatal("borrowed declarations leaked into the reusable generator")
	}
}
