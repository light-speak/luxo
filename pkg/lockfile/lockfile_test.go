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
	if lf.Version != 2 {
		t.Errorf("Version = %d, want 2", lf.Version)
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
	if lf.Version != 2 {
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
	lf.APIs["getUser"] = &APILock{ID: 1}

	if err := lf.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Version != 2 {
		t.Errorf("Version = %d, want 2", loaded.Version)
	}
	if loaded.Models["User"].NextID != 4 {
		t.Errorf("NextID = %d, want 4", loaded.Models["User"].NextID)
	}
	if loaded.Models["User"].Fields["name"] != 2 {
		t.Error("Field name should have ID 2")
	}
	if loaded.APIs["getUser"].ID != 1 {
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

func TestBreakingChangesProtectWireContracts(t *testing.T) {
	base := files(
		[]*ast.ModelDecl{model("User", field("id", "Int"), field("name", "String"))},
		[]*ast.ApiDecl{{
			Name:       "getProfile",
			Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
			ReturnType: &ast.TypeRef{Name: "User"},
		}},
	)
	lf := New()
	lf.Update(base)

	compatible := files(
		[]*ast.ModelDecl{model("User", field("id", "Int"), field("name", "String"), field("avatar", "String"))},
		[]*ast.ApiDecl{{
			Name: "getProfile",
			Params: []*ast.ParamDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "locale", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			},
			ReturnType: &ast.TypeRef{Name: "User"},
		}},
	)
	if changes := lf.BreakingChanges(compatible); len(changes) != 0 {
		t.Fatalf("additive fields and optional params must be compatible: %v", changes)
	}

	breaking := files(
		[]*ast.ModelDecl{model("User", field("id", "String"))},
		[]*ast.ApiDecl{{
			Name:       "getProfile",
			Params:     []*ast.ParamDecl{{Name: "tenant", Type: &ast.TypeRef{Name: "String"}}},
			ReturnType: &ast.TypeRef{Name: "String"},
		}},
	)
	changes := lf.BreakingChanges(breaking)
	for _, path := range []string{"model User.id", "model User.name", "api getProfile.id", "api getProfile.tenant", "api getProfile return"} {
		if !hasBreakingPath(changes, path) {
			t.Errorf("missing breaking change for %q: %v", path, changes)
		}
	}
}

func TestUpdateReservesRemovedAPIIDs(t *testing.T) {
	lf := New()
	lf.Update(files(nil, []*ast.ApiDecl{api("oldAPI")}))
	oldID := lf.APIID("oldAPI")

	lf.Update(files(nil, []*ast.ApiDecl{api("newAPI")}))
	if lf.APIID("oldAPI") != 0 {
		t.Fatal("removed API must not remain active")
	}
	if lf.APIID("newAPI") <= oldID {
		t.Fatalf("new API ID %d reused reserved ID %d", lf.APIID("newAPI"), oldID)
	}
	if changes := lf.BreakingChanges(files(nil, []*ast.ApiDecl{api("newAPI")})); len(changes) != 0 {
		t.Fatalf("accepted removal must not be reported forever: %v", changes)
	}
}

func TestAPIContractMetadataSurvivesJSONRoundTrip(t *testing.T) {
	lf := New()
	lf.Update(files(nil, []*ast.ApiDecl{{Name: "health", ReturnType: &ast.TypeRef{Name: "String"}}}))
	data, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	loaded := New()
	if err := json.Unmarshal(data, loaded); err != nil {
		t.Fatal(err)
	}
	changed := files(nil, []*ast.ApiDecl{{Name: "health", ReturnType: &ast.TypeRef{Name: "Int"}}})
	if changes := loaded.BreakingChanges(changed); !hasBreakingPath(changes, "api health return") {
		t.Fatalf("return contract was lost after JSON round trip: %v", changes)
	}
}

func TestBreakingChangeString(t *testing.T) {
	tests := []struct {
		name   string
		change BreakingChange
		want   string
	}{
		{name: "removed", change: BreakingChange{Path: "model User.name", Before: "String"}, want: "model User.name was removed (was String)"},
		{name: "required addition", change: BreakingChange{Path: "api load.id", After: "Int"}, want: "api load.id was added as required (Int)"},
		{name: "changed", change: BreakingChange{Path: "event Changed.id", Before: "Int", After: "String"}, want: "event Changed.id changed from Int to String"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.change.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBreakingChangesCoverAllWireEntities(t *testing.T) {
	service := &ast.Directive{Name: "service"}
	base := []*ast.File{{
		Name:   "contracts.luxo",
		Models: []*ast.ModelDecl{model("User", field("id", "Int"))},
		Extends: []*ast.ExtendDecl{{
			Name:   "User",
			Fields: []*ast.FieldDecl{field("nickname", "String")},
		}},
		Types: []*ast.TypeDecl{{
			Name:   "Payload",
			Fields: []*ast.FieldDecl{field("data", "JSON")},
		}},
		Events: []*ast.EventDecl{{
			Name:   "Published",
			Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
		}},
		Functions: []*ast.FnDecl{{
			Name:       "syncUser",
			Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
			ReturnType: &ast.TypeRef{Name: "String"},
			Directives: []*ast.Directive{service},
		}},
	}}
	lf := New()
	lf.Update(base)

	changed := []*ast.File{{
		Name:   "contracts.luxo",
		Models: []*ast.ModelDecl{model("User", field("id", "Int"))},
		Extends: []*ast.ExtendDecl{{
			Name:   "User",
			Fields: []*ast.FieldDecl{field("nickname", "Int")},
		}},
		Types: []*ast.TypeDecl{{
			Name:   "Payload",
			Fields: []*ast.FieldDecl{field("data", "String"), field("extra", "Boolean")},
		}},
		Events: []*ast.EventDecl{{
			Name: "Published",
			Params: []*ast.ParamDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "source", Type: &ast.TypeRef{Name: "String"}},
			},
		}},
		Functions: []*ast.FnDecl{{
			Name:       "syncUser",
			Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "String"}}},
			ReturnType: &ast.TypeRef{Name: "Int"},
			Directives: []*ast.Directive{service},
		}},
	}}
	changes := lf.BreakingChanges(changed)
	for _, path := range []string{
		"model User.nickname",
		"type Payload.data",
		"event Published.source",
		"api svc:syncUser.id",
		"api svc:syncUser return",
	} {
		if !hasBreakingPath(changes, path) {
			t.Errorf("missing breaking change for %q: %v", path, changes)
		}
	}
	if hasBreakingPath(changes, "type Payload.extra") {
		t.Error("additive response type fields must remain compatible")
	}

	removed := lf.BreakingChanges(nil)
	if !hasBreakingPath(removed, "api svc:syncUser") {
		t.Errorf("removed service API must be reported: %v", removed)
	}

	legacy := New()
	legacy.Models["Old"] = &ModelLock{NextID: 8, Fields: map[string]int{"id": 7}}
	legacyChanges := legacy.BreakingChanges(nil)
	if len(legacyChanges) != 1 || legacyChanges[0].String() != "model Old.id was removed (was field ID 7)" {
		t.Fatalf("legacy field contract should fall back to its stable ID: %v", legacyChanges)
	}
}

