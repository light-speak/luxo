package api

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// APIRegistry maps API IDs (from luxo.lock) to handler names and param metadata.
// Built at startup by RegisterHandlers, used for binary protocol routing.
type APIRegistry struct {
	idToName map[int]string
	nameToID map[string]int
	// paramOrder maps api_name → ordered param names (for positional binary decoding)
	paramOrder map[string][]ParamMeta
}

// ParamMeta describes an API parameter for binary decoding.
type ParamMeta struct {
	Name    string
	Type    string // "Int", "Float", "String", "Boolean"
	FieldID int    // from luxo.lock
}

// NewAPIRegistry creates an empty registry.
func NewAPIRegistry() *APIRegistry {
	return &APIRegistry{
		idToName:   make(map[int]string),
		nameToID:   make(map[string]int),
		paramOrder: make(map[string][]ParamMeta),
	}
}

// Register adds an API to the registry.
func (r *APIRegistry) Register(name string, id int) {
	r.idToName[id] = name
	r.nameToID[name] = id
}

// RegisterParams sets the ordered parameter metadata for an API.
func (r *APIRegistry) RegisterParams(name string, params []ParamMeta) {
	r.paramOrder[name] = params
}

// NameByID returns the API name for a given ID.
func (r *APIRegistry) NameByID(id int) (string, bool) {
	name, ok := r.idToName[id]
	return name, ok
}

// IDByName returns the API ID for a given name.
func (r *APIRegistry) IDByName(name string) (int, bool) {
	id, ok := r.nameToID[name]
	return id, ok
}

// ParseBinaryRequest decodes a Luxo binary request into a Request struct.
// Wire format:
//
//	[API ID varint] [Field Mask length varint] [Field Mask bytes] [Params...]
//	Params: [fieldID varint] [value] ... [0x00 terminator]
//
// Params are decoded to json.RawMessage so existing handlers work unchanged.
func (r *APIRegistry) ParseBinaryRequest(body []byte) (*Request, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty binary request")
	}

	off := 0

	// Read API ID
	apiID, n := codec.ReadVarint(body, off)
	if n == 0 {
		return nil, fmt.Errorf("invalid API ID varint")
	}
	off += n

	apiName, ok := r.idToName[int(apiID)]
	if !ok {
		return nil, fmt.Errorf("unknown API ID: %d", apiID)
	}

	// Read field mask length + bytes
	maskLen, n := codec.ReadVarint(body, off)
	if n == 0 {
		return nil, fmt.Errorf("invalid field mask length")
	}
	off += n

	var fields []*selection.Field
	if maskLen > 0 {
		if maskLen > uint64(len(body)) {
			return nil, fmt.Errorf("field mask length overflow")
		}
		maskEnd := off + int(maskLen)
		if maskEnd > len(body) {
			return nil, fmt.Errorf("field mask exceeds body")
		}
		fields = decodeFieldMask(body[off:maskEnd], r.paramOrder[apiName])
		off = maskEnd
	}

	// Read params directly to native Go values — zero JSON conversion
	typedParams := make(map[string]any)
	paramMeta := r.paramOrder[apiName]

	dec := codec.NewDecoder(body[off:])
	for dec.NextField() {
		fid := dec.FieldID()
		var meta *ParamMeta
		for i := range paramMeta {
			if paramMeta[i].FieldID == fid {
				meta = &paramMeta[i]
				break
			}
		}
		if meta == nil {
			continue
		}
		switch meta.Type {
		case "Int":
			typedParams[meta.Name] = dec.ReadInt()
		case "Float":
			typedParams[meta.Name] = dec.ReadFloat()
		case "String":
			typedParams[meta.Name] = dec.ReadString()
		case "Boolean":
			typedParams[meta.Name] = dec.ReadBool()
		case "IntArray":
			typedParams[meta.Name] = dec.ReadIntArray()
		case "StringArray":
			typedParams[meta.Name] = dec.ReadStringArray()
		case "FloatArray":
			typedParams[meta.Name] = dec.ReadFloatArray()
		}
	}

	if err := dec.Err(); err != nil {
		return nil, fmt.Errorf("param decode: %w", err)
	}

	req := &Request{
		API:         apiName,
		Select:      fields,
		TypedParams: typedParams,
		Page:        1,
		PageSize:    20,
		BinaryMode:  true,
		FieldMask:   nil, // TODO: pass actual field mask bytes
	}
	return req, nil
}

// decodeFieldMask converts a bitmap to selection.Field list.
// Each bit position corresponds to a field ID from the model.
// For now, returns nil (SELECT *) — full bitmap support comes next.
func decodeFieldMask(mask []byte, _ []ParamMeta) []*selection.Field {
	// TODO: map bit positions to model field names using lock file
	_ = mask
	return nil
}

// EncodeBinaryRequest creates a binary request for testing/CLI.
// apiID: from luxo.lock, params: name→value pairs.
func EncodeBinaryRequest(apiID int, params map[string]any, paramMeta []ParamMeta) []byte {
	var buf []byte

	// API ID
	buf = codec.AppendVarint(buf, uint64(apiID))

	// Field mask (empty for now — means SELECT *)
	buf = codec.AppendVarint(buf, 0) // mask length = 0

	// Params
	var enc codec.Encoder
	for _, meta := range paramMeta {
		v, ok := params[meta.Name]
		if !ok {
			continue
		}
		switch meta.Type {
		case "Int":
			switch iv := v.(type) {
			case int:
				enc.WriteFieldInt(meta.FieldID, int64(iv))
			case int64:
				enc.WriteFieldInt(meta.FieldID, iv)
			case float64:
				enc.WriteFieldInt(meta.FieldID, int64(iv))
			}
		case "Float":
			if fv, ok := v.(float64); ok {
				enc.WriteFieldFloat(meta.FieldID, fv)
			}
		case "String":
			if sv, ok := v.(string); ok {
				enc.WriteFieldString(meta.FieldID, sv)
			}
		case "Boolean":
			if bv, ok := v.(bool); ok {
				enc.WriteFieldBool(meta.FieldID, bv)
			}
		}
	}
	enc.WriteEnd()
	buf = append(buf, enc.Bytes()...)

	return buf
}
