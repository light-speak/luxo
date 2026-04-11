package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func pos() token.Position {
	return token.Position{File: "test.luxo", Line: 1, Col: 1}
}

func field(name, typeName string) *ast.FieldDecl {
	return &ast.FieldDecl{
		Pos:  pos(),
		Name: name,
		Type: &ast.TypeRef{Name: typeName},
	}
}

func computed(name string) *ast.FieldDecl {
	return &ast.FieldDecl{
		Pos:      pos(),
		Name:     name,
		Computed: &ast.ComputedField{},
	}
}

func model(name string, fields ...*ast.FieldDecl) *ast.ModelDecl {
	return &ast.ModelDecl{
		Pos:    pos(),
		Name:   name,
		Fields: fields,
	}
}

func api(name string) *ast.ApiDecl {
	return &ast.ApiDecl{
		Pos:  pos(),
		Name: name,
	}
}

func files(models []*ast.ModelDecl, apis []*ast.ApiDecl) []*ast.File {
	return []*ast.File{{
		Name:   "test.luxo",
		Models: models,
		APIs:   apis,
	}}
}

func TestNew(t *testing.T) {
	lf := New()
	if lf.Version != 1 {
		t.Errorf("Version = %d, want 1", lf.Version)
	}
	if len(lf.Models) != 0 {
		t.Error("Models should be empty")
	}
	if len(lf.APIs) != 0 {
		t.Error("APIs should be empty")
	}
}

func TestLoadNonExistent(t *testing.T) {
	lf, err := Load("/nonexistent/path/luxo.lock")
	if err != nil {
		t.Fatalf("Load non-existent should not error: %v", err)
	}
	if lf.Version != 1 {
		t.Error("Should return empty lock file")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luxo.lock")
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Expected error for invalid JSON")
	}
}

func TestLoadUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "luxo.lock")
	// Create subdir as a file so reading the path inside it fails
	os.WriteFile(filepath.Join(dir, "subdir"), []byte("file"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Expected error for unreadable path")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luxo.lock")

	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID:   4,
		Fields:   map[string]int{"id": 1, "name": 2, "email": 3},
		Reserved: []int{},
	}
	lf.APIs["getUser"] = 1

	if err := lf.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if loaded.Models["User"].NextID != 4 {
		t.Errorf("NextID = %d, want 4", loaded.Models["User"].NextID)
	}
	if loaded.Models["User"].Fields["name"] != 2 {
		t.Error("Field name should have ID 2")
	}
	if loaded.APIs["getUser"] != 1 {
		t.Error("API getUser should have ID 1")
	}
}

func TestUpdateNewModel(t *testing.T) {
	lf := New()
	f := files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
			field("email", "String"),
		)},
		nil,
	)

	lf.Update(f)

	ml := lf.Models["User"]
	if ml == nil {
		t.Fatal("User model should exist in lock")
	}
	if ml.NextID != 4 {
		t.Errorf("NextID = %d, want 4", ml.NextID)
	}
	if ml.Fields["id"] != 1 || ml.Fields["name"] != 2 || ml.Fields["email"] != 3 {
		t.Errorf("Fields = %v, want id:1, name:2, email:3", ml.Fields)
	}
}

func TestUpdateExistingModelKeepsIDs(t *testing.T) {
	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID: 4,
		Fields: map[string]int{"id": 1, "name": 2, "email": 3},
	}

	f := files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
			field("email", "String"),
			field("phone", "String"),
		)},
		nil,
	)

	lf.Update(f)

	ml := lf.Models["User"]
	if ml.Fields["id"] != 1 || ml.Fields["name"] != 2 || ml.Fields["email"] != 3 {
		t.Error("Existing field IDs should not change")
	}
	if ml.Fields["phone"] != 4 {
		t.Errorf("phone = %d, want 4", ml.Fields["phone"])
	}
	if ml.NextID != 5 {
		t.Errorf("NextID = %d, want 5", ml.NextID)
	}
}

