package luvia

import (
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Merge combines a primary binary response with extend field responses.
//
// The primary response is a standard WriteLuxo output:
//
//	[arenaHeader varint][fieldID][value]...[0x00 end]
//
// Each extend response is a canonical field value (presence marker when
// nullable, then a length-prefixed object or columnar sub-message).
//
// Strategy: strip the primary's end marker, append extend fields (each
// prefixed with its field ID), then write a new end marker.
// This preserves field-ID-based decoding — the client reads fields by ID,
// not by position, so ordering doesn't matter.
//
// Returns the merged binary response.
func Merge(primary []byte, extends []ExtendResult) []byte {
	if len(extends) == 0 {
		return primary
	}

	// Calculate total size for pre-allocation
	totalSize := len(primary) // includes arena header + fields + 0x00
	for _, ext := range extends {
		totalSize += len(ext.Data) // fieldID varint + value bytes
	}

	result := make([]byte, 0, totalSize)

	// Copy primary response WITHOUT the trailing end marker (0x00)
	if len(primary) > 0 && primary[len(primary)-1] == 0x00 {
		result = append(result, primary[:len(primary)-1]...)
	} else {
		result = append(result, primary...)
	}

	// Append each extend field: [fieldID varint][value data]
	for _, ext := range extends {
		if len(ext.Data) == 0 {
			continue
		}
		result = codec.AppendVarint(result, uint64(ext.FieldID))
		result = append(result, ext.Data...)
	}

	// Write new end marker
	result = append(result, 0x00)
	return result
}

// ExtendResult holds the resolved data for one extend field.
type ExtendResult struct {
	FieldID int    // field ID in the parent model
	Data    []byte // canonical field value: optional presence marker + length-prefixed sub-message
}

// BinaryKeys stores primary-key values exactly as encoded on the wire. The
// gateway can forward them as a list parameter without decoding, reflection,
// or per-key allocation.
type BinaryKeys struct {
	count  int
	values []byte
}

// Len returns the number of encoded keys.
func (k BinaryKeys) Len() int {
	return k.count
}

// EncodeParam wraps the raw values as a list parameter message.
func (k BinaryKeys) EncodeParam(fieldID int) []byte {
	result := make([]byte, 0, len(k.values)+22)
	result = codec.AppendVarint(result, uint64(fieldID))
	result = codec.AppendVarint(result, uint64(k.count))
	result = append(result, k.values...)
	return append(result, 0)
}

// EncodeResolveParams adds the requested value shape without re-encoding keys.
func (k BinaryKeys) EncodeResolveParams(shape int) []byte {
	result := k.EncodeParam(1)
	result = append(result[:len(result)-1], 2)
	result = codec.AppendSvarint(result, int64(shape))
	return append(result, 0)
}

// ExtractPrimaryKeys extracts one primary key while preserving its wire
// representation. This is the single-record federation path.
func ExtractPrimaryKeys(primary []byte, keyField *schema.Field, model *schema.Model, registry *schema.Schema) (BinaryKeys, bool) {
	if len(primary) == 0 || keyField == nil || model == nil || registry == nil || !isFederationKeyType(keyField.Type) || keyField.Nullable || keyField.IsList {
		return BinaryKeys{}, false
	}
	dec := codec.NewDecoder(primary)
	dec.ReadArenaSize()
	var keys BinaryKeys
	for dec.NextField() {
		fieldID := dec.FieldID()
		field := model.FieldByID(fieldID)
		if field == nil {
			return BinaryKeys{}, false
		}
		start := dec.Offset()
		if !skipResponseField(dec, field, registry, false, 0) {
			return BinaryKeys{}, false
		}
		if fieldID == keyField.ID {
			if keys.count != 0 {
				return BinaryKeys{}, false
			}
			keys = BinaryKeys{count: 1, values: primary[start:dec.Offset()]}
		}
	}
	if dec.Finish() != nil || keys.count == 0 {
		return BinaryKeys{}, false
	}
	return keys, true
}

// ExtractID reads the id field from a primary binary response using schema type info.
// Handles any field ordering by skipping fields of known types.
// Returns (id, true) on success.
func ExtractID(primary []byte, idFieldID int, fieldTypes map[int]codec.FieldSkipType) (int64, bool) {
	if len(primary) == 0 {
		return 0, false
	}
	dec := codec.NewDecoder(primary)
	dec.SkipArenaHeader()
	for dec.NextField() {
		fid := dec.FieldID()
		if fid == idFieldID {
			return dec.ReadInt(), dec.Err() == nil
		}
		// Skip this field based on its known type
		st, ok := fieldTypes[fid]
		if !ok {
			return 0, false // unknown field, can't skip
		}
		skipField(dec, st)
		if dec.Err() != nil {
			return 0, false
		}
	}
	return 0, false
}

// skipField advances the decoder past one field value of the given type.
// skipField advances the decoder past one field value. Zero allocation.
func skipField(dec *codec.Decoder, st codec.FieldSkipType) {
	dec.SkipValue(st)
}

// --- Columnar (list) federation ---

// ExtractIDColumn reads the ID column values from columnar-encoded data.
// Uses the model schema to skip non-ID columns (need type info to advance past column data).
// Returns nil if the ID column is not found.
func ExtractIDColumn(data []byte, idFieldID int, model *schema.Model) []int64 {
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || r.Count() == 0 {
		return nil
	}
	for r.NextColumn() {
		if r.FieldID() == idFieldID {
			return r.ReadColumnInt()
		}
		// Skip this column using schema type info
		f := model.FieldByID(r.FieldID())
		if f == nil {
			return nil // unknown column, can't skip
		}
		skipColumnarColumn(r, f)
		if r.Err() != nil {
			return nil
		}
	}
	return nil
}

// ExtractPrimaryKeyColumn extracts a complete primary-key column while
// preserving its wire representation. The returned bytes reference data.
func ExtractPrimaryKeyColumn(data []byte, keyField *schema.Field, model *schema.Model) (BinaryKeys, bool) {
	if len(data) == 0 || keyField == nil || model == nil || !isFederationKeyType(keyField.Type) || keyField.Nullable || keyField.IsList {
		return BinaryKeys{}, false
	}
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || r.Count() == 0 {
		return BinaryKeys{}, false
	}
	var keys BinaryKeys
	for {
		columnStart := r.Offset()
		if !r.NextColumn() {
			end, size := codec.ReadVarint(data, columnStart)
			if r.Err() != nil || size <= 0 || end != 0 || columnStart+size != len(data) || keys.count == 0 {
				return BinaryKeys{}, false
			}
			return keys, true
		}
		field := model.FieldByID(r.FieldID())
		if field == nil {
			return BinaryKeys{}, false
		}
		start := r.Offset()
		skipColumnarColumn(r, field)
		if r.Err() != nil {
			return BinaryKeys{}, false
		}
		if field.ID == keyField.ID {
			if keys.count != 0 {
				return BinaryKeys{}, false
			}
			keys = BinaryKeys{count: r.Count(), values: data[start:r.Offset()]}
		}
	}
}

// skipColumnarColumn advances past all values in a column without allocating.
func skipColumnarColumn(r *codec.ColumnarReader, f *schema.Field) {
	if f.IsList {
		if f.Nullable {
			r.SkipColumnBytesPtr()
		} else {
			r.SkipColumnBytes()
		}
		return
	}
	switch f.Type {
	case schema.FieldDateTime:
		if f.Nullable {
			r.SkipColumnDateTimePtr()
		} else {
			r.SkipColumnDateTime()
		}
	case schema.FieldInt, schema.FieldDuration:
		if f.Nullable {
			r.SkipColumnIntPtr()
		} else {
			r.SkipColumnInt()
		}
	case schema.FieldFloat:
		if f.Nullable {
			r.SkipColumnFloatPtr()
		} else {
			r.SkipColumnFloat()
		}
	case schema.FieldString, schema.FieldEnum, schema.FieldDecimal:
		if f.Nullable {
			r.SkipColumnStringPtr()
		} else {
			r.SkipColumnString()
		}
	case schema.FieldBool:
		if f.Nullable {
			r.SkipColumnBoolPtr()
		} else {
			r.SkipColumnBool()
		}
	case schema.FieldUUID:
		if f.Nullable {
			r.SkipColumnUUIDPtr()
		} else {
			r.SkipColumnUUID()
		}
	default:
		if f.Nullable {
			r.SkipColumnBytesPtr()
		} else {
			r.SkipColumnBytes()
		}
	}
}

// MergeColumnar appends extend field columns to a columnar-encoded response.
// Each ExtendColumnResult represents one extend field with per-record blob data.
//
// Format of extend column: [fieldID varint][blob0][blob1]...[blobN]
// Each blob already includes its optional presence marker and length prefix.
// The body is WriteColumnar output for a list or WriteLuxo for an object.
//
// Strategy: strip the end marker (0x00), append extend columns, new end marker.
func MergeColumnar(primary []byte, extends []ExtendColumnResult) []byte {
	if len(extends) == 0 {
		return primary
	}

	totalSize := len(primary)
	for _, ext := range extends {
		totalSize += 10 // fieldID varint overhead
		for _, blob := range ext.Blobs {
			totalSize += 10 + len(blob) // varint len + blob data
		}
	}

	result := make([]byte, 0, totalSize)

	// Copy primary WITHOUT end marker
	if len(primary) > 0 && primary[len(primary)-1] == 0x00 {
		result = append(result, primary[:len(primary)-1]...)
	} else {
		result = append(result, primary...)
	}

	// Append extend columns
	for _, ext := range extends {
		if len(ext.Blobs) == 0 {
			continue
		}
		result = codec.AppendVarint(result, uint64(ext.FieldID))
		for _, blob := range ext.Blobs {
			result = append(result, blob...)
		}
	}

	// New end marker
	result = append(result, 0x00)
	return result
}

// ExtendColumnResult holds per-record blob data for one extend field.
type ExtendColumnResult struct {
	FieldID int      // field ID in the parent model
	Blobs   [][]byte // canonical field values, one per record (index = record index)
}

// ParseGroupedResponse indexes canonical relation values in request key order.
// Each value is [present?][length][sub-message], exactly as embedded in a row
// or column. A null is a one-byte value, never an omitted field. The returned
// slices borrow resp; callers must keep it alive until merging completes.
func ParseGroupedResponse(resp []byte, nullable bool) [][]byte {
	keyCount, n := codec.ReadVarint(resp, 0)
	if n <= 0 || keyCount > codec.MaxArrayElements || keyCount > uint64(len(resp)-n) {
		return nil
	}
	off := n
	blobs := make([][]byte, int(keyCount))
	for i := range blobs {
		start := off
		if nullable {
			if off >= len(resp) || resp[off] > 1 {
				return nil
			}
			off++
			if resp[start] == 0 {
				blobs[i] = resp[start:off]
				continue
			}
		}
		body, consumed := codec.ReadBytes(resp, off)
		if consumed <= 0 || len(body) == 0 {
			return nil
		}
		off += consumed
		blobs[i] = resp[start:off]
	}
	if off != len(resp) {
		return nil
	}
	return blobs
}

// ValidateResolvedFields checks child boundaries without decoding models or
// converting row/column formats. Missing required values and malformed children
// fail the entire group before the gateway publishes any bytes.
func ValidateResolvedFields(blobs [][]byte, step ExtendStep, registry *schema.Schema) bool {
	if registry == nil {
		return false
	}
	fields, ok := nestedResponseFields(registry, step.ModelName)
	if !ok {
		return false
	}
	for _, blob := range blobs {
		dec := codec.NewDecoder(blob)
		if step.Nullable && !dec.ReadBool() {
			if dec.Finish() != nil {
				return false
			}
			continue
		}
		body := dec.ReadBytes()
		if dec.Finish() != nil || !skipModelResponse(body, fields, registry, step.IsList, 0) {
			return false
		}
	}
	return true
}
