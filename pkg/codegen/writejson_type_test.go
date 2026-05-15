package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

// ─── generateTypeWriteLuxo — all scalar types ──────────────────────────────────

func TestGenerateTypeWriteLuxo_AllScalarTypes(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Payload": {
			"intField":      1,
			"strField":      2,
			"floatField":    3,
			"boolField":     4,
			"dtField":       5,
			"durField":      6,
			"intNullable":   7,
			"strNullable":   8,
			"floatNullable": 9,
			"boolNullable":  10,
			"dtNullable":    11,
			"durNullable":   12,
		},
	})

	pseudo := &ast.ModelDecl{
		Name: "Payload",
		Fields: []*ast.FieldDecl{
			{Name: "intField", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "strField", Type: &ast.TypeRef{Name: "String"}},
			{Name: "floatField", Type: &ast.TypeRef{Name: "Float"}},
			{Name: "boolField", Type: &ast.TypeRef{Name: "Boolean"}},
			{Name: "dtField", Type: &ast.TypeRef{Name: "DateTime"}},
			{Name: "durField", Type: &ast.TypeRef{Name: "Duration"}},
			{Name: "intNullable", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
			{Name: "strNullable", Type: &ast.TypeRef{Name: "String", Nullable: true}},
			{Name: "floatNullable", Type: &ast.TypeRef{Name: "Float", Nullable: true}},
			{Name: "boolNullable", Type: &ast.TypeRef{Name: "Boolean", Nullable: true}},
			{Name: "dtNullable", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}},
			{Name: "durNullable", Type: &ast.TypeRef{Name: "Duration", Nullable: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	// Value receiver
	if !strings.Contains(code, "func (p Payload) WriteLuxo") {
		t.Errorf("expected value receiver:\n%s", code)
	}

	// Non-nullable Int
	if !strings.Contains(code, "AppendSvarint(buf.B, p.IntField)") {
		t.Errorf("missing non-nullable Int write:\n%s", code)
	}
	// Non-nullable String
	if !strings.Contains(code, "AppendString(buf.B, p.StrField)") {
		t.Errorf("missing non-nullable String write:\n%s", code)
	}
	// Non-nullable Float
	if !strings.Contains(code, "AppendFixed64(buf.B, p.FloatField)") {
		t.Errorf("missing non-nullable Float write:\n%s", code)
	}
	// Non-nullable Boolean
	if !strings.Contains(code, "AppendBool(buf.B, p.BoolField)") {
		t.Errorf("missing non-nullable Boolean write:\n%s", code)
	}
	// Non-nullable DateTime (.Unix())
	if !strings.Contains(code, "p.DtField.Unix()") {
		t.Errorf("missing non-nullable DateTime .Unix():\n%s", code)
	}
	// Non-nullable Duration (int64 cast)
	if !strings.Contains(code, "int64(p.DurField)") {
		t.Errorf("missing non-nullable Duration int64 cast:\n%s", code)
	}

	// Nullable fields should have AppendPresent/AppendNull patterns
	if !strings.Contains(code, "AppendPresent") {
		t.Errorf("missing AppendPresent for nullable:\n%s", code)
	}
	if !strings.Contains(code, "AppendNull") {
		t.Errorf("missing AppendNull for nullable:\n%s", code)
	}

	// Nullable Int: *p.IntNullable
	if !strings.Contains(code, "*p.IntNullable") {
		t.Errorf("missing nullable Int deref:\n%s", code)
	}
	// Nullable String: *p.StrNullable
	if !strings.Contains(code, "*p.StrNullable") {
		t.Errorf("missing nullable String deref:\n%s", code)
	}
	// Nullable Float: *p.FloatNullable
	if !strings.Contains(code, "*p.FloatNullable") {
		t.Errorf("missing nullable Float deref:\n%s", code)
	}
	// Nullable Boolean: *p.BoolNullable
	if !strings.Contains(code, "*p.BoolNullable") {
		t.Errorf("missing nullable Boolean deref:\n%s", code)
	}
	// Nullable DateTime: p.DtNullable.Unix()
	if !strings.Contains(code, "p.DtNullable.Unix()") {
		t.Errorf("missing nullable DateTime Unix:\n%s", code)
	}
	// Nullable Duration: int64(*p.DurNullable)
	if !strings.Contains(code, "int64(*p.DurNullable)") {
		t.Errorf("missing nullable Duration cast:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_NestedModel(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"AuthPayload": {"token": 1, "member": 2},
	})

	pseudo := &ast.ModelDecl{
		Name: "AuthPayload",
		Fields: []*ast.FieldDecl{
			{Name: "token", Type: &ast.TypeRef{Name: "String"}},
			{Name: "member", Type: &ast.TypeRef{Name: "User"}}, // relation/nested
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	// Nested model should use inline WriteLuxo
	if !strings.Contains(code, "a.Member.WriteLuxo(buf, nil)") {
		t.Errorf("nested model should call WriteLuxo inline:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_ListRelation(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Result": {"items": 1, "count": 2},
	})

	pseudo := &ast.ModelDecl{
		Name: "Result",
		Fields: []*ast.FieldDecl{
			{Name: "items", Type: &ast.TypeRef{Name: "User", IsList: true}}, // list relation
			{Name: "count", Type: &ast.TypeRef{Name: "Int"}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	// List relation: loop with WriteLuxo
	if !strings.Contains(code, "len(r.Items)") {
		t.Errorf("list relation should append count:\n%s", code)
	}
	if !strings.Contains(code, "r.Items[i].WriteLuxo(buf, nil)") {
		t.Errorf("list relation should call WriteLuxo per item:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_NullableRelation(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Wrapper": {"nested": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Wrapper",
		Fields: []*ast.FieldDecl{
			{Name: "nested", Type: &ast.TypeRef{Name: "User", Nullable: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	// Nullable relation: nil check + WriteLuxo or AppendNull
	if !strings.Contains(code, "w.Nested != nil") {
		t.Errorf("nullable relation should have nil check:\n%s", code)
	}
	if !strings.Contains(code, "w.Nested.WriteLuxo(buf, nil)") {
		t.Errorf("nullable relation should call WriteLuxo when present:\n%s", code)
	}
	if !strings.Contains(code, "AppendNull") {
		t.Errorf("nullable relation should AppendNull when nil:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_EnumField(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"WithEnum": {"role": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "WithEnum",
		Fields: []*ast.FieldDecl{
			{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
		},
	}

	enums := map[string]bool{"Role": true}
	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, enums)
	code := b.String()

	// Enum field: string(w.Role)
	if !strings.Contains(code, "string(w.Role)") {
		t.Errorf("enum field should convert to string:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_NilTypeFieldSkipped(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Partial": {"name": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Partial",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "noType", Type: nil}, // nil type should be skipped
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if strings.Contains(code, "NoType") {
		t.Errorf("nil-type field should be skipped:\n%s", code)
	}
}

func TestGenerateTypeWriteLuxo_NoFieldID(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	// No field IDs set for this type
	SetModelFieldIDs(map[string]map[string]int{})

	pseudo := &ast.ModelDecl{
		Name: "NoIDs",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	// Should still produce a valid function, just with no field writes
	if !strings.Contains(code, "func (n NoIDs) WriteLuxo") {
		t.Errorf("should still generate function:\n%s", code)
	}
	// Should have terminator
	if !strings.Contains(code, "0x00") {
		t.Errorf("should have terminator:\n%s", code)
	}
}

// ─── generateWriteJSONFile with Types ──────────────────────────────────────────

func TestGenerateWriteJSONFile_TypeDecls(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"User":        {"id": 1, "name": 2},
		"AuthPayload": {"token": 1, "userId": 2},
	})

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Models: []*ast.ModelDecl{{
				Name: "User",
				Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				},
			}},
			Types: []*ast.TypeDecl{{
				Name: "AuthPayload",
				Fields: []*ast.FieldDecl{
					{Name: "token", Type: &ast.TypeRef{Name: "String"}},
					{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
		}},
	}

	src := generateWriteJSONFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate")
	}
	code := string(src)

	// Model WriteLuxo (pointer receiver)
	if !strings.Contains(code, "func (u *User) WriteLuxo") {
		t.Errorf("missing User WriteLuxo:\n%s", code)
	}
	// Type WriteLuxo (value receiver)
	if !strings.Contains(code, "func (a AuthPayload) WriteLuxo") {
		t.Errorf("missing AuthPayload type WriteLuxo:\n%s", code)
	}
}

// ─── writeTypeListScalarField — [String], [Int], [Boolean], [Float] ─────────

func TestWriteTypeListScalarField_StringList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Tags": {"names": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Tags",
		Fields: []*ast.FieldDecl{
			{Name: "names", Type: &ast.TypeRef{Name: "String", IsList: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if !strings.Contains(code, "len(t.Names)") {
		t.Errorf("should append list length:\n%s", code)
	}
	if !strings.Contains(code, "AppendString(buf.B, v)") {
		t.Errorf("should AppendString for each element:\n%s", code)
	}
}

func TestWriteTypeListScalarField_IntList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Nums": {"values": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Nums",
		Fields: []*ast.FieldDecl{
			{Name: "values", Type: &ast.TypeRef{Name: "Int", IsList: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if !strings.Contains(code, "AppendSvarint(buf.B, v)") {
		t.Errorf("should AppendSvarint for each element:\n%s", code)
	}
}

func TestWriteTypeListScalarField_BooleanList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Flags": {"active": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Flags",
		Fields: []*ast.FieldDecl{
			{Name: "active", Type: &ast.TypeRef{Name: "Boolean", IsList: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if !strings.Contains(code, "AppendBool(buf.B, v)") {
		t.Errorf("should AppendBool for each element:\n%s", code)
	}
}

func TestWriteTypeListScalarField_FloatList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Scores": {"points": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Scores",
		Fields: []*ast.FieldDecl{
			{Name: "points", Type: &ast.TypeRef{Name: "Float", IsList: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if !strings.Contains(code, "AppendFixed64(buf.B, v)") {
		t.Errorf("should AppendFixed64 for each element:\n%s", code)
	}
}

func TestWriteTypeListScalarField_EnumList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Perms": {"roles": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Perms",
		Fields: []*ast.FieldDecl{
			{Name: "roles", Type: &ast.TypeRef{Name: "Role", IsList: true}},
		},
	}

	enums := map[string]bool{"Role": true}
	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, enums)
	code := b.String()

	if !strings.Contains(code, "string(v)") {
		t.Errorf("should convert enum to string:\n%s", code)
	}
}

func TestWriteTypeListScalarField_DurationList(t *testing.T) {
	old := modelFieldIDs
	defer func() { modelFieldIDs = old }()

	SetModelFieldIDs(map[string]map[string]int{
		"Timers": {"durations": 1},
	})

	pseudo := &ast.ModelDecl{
		Name: "Timers",
		Fields: []*ast.FieldDecl{
			{Name: "durations", Type: &ast.TypeRef{Name: "Duration", IsList: true}},
		},
	}

	var b strings.Builder
	generateTypeWriteLuxo(&b, pseudo, nil)
	code := b.String()

	if !strings.Contains(code, "int64(v)") {
		t.Errorf("should cast Duration to int64:\n%s", code)
	}
}