func TestUpdateRemovedFieldReserved(t *testing.T) {
	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID: 4,
		Fields: map[string]int{"id": 1, "name": 2, "avatar": 3},
	}

	// avatar removed, profileImage added
	f := files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
			field("profileImage", "String"),
		)},
		nil,
	)

	lf.Update(f)

	ml := lf.Models["User"]
	if _, ok := ml.Fields["avatar"]; ok {
		t.Error("avatar should be removed from fields")
	}
	if len(ml.Reserved) != 1 || ml.Reserved[0] != 3 {
		t.Errorf("Reserved = %v, want [3]", ml.Reserved)
	}
	if ml.Fields["profileImage"] != 4 {
		t.Errorf("profileImage = %d, want 4", ml.Fields["profileImage"])
	}
	if ml.NextID != 5 {
		t.Errorf("NextID = %d, want 5", ml.NextID)
	}
}

func TestUpdateModelRemovedEntirely(t *testing.T) {
	lf := New()
	lf.Models["OldModel"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"a": 1, "b": 2},
	}

	// No models in AST
	f := files(nil, nil)
	lf.Update(f)

	ml := lf.Models["OldModel"]
	if len(ml.Fields) != 0 {
		t.Errorf("Fields should be empty, got %v", ml.Fields)
	}
	if len(ml.Reserved) != 2 {
		t.Errorf("Reserved = %v, want 2 entries", ml.Reserved)
	}
}

func TestUpdateComputedFieldsSkipped(t *testing.T) {
	lf := New()
	f := files(
		[]*ast.ModelDecl{model("Post",
			field("title", "String"),
			computed("totalLikes"),
		)},
		nil,
	)

	lf.Update(f)

	ml := lf.Models["Post"]
	if _, ok := ml.Fields["totalLikes"]; ok {
		t.Error("Computed fields should not get IDs")
	}
	if ml.NextID != 2 {
		t.Errorf("NextID = %d, want 2", ml.NextID)
	}
}

func TestUpdateAPIs(t *testing.T) {
	lf := New()
	f := files(nil, []*ast.ApiDecl{
		api("getUser"),
		api("listUsers"),
	})

	lf.Update(f)

	if lf.APIs["getUser"] != 1 {
		t.Errorf("getUser = %d, want 1", lf.APIs["getUser"])
	}
	if lf.APIs["listUsers"] != 2 {
		t.Errorf("listUsers = %d, want 2", lf.APIs["listUsers"])
	}
}

func TestUpdateAPIsPreservesExisting(t *testing.T) {
	lf := New()
	lf.APIs["getUser"] = 5
	lf.nextAPI = 5

	f := files(nil, []*ast.ApiDecl{
		api("getUser"),
		api("createUser"),
	})

	lf.Update(f)

	if lf.APIs["getUser"] != 5 {
		t.Errorf("getUser = %d, want 5 (preserved)", lf.APIs["getUser"])
	}
	if lf.APIs["createUser"] != 6 {
		t.Errorf("createUser = %d, want 6", lf.APIs["createUser"])
	}
}

func TestFieldID(t *testing.T) {
	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"id": 1, "name": 2},
	}

	if id := lf.FieldID("User", "name"); id != 2 {
		t.Errorf("FieldID = %d, want 2", id)
	}
	if id := lf.FieldID("User", "missing"); id != 0 {
		t.Errorf("FieldID = %d, want 0 for missing", id)
	}
	if id := lf.FieldID("NoModel", "x"); id != 0 {
		t.Errorf("FieldID = %d, want 0 for missing model", id)
	}
}

func TestAPIID(t *testing.T) {
	lf := New()
	lf.APIs["getUser"] = 3

	if id := lf.APIID("getUser"); id != 3 {
		t.Errorf("APIID = %d, want 3", id)
	}
	if id := lf.APIID("missing"); id != 0 {
		t.Errorf("APIID = %d, want 0 for missing", id)
	}
}

func TestSortReserved(t *testing.T) {
	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID:   5,
		Fields:   map[string]int{"id": 1},
		Reserved: []int{4, 2, 3},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "luxo.lock")
	lf.Save(path)

	data, _ := os.ReadFile(path)
	var loaded LockFile
	json.Unmarshal(data, &loaded)

	r := loaded.Models["User"].Reserved
	if len(r) != 3 || r[0] != 2 || r[1] != 3 || r[2] != 4 {
		t.Errorf("Reserved should be sorted: %v", r)
	}
}

