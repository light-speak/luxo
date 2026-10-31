package schema

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

func dependencyTestSchema() *Schema {
	s := New()
	s.RegisterModel(&Model{Name: "User", Module: "accounts", Fields: []Field{
		{ID: 1, Name: "id", Type: FieldInt},
		{ID: 2, Name: "posts", Type: FieldModel, TypeName: "Post", Relation: true},
		{ID: 3, Name: "postCount", Type: FieldInt, Computed: true},
	}, ComputedSources: map[string]ComputedSource{"postCount": {Model: "Post", Module: "content"}}})
	s.RegisterModel(&Model{Name: "Post", Module: "content", Fields: []Field{
		{ID: 1, Name: "id", Type: FieldInt},
		{ID: 2, Name: "author", Type: FieldModel, TypeName: "User", Relation: true, TargetModule: "accounts"},
	}})
	s.RegisterType(&TypeDecl{Name: "Result", Fields: []Field{
		{ID: 1, Name: "user", Type: FieldModel, TypeName: "User", Relation: true},
	}})
	return s
}

func TestSelectionDependencies(t *testing.T) {
	s := dependencyTestSchema()
	for _, test := range []struct {
		name, root, selected string
		want                 []ComputedSource
	}{
		{"scalar does not invent root read", "User", "id", nil},
		{"selected aggregate", "User", "postCount", []ComputedSource{{Model: "Post", Module: "content"}}},
		{"selected relation", "User", "posts { id }", []ComputedSource{{Model: "Post", Module: "content"}}},
		{"deduplicate reads", "User", "postCount, posts { id }", []ComputedSource{{Model: "Post", Module: "content"}}},
		{"nested cycle", "User", "posts { author { postCount } }", []ComputedSource{{Model: "Post", Module: "content"}, {Model: "User", Module: "accounts"}}},
		{"plain result is not a loader", "Result", "user { id }", nil},
		{"plain result still descends", "Result", "user { postCount }", []ComputedSource{{Model: "Post", Module: "content"}}},
		{"unspecified children conservatively bounded", "User", "posts", []ComputedSource{{Model: "Post", Module: "content"}, {Model: "User", Module: "accounts"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := s.PrepareSelectionDependencies(test.root)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := selection.Parse(test.selected)
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.AppendSources(nil, fields)
			if err != nil || !slices.Equal(got, test.want) {
				t.Fatalf("got %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestSelectionDependenciesRejectIncompleteMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*Schema)
		message string
	}{
		{"missing aggregate source", func(s *Schema) { s.Models["User"].ComputedSources = nil }, "postCount"},
		{"unknown source", func(s *Schema) { s.Models["User"].ComputedSources["postCount"] = ComputedSource{Model: "Missing"} }, "Missing"},
		{"incorrect source owner", func(s *Schema) {
			s.Models["User"].ComputedSources["postCount"] = ComputedSource{Model: "Post", Module: "wrong"}
		}, "module"},
		{"unregistered relation", func(s *Schema) { delete(s.Models, "Post") }, "Post"},
		{"incorrect relation owner", func(s *Schema) { s.Models["Post"].Fields[1].TargetModule = "wrong" }, "module"},
		{"orphan source", func(s *Schema) { s.Models["User"].ComputedSources["absent"] = ComputedSource{Model: "Post"} }, "absent"},
		{"noncomputed source", func(s *Schema) { s.Models["User"].ComputedSources["id"] = ComputedSource{Model: "Post"} }, "id"},
		{"duplicate field", func(s *Schema) { s.Models["User"].Fields = append(s.Models["User"].Fields, s.Models["User"].Fields[0]) }, "duplicate"},
		{"structured computed", func(s *Schema) { s.Models["User"].Fields[2].Type = FieldModel }, "structured"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := dependencyTestSchema()
			test.mutate(s)
			plan, err := s.PrepareSelectionDependencies("User")
			if err == nil || plan != nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, %v", plan, err)
			}
		})
	}
	if _, err := dependencyTestSchema().PrepareSelectionDependencies("Missing"); err == nil {
		t.Fatal("accepted missing root")
	}
}

func TestSelectionDependenciesClosureWithinModule(t *testing.T) {
	s := dependencyTestSchema()
	s.RegisterModel(&Model{Name: "Comment", Module: "content", Fields: []Field{{ID: 1, Name: "id", Type: FieldInt}}})
	s.RegisterModel(&Model{Name: "User", Fields: []Field{{ID: 4, Name: "comments", Type: FieldModel, TypeName: "Comment", Relation: true}}})
	plan, err := s.PrepareSelectionDependencies("Result")
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.AppendSources(nil, []*selection.Field{{Name: "user"}})
	want := []ComputedSource{{Model: "User", Module: "accounts"}, {Model: "Comment", Module: "content"}, {Model: "Post", Module: "content"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

func TestSelectionDependenciesValidateSelectionAndRollback(t *testing.T) {
	plan, err := dependencyTestSchema().PrepareSelectionDependencies("User")
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range [][]*selection.Field{
		nil, {}, {nil}, {{Name: "missing"}},
		{{Name: "postCount"}, {Name: "missing"}},
		{{Name: "id", Children: []*selection.Field{{Name: "id"}}}},
		{{Name: "posts", Children: []*selection.Field{{Name: "missing"}}}},
	} {
		original := []ComputedSource{{Model: "Existing"}}
		got, err := plan.AppendSources(original, fields)
		if err == nil || !slices.Equal(got, original) {
			t.Fatalf("expected rollback, got %v, %v", got, err)
		}
	}
	deep := &selection.Field{Name: "posts"}
	deep.Children = []*selection.Field{{Name: "author", Children: []*selection.Field{deep}}}
	if _, err := plan.AppendSources(nil, []*selection.Field{deep}); err == nil {
		t.Fatal("accepted cyclic selection")
	}
}

func TestSelectionDependenciesSnapshotAndConcurrentReuse(t *testing.T) {
	s := dependencyTestSchema()
	plan, err := s.PrepareSelectionDependencies("User")
	if err != nil {
		t.Fatal(err)
	}
	// A replacement Schema is built separately; the plan keeps its own metadata.
	s.Models["User"].ComputedSources["postCount"] = ComputedSource{Model: "Changed"}
	s.Models["User"].Fields[2].Name = "changed"
	want := ComputedSource{Model: "Post", Module: "content"}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			storage := make([]ComputedSource, 0, 2)
			for range 50 {
				got, err := plan.AppendSources(storage, []*selection.Field{{Name: "postCount"}})
				if err != nil || len(got) != 1 || got[0] != want {
					t.Errorf("got %v, %v", got, err)
					return
				}
			}
		})
	}
	workers.Wait()
}

func TestSelectionDependenciesLargePlanAndExistingSources(t *testing.T) {
	s, fields := dependencyBenchmarkSchema(257)
	plan, err := s.PrepareSelectionDependencies("Root")
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]ComputedSource, 2, 258)
	dst[0] = ComputedSource{Model: "External"}
	dst[1] = ComputedSource{Model: "Model256", Module: "test"}
	got, err := plan.AppendSources(dst, fields)
	if err != nil || len(got) != 258 || got[0] != dst[0] || got[1] != dst[1] {
		t.Fatalf("got %d entries, %v", len(got), err)
	}
	seen := make(map[ComputedSource]bool)
	for _, source := range got {
		if seen[source] {
			t.Fatalf("duplicate %v", source)
		}
		seen[source] = true
	}
}

func TestSelectionDependenciesPureModel(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "Pure", Fields: []Field{{ID: 1, Name: "id", Type: FieldInt}}})
	plan, err := s.PrepareSelectionDependencies("Pure")
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.AppendSources(nil, []*selection.Field{{Name: "id"}})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestSelectionDependenciesReuseDoesNotAllocate(t *testing.T) {
	s, fields := dependencyBenchmarkSchema(128)
	plan, err := s.PrepareSelectionDependencies("Root")
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]ComputedSource, 0, 128)
	allocs := testing.AllocsPerRun(100, func() {
		got, err := plan.AppendSources(dst, fields)
		if err != nil || len(got) != 128 {
			t.Fatalf("got %d entries, %v", len(got), err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm collection allocated: %v", allocs)
	}
}
