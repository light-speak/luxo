package luvia

import (
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

	// IDFieldID is the field ID of the "id" field in the primary model.
	IDFieldID int

	// FieldTypes maps field ID → skip type for all primary fields.
	// Used by ExtractID to skip non-ID fields without schema lookup.
	FieldTypes map[int]codec.FieldSkipType
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
func Plan(model *schema.Model, mask []byte, apiModule string) *QueryPlan {
	if model == nil {
		return nil
	}
	// Quick check: if no extend fields exist in model, skip planning entirely
	if !model.HasExtendFields() {
		return nil
	}

	// Find the id field's ID first (needed for FK resolution)
	var idFieldID int
	for i := range model.Fields {
		if model.Fields[i].Name == "id" {
			idFieldID = model.Fields[i].ID
			break
		}
	}

	var extends []ExtendStep
	primaryMask := []byte{}

	for i := range model.Fields {
		f := &model.Fields[i]

		// Skip fields not in the requested mask
		if !codec.FieldMaskHas(mask, f.ID) {
			continue
		}

		if f.Module == "" {
			// Local field → include in primary mask
			primaryMask = codec.FieldMaskSet(primaryMask, f.ID)
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
				Mask:       nil,
			})
		}
		// Non-relation extend fields (scalar like postCount) are skipped —
		// no resolve endpoint exists for them. They require aggregation RPC
		// which is not yet supported.
	}

	// No extend fields in request → direct forward
	if len(extends) == 0 {
		return nil
	}

	// Auto-include id in primary mask (needed for extend FK resolution)
	if idFieldID > 0 {
		primaryMask = codec.FieldMaskSet(primaryMask, idFieldID)
	}

	// Build field types map for schema-aware ID extraction
	fieldTypes := make(map[int]codec.FieldSkipType, len(model.Fields))
	for i := range model.Fields {
		f := &model.Fields[i]
		if f.Module != "" {
			continue // extend fields not in primary response
		}
		fieldTypes[f.ID] = schemaFieldToSkipType(f)
	}

	return &QueryPlan{
		Primary: PlanStep{
			Module: apiModule,
			Mask:   primaryMask,
		},
		Extends:      extends,
		OriginalMask: mask,
		IDFieldID:    idFieldID,
		FieldTypes:   fieldTypes,
	}
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
	case schema.FieldString, schema.FieldEnum, schema.FieldBytes, schema.FieldModel:
		if f.Nullable {
			return codec.SkipNullBytes
		}
		return codec.SkipBytes
	default:
		return codec.SkipBytes
	}
}