func TestReservedNotDuplicated(t *testing.T) {
	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID:   4,
		Fields:   map[string]int{"id": 1, "name": 2, "avatar": 3},
		Reserved: []int{3}, // avatar already reserved
	}

	// avatar removed again (was already in reserved)
	f := files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
		)},
		nil,
	)

	lf.Update(f)

	ml := lf.Models["User"]
	// Should not duplicate the reserved entry
	count := 0
	for _, id := range ml.Reserved {
		if id == 3 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Reserved ID 3 appears %d times, want 1", count)
	}
}

func TestModelRemovedAllFieldsReservedNoDuplicates(t *testing.T) {
	lf := New()
	lf.Models["Old"] = &ModelLock{
		NextID:   3,
		Fields:   map[string]int{"a": 1, "b": 2},
		Reserved: []int{1}, // a already reserved
	}

	f := files(nil, nil) // model removed
	lf.Update(f)

	ml := lf.Models["Old"]
	// a(1) was already reserved, b(2) should be newly reserved
	count1 := 0
	for _, id := range ml.Reserved {
		if id == 1 {
			count1++
		}
	}
	if count1 != 1 {
		t.Errorf("Reserved ID 1 appears %d times, want 1", count1)
	}
	if len(ml.Reserved) != 2 {
		t.Errorf("Reserved = %v, want 2 entries", ml.Reserved)
	}
}

func TestMultipleFilesMultipleModels(t *testing.T) {
	lf := New()
	ff := []*ast.File{
		{
			Name: "user.luxo",
			Models: []*ast.ModelDecl{model("User",
				field("id", "Int"),
				field("name", "String"),
			)},
			APIs: []*ast.ApiDecl{api("getUser")},
		},
		{
			Name: "post.luxo",
			Models: []*ast.ModelDecl{model("Post",
				field("id", "Int"),
				field("title", "String"),
			)},
			APIs: []*ast.ApiDecl{api("getPost")},
		},
	}

	lf.Update(ff)

	if lf.Models["User"].Fields["id"] != 1 {
		t.Error("User.id should be 1")
	}
	if lf.Models["Post"].Fields["id"] != 1 {
		t.Error("Post.id should be 1 (per-model numbering)")
	}
	if lf.APIs["getUser"] != 1 {
		t.Error("getUser should be 1")
	}
	if lf.APIs["getPost"] != 2 {
		t.Error("getPost should be 2")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luxo.lock")

	// First gen: create User with 3 fields
	lf, _ := Load(path)
	lf.Update(files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
			field("avatar", "String"),
		)},
		[]*ast.ApiDecl{api("getUser")},
	))
	lf.Save(path)

	// Second gen: remove avatar, add phone
	lf2, _ := Load(path)
	lf2.Update(files(
		[]*ast.ModelDecl{model("User",
			field("id", "Int"),
			field("name", "String"),
			field("phone", "String"),
		)},
		[]*ast.ApiDecl{api("getUser"), api("listUsers")},
	))
	lf2.Save(path)

	// Verify
	lf3, _ := Load(path)
	ml := lf3.Models["User"]
	if ml.Fields["id"] != 1 || ml.Fields["name"] != 2 {
		t.Error("Original IDs should be preserved")
	}
	if ml.Fields["phone"] != 4 {
		t.Errorf("phone = %d, want 4 (after avatar's 3)", ml.Fields["phone"])
	}
	if len(ml.Reserved) != 1 || ml.Reserved[0] != 3 {
		t.Errorf("Reserved = %v, want [3]", ml.Reserved)
	}
	if lf3.APIs["getUser"] != 1 || lf3.APIs["listUsers"] != 2 {
		t.Error("API IDs should be preserved across rounds")
	}
}

func TestSaveError(t *testing.T) {
	lf := New()
	err := lf.Save("/nonexistent/dir/luxo.lock")
	if err == nil {
		t.Fatal("Expected error for invalid path")
	}
}

func TestEmptyReservedOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "luxo.lock")

	lf := New()
	lf.Models["User"] = &ModelLock{
		NextID: 2,
		Fields: map[string]int{"id": 1},
	}
	lf.Save(path)

	data, _ := os.ReadFile(path)
	// Empty reserved should be omitted (omitempty)
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)

	var models map[string]json.RawMessage
	json.Unmarshal(raw["models"], &models)

	var userRaw map[string]json.RawMessage
	json.Unmarshal(models["User"], &userRaw)

	if _, ok := userRaw["reserved"]; ok {
		t.Error("Empty reserved should be omitted from JSON")
	}
}
