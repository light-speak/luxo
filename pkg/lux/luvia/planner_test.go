package luvia

import (
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func userModelWithExtend() *schema.Model {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 3, Name: "email", Type: schema.FieldString},
			// Extend field from post module
			{ID: 10, Name: "posts", Type: schema.FieldModel, TypeName: "Post",
				IsList: true, Relation: true, Module: "post", ForeignKey: "userId"},
			// Extend field from comment module (relation, resolves via Comment model)
			{ID: 11, Name: "comments", Type: schema.FieldModel, TypeName: "Comment",
				IsList: true, Relation: true, Module: "comment", ForeignKey: "userId"},
		},
	})
	return s.Models["User"]
}

func simpleUserModel() *schema.Model {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 3, Name: "email", Type: schema.FieldString},
		},
	})
	return s.Models["User"]
}

func TestPlan_NilModelReturnsNil(t *testing.T) {
	p := Plan(nil, nil, "user")
	if p != nil {
		t.Error("nil model should return nil plan")
	}
}

func TestPlan_NoExtendFieldsReturnsNil(t *testing.T) {
	m := simpleUserModel()
	p := Plan(m, nil, "user")
	if p != nil {
		t.Error("model without extend fields should return nil plan")
	}
}

func TestPlan_NoExtendFieldsInMaskReturnsNil(t *testing.T) {
	m := userModelWithExtend()
	// Only request name and email (no extend fields)
	mask := codec.FieldMaskSet(nil, 2) // name
	mask = codec.FieldMaskSet(mask, 3) // email
	p := Plan(m, mask, "user")
	if p != nil {
		t.Error("mask without extend fields should return nil plan")
	}
}

func TestPlan_WithExtendFields(t *testing.T) {
	m := userModelWithExtend()
	// Request name + posts (extend from post module)
	mask := codec.FieldMaskSet(nil, 2)  // name
	mask = codec.FieldMaskSet(mask, 10) // posts (extend)
	p := Plan(m, mask, "user")
	if p == nil {
		t.Fatal("expected non-nil plan")
	}

	// Primary should include name + id (auto-added)
	if p.Primary.Module != "user" {
		t.Errorf("primary module = %q, want %q", p.Primary.Module, "user")
	}
	if !codec.FieldMaskHas(p.Primary.Mask, 2) {
		t.Error("primary mask should include name (field 2)")
	}
	if !codec.FieldMaskHas(p.Primary.Mask, 1) {
		t.Error("primary mask should auto-include id (field 1)")
	}
	// posts should NOT be in primary mask
	if codec.FieldMaskHas(p.Primary.Mask, 10) {
		t.Error("primary mask should not include extend field posts (10)")
	}

	// Should have 1 extend step
	if len(p.Extends) != 1 {
		t.Fatalf("expected 1 extend step, got %d", len(p.Extends))
	}
	ext := p.Extends[0]
	if ext.Module != "post" {
		t.Errorf("extend module = %q, want %q", ext.Module, "post")
	}
	if ext.FieldID != 10 {
		t.Errorf("extend fieldID = %d, want 10", ext.FieldID)
	}
	if ext.ForeignKey != "userId" {
		t.Errorf("extend FK = %q, want %q", ext.ForeignKey, "userId")
	}
	if !ext.IsList {
		t.Error("posts should be a list")
	}

	if p.IDFieldID != 1 {
		t.Errorf("IDFieldID = %d, want 1", p.IDFieldID)
	}
}

func TestPlan_MultipleExtendModules(t *testing.T) {
	m := userModelWithExtend()
	// Request name + posts + comments (two different extend modules)
	mask := codec.FieldMaskSet(nil, 2)  // name
	mask = codec.FieldMaskSet(mask, 10) // posts (post module)
	mask = codec.FieldMaskSet(mask, 11) // comments (comment module)
	p := Plan(m, mask, "user")
	if p == nil {
		t.Fatal("expected non-nil plan")
	}

	if len(p.Extends) != 2 {
		t.Fatalf("expected 2 extend steps, got %d", len(p.Extends))
	}

	modules := map[string]bool{}
	for _, ext := range p.Extends {
		modules[ext.Module] = true
	}
	if !modules["post"] {
		t.Error("missing extend step for post module")
	}
	if !modules["comment"] {
		t.Error("missing extend step for comment module")
	}
}

