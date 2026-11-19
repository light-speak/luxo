package semantic

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestREADMEStreamExamples(t *testing.T) {
	for file, heading := range map[string]string{
		"README.md": "### Real-time Streams", "README_CN.md": "### 实时流",
	} {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile("../../" + file)
			if err != nil {
				t.Fatal(err)
			}
			_, section, ok := strings.Cut(string(content), heading)
			if !ok {
				t.Fatal("missing stream section")
			}
			_, code, ok := strings.Cut(section, "```luxo\n")
			if !ok {
				t.Fatal("missing Luxo example")
			}
			code, _, ok = strings.Cut(code, "```")
			if !ok {
				t.Fatal("unterminated example")
			}
			identity := "model User @withAuth(stores: [id], default: true) { id: Int }\n"
			expectNoErrors(t, analyze(t, identity+code))
		})
	}
}

func TestEventPayloadRequiresExplicitSnapshots(t *testing.T) {
	for _, test := range []struct {
		name, declarations, payload, path string
	}{
		{"model", "", "User", "Changed.payload"},
		{"nullable model", "", "User?", "Changed.payload"},
		{"model list", "", "[User]", "Changed.payload"},
		{"nested model", "type Snapshot { owner: User }", "Snapshot", "Changed.payload.owner"},
		{"nested list", "type Snapshot { owners: [User] }", "[Snapshot]", "Changed.payload.owners"},
		{"forward reference", "type Snapshot { details: Details? }\ntype Details { owner: User }", "Snapshot", "Changed.payload.details.owner"},
		{"cyclic snapshot", "type Snapshot { next: Snapshot?\n owner: User }", "Snapshot", "Changed.payload.owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := analyze(t, "model User { id: Int }\n"+test.declarations+"\nevent Changed(payload: "+test.payload+")")
			expectError(t, result, "event payload '"+test.path+"' cannot reference database model 'User'")
		})
	}
}

func TestEventSnapshotsResolveAcrossFiles(t *testing.T) {
	files := []*ast.File{
		parseFileWithName(t, "event Changed(payload: Snapshot)", "event.luxo"),
		parseFileWithName(t, "type Snapshot { details: Details? }", "snapshot.luxo"),
		parseFileWithName(t, "type Details { owner: User }\nmodel User { id: Int }", "details.luxo"),
	}
	for range 2 {
		expectError(t, Analyze(files), "event payload 'Changed.payload.details.owner' cannot reference database model 'User'")
		files[0], files[2] = files[2], files[0]
	}
}

func TestEventPayloadRejectsExtendProjection(t *testing.T) {
	files := []*ast.File{
		parseFileWithName(t, "model User { id: Int }", "origin/user/model.luxo"),
		parseFileWithName(t, "extend User { id: Int }\ntype Snapshot { owner: User }\nevent Changed(payload: Snapshot)", "origin/activity/event.luxo"),
	}
	expectError(t, AnalyzeModules(files), "event payload 'Changed.payload.owner' cannot reference database model 'User'")
}

func TestEventSnapshotRejectsUnsupportedNestedWireTypes(t *testing.T) {
	for _, declaration := range []string{
		"interface Nested { id: Int }",
	} {
		result := analyze(t, declaration+"\ntype Snapshot { value: Nested }\nevent Changed(payload: Snapshot)")
		expectError(t, result, "event payload 'Changed.payload.value' has unsupported wire type 'Nested'")
	}
}

func TestEventSnapshotsAcceptClosedSealedPayloads(t *testing.T) {
	for _, payload := range []string{"Outcome", "Outcome?", "[Outcome]", "Box<Outcome>"} {
		t.Run(payload, func(t *testing.T) {
			result := analyze(t, `type Box<T> { value: T }
sealed Outcome { Success(value: Int) Nested(value: Outcome?) Pending }
event Changed(payload: `+payload+`)
`)
			expectNoErrors(t, result)
		})
	}
}

func TestEventSealedPayloadsCannotHideDatabaseModels(t *testing.T) {
	for _, payload := range []string{"Outcome", "Box<Outcome>"} {
		t.Run(payload, func(t *testing.T) {
			result := analyze(t, `model User { id: Int }
type Box<T> { value: T }
sealed Outcome { Value(user: Box<User>) Pending }
event Changed(payload: `+payload+`)
`)
			expectError(t, result, "cannot reference database model 'User'")
		})
	}
}

func TestEventSealedPayloadRejectsUnsupportedFields(t *testing.T) {
	result := analyze(t, `interface Contract { id: Int }
sealed Outcome { Value(value: Contract) Pending }
event Changed(payload: Outcome)
`)
	expectError(t, result, "has unsupported wire type 'Contract'")
}

func TestEventPayloadAcceptsExplicitSnapshots(t *testing.T) {
	result := analyze(t, `
model User { id: Int }
enum State { OPEN CLOSED }
type UserSnapshot { id: Int name: String state: State }
type Envelope { owner: UserSnapshot? owners: [UserSnapshot] metadata: JSON bytes: Bytes }
event Changed(payload: Envelope, owners: [UserSnapshot], id: Int, note: String?)
event Empty()
`)
	expectNoErrors(t, result)
}

func TestNonEventTypeMayReferenceModel(t *testing.T) {
	expectNoErrors(t, analyze(t, "model User { id: Int }\ntype Response { owner: User }\nevent Changed(id: Int)"))
}

