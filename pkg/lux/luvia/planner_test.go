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