func TestPlan_ExtendFieldNonRelation(t *testing.T) {
	// Test the case where a field has Module set but is NOT a relation
	// (e.g., a scalar extend field like postCount) — should be skipped
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
			// Scalar extend field (non-relation) — no FK, not resolvable
			{ID: 10, Name: "postCount", Type: schema.FieldInt, Module: "post"},
			// Relation extend field — resolvable
			{ID: 11, Name: "posts", Type: schema.FieldModel, TypeName: "Post",
				IsList: true, Relation: true, Module: "post", ForeignKey: "userId"},
		},
	})
	m := s.Models["User"]

	// Request all fields including the scalar extend
	p := Plan(m, nil, "user")
	if p == nil {
		t.Fatal("expected non-nil plan (has relation extend)")
	}
	// Only the relation extend should be in the plan
	if len(p.Extends) != 1 {
		t.Fatalf("expected 1 extend step, got %d", len(p.Extends))
	}
	if p.Extends[0].FieldName != "posts" {
		t.Errorf("extend field = %q, want posts", p.Extends[0].FieldName)
	}
}

func TestPlan_NoIDField(t *testing.T) {
	// Model without an "id" field — IDFieldID should be 0
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "Event",
		Fields: []schema.Field{
			{ID: 1, Name: "key", Type: schema.FieldString},
			{ID: 10, Name: "user", Type: schema.FieldModel, TypeName: "User",
				IsList: false, Relation: true, Module: "user", ForeignKey: "eventKey"},
		},
	})
	m := s.Models["Event"]
	p := Plan(m, nil, "event")
	if p == nil {
		t.Fatal("expected non-nil plan")
	}
	if p.IDFieldID != 0 {
		t.Errorf("IDFieldID = %d, want 0 (no id field)", p.IDFieldID)
	}
}

func TestSchemaFieldToSkipType_NullableEnum(t *testing.T) {
	f := &schema.Field{Type: schema.FieldEnum, Nullable: true}
	got := schemaFieldToSkipType(f)
	if got != codec.SkipNullBytes {
		t.Errorf("nullable enum skip type = %d, want SkipNullBytes", got)
	}
}

func TestSchemaFieldToSkipType_NullableBytes(t *testing.T) {
	f := &schema.Field{Type: schema.FieldBytes, Nullable: true}
	got := schemaFieldToSkipType(f)
	if got != codec.SkipNullBytes {
		t.Errorf("nullable bytes skip type = %d, want SkipNullBytes", got)
	}
}

func TestSchemaFieldToSkipType_NullableModel(t *testing.T) {
	f := &schema.Field{Type: schema.FieldModel, Nullable: true}
	got := schemaFieldToSkipType(f)
	if got != codec.SkipNullBytes {
		t.Errorf("nullable model skip type = %d, want SkipNullBytes", got)
	}
}

func TestSchemaFieldToSkipType_NullableDateTime(t *testing.T) {
	f := &schema.Field{Type: schema.FieldDateTime, Nullable: true}
	got := schemaFieldToSkipType(f)
	if got != codec.SkipNullVarint {
		t.Errorf("nullable datetime skip type = %d, want SkipNullVarint", got)
	}
}

func TestSchemaFieldToSkipType_NullableDuration(t *testing.T) {
	f := &schema.Field{Type: schema.FieldDuration, Nullable: true}
	got := schemaFieldToSkipType(f)
	if got != codec.SkipNullVarint {
		t.Errorf("nullable duration skip type = %d, want SkipNullVarint", got)
	}
}

func TestPlan_NilMaskSelectAll(t *testing.T) {
	m := userModelWithExtend()
	// nil mask = select all → should include extend fields
	p := Plan(m, nil, "user")
	if p == nil {
		t.Fatal("nil mask on model with extend should produce a plan")
	}

	// All local fields in primary
	if !codec.FieldMaskHas(p.Primary.Mask, 1) {
		t.Error("primary should include id")
	}
	if !codec.FieldMaskHas(p.Primary.Mask, 2) {
		t.Error("primary should include name")
	}
	if !codec.FieldMaskHas(p.Primary.Mask, 3) {
		t.Error("primary should include email")
	}

	// Extend fields should be in extends
	if len(p.Extends) != 2 {
		t.Fatalf("expected 2 extend steps for all fields, got %d", len(p.Extends))
	}
}
