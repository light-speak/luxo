package schema

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestRegistrationRejectsInvalidFieldsWithoutReplacingIndexes(t *testing.T) {
	for _, fields := range [][]Field{
		{{ID: 0, Name: "bad"}}, {{ID: -1, Name: "bad"}}, {{ID: codec.MaxSelectionFieldID + 1, Name: "bad"}},
		{{ID: 1}}, {{ID: 1, Name: "a"}, {ID: 1, Name: "b"}}, {{ID: 1, Name: "a"}, {ID: 2, Name: "a"}},
	} {
		s := New()
		if err := s.RegisterModel(&Model{Name: "Record", Fields: []Field{{ID: 1, Name: "id"}}}); err != nil {
			t.Fatal(err)
		}
		if err := s.RegisterType(&TypeDecl{Name: "Value", Fields: []Field{{ID: 1, Name: "id"}}}); err != nil {
			t.Fatal(err)
		}
		model, typ := s.LookupModel("Record"), s.LookupModel("Value")
		if err := s.RegisterModel(&Model{Name: "Record", Fields: fields}); err == nil {
			t.Fatal("invalid model accepted")
		}
		if err := s.RegisterType(&TypeDecl{Name: "Value", Fields: fields}); err == nil {
			t.Fatal("invalid type accepted")
		}
		if s.LookupModel("Record") != model || s.LookupModel("Value") != typ {
			t.Fatal("failed registration replaced indexes")
		}
	}
}

func TestRegistrationRejectsNilOrUnnamedDeclarations(t *testing.T) {
	s := New()
	for _, model := range []*Model{nil, {}} {
		if err := s.RegisterModel(model); err == nil {
			t.Fatal("invalid model accepted")
		}
	}
	for _, typ := range []*TypeDecl{nil, {}} {
		if err := s.RegisterType(typ); err == nil {
			t.Fatal("invalid type accepted")
		}
	}
	for _, api := range []*API{nil, {}, {Name: "bad", ID: -1}, {Name: "bad", ID: codec.MaxAPIID + 1}} {
		if err := s.RegisterAPI(api); err == nil {
			t.Fatal("invalid API accepted")
		}
	}
}

func TestRegistrationRejectsAPIIdentityConflicts(t *testing.T) {
	s := New()
	old := &API{Name: "read", ID: 1}
	if err := s.RegisterAPI(old); err != nil {
		t.Fatal(err)
	}
	for _, api := range []*API{
		{Name: "other", ID: 1}, {Name: "read", ID: 2},
		{Name: "read", ID: 1, Params: []Param{{Name: "id", ID: 0}}},
		{Name: "read", ID: 1, Params: []Param{{Name: "id", ID: codec.FiltersFieldID}}},
		{Name: "read", ID: 1, Params: []Param{{Name: "id", ID: 1}, {Name: "other", ID: 1}}},
	} {
		if err := s.RegisterAPI(api); err == nil {
			t.Fatalf("invalid API accepted: %+v", api)
		}
		if len(s.APIs) != 1 || s.APIs["read"] != old {
			t.Fatal("failed API registration changed state")
		}
	}
	if err := s.RegisterAPI(&API{Name: "read", ID: 1, Description: "updated"}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareValidatesBeforeChangingRuntimeIndexes(t *testing.T) {
	s := New()
	if err := s.RegisterType(&TypeDecl{Name: "Value", Fields: []Field{{ID: 1, Name: "id"}}}); err != nil {
		t.Fatal(err)
	}
	old := s.LookupModel("Value")
	s.Types["Value"].Fields = []Field{{ID: 2, Name: "new"}}
	s.APIs["invalid"] = &API{Name: "invalid", ID: 0}
	if err := s.Prepare(); err == nil {
		t.Fatal("invalid startup schema accepted")
	}
	if s.LookupModel("Value") != old || old.FieldByName("id") == nil {
		t.Fatal("failed preparation replaced working indexes")
	}
	delete(s.APIs, "invalid")
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	if s.LookupModel("Value").FieldByName("new") == nil {
		t.Fatal("retry did not rebuild indexes")
	}
}

func TestPrepareRejectsCorruptDeclarations(t *testing.T) {
	for _, s := range []*Schema{
		nil,
		{Models: map[string]*Model{"bad": nil}},
		{Models: map[string]*Model{"bad": {Name: "other"}}},
		{Models: map[string]*Model{"bad": {Name: "bad", Fields: []Field{{ID: 0, Name: "id"}}}}},
		{Types: map[string]*TypeDecl{"bad": nil}},
		{Types: map[string]*TypeDecl{"bad": {Name: "other"}}},
		{Types: map[string]*TypeDecl{"bad": {Name: "bad", Fields: []Field{{ID: 0, Name: "id"}}}}},
		{APIs: map[string]*API{"bad": nil}},
		{APIs: map[string]*API{"bad": {Name: "other", ID: 1}}},
		{APIs: map[string]*API{"a": {Name: "a", ID: 1}, "b": {Name: "b", ID: 1}}},
	} {
		if err := s.Prepare(); err == nil {
			t.Fatal("invalid declarations accepted")
		}
	}
}

func TestRegistrationSupportsZeroSchemaAndMaximumIDs(t *testing.T) {
	var s Schema
	if err := s.RegisterModel(&Model{Name: "Record", Fields: []Field{{ID: codec.MaxSelectionFieldID, Name: "last"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterType(&TypeDecl{Name: "Value", Fields: []Field{{ID: codec.MaxSelectionFieldID, Name: "last"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterAPI(&API{Name: "read", ID: codec.MaxAPIID, Params: []Param{{ID: codec.MaxParameterID, Name: "last"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	if s.LookupModel("Value").FieldByID(codec.MaxSelectionFieldID) == nil {
		t.Fatal("lost largest field")
	}
}

func TestRegistrationRejectsInvalidMergedModelAndDecodedAPIs(t *testing.T) {
	s := New()
	model := &Model{Name: "Record", Fields: []Field{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}}
	if err := s.RegisterModel(model); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterModel(&Model{Name: "Record", Fields: []Field{{ID: 1, Name: "b"}}}); err == nil {
		t.Fatal("merged duplicate name accepted")
	}
	if s.Models["Record"] != model || model.FieldByName("a") == nil {
		t.Fatal("invalid merge replaced existing model")
	}
	s.APIs["bad"] = &API{Name: "bad", ID: 0}
	if err := s.RegisterAPI(&API{Name: "good", ID: 1}); err == nil || s.APIs["good"] != nil {
		t.Fatal("registered over an invalid decoded directory")
	}
	delete(s.APIs, "bad")
	if err := s.RegisterAPI(&API{Name: "good", ID: 1}); err != nil {
		t.Fatal(err)
	}
}
