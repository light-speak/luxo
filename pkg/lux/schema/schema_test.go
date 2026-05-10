package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func TestNew(t *testing.T) {
	s := New()
	if s.Models == nil {
		t.Fatal("Models map should be initialized")
	}
	if s.APIs == nil {
		t.Fatal("APIs map should be initialized")
	}
	if len(s.Models) != 0 {
		t.Error("Models should be empty")
	}
	if len(s.APIs) != 0 {
		t.Error("APIs should be empty")
	}
}

func TestRegisterModel(t *testing.T) {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
			{ID: 5, Name: "email", Type: FieldString},
		},
	}
	s.RegisterModel(m)

	// Model is registered
	got := s.Models["User"]
	if got == nil {
		t.Fatal("User model should be registered")
	}

	// byID index works
	f := got.FieldByID(1)
	if f == nil || f.Name != "id" {
		t.Errorf("FieldByID(1) = %v, want id", f)
	}
	f = got.FieldByID(5)
	if f == nil || f.Name != "email" {
		t.Errorf("FieldByID(5) = %v, want email", f)
	}

	// byName index works
	f = got.FieldByName("name")
	if f == nil || f.ID != 2 {
		t.Errorf("FieldByName(name) = %v, want ID 2", f)
	}

	// Non-existent returns nil
	if got.FieldByID(999) != nil {
		t.Error("FieldByID(999) should be nil")
	}
	if got.FieldByName("missing") != nil {
		t.Error("FieldByName(missing) should be nil")
	}

	// JSONPrefix pre-computed
	f = got.FieldByName("id")
	if string(f.JSONPrefix) != `"id":` {
		t.Errorf("JSONPrefix = %q, want %q", f.JSONPrefix, `"id":`)
	}
	f = got.FieldByName("email")
	if string(f.JSONPrefix) != `"email":` {
		t.Errorf("JSONPrefix = %q, want %q", f.JSONPrefix, `"email":`)
	}
}

func TestRegisterModelOverwrite(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "A", Fields: []Field{{ID: 1, Name: "x", Type: FieldInt}}})
	s.RegisterModel(&Model{Name: "A", Fields: []Field{{ID: 1, Name: "y", Type: FieldString}}})
	if s.Models["A"].FieldByName("y") == nil {
		t.Error("second registration should overwrite first")
	}
}

func TestRegisterAPI(t *testing.T) {
	s := New()
	a := &API{ID: 1, Name: "getUser", Module: "user", ReturnType: "User"}
	s.RegisterAPI(a)

	got := s.APIs["getUser"]
	if got == nil {
		t.Fatal("API should be registered")
	}
	if got.ID != 1 || got.Module != "user" || got.ReturnType != "User" {
		t.Errorf("API fields mismatch: %+v", got)
	}
}

func TestFieldTypeMarshalJSON(t *testing.T) {
	tests := []struct {
		ft   FieldType
		want string
	}{
		{FieldInt, `"Int"`},
		{FieldFloat, `"Float"`},
		{FieldString, `"String"`},
		{FieldBool, `"Boolean"`},
		{FieldDateTime, `"DateTime"`},
		{FieldDuration, `"Duration"`},
		{FieldBytes, `"Bytes"`},
		{FieldEnum, `"Enum"`},
		{FieldModel, `"Model"`},
	}
	for _, tt := range tests {
		b, err := tt.ft.MarshalJSON()
		if err != nil {
			t.Errorf("FieldType(%d).MarshalJSON() error: %v", tt.ft, err)
		}
		if string(b) != tt.want {
			t.Errorf("FieldType(%d).MarshalJSON() = %s, want %s", tt.ft, b, tt.want)
		}
	}
}

