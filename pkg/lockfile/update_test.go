package lockfile

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestUpdateExhaustionDoesNotMutateOriginal(t *testing.T) {
	for _, kind := range []string{"model", "type", "event", "api", "param"} {
		t.Run(kind, func(t *testing.T) {
			lf := New()
			lf.Models["Removed"] = &ModelLock{NextID: 2, Fields: map[string]int{"old": 1}, FieldTypes: map[string]string{"old": "String"}}
			input := &ast.File{Models: []*ast.ModelDecl{model("Added", field("value", "Int"))}}
			full := &ModelLock{NextID: codec.MaxSelectionFieldID + 1, Fields: map[string]int{}}
			switch kind {
			case "model":
				lf.Models["Added"] = full
			case "type":
				lf.Types = map[string]*ModelLock{"T": full}
				input.Types = []*ast.TypeDecl{{Name: "T", Fields: []*ast.FieldDecl{field("value", "Int")}}}
			case "event":
				full.NextID = codec.MaxEventFieldID + 1
				lf.Events["E"] = full
				input.Events = []*ast.EventDecl{{Name: "E", Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}}
			case "api":
				lf.ReservedAPI = []int{codec.MaxAPIID}
				input.APIs = []*ast.ApiDecl{api("added")}
			case "param":
				lf.APIs["added"] = &APILock{ID: 1, Reserved: []int{MaxParameterID}}
				input.APIs = []*ast.ApiDecl{{Name: "added", Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}}}
			}
			lf.computeNextAPI()
			before := lockJSON(t, lf)
			next := lf.nextAPI
			original := lf.Models["Removed"]
			if err := lf.Update([]*ast.File{input}); err == nil {
				t.Fatal("exhausted identifier space accepted")
			}
			if after := lockJSON(t, lf); after != before || lf.nextAPI != next || lf.Models["Removed"] != original {
				t.Fatalf("failed update mutated original\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func lockJSON(t *testing.T, lf *LockFile) string {
	t.Helper()
	data, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustUpdate(t testing.TB, lf *LockFile, input []*ast.File) {
	t.Helper()
	if err := lf.Update(input); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDerivesCountersFromAssignments(t *testing.T) {
	lf := New()
	lf.APIs["existing"] = &APILock{ID: 7, Params: map[string]int{"value": 3}, Reserved: []int{8}, nextP: math.MaxInt}
	lf.ReservedAPI = []int{9}
	lf.nextAPI = math.MaxInt
	if err := lf.Update([]*ast.File{{APIs: []*ast.ApiDecl{
		{Name: "existing", Params: []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}, {Name: "extra", Type: &ast.TypeRef{Name: "Int"}}}},
		{Name: "added"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if lf.APIID("added") != 10 || lf.APIParamID("existing", "extra") != 9 {
		t.Fatalf("trusted stale transient counters: api=%d param=%d", lf.APIID("added"), lf.APIParamID("existing", "extra"))
	}
}

func TestUpdateRejectsInvalidOriginal(t *testing.T) {
	var missing *LockFile
	if err := missing.Update(nil); err == nil {
		t.Fatal("nil lock accepted")
	}
	lf := New()
	lf.APIs["invalid"] = &APILock{ID: 0}
	before := lockJSON(t, lf)
	if err := lf.Update(nil); err == nil || lockJSON(t, lf) != before {
		t.Fatalf("invalid original changed: %v", err)
	}
}

func TestUpdateOwnsEveryMutableMapAndSlice(t *testing.T) {
	lf := New()
	lf.Models["M"] = &ModelLock{NextID: 4, Fields: map[string]int{"old": 1}, FieldTypes: map[string]string{"old": "Int"}, Reserved: make([]int, 1, 8)}
	lf.Models["M"].Reserved[0] = 3
	lf.Types = map[string]*ModelLock{"T": {NextID: 1}}
	lf.Events["E"] = &ModelLock{NextID: 1}
	lf.APIs["a"] = &APILock{ID: 1, Params: map[string]int{"old": 1}, ParamTypes: map[string]string{"old": "Int"}, Reserved: make([]int, 1, 8)}
	lf.APIs["a"].Reserved[0] = 3
	lf.ReservedAPI = make([]int, 1, 8)
	lf.ReservedAPI[0] = 2
	old := *lf
	before := lockJSON(t, &old)
	input := []*ast.File{{
		Models: []*ast.ModelDecl{model("M", field("next", "Int"))},
		Types:  []*ast.TypeDecl{{Name: "T", Fields: []*ast.FieldDecl{field("next", "Int")}}},
		Events: []*ast.EventDecl{{Name: "E", Params: []*ast.ParamDecl{{Name: "next", Type: &ast.TypeRef{Name: "Int"}}}}},
		APIs:   []*ast.ApiDecl{{Name: "a", Params: []*ast.ParamDecl{{Name: "next", Type: &ast.TypeRef{Name: "Int"}}}}},
	}}
	if err := lf.Update(input); err != nil {
		t.Fatal(err)
	}
	lf.Models["M"].FieldTypes["old"] = "changed"
	lf.APIs["a"].ParamTypes["old"] = "changed"
	lf.Models["M"].Reserved[0] = 99
	lf.APIs["a"].Reserved[0] = 99
	lf.ReservedAPI[0] = 99
	if lockJSON(t, &old) != before {
		t.Fatal("successful update retained mutable aliases")
	}
}

func TestUpdateRepeatedRemovalPreservesTombstones(t *testing.T) {
	lf := New()
	lf.Models["M"] = &ModelLock{NextID: 4, Fields: map[string]int{"old": 1}, Reserved: []int{3}}
	lf.Types = map[string]*ModelLock{"T": {NextID: 4, Fields: map[string]int{"old": 1}, Reserved: []int{3}}}
	lf.Events = nil
	input := []*ast.File{{Enums: []*ast.EnumDecl{{Name: "State", Values: []string{"READY"}}}}}
	mustUpdate(t, lf, input)
	before := lockJSON(t, lf)
	mustUpdate(t, lf, input)
	if lockJSON(t, lf) != before {
		t.Fatal("repeated removal changed tombstones")
	}
	for _, group := range []map[string]*ModelLock{lf.Models, lf.Types} {
		for _, entry := range group {
			if len(entry.Fields) != 0 || len(entry.Reserved) != 2 {
				t.Fatal("removed fields were lost or duplicated")
			}
		}
	}
}
