package luvia

import (
	"bytes"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestResolvedResponseUsesCanonicalFieldValues(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		value := codec.AppendBytes(nil, []byte{0, 1, 2, 0})
		if nullable {
			value = append([]byte{1}, value...)
		}
		response := append([]byte{1}, value...)
		blobs := ParseGroupedResponse(response, nullable)
		if len(blobs) != 1 || !bytes.Equal(blobs[0], value) {
			t.Fatalf("nullable=%v: value changed: %x", nullable, blobs)
		}
		if &blobs[0][0] != &response[1] {
			t.Fatal("resolver parser copied the field payload")
		}
		row := Merge([]byte{0, 0}, []ExtendResult{{FieldID: 1, Data: blobs[0]}})
		if !bytes.Equal(row, append(append([]byte{0, 1}, value...), 0)) {
			t.Fatalf("row field framing changed: %x", row)
		}
		column := MergeColumnar([]byte{1, 0, 0}, []ExtendColumnResult{{FieldID: 1, Blobs: blobs}})
		if !bytes.Equal(column, append(append([]byte{1, 0, 1}, value...), 0)) {
			t.Fatalf("column field framing changed: %x", column)
		}
	}
}

func TestResolvedNullRemainsPresent(t *testing.T) {
	blobs := ParseGroupedResponse([]byte{1, 0}, true)
	if len(blobs) != 1 || !bytes.Equal(blobs[0], []byte{0}) {
		t.Fatalf("null was omitted: %x", blobs)
	}
	if ParseGroupedResponse([]byte{1, 2, 0}, true) != nil {
		t.Fatal("invalid nullable marker accepted")
	}
}

func TestPlanPreservesResolverNullability(t *testing.T) {
	model := &schema.Model{Name: "Parent", Fields: []schema.Field{
		{ID: 1, Name: "id", Type: schema.FieldInt, PrimaryKey: true},
		{ID: 2, Name: "child", Type: schema.FieldModel, TypeName: "Child", Module: "child", Relation: true, ForeignKey: "parentId", Nullable: true},
	}}
	plan, err := Plan(model, nil, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || len(plan.Extends) != 1 || !plan.Extends[0].Nullable {
		t.Fatal("resolver nullability was lost")
	}
}

func TestValidateResolvedFields(t *testing.T) {
	registry := schema.New()
	registry.RegisterModel(&schema.Model{Name: "Child", Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}}})
	registry.RegisterType(&schema.TypeDecl{Name: "Payload", Fields: registry.Models["Child"].Fields})
	for _, name := range []string{"Child", "Payload"} {
		for shape := 0; shape < 4; shape++ {
			step := ExtendStep{ModelName: name, IsList: shape&1 != 0, Nullable: shape&2 != 0}
			if step.Shape() != shape {
				t.Fatal("shape bits changed")
			}
			body := []byte{0, 1, 2, 0}
			if step.IsList {
				body = append([]byte{1}, body...)
			}
			value := codec.AppendBytes(nil, body)
			if step.Nullable {
				value = append([]byte{1}, value...)
			}
			values := [][]byte{value}
			if !ValidateResolvedFields(values, step, registry) {
				t.Fatalf("valid shape %d rejected", shape)
			}
			if allocs := testing.AllocsPerRun(100, func() { ValidateResolvedFields(values, step, registry) }); allocs != 0 {
				t.Fatalf("validation allocates: %v", allocs)
			}
			for length := 0; length < len(value); length++ {
				if ValidateResolvedFields([][]byte{value[:length]}, step, registry) {
					t.Fatalf("accepted truncated shape %d", shape)
				}
			}
			for _, invalid := range [][]byte{{2, 0}, append(bytes.Clone(value), 0), codec.AppendBytes(nil, []byte{0, 99, 2, 0})} {
				if ValidateResolvedFields([][]byte{invalid}, step, registry) {
					t.Fatalf("accepted invalid value: %x", invalid)
				}
			}
			if step.Nullable && !ValidateResolvedFields([][]byte{{0}}, step, registry) {
				t.Fatal("selected null rejected")
			}
			if ValidateResolvedFields(values, step, nil) {
				t.Fatal("missing registry accepted")
			}
		}
	}
	if ValidateResolvedFields(nil, ExtendStep{ModelName: "Missing"}, registry) {
		t.Fatal("missing child schema accepted")
	}
}

func TestResolveParamsPreservePrimaryKeyBytes(t *testing.T) {
	keys := BinaryKeys{count: 2, values: codec.AppendSvarint(codec.AppendSvarint(nil, -9223372036854775808), 9223372036854775807)}
	for shape := 0; shape < 4; shape++ {
		dec := codec.NewDecoder(keys.EncodeResolveParams(shape))
		if !dec.NextField() || dec.FieldID() != 1 {
			t.Fatal("missing keys")
		}
		values := dec.ReadIntArray()
		if len(values) != 2 || values[0] != -9223372036854775808 || values[1] != 9223372036854775807 {
			t.Fatal("key bytes changed")
		}
		if !dec.NextField() || dec.FieldID() != 2 || dec.ReadInt() != int64(shape) {
			t.Fatal("missing result shape")
		}
		if dec.NextField() || dec.Finish() != nil {
			t.Fatal("invalid parameter boundary")
		}
	}
}
