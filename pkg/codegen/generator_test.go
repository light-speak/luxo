package codegen

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

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
	_, err := Generate(&semantic.Result{}, "luxo", DBDriver("oracle"))
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
				IDs: StableIDs{ModelFields: map[string]map[string]int{
					"User": {"name": fieldID},
				}},
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
