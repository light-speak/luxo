package luvia

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// QueryPlan describes how a gateway request should be split across services.
// Nil plan = no extend fields in the mask → direct forward (zero overhead).
type QueryPlan struct {
	// Primary is the main request sent to the API's owning module.
	// Its mask contains only fields owned by that module + the id field.
	Primary PlanStep

	// Extends are parallel requests to other modules for extend fields.
	Extends []ExtendStep

	// OriginalMask is the client's original field mask (for response merging).
	OriginalMask []byte

	// PrimaryKeyField identifies the schema-declared key used for federation.
	PrimaryKeyField *schema.Field
}

// PlanStep describes a request to a single module.
type PlanStep struct {
	Module string // target module name
	Mask   []byte // field mask for this module's fields
}

// ExtendStep describes a request to resolve an extend field from another module.
type ExtendStep struct {
	SvcName    string // resolve endpoint name (e.g. "svc:resolve:Post:userId")
	Module     string // source module that owns the extend field
	FieldID    int    // field ID in the parent model (for response ordering)
	FieldName  string // field name (e.g. "posts")
	ModelName  string // target model name (e.g. "Post")
	ForeignKey string // FK field name on the target model (e.g. "userId")
	IsList     bool   // true if the field is a list ([Post] vs Post)
	Mask       []byte // nested field mask for the extend model's fields (nil = all)
}

// Plan analyzes a field mask against a model's schema and produces a QueryPlan.
// Returns nil if there are no cross-module extend fields in the mask,
// meaning the gateway should directly forward the request (zero overhead).
//
// Parameters:
//   - model: the schema model for the API's return type
//   - mask: the client's field mask (nil = all fields)
//   - apiModule: the module that owns the API
func Plan(model *schema.Model, mask []byte, apiModule string) (*QueryPlan, error) {
	if model == nil {
		return nil, nil
	}
	// Quick check: if no extend fields exist in model, skip planning entirely
	if !model.HasExtendFields() {
		return nil, nil
	}

	primaryKey := model.PrimaryKeyField()

	var extends []ExtendStep
	primaryMask := []byte{}
	var primaryChildren []codec.SelectionMaskChild
	selectedFields := codec.SelectionMaskFields(mask)

	for i := range model.Fields {
		f := &model.Fields[i]

		// Skip fields not in the requested mask
		if !codec.FieldMaskHas(selectedFields, f.ID) {
			continue
		}

		if f.Module == "" {
			// Local field → include in primary mask
			primaryMask = codec.FieldMaskSet(primaryMask, f.ID)
			if child, ok := codec.SelectionMaskNested(mask, f.ID); ok {
				primaryChildren = append(primaryChildren, codec.SelectionMaskChild{FieldID: f.ID, Mask: child})
			}
		} else if f.Relation && f.ForeignKey != "" {
			// Relation extend field (e.g. posts: [Post] @hasMany) → resolve via RPC
			extends = append(extends, ExtendStep{
				SvcName:    "svc:resolve:" + f.TypeName + ":" + f.ForeignKey,
				Module:     f.Module,
				FieldID:    f.ID,
				FieldName:  f.Name,
				ModelName:  f.TypeName,
				ForeignKey: f.ForeignKey,
				IsList:     f.IsList,
				Mask:       selectionChild(mask, f.ID),
			})
		} else {
			return nil, fmt.Errorf("luvia: extend field %s.%s has no executable resolver", model.Name, f.Name)
		}
	}

	// No extend fields in request → direct forward
	if len(extends) == 0 {
		return nil, nil
	}
	if primaryKey == nil {
		return nil, fmt.Errorf("luvia: model %s requires a primary key for federation", model.Name)
	}
	if primaryKey.Nullable || primaryKey.IsList || !isFederationKeyType(primaryKey.Type) {
		return nil, fmt.Errorf("luvia: model %s has unsupported federation primary key %s", model.Name, primaryKey.Name)
	}

	// Auto-include the primary key in the primary mask for extend resolution.
	primaryMask = codec.FieldMaskSet(primaryMask, primaryKey.ID)

	return &QueryPlan{
		Primary: PlanStep{
			Module: apiModule,
			Mask:   codec.AppendSelectionMask(nil, primaryMask, primaryChildren),
		},
		Extends:         extends,
		OriginalMask:    mask,
		PrimaryKeyField: primaryKey,
	}, nil
}

func isFederationKeyType(fieldType schema.FieldType) bool {
	return fieldType == schema.FieldInt || fieldType == schema.FieldString || fieldType == schema.FieldUUID
}

func selectionChild(mask []byte, fieldID int) []byte {
	child, _ := codec.SelectionMaskNested(mask, fieldID)
	return child
}

// schemaFieldToSkipType maps a schema field type to a codec skip type.
func schemaFieldToSkipType(f *schema.Field) codec.FieldSkipType {
	switch f.Type {
	case schema.FieldInt, schema.FieldDateTime, schema.FieldDuration, schema.FieldBool:
		if f.Nullable {
			return codec.SkipNullVarint
		}
		return codec.SkipVarint
	case schema.FieldFloat:
		if f.Nullable {
			return codec.SkipNullFixed64
		}
		return codec.SkipFixed64
	case schema.FieldUUID:
		if f.Nullable {
			return codec.SkipNullFixed16
		}
		return codec.SkipFixed16
	case schema.FieldString, schema.FieldEnum, schema.FieldBytes, schema.FieldModel:
		if f.Nullable {
			return codec.SkipNullBytes
		}
		return codec.SkipBytes
	default:
		return codec.SkipBytes
	}
}
