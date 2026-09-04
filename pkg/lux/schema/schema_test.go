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

func TestRegisterModelMergesFederationStub(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{
		Name:   "User",
		Module: "user",
		Fields: []Field{
			{ID: 1, Name: "key", Type: FieldUUID, PrimaryKey: true},
			{ID: 2, Name: "name", Type: FieldString},
		},
	})
	s.RegisterModel(&Model{
		Name: "User",
		Fields: []Field{{
			ID: 10, Name: "posts", Type: FieldModel, TypeName: "Post",
			Relation: true, IsList: true, Module: "post", ForeignKey: "userKey",
		}},
	})

	model := s.Models["User"]
	if model.Module != "user" || len(model.Fields) != 3 {
		t.Fatalf("merged model = %#v", model)
	}
	if model.PrimaryKeyField() == nil || model.PrimaryKeyField().Name != "key" {
		t.Fatalf("merged primary key = %#v", model.PrimaryKeyField())
	}
	if model.FieldByName("posts") == nil || model.FieldByName("name") == nil {
		t.Fatalf("merged fields = %#v", model.Fields)
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
		{FieldUUID, `"UUID"`},
		{FieldDecimal, `"Decimal"`},
		{FieldJSON, `"JSON"`},
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

func TestFieldTypeUnmarshalJSON(t *testing.T) {
	for _, want := range []FieldType{
		FieldInt, FieldFloat, FieldString, FieldBool, FieldDateTime, FieldDuration,
		FieldBytes, FieldEnum, FieldModel, FieldUUID, FieldDecimal, FieldJSON,
	} {
		var got FieldType
		data, _ := want.MarshalJSON()
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if got != want {
			t.Fatalf("Unmarshal(%s) = %v, want %v", data, got, want)
		}
	}
	var unknown FieldType
	if err := json.Unmarshal([]byte(`"Bogus"`), &unknown); err == nil {
		t.Fatal("unknown field type should fail")
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

func TestInferTypeUsageFollowsInputAndOutputGraphs(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{
		Name:   "User",
		Fields: []Field{{ID: 1, Name: "profile", Type: FieldModel, TypeName: "Profile"}},
	})
	s.RegisterType(&TypeDecl{
		Name:   "CreateInput",
		Fields: []Field{{ID: 1, Name: "profile", Type: FieldModel, TypeName: "Profile"}},
	})
	s.RegisterType(&TypeDecl{Name: "Profile", Fields: []Field{{ID: 1, Name: "name", Type: FieldString}}})
	s.RegisterType(&TypeDecl{Name: "Unused"})
	s.RegisterAPI(&API{
		ID: 1, Name: "createUser", ReturnType: "User",
		Params: []Param{{ID: 1, Name: "input", Type: FieldJSON, TypeName: "CreateInput"}},
	})
	s.RegisterAPI(&API{
		ID: 2, Name: "updateProfile", ReturnType: "Profile",
		Params: []Param{{ID: 1, Name: "profile", Type: FieldJSON, TypeName: "Profile"}},
	})

	s.InferTypeUsage()

	if got := s.Models["User"].Usage; got != TypeUsageOutput {
		t.Fatalf("User usage = %q, want output", got)
	}
	if got := s.Types["CreateInput"].Usage; got != TypeUsageInput {
		t.Fatalf("CreateInput usage = %q, want input", got)
	}
	if got := s.Types["Profile"].Usage; got != TypeUsageInputOutput {
		t.Fatalf("Profile usage = %q, want inputOutput", got)
	}
	if got := s.Types["Unused"].Usage; got != TypeUsageUnused {
		t.Fatalf("Unused usage = %q, want unused", got)
	}
}

func TestMergeTypeUsage(t *testing.T) {
	tests := []struct {
		current TypeUsage
		next    TypeUsage
		want    TypeUsage
	}{
		{current: "", next: TypeUsageInput, want: TypeUsageInput},
		{current: TypeUsageUnused, next: TypeUsageOutput, want: TypeUsageOutput},
		{current: TypeUsageInput, next: TypeUsageInput, want: TypeUsageInput},
		{current: TypeUsageInputOutput, next: TypeUsageOutput, want: TypeUsageInputOutput},
		{current: TypeUsageInput, next: TypeUsageOutput, want: TypeUsageInputOutput},
	}
	for _, test := range tests {
		if got := mergeTypeUsage(test.current, test.next); got != test.want {
			t.Errorf("mergeTypeUsage(%q, %q) = %q, want %q", test.current, test.next, got, test.want)
		}
	}
}

func TestTypeUsageIsSerializedInIntrospection(t *testing.T) {
	s := New()
	s.RegisterModel(&Model{Name: "User"})
	s.RegisterType(&TypeDecl{Name: "Input"})
	s.RegisterAPI(&API{
		ID: 1, Name: "create", ReturnType: "User",
		Params: []Param{{ID: 1, Name: "input", Type: FieldJSON, TypeName: "Input"}},
	})
	s.InferTypeUsage()

	data, err := s.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Models map[string]Model    `json:"models"`
		Types  map[string]TypeDecl `json:"types"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Models["User"].Usage != TypeUsageOutput {
		t.Fatalf("model usage was not serialized: %s", data)
	}
	if decoded.Types["Input"].Usage != TypeUsageInput {
		t.Fatalf("type usage was not serialized: %s", data)
	}
}

func TestSelectToFieldMask_Empty(t *testing.T) {
	m := &Model{Name: "Test"}
	mask, err := SelectToFieldMask(nil, m, New())
	if err != nil {
		t.Fatal(err)
	}
	if mask != nil {
		t.Error("nil fields should return nil mask (select all)")
	}
	mask, err = SelectToFieldMask([]*selection.Field{}, m, New())
	if err != nil {
		t.Fatal(err)
	}
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
	mask, err := SelectToFieldMask(fields, m, s)
	if err != nil {
		t.Fatal(err)
	}
	mask = codec.SelectionMaskFields(mask)

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

func TestSelectToFieldMask_EncodesRelationsRecursively(t *testing.T) {
	s := New()
	posts := &Model{
		Name: "Post",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 2, Name: "title", Type: FieldString},
		},
	}
	m := &Model{
		Name: "User",
		Fields: []Field{
			{ID: 1, Name: "id", Type: FieldInt},
			{ID: 4, Name: "posts", Type: FieldModel, TypeName: "Post", IsList: true, Relation: true},
		},
	}
	s.RegisterModel(posts)
	s.RegisterModel(m)

	fields := []*selection.Field{
		{Name: "id"},
		{Name: "posts", Children: []*selection.Field{{Name: "title"}}}, // relation
	}
	mask, err := SelectToFieldMask(fields, m, s)
	if err != nil {
		t.Fatal(err)
	}
	root := codec.SelectionMaskFields(mask)
	if !codec.FieldMaskHas(root, 1) || !codec.FieldMaskHas(root, 4) {
		t.Fatalf("root mask should include id and posts: %v", root)
	}
	child, ok := codec.SelectionMaskNested(mask, 4)
	if !ok || !codec.FieldMaskHas(codec.SelectionMaskFields(child), 2) {
		t.Fatalf("posts mask should include title: %v", child)
	}
}

func TestSelectToFieldMask_RejectsInvalidSelections(t *testing.T) {
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
	if _, err := SelectToFieldMask(fields, m, s); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := SelectToFieldMask([]*selection.Field{{Name: "id", Children: []*selection.Field{{Name: "x"}}}}, m, s); err == nil {
		t.Fatal("nested selection on scalar field must be rejected")
	}
	if _, err := SelectToFieldMask([]*selection.Field{{Name: "id"}, {Name: "id"}}, m, s); err == nil {
		t.Fatal("duplicate fields must be rejected")
	}
	if _, err := SelectToFieldMask([]*selection.Field{{Name: "id"}}, nil, s); err == nil {
		t.Fatal("selection without a return model must be rejected")
	}
}

func TestSelectToFieldMaskSupportsTypeDeclarationsAndSelectAllRelations(t *testing.T) {
	s := New()
	s.RegisterType(&TypeDecl{Name: "Payload", Fields: []Field{{ID: 1, Name: "value", Type: FieldString}}})
	root := &Model{Name: "Root", Fields: []Field{{ID: 1, Name: "payload", Type: FieldModel, TypeName: "Payload", Relation: true}}}
	s.RegisterModel(root)
	mask, err := SelectToFieldMask([]*selection.Field{{Name: "payload", Children: []*selection.Field{}}}, root, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := codec.SelectionMaskNested(mask, 1); found {
		t.Fatal("empty child selection should use the omitted-node select-all representation")
	}
}

func TestSelectToFieldMaskRejectsMissingNestedMetadataAndExcessiveDepth(t *testing.T) {
	s := New()
	root := &Model{Name: "Node", Fields: []Field{{ID: 1, Name: "child", Type: FieldModel, TypeName: "Node", Relation: true}}}
	s.RegisterModel(root)
	selectionRoot := &selection.Field{Name: "child"}
	cursor := selectionRoot
	for range 33 {
		child := &selection.Field{Name: "child"}
		cursor.Children = []*selection.Field{child}
		cursor = child
	}
	if _, err := SelectToFieldMask([]*selection.Field{selectionRoot}, root, s); err == nil {
		t.Fatal("selection deeper than 32 must be rejected")
	}

	missing := &Model{Name: "MissingRoot", Fields: []Field{{ID: 1, Name: "child", Type: FieldModel, TypeName: "Missing", Relation: true}}}
	s.RegisterModel(missing)
	fields := []*selection.Field{{Name: "child", Children: []*selection.Field{{Name: "value"}}}}
	if _, err := SelectToFieldMask(fields, missing, s); err == nil {
		t.Fatal("missing nested type must be rejected")
	}
	if _, err := SelectToFieldMask(fields, missing, nil); err == nil {
		t.Fatal("nested selection without schema must be rejected")
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

func TestInvalidNegativeFieldTypeIsUnknown(t *testing.T) {
	if got := FieldType(-1).String(); got != "Unknown" {
		t.Fatalf("String() = %q", got)
	}
	data, err := FieldType(-1).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"Unknown"` {
		t.Fatalf("MarshalJSON() = %s", data)
	}
}