func TestFieldTypeMarshalJSON_Unknown(t *testing.T) {
	// Out-of-range FieldType should produce "Unknown"
	ft := FieldType(999)
	b, err := ft.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"Unknown"` {
		t.Errorf("got %s, want %q", b, "Unknown")
	}
}

func TestSchemaToJSON(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{
		Name: "Post",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "title", Type: FieldString},
		},
	})
	s.RegisterAPI(&API{ID: 10, Name: "listPosts", Module: "post", ReturnType: "Post", ReturnList: true, Paginated: true})

	data, err := s.ToJSON()
	if err != nil {
		t.Fatal(err)
	}

	// Parse back to verify structure
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Check models
	models := parsed["models"].(map[string]any)
	post := models["Post"].(map[string]any)
	if post["name"] != "Post" {
		t.Errorf("model name = %v", post["name"])
	}

	// Check APIs
	apis := parsed["apis"].(map[string]any)
	listPosts := apis["listPosts"].(map[string]any)
	if listPosts["paginated"] != true {
		t.Errorf("paginated = %v", listPosts["paginated"])
	}
	if listPosts["returnList"] != true {
		t.Errorf("returnList = %v", listPosts["returnList"])
	}
}

func TestSchemaToJSON_FieldTypesInJSON(t *testing.T) {
	// Verify FieldType serializes as string names in JSON output
	s := New()
	s.RegisterModel(&Model{
		Name: "M",
		Fields: []Field{
			{ID: 1, Name: "a", Type: FieldDateTime},
			{ID: 2, Name: "b", Type: FieldBool, Nullable: true},
		},
	})
	data, _ := s.ToJSON()

	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	models := parsed["models"].(map[string]any)
	fields := models["M"].(map[string]any)["fields"].([]any)

	f0 := fields[0].(map[string]any)
	if f0["type"] != "DateTime" {
		t.Errorf("field type = %v, want DateTime", f0["type"])
	}
	f1 := fields[1].(map[string]any)
	if f1["type"] != "Boolean" {
		t.Errorf("field type = %v, want Boolean", f1["type"])
	}
	if f1["nullable"] != true {
		t.Errorf("nullable = %v, want true", f1["nullable"])
	}
}

func TestSelectToFieldMask_Empty(t *testing.T) {
	m := &Model{Name: "Test"}
	mask := SelectToFieldMask(nil, m)
	if mask != nil {
		t.Error("nil fields should return nil mask (select all)")
	}
	mask = SelectToFieldMask([]*selection.Field{}, m)
	if mask != nil {
		t.Error("empty fields should return nil mask")
	}
}

func TestSelectToFieldMask_ScalarFields(t *testing.T) {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "name", Type: FieldString},
			{ID: 10, Name: "email", Type: FieldString},
		},
	}
	s.RegisterModel(m)

	fields := []*selection.Field{
		{Name: "id"},
		{Name: "email"},
	}
	mask := SelectToFieldMask(fields, m)

	// Should include field IDs 1 and 10
	if !codec.FieldMaskHas(mask, 1) {
		t.Error("mask should include field ID 1 (id)")
	}
	if codec.FieldMaskHas(mask, 2) {
		t.Error("mask should NOT include field ID 2 (name)")
	}
	if !codec.FieldMaskHas(mask, 10) {
		t.Error("mask should include field ID 10 (email)")
	}
}

func TestSelectToFieldMask_SkipsRelations(t *testing.T) {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
		},
	}
	s.RegisterModel(m)

	fields := []*selection.Field{
		{Name: "id"},
		{Name: "posts", Children: []*selection.Field{{Name: "title"}}}, // relation
	}
	mask := SelectToFieldMask(fields, m)

	// "posts" should be skipped (has children)
	if !codec.FieldMaskHas(mask, 1) {
		t.Error("should include id")
	}
}

func TestSelectToFieldMask_UnknownFieldIgnored(t *testing.T) {
	s := New()
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
		},
	}
	s.RegisterModel(m)

	fields := []*selection.Field{
		{Name: "id"},
		{Name: "nonexistent"},
	}
	mask := SelectToFieldMask(fields, m)
	if !codec.FieldMaskHas(mask, 1) {
		t.Error("should include id")
	}
}

func TestRegisterModelNoFields(t *testing.T) {
	s := New()
	m := &Model{Name: "Empty", Fields: []Field{}}
	s.RegisterModel(m)

	got := s.Models["Empty"]
	if got == nil {
		t.Fatal("empty model should be registered")
	}
	if got.FieldByID(1) != nil {
		t.Error("empty model should have no fields")
	}
}

func TestRegisterEnum(t *testing.T) {
	s := New()
	s.RegisterEnum(&Enum{Name: "Status", Values: []string{"ACTIVE", "INACTIVE"}})

	if s.Enums["Status"] == nil {
		t.Fatal("enum should be registered")
	}
	if len(s.Enums["Status"].Values) != 2 {
		t.Errorf("expected 2 values, got %d", len(s.Enums["Status"].Values))
	}
}

func TestRegisterType(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{
		Name: "AuthPayload",
		Fields: []Field{
			{ID: 1, Name: "token", Type: FieldString},
			{ID: 2, Name: "userId", Type: FieldInt},
		},
	})

	if s.Types["AuthPayload"] == nil {
		t.Fatal("type should be registered")
	}
	if len(s.Types["AuthPayload"].Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(s.Types["AuthPayload"].Fields))
	}
}

func TestSchemaToJSONWithEnumsAndTypes(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "User", Fields: []Field{{ID: 1, Name: "id", Type: FieldInt}}})
	s.RegisterEnum(&Enum{Name: "Role", Values: []string{"ADMIN", "USER"}})
	s.RegisterType(&TypeDecl{Name: "Token", Fields: []Field{{ID: 1, Name: "value", Type: FieldString}}})

	data, err := s.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	j := string(data)
	if !strings.Contains(j, `"Role"`) {
		t.Error("JSON should contain enum")
	}
	if !strings.Contains(j, `"ADMIN"`) {
		t.Error("JSON should contain enum value")
	}
	if !strings.Contains(j, `"Token"`) {
		t.Error("JSON should contain type")
	}
}

func TestFieldTypeString(t *testing.T) {
	if FieldInt.String() != "Int" {
		t.Errorf("FieldInt.String() = %q", FieldInt.String())
	}
	if FieldString.String() != "String" {
		t.Errorf("FieldString.String() = %q", FieldString.String())
	}
	if FieldEnum.String() != "Enum" {
		t.Errorf("FieldEnum.String() = %q", FieldEnum.String())
	}
}