func TestEventPublishesExplicitSnapshot(t *testing.T) {
	expectNoErrors(t, analyze(t, `
type UserSnapshot { id: Int name: String }
event Changed(user: UserSnapshot)
api publishSnapshot(id: Int, name: String): Boolean {
  emit Changed(user: UserSnapshot { id: id, name: name })
  true
}
`))
}

func TestEventSnapshotChecksInvalidTypeArguments(t *testing.T) {
	a := New()
	a.types["User"] = &ResolvedType{Kind: TypeModel, Name: "User"}
	for _, ref := range []*ast.TypeRef{
		nil,
		{Name: "Missing"},
		{Name: "Page", TypeArgs: []*ast.TypeRef{{Name: "User"}}},
		{Tuple: []*ast.TypeRef{{Name: "User"}}},
	} {
		a.validateEventPayload(eventPayloadField{ref: ref, path: "Changed.payload"}, nil)
	}
	if len(a.errors) != 2 {
		t.Fatalf("invalid containers must not hide model references: %v", a.errors)
	}
}

// This measures an additional compiler pass, not publishing or handling events.
func BenchmarkEventSnapshotContract(b *testing.B) {
	for _, count := range []int{0, 1, 16, 128} {
		b.Run(fmt.Sprintf("nested_types_%d", count), func(b *testing.B) {
			a := New()
			file := &ast.File{}
			for index := range count {
				name := fmt.Sprintf("Snapshot%d", index)
				next := "Int"
				if index+1 < count {
					next = fmt.Sprintf("Snapshot%d", index+1)
				}
				file.Types = append(file.Types, &ast.TypeDecl{Name: name, Fields: []*ast.FieldDecl{{Name: "value", Type: &ast.TypeRef{Name: next}}}})
			}
			if count != 0 {
				file.Events = []*ast.EventDecl{{Name: "Changed", Params: []*ast.ParamDecl{{Name: "payload", Type: &ast.TypeRef{Name: "Snapshot0"}}}}}
			}
			a.collectDeclarations(file)
			files := []*ast.File{file}
			b.ReportAllocs()
			for b.Loop() {
				a.validateEventSnapshots(files)
			}
			if len(a.errors) != 0 {
				b.Fatal(a.errors)
			}
		})
	}
}

func TestEmitRejectsInvalidArguments(t *testing.T) {
	for _, sample := range []struct{ name, call string }{
		{"unknown event", "emit Missing(value: 1)"},
		{"unknown field", "emit Changed(other: 1)"},
		{"missing field", "emit Changed()"},
		{"wrong type", "emit Changed(value: \"bad\")"},
		{"null", "emit Changed(value: null)"},
		{"duplicate", "emit Changed(value: 1, value: 2)"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := analyze(t, "event Changed(value: Int)\nfn send() { "+sample.call+" }")
			if len(result.Errors) == 0 {
				t.Fatal("invalid event emission accepted")
			}
		})
	}
}

func TestEventRejectsInvalidDefault(t *testing.T) {
	result := analyze(t, `event Changed(value: Int = "bad")`)
	if len(result.Errors) == 0 {
		t.Fatal("event default with wrong type accepted")
	}
}

func TestRejectsDuplicateEventDeclarations(t *testing.T) {
	t.Run("same file", func(t *testing.T) {
		result := analyze(t, `event Changed(value: Int)
event Changed(value: String)`)
		expectError(t, result, "event 'Changed' already declared")
	})
	t.Run("cross module", func(t *testing.T) {
		first := parseFileWithName(t, `event Changed(value: Int)`, "origin/first.luxo")
		second := parseFileWithName(t, `event Changed(value: String)`, "origin/second.luxo")
		expectError(t, analyzeModules(t, []*ast.File{first, second}), "event 'Changed' already declared")
	})
}

func TestCrossModuleEventEmissionRemainsValid(t *testing.T) {
	declaration := parseFileWithName(t, `event Changed(value: Int)`, "origin/first.luxo")
	caller := parseFileWithName(t, `fn send() { emit Changed(value: 1) }`, "origin/second.luxo")
	expectNoErrors(t, analyzeModules(t, []*ast.File{declaration, caller}))
}

func TestListenerRejectsInvalidContracts(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"unknown", `on Missing {}`},
		{"native body", `event Changed(value: Int)
on Changed @native { val value = it.value }`},
		{"missing body", `event Changed(value: Int)
on Changed`},
		{"duplicate binding", `event Changed(value: Int)
on Changed { value, value -> }`},
		{"value return", `event Changed(value: Int)
on Changed { return 1 }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			if result := analyze(t, sample.source); len(result.Errors) == 0 {
				t.Fatal("invalid listener accepted")
			}
		})
	}
}

func TestListenerRequiresSinglePayloadBinding(t *testing.T) {
	for _, binding := range []string{"first, second", "first, first", "first, second, third"} {
		t.Run(binding, func(t *testing.T) {
			result := analyze(t, "event Changed(value: Int, name: String)\non Changed { "+binding+" -> }")
			expectError(t, result, "listener accepts at most one payload binding")
		})
	}
	for _, body := range []string{"val value = it.value", "payload -> val value = payload.value", "ctx -> val value = ctx.value"} {
		expectNoErrors(t, analyze(t, "event Changed(value: Int, name: String)\non Changed { "+body+" }"))
	}
}