func TestFormatTypeRef(t *testing.T) {
	tests := []struct {
		name string
		ref  *ast.TypeRef
		want string
	}{
		{name: "void", want: "Void"},
		{name: "scalar", ref: &ast.TypeRef{Name: "String"}, want: "String"},
		{name: "nullable list", ref: &ast.TypeRef{Name: "User", IsList: true, Nullable: true}, want: "[User]?"},
		{name: "generic", ref: &ast.TypeRef{Name: "Page", TypeArgs: []*ast.TypeRef{{Name: "User"}}}, want: "Page<User>"},
		{name: "tuple", ref: &ast.TypeRef{Tuple: []*ast.TypeRef{{Name: "Post"}, {Name: "Video"}}}, want: "(Post,Video)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTypeRef(tt.ref); got != tt.want {
				t.Fatalf("formatTypeRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadVersionCompatibility(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.lock")
	legacy := `{"version":1,"models":{},"apis":{"health":{"id":3}}}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != currentVersion || loaded.APIID("health") != 3 {
		t.Fatalf("legacy lock was not upgraded safely: %+v", loaded)
	}
	if changes := loaded.BreakingChanges(files(nil, []*ast.ApiDecl{api("health")})); len(changes) != 0 {
		t.Fatalf("legacy lock without contract metadata must not report false positives: %v", changes)
	}

	futurePath := filepath.Join(dir, "future.lock")
	future := `{"version":3,"models":{},"apis":{}}`
	if err := os.WriteFile(futurePath, []byte(future), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(futurePath); err == nil {
		t.Fatal("newer lock version must be rejected")
	}
}

func hasBreakingPath(changes []BreakingChange, path string) bool {
	for _, change := range changes {
		if change.Path == path {
			return true
		}
	}
	return false
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

func TestUpdateComputedFieldsReceiveStableIDs(t *testing.T) {
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
	if ml.Fields["totalLikes"] != 2 {
		t.Errorf("computed field ID = %d, want 2", ml.Fields["totalLikes"])
	}
	if ml.NextID != 3 {
		t.Errorf("NextID = %d, want 3", ml.NextID)
	}
}

func TestUpdateAPIs(t *testing.T) {
	lf := New()
	f := files(nil, []*ast.ApiDecl{
		api("getUser"),
		api("listUsers"),
	})

	lf.Update(f)

	if lf.APIs["getUser"].ID != 1 {
		t.Errorf("getUser = %d, want 1", lf.APIs["getUser"].ID)
	}
	if lf.APIs["listUsers"].ID != 2 {
		t.Errorf("listUsers = %d, want 2", lf.APIs["listUsers"].ID)
	}
}

func TestUpdateAPIsPreservesExisting(t *testing.T) {
	lf := New()
	lf.APIs["getUser"] = &APILock{ID: 5}
	lf.nextAPI = 5

	f := files(nil, []*ast.ApiDecl{
		api("getUser"),
		api("createUser"),
	})

	lf.Update(f)

	if lf.APIs["getUser"].ID != 5 {
		t.Errorf("getUser = %d, want 5 (preserved)", lf.APIs["getUser"].ID)
	}
	if lf.APIs["createUser"].ID != 6 {
		t.Errorf("createUser = %d, want 6", lf.APIs["createUser"].ID)
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
	lf.APIs["getUser"] = &APILock{ID: 3}

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
	if lf.APIs["getUser"].ID != 1 {
		t.Error("getUser should be 1")
	}
	if lf.APIs["getPost"].ID != 2 {
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
	if lf3.APIs["getUser"].ID != 1 || lf3.APIs["listUsers"].ID != 2 {
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

// --- APIParamID ---

func TestAPIParamID(t *testing.T) {
	lf := New()
	lf.APIs["getUser"] = &APILock{
		ID:     1,
		Params: map[string]int{"id": 1, "name": 2},
	}

	if id := lf.APIParamID("getUser", "id"); id != 1 {
		t.Errorf("APIParamID = %d, want 1", id)
	}
	if id := lf.APIParamID("getUser", "name"); id != 2 {
		t.Errorf("APIParamID = %d, want 2", id)
	}
	if id := lf.APIParamID("getUser", "missing"); id != 0 {
		t.Errorf("APIParamID = %d, want 0 for missing param", id)
	}
	if id := lf.APIParamID("noAPI", "x"); id != 0 {
		t.Errorf("APIParamID = %d, want 0 for missing API", id)
	}
	// API with nil Params
	lf.APIs["noParams"] = &APILock{ID: 2}
	if id := lf.APIParamID("noParams", "x"); id != 0 {
		t.Errorf("APIParamID = %d, want 0 for nil params", id)
	}
}

// --- EventFieldID ---

func TestEventFieldID(t *testing.T) {
	lf := New()
	lf.Events["UserCreated"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"userId": 1, "email": 2},
	}

	if id := lf.EventFieldID("UserCreated", "userId"); id != 1 {
		t.Errorf("EventFieldID = %d, want 1", id)
	}
	if id := lf.EventFieldID("UserCreated", "missing"); id != 0 {
		t.Errorf("EventFieldID = %d, want 0", id)
	}
	if id := lf.EventFieldID("NoEvent", "x"); id != 0 {
		t.Errorf("EventFieldID = %d, want 0 for missing event", id)
	}

	// nil Events map
	lf2 := New()
	lf2.Events = nil
	if id := lf2.EventFieldID("X", "Y"); id != 0 {
		t.Errorf("EventFieldID = %d, want 0 for nil events", id)
	}
}

// --- updateEvents ---

func TestUpdateEventsNewEvent(t *testing.T) {
	lf := New()
	ff := []*ast.File{{
		Name: "test.luxo",
		Events: []*ast.EventDecl{{
			Pos:  pos(),
			Name: "OrderPlaced",
			Params: []*ast.ParamDecl{
				{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "total", Type: &ast.TypeRef{Name: "Float"}},
			},
		}},
	}}

	lf.Update(ff)

	el := lf.Events["OrderPlaced"]
	if el == nil {
		t.Fatal("event should exist")
	}
	if el.Fields["orderId"] != 1 || el.Fields["total"] != 2 {
		t.Errorf("Fields = %v", el.Fields)
	}
	if el.NextID != 3 {
		t.Errorf("NextID = %d, want 3", el.NextID)
	}
}

func TestUpdateEventsPreservesExisting(t *testing.T) {
	lf := New()
	lf.Events["OrderPlaced"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"orderId": 1, "total": 2},
	}

	ff := []*ast.File{{
		Name: "test.luxo",
		Events: []*ast.EventDecl{{
			Pos:  pos(),
			Name: "OrderPlaced",
			Params: []*ast.ParamDecl{
				{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
				{Name: "total", Type: &ast.TypeRef{Name: "Float"}},
				{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			},
		}},
	}}

	lf.Update(ff)

	el := lf.Events["OrderPlaced"]
	if el.Fields["orderId"] != 1 || el.Fields["total"] != 2 {
		t.Error("existing IDs should be preserved")
	}
	if el.Fields["userId"] != 3 {
		t.Errorf("userId = %d, want 3", el.Fields["userId"])
	}
}

func TestUpdateEventsRemovedParam(t *testing.T) {
	lf := New()
	lf.Events["OrderPlaced"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"orderId": 1, "total": 2},
	}

	// Remove "total"
	ff := []*ast.File{{
		Name: "test.luxo",
		Events: []*ast.EventDecl{{
			Pos:  pos(),
			Name: "OrderPlaced",
			Params: []*ast.ParamDecl{
				{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
			},
		}},
	}}

	lf.Update(ff)

	el := lf.Events["OrderPlaced"]
	if _, ok := el.Fields["total"]; ok {
		t.Error("total should be removed")
	}
	if len(el.Reserved) != 1 || el.Reserved[0] != 2 {
		t.Errorf("Reserved = %v, want [2]", el.Reserved)
	}
}

func TestUpdateEventsRemovedEntirely(t *testing.T) {
	lf := New()
	lf.Events["OldEvent"] = &ModelLock{
		NextID: 3,
		Fields: map[string]int{"a": 1, "b": 2},
	}

	ff := []*ast.File{{Name: "test.luxo"}}
	lf.Update(ff)

	el := lf.Events["OldEvent"]
	if len(el.Fields) != 0 {
		t.Errorf("Fields should be empty, got %v", el.Fields)
	}
	if len(el.Reserved) != 2 {
		t.Errorf("Reserved = %v, want 2 entries", el.Reserved)
	}
}

func TestUpdateEventsNilEventsMap(t *testing.T) {
	lf := New()
	lf.Events = nil

	ff := []*ast.File{{
		Name: "test.luxo",
		Events: []*ast.EventDecl{{
			Pos:  pos(),
			Name: "TestEvent",
			Params: []*ast.ParamDecl{
				{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			},
		}},
	}}

	lf.Update(ff)

	if lf.Events == nil {
		t.Fatal("Events should be initialized")
	}
	if lf.Events["TestEvent"].Fields["id"] != 1 {
		t.Error("field id should be 1")
	}
}

// --- updateAPIs with @crud ---

func TestUpdateAPIsWithCrud(t *testing.T) {
	lf := New()
	m := model("Post",
		field("title", "String"),
		field("body", "String"),
	)
	m.Directives = []*ast.Directive{{
		Pos:  pos(),
		Name: "crud",
	}}

	ff := []*ast.File{{
		Name:   "test.luxo",
		Models: []*ast.ModelDecl{m},
	}}

	lf.Update(ff)

	// CRUD generates: getPost, listPosts, createPost, updatePost, deletePost, deletePosts
	expectedAPIs := []string{"getPost", "listPosts", "createPost", "updatePost", "deletePost", "deletePosts"}
	for _, name := range expectedAPIs {
		if lf.APIs[name] == nil {
			t.Errorf("API %s should exist", name)
		}
		if lf.APIs[name].ID == 0 {
			t.Errorf("API %s should have an ID", name)
		}
	}

	// Check params: getPost should have "id"
	if lf.APIs["getPost"].Params["id"] != 1 {
		t.Errorf("getPost params = %v", lf.APIs["getPost"].Params)
	}
	// listPosts should have "page" and "pageSize"
	if lf.APIs["listPosts"].Params["page"] == 0 || lf.APIs["listPosts"].Params["pageSize"] == 0 {
		t.Errorf("listPosts params = %v", lf.APIs["listPosts"].Params)
	}
	// createPost should have field names
	if lf.APIs["createPost"].Params["title"] == 0 || lf.APIs["createPost"].Params["body"] == 0 {
		t.Errorf("createPost params = %v", lf.APIs["createPost"].Params)
	}
	// updatePost should have "id" + field names
	if lf.APIs["updatePost"].Params["id"] == 0 {
		t.Errorf("updatePost should have id param")
	}
}

func TestUpdateAPIsWithCrudUsesOnlyHandlerParams(t *testing.T) {
	lf := New()
	relation := field("author", "User")
	autoID := field("id", "UUID")
	autoID.Directives = []*ast.Directive{{Pos: pos(), Name: "auto"}}
	internal := field("secret", "String")
	internal.Directives = []*ast.Directive{{Pos: pos(), Name: "internal"}}
	immutable := field("slug", "String")
	immutable.Directives = []*ast.Directive{{Pos: pos(), Name: "immutable"}}
	m := model("Post", autoID, field("title", "String"), immutable, internal, relation)
	m.Directives = []*ast.Directive{{Pos: pos(), Name: "crud"}}

	lf.Update([]*ast.File{{
		Name:   "test.luxo",
		Models: []*ast.ModelDecl{m, model("User", field("id", "UUID"))},
	}})

	create := lf.APIs["createPost"].Params
	update := lf.APIs["updatePost"].Params
	if len(create) != 2 || create["title"] == 0 || create["slug"] == 0 {
		t.Fatalf("create params = %v", create)
	}
	if len(update) != 2 || update["id"] == 0 || update["title"] == 0 {
		t.Fatalf("update params = %v", update)
	}
}

func TestUpdateAPIsWithParams(t *testing.T) {
	lf := New()
	ff := []*ast.File{{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{{
			Pos:  pos(),
			Name: "search",
			Params: []*ast.ParamDecl{
				{Name: "query", Type: &ast.TypeRef{Name: "String"}},
				{Name: "limit", Type: &ast.TypeRef{Name: "Int"}},
			},
		}},
	}}

	lf.Update(ff)

	al := lf.APIs["search"]
	if al == nil {
		t.Fatal("search API should exist")
	}
	if al.Params["query"] != 1 || al.Params["limit"] != 2 {
		t.Errorf("params = %v", al.Params)
	}
}

func TestEnsureAPIIDExistingWithNewParam(t *testing.T) {
	lf := New()
	lf.APIs["getUser"] = &APILock{
		ID:     5,
		Params: map[string]int{"id": 1},
	}
	lf.nextAPI = 5

	// Add a new param to existing API
	lf.ensureAPIID("getUser", []string{"id", "includeProfile"})

	al := lf.APIs["getUser"]
	if al.ID != 5 {
		t.Error("ID should be preserved")
	}
	if al.Params["id"] != 1 {
		t.Error("existing param should keep its ID")
	}
	if al.Params["includeProfile"] != 2 {
		t.Errorf("new param = %d, want 2", al.Params["includeProfile"])
	}
}

func TestHasCrudDirective(t *testing.T) {
	// with @crud
	m := model("User")
	m.Directives = []*ast.Directive{{Pos: pos(), Name: "crud"}}
	if !hasCrudDirective(m) {
		t.Error("should detect @crud")
	}

	// without @crud
	m2 := model("Post")
	if hasCrudDirective(m2) {
		t.Error("should not detect @crud on empty directives")
	}

	// with other directive
	m3 := model("Tag")
	m3.Directives = []*ast.Directive{{Pos: pos(), Name: "withAuth"}}
	if hasCrudDirective(m3) {
		t.Error("should not detect @crud for @withAuth")
	}
}

func TestCollectFieldNames(t *testing.T) {
	m := model("User",
		field("id", "Int"),
		field("name", "String"),
		computed("fullName"),
	)

	names := collectFieldNames(m)
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d: %v", len(names), names)
	}
	if names[0] != "id" || names[1] != "name" || names[2] != "fullName" {
		t.Errorf("names = %v", names)
	}
}

// --- updateExtends ---

func TestUpdateExtendsAssignsFieldIDs(t *testing.T) {
	lf := New()
	ff := []*ast.File{
		{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{model("User",
				field("id", "Int"),
				field("name", "String"),
			)},
		},
		{
			Name: "origin/post.luxo",
			Models: []*ast.ModelDecl{model("Post",
				field("id", "Int"),
				field("title", "String"),
				field("userId", "Int"),
			)},
			Extends: []*ast.ExtendDecl{
				{Pos: pos(), Name: "User", Fields: []*ast.FieldDecl{
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
				}},
			},
		},
	}

	lf.Update(ff)

	ml := lf.Models["User"]
	if ml == nil {
		t.Fatal("expected User ModelLock")
	}
	if ml.Fields["id"] == 0 || ml.Fields["name"] == 0 {
		t.Error("model fields should have IDs")
	}
	if ml.Fields["posts"] == 0 {
		t.Error("extend field 'posts' should have an ID")
	}
	// All IDs distinct
	ids := make(map[int]string)
	for name, id := range ml.Fields {
		if prev, exists := ids[id]; exists {
			t.Errorf("duplicate ID %d for %q and %q", id, prev, name)
		}
		ids[id] = name
	}
}

func TestUpdateExtendsMultipleModules(t *testing.T) {
	lf := New()
	ff := []*ast.File{
		{
			Name:   "origin/user.luxo",
			Models: []*ast.ModelDecl{model("User", field("id", "Int"))},
		},
		{
			Name: "origin/post.luxo",
			Extends: []*ast.ExtendDecl{
				{Name: "User", Fields: []*ast.FieldDecl{
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
				}},
			},
		},
		{
			Name: "origin/order.luxo",
			Extends: []*ast.ExtendDecl{
				{Name: "User", Fields: []*ast.FieldDecl{
					{Name: "orders", Type: &ast.TypeRef{Name: "Order", IsList: true}},
				}},
			},
		},
	}

	lf.Update(ff)

	ml := lf.Models["User"]
	if len(ml.Fields) != 3 { // id + posts + orders
		t.Fatalf("expected 3 fields, got %d: %v", len(ml.Fields), ml.Fields)
	}
	if ml.Fields["posts"] == 0 || ml.Fields["orders"] == 0 {
		t.Errorf("extend fields missing: %v", ml.Fields)
	}
}

func TestUpdateExtendsIdempotent(t *testing.T) {
	lf := New()
	ff := []*ast.File{
		{
			Name:   "origin/user.luxo",
			Models: []*ast.ModelDecl{model("User", field("id", "Int"))},
		},
		{
			Name: "origin/post.luxo",
			Extends: []*ast.ExtendDecl{
				{Name: "User", Fields: []*ast.FieldDecl{
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
				}},
			},
		},
	}

	lf.Update(ff)
	postsID := lf.Models["User"].Fields["posts"]
	nextID := lf.Models["User"].NextID

	lf.Update(ff)
	if lf.Models["User"].Fields["posts"] != postsID {
		t.Error("posts ID changed after second update")
	}
	if lf.Models["User"].NextID != nextID {
		t.Error("NextID changed after second update")
	}
}

func TestUpdateExtendsComputedReceivesStableID(t *testing.T) {
	lf := New()
	ff := []*ast.File{
		{
			Name:   "origin/user.luxo",
			Models: []*ast.ModelDecl{model("User", field("id", "Int"))},
		},
		{
			Name: "origin/post.luxo",
			Extends: []*ast.ExtendDecl{
				{Name: "User", Fields: []*ast.FieldDecl{
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					{Name: "postCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
				}},
			},
		},
	}

	lf.Update(ff)
	ml := lf.Models["User"]
	if ml.Fields["postCount"] == 0 {
		t.Error("computed extend field should get ID")
	}
	if ml.Fields["posts"] == 0 {
		t.Error("non-computed extend field should get ID")
	}
}

func TestUpdateExtendsParentNotDeclared(t *testing.T) {
	// Extend targets a model not defined in current files
	lf := New()
	ff := []*ast.File{
		{
			Name: "origin/post.luxo",
			Extends: []*ast.ExtendDecl{
				{Name: "User", Fields: []*ast.FieldDecl{
					{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
				}},
			},
		},
	}

	lf.Update(ff)

	ml := lf.Models["User"]
	if ml == nil {
		t.Fatal("User ModelLock should be created for extend even without model decl")
	}
	if ml.Fields["posts"] == 0 {
		t.Error("posts should have ID")
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

func TestUpdateTypesNewType(t *testing.T) {
	lf := New()
	files := []*ast.File{{
		Name: "origin/auth.luxo",
		Types: []*ast.TypeDecl{{
			Name: "AuthPayload",
			Fields: []*ast.FieldDecl{
				field("token", "String"),
				field("expiresAt", "DateTime"),
			},
		}},
	}}

	lf.updateTypes(files)

	ml, ok := lf.Types["AuthPayload"]
	if !ok {
		t.Fatal("Types should contain AuthPayload")
	}
	if len(ml.Fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(ml.Fields))
	}
	if ml.Fields["token"] != 1 {
		t.Errorf("token ID = %d, want 1", ml.Fields["token"])
	}
	if ml.Fields["expiresAt"] != 2 {
		t.Errorf("expiresAt ID = %d, want 2", ml.Fields["expiresAt"])
	}
	if ml.NextID != 3 {
		t.Errorf("NextID = %d, want 3", ml.NextID)
	}
}

func TestUpdateTypesPreservesExistingIDs(t *testing.T) {
	lf := New()
	lf.Types = map[string]*ModelLock{
		"AuthPayload": {
			NextID: 3,
			Fields: map[string]int{"token": 1, "expiresAt": 2},
		},
	}

	// Same fields + a new one
	files := []*ast.File{{
		Name: "origin/auth.luxo",
		Types: []*ast.TypeDecl{{
			Name: "AuthPayload",
			Fields: []*ast.FieldDecl{
				field("token", "String"),
				field("expiresAt", "DateTime"),
				field("refreshToken", "String"),
			},
		}},
	}}

	lf.updateTypes(files)

	ml := lf.Types["AuthPayload"]
	if ml.Fields["token"] != 1 {
		t.Errorf("token ID should stay 1, got %d", ml.Fields["token"])
	}
	if ml.Fields["expiresAt"] != 2 {
		t.Errorf("expiresAt ID should stay 2, got %d", ml.Fields["expiresAt"])
	}
	if ml.Fields["refreshToken"] != 3 {
		t.Errorf("refreshToken ID = %d, want 3", ml.Fields["refreshToken"])
	}
	if ml.NextID != 4 {
		t.Errorf("NextID = %d, want 4", ml.NextID)
	}
}

func TestUpdateTypesMultipleTypes(t *testing.T) {
	lf := New()
	files := []*ast.File{{
		Name: "origin/auth.luxo",
		Types: []*ast.TypeDecl{
			{
				Name: "AuthPayload",
				Fields: []*ast.FieldDecl{
					field("token", "String"),
				},
			},
			{
				Name: "PaginationInfo",
				Fields: []*ast.FieldDecl{
					field("total", "Int"),
					field("page", "Int"),
				},
			},
		},
	}}

	lf.updateTypes(files)

	if _, ok := lf.Types["AuthPayload"]; !ok {
		t.Error("missing AuthPayload")
	}
	if _, ok := lf.Types["PaginationInfo"]; !ok {
		t.Error("missing PaginationInfo")
	}
	pi := lf.Types["PaginationInfo"]
	if pi.Fields["total"] != 1 || pi.Fields["page"] != 2 {
		t.Errorf("PaginationInfo fields wrong: %+v", pi.Fields)
	}
}

func TestUpdateTypesNilTypesMap(t *testing.T) {
	lf := New()
	lf.Types = nil // explicitly nil

	files := []*ast.File{{
		Name: "origin/auth.luxo",
		Types: []*ast.TypeDecl{{
			Name: "Token",
			Fields: []*ast.FieldDecl{
				field("value", "String"),
			},
		}},
	}}

	lf.updateTypes(files)

	if lf.Types == nil {
		t.Fatal("Types should be initialized")
	}
	if _, ok := lf.Types["Token"]; !ok {
		t.Fatal("missing Token type")
	}
}

func TestUpdateAPIsIncludesInternalClusterEndpoints(t *testing.T) {
	lf := New()
	user := model("User", field("id", "Int"))
	user.Directives = []*ast.Directive{{Name: "crud"}}
	post := model("Post", field("id", "Int"), field("userId", "Int"))
	post.Directives = []*ast.Directive{{Name: "crud"}}
	allFiles := []*ast.File{
		{Name: "origin/user.luxo", Models: []*ast.ModelDecl{user}},
		{
			Name:   "origin/post.luxo",
			Models: []*ast.ModelDecl{post},
			Extends: []*ast.ExtendDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{{
					Name: "posts",
					Type: &ast.TypeRef{Name: "Post", IsList: true},
				}},
			}},
		},
	}

	lf.updateAPIs(allFiles)
	for _, name := range []string{"svc:batchLoad:User", "svc:batchLoad:Post", "svc:resolve:Post:userId"} {
		entry := lf.APIs[name]
		if entry == nil {
			t.Fatalf("missing internal API %s", name)
		}
		if entry.Params["keys"] != 1 {
			t.Errorf("%s keys field ID = %d, want 1", name, entry.Params["keys"])
		}
	}
}

func TestUpdateAPIsIncludesNamedLoadEndpoint(t *testing.T) {
	lf := New()
	files := []*ast.File{{
		Name: "origin/post.luxo",
		APIs: []*ast.ApiDecl{{
			Name: "findAuthors",
			Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{Value: &ast.CallExpr{
				Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
				Args: []*ast.NamedArg{
					{Name: "tenantId", Value: &ast.Ident{Name: "tenantId"}},
					{Name: "email", Value: &ast.Ident{Name: "email"}},
				},
			}}}},
		}},
	}}

	lf.updateAPIs(files)
	entry := lf.APIs["svc:load:User:tenantId:email"]
	if entry == nil {
		t.Fatal("missing named load endpoint")
	}
	if entry.Params["tenantId"] != 1 || entry.Params["email"] != 2 {
		t.Fatalf("named load param IDs = %v", entry.Params)
	}
}

func TestFederationForeignKey(t *testing.T) {
	listField := &ast.FieldDecl{Type: &ast.TypeRef{Name: "Post", IsList: true}}
	if got := federationForeignKey("User", "id", listField); got != "userId" {
		t.Fatalf("list foreign key = %q", got)
	}
	if got := federationForeignKey("User", "id", &ast.FieldDecl{Type: &ast.TypeRef{Name: "Profile"}}); got != "id" {
		t.Fatalf("single foreign key = %q", got)
	}
	explicit := &ast.FieldDecl{
		Type: &ast.TypeRef{Name: "Post", IsList: true},
		Directives: []*ast.Directive{{
			Name: "by",
			Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "authorId"}}},
		}},
	}
	if got := federationForeignKey("User", "id", explicit); got != "authorId" {
		t.Fatalf("explicit foreign key = %q", got)
	}
	invalidExplicit := &ast.FieldDecl{
		Type:       &ast.TypeRef{Name: "Post", IsList: true},
		Directives: []*ast.Directive{{Name: "by", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "authorId"}}}}},
	}
	if got := federationForeignKey("User", "id", invalidExplicit); got != "userId" {
		t.Fatalf("invalid explicit foreign key fallback = %q", got)
	}
	if got := lowerFirst(""); got != "" {
		t.Fatalf("lowerFirst(empty) = %q", got)
	}
}

func TestInternalAPIHelperBoundaries(t *testing.T) {
	service := &ast.FnDecl{
		Name:       "syncUser",
		Directives: []*ast.Directive{{Name: "deprecated"}, {Name: "service"}},
		Params:     []*ast.ParamDecl{{Name: "id"}},
	}
	regular := &ast.FnDecl{Name: "localOnly"}
	lf := New()
	lf.updateAPIs([]*ast.File{{Functions: []*ast.FnDecl{service, regular}}})
	if entry := lf.APIs["svc:syncUser"]; entry == nil || entry.Params["id"] != 1 {
		t.Fatalf("service API = %#v", entry)
	}
	if lf.APIs["svc:localOnly"] != nil || !hasServiceDirective(service) || hasServiceDirective(regular) {
		t.Fatal("service directive detection failed")
	}

	if namedLoadArgNames([]*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}) != nil {
		t.Fatal("positional load arguments were accepted")
	}
	if got := modelPrimaryKeyName(&ast.ModelDecl{Fields: []*ast.FieldDecl{{Name: "key", Directives: []*ast.Directive{{Name: "id"}}}}}); got != "key" {
		t.Fatalf("directed primary key = %q", got)
	}
	if got := modelPrimaryKeyName(&ast.ModelDecl{Fields: []*ast.FieldDecl{{Name: "id"}}}); got != "id" {
		t.Fatalf("conventional primary key = %q", got)
	}
	if got := modelPrimaryKeyName(&ast.ModelDecl{}); got != "id" {
		t.Fatalf("fallback primary key = %q", got)
	}
	listField := &ast.FieldDecl{Type: &ast.TypeRef{Name: "Post", IsList: true}}
	if got := federationForeignKey("User", "", listField); got != "userId" {
		t.Fatalf("empty primary-key fallback = %q", got)
	}
	if upperFirst("") != "" || upperFirst("über") != "Über" || lowerFirst("Üser") != "üser" {
		t.Fatal("identifier case conversion failed")
	}
}

func TestNamedLoadDiscoveryRejectsInvalidCallsAndDeduplicates(t *testing.T) {
	validCall := func() ast.Expr {
		return &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"},
			Args: []*ast.NamedArg{{Name: "email", Value: &ast.Ident{Name: "email"}}},
		}
	}
	invalid := []ast.Expr{
		&ast.Literal{},
		&ast.CallExpr{Func: &ast.Ident{Name: "load"}},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "find"}},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Literal{}, Field: "load"}},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{}, Field: "load"}},
		&ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "User"}, Field: "load"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}},
	}
	statements := make([]ast.Stmt, 0, len(invalid)+2)
	for _, expr := range invalid {
		statements = append(statements, &ast.ExprStmt{Expr: expr})
	}
	statements = append(statements, &ast.ExprStmt{Expr: validCall()}, &ast.ExprStmt{Expr: validCall()})
	lf := New()
	lf.updateNamedLoadAPIs([]*ast.File{
		{APIs: []*ast.ApiDecl{{Body: &ast.Block{Stmts: statements}}}},
		{Functions: []*ast.FnDecl{{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: validCall()}}}}}},
	})
	if len(lf.APIs) != 1 || lf.APIs["svc:load:User:email"] == nil {
		t.Fatalf("named load APIs = %#v", lf.APIs)
	}
}
