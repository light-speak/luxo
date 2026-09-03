package luvia

import (
	"bytes"
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

func selectionNode(mask []byte, children ...codec.SelectionMaskChild) []byte {
	return codec.AppendSelectionMask(nil, mask, children)
}

func selectionFields(mask []byte) []byte {
	return codec.SelectionMaskFields(mask)
}

func planForTest(t *testing.T, model *schema.Model, mask []byte, module string) *QueryPlan {
	t.Helper()
	plan, err := Plan(model, mask, module)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPlan_NilModelReturnsNil(t *testing.T) {
	p := planForTest(t, nil, nil, "user")
	if p != nil {
		t.Error("nil model should return nil plan")
	}
}

func TestPlan_NoExtendFieldsReturnsNil(t *testing.T) {
	m := simpleUserModel()
	p := planForTest(t, m, nil, "user")
	if p != nil {
		t.Error("model without extend fields should return nil plan")
	}
}

func TestPlan_NoExtendFieldsInMaskReturnsNil(t *testing.T) {
	m := userModelWithExtend()
	// Only request name and email (no extend fields)
	mask := codec.FieldMaskSet(nil, 2) // name
	mask = codec.FieldMaskSet(mask, 3) // email
	mask = selectionNode(mask)
	p := planForTest(t, m, mask, "user")
	if p != nil {
		t.Error("mask without extend fields should return nil plan")
	}
}

func TestPlan_WithExtendFields(t *testing.T) {
	m := userModelWithExtend()
	// Request name + posts (extend from post module)
	mask := codec.FieldMaskSet(nil, 2)  // name
	mask = codec.FieldMaskSet(mask, 10) // posts (extend)
	postsMask := selectionNode(codec.FieldMaskSet(nil, 2))
	mask = selectionNode(mask, codec.SelectionMaskChild{FieldID: 10, Mask: postsMask})
	p := planForTest(t, m, mask, "user")
	if p == nil {
		t.Fatal("expected non-nil plan")
	}

	// Primary should include name + id (auto-added)
	if p.Primary.Module != "user" {
		t.Errorf("primary module = %q, want %q", p.Primary.Module, "user")
	}
	if !codec.FieldMaskHas(selectionFields(p.Primary.Mask), 2) {
		t.Error("primary mask should include name (field 2)")
	}
	if !codec.FieldMaskHas(selectionFields(p.Primary.Mask), 1) {
		t.Error("primary mask should auto-include id (field 1)")
	}
	// posts should NOT be in primary mask
	if codec.FieldMaskHas(selectionFields(p.Primary.Mask), 10) {
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
	if !bytes.Equal(ext.Mask, postsMask) {
		t.Errorf("extend mask = %v, want %v", ext.Mask, postsMask)
	}

	if p.PrimaryKeyField == nil || p.PrimaryKeyField.ID != 1 {
		t.Errorf("PrimaryKeyField = %#v, want field 1", p.PrimaryKeyField)
	}
}

func TestPlan_UsesDeclaredPrimaryKey(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "Product",
		Fields: []schema.Field{
			{ID: 7, Name: "sku", Type: schema.FieldString, PrimaryKey: true},
			{ID: 8, Name: "name", Type: schema.FieldString},
			{ID: 10, Name: "reviews", Type: schema.FieldModel, TypeName: "Review",
				IsList: true, Relation: true, Module: "review", ForeignKey: "productSku"},
		},
	})

	plan, err := Plan(s.Models["Product"], nil, "product")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil {
		t.Fatal("expected federation plan")
	}
	if plan.PrimaryKeyField == nil || plan.PrimaryKeyField.Name != "sku" {
		t.Fatalf("primary key field = %#v, want sku", plan.PrimaryKeyField)
	}
	if !codec.FieldMaskHas(selectionFields(plan.Primary.Mask), 7) {
		t.Fatal("primary mask must include the declared primary key")
	}
}

func TestPlan_MultipleExtendModules(t *testing.T) {
	m := userModelWithExtend()
	// Request name + posts + comments (two different extend modules)
	mask := codec.FieldMaskSet(nil, 2)  // name
	mask = codec.FieldMaskSet(mask, 10) // posts (post module)
	mask = codec.FieldMaskSet(mask, 11) // comments (comment module)
	mask = selectionNode(mask)
	p := planForTest(t, m, mask, "user")
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

func TestPlan_ExtendFieldNonRelationReturnsError(t *testing.T) {
	s := schema.New()
	s.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt, PrimaryKey: true},
			{ID: 2, Name: "name", Type: schema.FieldString},
			{ID: 10, Name: "postCount", Type: schema.FieldInt, Module: "post"},
		},
	})

	if _, err := Plan(s.Models["User"], nil, "user"); err == nil {
		t.Fatal("unsupported scalar extend field must not be silently omitted")
	}
}

func TestPlan_NoPrimaryKeyField(t *testing.T) {
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
	if _, err := Plan(m, nil, "event"); err == nil {
		t.Fatal("federation model without a primary key must fail planning")
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
	p := planForTest(t, m, nil, "user")
	if p == nil {
		t.Fatal("nil mask on model with extend should produce a plan")
	}

	// All local fields in primary
	if !codec.FieldMaskHas(selectionFields(p.Primary.Mask), 1) {
		t.Error("primary should include id")
	}
	if !codec.FieldMaskHas(selectionFields(p.Primary.Mask), 2) {
		t.Error("primary should include name")
	}
	if !codec.FieldMaskHas(selectionFields(p.Primary.Mask), 3) {
		t.Error("primary should include email")
	}

	// Extend fields should be in extends
	if len(p.Extends) != 2 {
		t.Fatalf("expected 2 extend steps for all fields, got %d", len(p.Extends))
	}
}
