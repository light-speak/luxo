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
		"sealed Nested { Success(value: Int) }",
	} {
		result := analyze(t, declaration+"\ntype Snapshot { value: Nested }\nevent Changed(payload: Snapshot)")
		expectError(t, result, "event payload 'Changed.payload.value' has unsupported wire type 'Nested'")
	}
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
