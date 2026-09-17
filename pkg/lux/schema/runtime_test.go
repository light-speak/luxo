package schema

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestLookupModelUsesPreparedType(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 2, Name: "value", Type: FieldInt}}})
	m := s.LookupModel("Payload")
	if m == nil || m.FieldByID(2) != m.FieldByName("value") || string(m.Fields[0].JSONPrefix) != `"value":` {
		t.Fatalf("unprepared type: %#v", m)
	}
	if s.LookupModel("Payload") != m || s.LookupModel("missing") != nil {
		t.Fatal("lookup did not reuse prepared metadata")
	}
	if got := testing.AllocsPerRun(100, func() {
		if s.LookupModel("Payload") != m {
			t.Fatal("metadata changed during lookup")
		}
	}); got != 0 {
		t.Fatalf("lookup allocates: %g", got)
	}
}

func TestRegisterTypeReplacesIndexesAndDeletedFields(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 1, Name: "old"}, {ID: 2, Name: "deleted"}}})
	previous := s.LookupModel("Payload")
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 1, Name: "renamed"}}})
	current := s.LookupModel("Payload")
	if current == previous || len(current.Fields) != 1 || current.FieldByName("renamed") == nil {
		t.Fatal("type re-registration did not rebuild metadata")
	}
	if current.FieldByName("old") != nil || current.FieldByID(2) != nil || current.FieldByName("deleted") != nil {
		t.Fatal("removed type field remains indexed")
	}
	if previous.FieldByName("old") == nil || string(previous.Fields[0].JSONPrefix) != `"old":` {
		t.Fatal("re-registration mutated the previous type view")
	}
}

func TestLookupModelPreservesModelPriorityAndStubMerging(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Item", Fields: []Field{{ID: 9, Name: "typeOnly"}}})
	s.RegisterModel(&Model{Name: "Item", Fields: []Field{{ID: 1, Name: "id", PrimaryKey: true}}})
	s.RegisterModel(&Model{Name: "Item", Fields: []Field{{ID: 2, Name: "name", Type: FieldString}}})
	m := s.LookupModel("Item")
	if m != s.Models["Item"] || len(m.Fields) != 2 || m.FieldByName("id") == nil || m.FieldByName("name") == nil {
		t.Fatal("model lookup lost stub merging")
	}
	if m.FieldByID(9) != nil || !m.PrimaryKeyField().PrimaryKey {
		t.Fatal("type took priority over the model")
	}
}

func TestRegisteredTypeOwnershipAndUsageAreSchemaLocal(t *testing.T) {
	decl := &TypeDecl{Name: "Payload", Directives: []string{"doc"}, Fields: []Field{{ID: 1, Name: "value", Directives: []string{"visible"}}}}
	first, second := New(), New()
	first.RegisterType(decl)
	second.RegisterType(decl)
	decl.Fields[0].Name, decl.Directives[0], decl.Fields[0].Directives[0] = "changed", "changed", "changed"
	first.RegisterAPI(&API{Name: "read", ReturnType: "Payload"})
	first.InferTypeUsage()
	second.InferTypeUsage()
	for _, s := range []*Schema{first, second} {
		m := s.LookupModel("Payload")
		if m.FieldByName("value") == nil || m.Directives[0] != "doc" || m.Fields[0].Directives[0] != "visible" {
			t.Fatal("registration retained mutable caller storage")
		}
		if m.Usage != s.Types["Payload"].Usage {
			t.Fatal("prepared usage differs from introspection")
		}
	}
	if first.LookupModel("Payload") == second.LookupModel("Payload") || first.Types["Payload"].Usage != TypeUsageOutput {
		t.Fatal("separate schemas share type state")
	}
	if second.LookupModel("Payload").Usage != TypeUsageUnused || decl.Usage != "" {
		t.Fatal("usage inference escaped its schema")
	}
	delete(first.APIs, "read")
	first.InferTypeUsage()
	if first.LookupModel("Payload").Usage != TypeUsageUnused {
		t.Fatal("usage inference did not reset prepared metadata")
	}
}

func TestPrepareDecodedSchemaRebuildsWithoutChangingIntrospection(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "Item", Fields: []Field{{ID: 1, Name: "id"}}})
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 2, Name: "value"}}})
	s.RegisterAPI(&API{Name: "read", ReturnType: "Payload"})
	s.InferTypeUsage()
	before, err := s.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Schema
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LookupModel("Payload") != nil {
		t.Fatal("decoded types must not be lazily prepared by lookup")
	}
	decoded.InferTypeUsage()
	decoded.Prepare()
	for _, name := range []string{"Item", "Payload"} {
		m := decoded.LookupModel(name)
		if m == nil || m.FieldByID(m.Fields[0].ID) == nil || len(m.Fields[0].JSONPrefix) == 0 {
			t.Fatalf("unprepared decoded metadata: %s", name)
		}
	}
	after, err := decoded.ToJSON()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("introspection changed: %s / %s: %v", before, after, err)
	}
	delete(decoded.Types, "Payload")
	decoded.Prepare()
	if decoded.LookupModel("Payload") != nil {
		t.Fatal("explicit rebuild retained a deleted type")
	}
}

func TestPrepareRebuildsEditedTypeAndEmptySchema(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 2, Name: "old"}, {ID: 3, Name: "deleted"}}})
	s.Types["Payload"].Fields = []Field{{ID: 4, Name: "new"}}
	s.Prepare()
	m := s.LookupModel("Payload")
	if m.FieldByID(2) != nil || m.FieldByID(3) != nil || m.FieldByName("new") == nil {
		t.Fatal("explicit startup rebuild did not reflect changed fields")
	}
	var empty Schema
	empty.Prepare()
	if empty.LookupModel("missing") != nil {
		t.Fatal("empty schema has metadata")
	}
}

func TestPreparedSchemaConcurrentConversions(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Child", Fields: []Field{{ID: 1, Name: "value", Type: FieldInt}}})
	s.RegisterType(&TypeDecl{Name: "Parent", Fields: []Field{{ID: 2, Name: "child", Type: FieldModel, TypeName: "Child", Relation: true}}})
	s.RegisterAPI(&API{Name: "read", ReturnType: "Parent"})
	s.InferTypeUsage()
	fields, err := selection.Parse("child { value }")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				assertPreparedSchemaConversion(t, s, fields)
			}
		})
	}
	wg.Wait()
}

func assertPreparedSchemaConversion(t *testing.T, s *Schema, fields []*selection.Field) {
	t.Helper()
	model := s.LookupModel("Parent")
	if _, err := SelectToFieldMask(fields, model, s); err != nil {
		t.Error(err)
	}
	wire, err := s.JSONToBinary("Child", json.RawMessage(`{"value":42}`))
	if err != nil {
		t.Error(err)
		return
	}
	result, err := BinaryToJSON(nil, wire, s.LookupModel("Child"), s)
	if err != nil || string(result) != `{"value":42}` {
		t.Errorf("conversion: %q, %v", result, err)
	}
	copy := s.Types["Child"].AsModel()
	copy.Fields[0].Name = "local"
	copy.Fields[0].JSONPrefix[1] = 'X'
	if s.LookupModel("Child").Fields[0].Name != "value" {
		t.Error("public copy altered prepared metadata")
	}
}
