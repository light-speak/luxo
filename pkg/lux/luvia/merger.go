package luvia

import (
	"math/bits"
	"slices"

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
	return AppendMerged(nil, primary, extends)
}

// AppendMerged appends already validated messages to caller-owned storage.
// Source buffers must not overlap dst's backing array and remain unchanged.
// With sufficient capacity, no intermediate merged message is allocated.
func AppendMerged(dst, primary []byte, extends []ExtendResult) []byte {
	if len(extends) == 0 {
		return append(dst, primary...)
	}

	// Calculate total size for pre-allocation
	totalSize := len(primary) // includes arena header + fields + 0x00
	for _, ext := range extends {
		if len(ext.Data) > 0 {
			totalSize += max(1, (bits.Len64(uint64(ext.FieldID))+6)/7) + len(ext.Data) // fieldID varint + value bytes
		}
	}
	if len(primary) == 0 || primary[len(primary)-1] != 0 {
		totalSize++
	}
	result := slices.Grow(dst, totalSize)

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
	count      int
	values     []byte
	fieldStart int
	fieldEnd   int
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

// EncodeResolveParams uses registered parameter IDs without re-encoding keys.
// The caller validates the parameter IDs and shape against resolver metadata.
func (k BinaryKeys) EncodeResolveParams(shape, keysID, shapeID int) []byte {
	result := k.EncodeParam(keysID)
	result = codec.AppendVarint(result[:len(result)-1], uint64(shapeID))
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
	for {
		fieldStart := dec.Offset()
		if !dec.NextField() {
			break
		}
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
			keys = BinaryKeys{count: 1, values: primary[start:dec.Offset()], fieldStart: fieldStart, fieldEnd: dec.Offset()}
		}
	}
	if dec.Finish() != nil || keys.count == 0 {
		return BinaryKeys{}, false
	}
	return keys, true
}

// ExtractID reads the id field from a primary binary response using schema type info.
// Handles any field ordering by skipping fields of known types.
// Returns (id, true) only after the complete parent boundary is validated.
func ExtractID(primary []byte, idFieldID int, fieldTypes map[int]codec.FieldSkipType) (int64, bool) {
	if len(primary) == 0 {
		return 0, false
	}
	dec := codec.NewDecoder(primary)
	dec.SkipArenaHeader()
	var id int64
	found := false
	for dec.NextField() {
		fid := dec.FieldID()
		if fid == idFieldID {
			if found {
				return 0, false
			}
			id, found = dec.ReadInt(), true
		} else {
			// Skip this field based on its known type.
			st, ok := fieldTypes[fid]
			if !ok {
				return 0, false
			}
			skipField(dec, st)
		}
		if dec.Err() != nil {
			return 0, false
		}
	}
	if !found || dec.Finish() != nil {
		return 0, false
	}
	return id, true
}

// skipField advances the decoder past one field value of the given type.
// skipField advances the decoder past one field value. Zero allocation.
func skipField(dec *codec.Decoder, st codec.FieldSkipType) {
	dec.SkipValue(st)
}

// --- Columnar (list) federation ---

// ExtractIDColumn reads the ID column values from columnar-encoded data.
// Uses the model schema to skip non-ID columns (need type info to advance past column data).
// Returns nil for an invalid parent or a key that is not a non-null scalar Int.
func ExtractIDColumn(data []byte, idFieldID int, model *schema.Model) []int64 {
	if model == nil {
		return nil
	}
	field := model.FieldByID(idFieldID)
	if field == nil || field.Type != schema.FieldInt || field.Nullable || field.IsList {
		return nil
	}
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || r.Count() == 0 {
		return nil
	}
	var ids []int64
	for {
		columnStart := r.Offset()
		if !r.NextColumn() {
			end, size := codec.ReadVarint(data, columnStart)
			if r.Err() != nil || size <= 0 || end != 0 || columnStart+size != len(data) {
				return nil
			}
			return ids
		}
		column := model.FieldByID(r.FieldID())
		if column == nil {
			return nil
		}
		if column.ID == idFieldID {
			if ids != nil {
				return nil
			}
			ids = r.ReadColumnInt()
		} else {
			skipColumnarColumn(r, column)
		}
		if r.Err() != nil {
			return nil
		}
	}
}

// ExtractPrimaryKeyColumn extracts a complete primary-key column while
// preserving its wire representation. The returned bytes reference data.
func ExtractPrimaryKeyColumn(data []byte, keyField *schema.Field, model *schema.Model) (BinaryKeys, bool) {
	keys, _, ok := extractPrimaryKeyColumn(data, keyField, model, false)
	return keys, ok
}

// ExtractPrimaryKeyPage validates a columnar page and its three metadata values.
// The returned offset separates columns (including their terminator) from metadata.
// Both slices may then be merged without copying or re-encoding pagination values.
func ExtractPrimaryKeyPage(data []byte, keyField *schema.Field, model *schema.Model) (BinaryKeys, int, bool) {
	keys, end, ok := extractPrimaryKeyColumn(data, keyField, model, true)
	if !ok {
		return BinaryKeys{}, 0, false
	}
	tail := codec.NewDecoder(data[end:])
	tail.ReadInt()
	tail.ReadInt()
	tail.ReadInt()
	if tail.Finish() != nil {
		return BinaryKeys{}, 0, false
	}
	return keys, end, true
}

func extractPrimaryKeyColumn(data []byte, keyField *schema.Field, model *schema.Model, page bool) (BinaryKeys, int, bool) {
	if len(data) == 0 || keyField == nil || model == nil || !isFederationKeyType(keyField.Type) || keyField.Nullable || keyField.IsList {
		return BinaryKeys{}, 0, false
	}
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || (!page && r.Count() == 0) {
		return BinaryKeys{}, 0, false
	}
	var keys BinaryKeys
	for {
		columnStart := r.Offset()
		if !r.NextColumn() {
			end, size := codec.ReadVarint(data, columnStart)
			if r.Err() != nil || size <= 0 || end != 0 || (!page && columnStart+size != len(data)) || (keys.count == 0 && r.Count() != 0) {
				return BinaryKeys{}, 0, false
			}
			return keys, columnStart + size, true
		}
		field := model.FieldByID(r.FieldID())
		if field == nil {
			return BinaryKeys{}, 0, false
		}
		start := r.Offset()
		skipColumnarColumn(r, field)
		if r.Err() != nil {
			return BinaryKeys{}, 0, false
		}
		if field.ID == keyField.ID {
			if keys.fieldEnd != 0 {
				return BinaryKeys{}, 0, false
			}
			keys = BinaryKeys{count: r.Count(), values: data[start:r.Offset()], fieldStart: columnStart, fieldEnd: r.Offset()}
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
	return AppendMergedColumnar(nil, primary, extends)
}

// AppendMergedColumnar appends validated columns directly to owned output.
// A row uses the same field layout with exactly one blob per extend field.
// Source buffers must not overlap dst's backing array and remain unchanged.
func AppendMergedColumnar(dst, primary []byte, extends []ExtendColumnResult) []byte {
	return appendMergedColumnar(dst, nil, primary, extends)
}

// AppendMergedSelected omits a federation-only primary key while appending
// validated fields. keys must come from the same unchanged primary message.
// It copies each retained byte once, without decoding or an intermediate buffer.
func AppendMergedSelected(dst, primary []byte, extends []ExtendColumnResult, keys BinaryKeys, includeKey bool) []byte {
	if includeKey || keys.count == 0 {
		return AppendMergedColumnar(dst, primary, extends)
	}
	return appendMergedColumnar(dst, primary[:keys.fieldStart], primary[keys.fieldEnd:], extends)
}

func appendMergedColumnar(dst, prefix, primary []byte, extends []ExtendColumnResult) []byte {
	if len(extends) == 0 {
		return append(append(dst, prefix...), primary...)
	}

	totalSize := len(prefix) + len(primary)
	for _, ext := range extends {
		if len(ext.Blobs) == 0 {
			continue
		}
		totalSize += max(1, (bits.Len64(uint64(ext.FieldID))+6)/7) // fieldID varint overhead
		for _, blob := range ext.Blobs {
			totalSize += len(blob) // Each blob already contains its varint length.
		}
	}
	if len(primary) == 0 || primary[len(primary)-1] != 0 {
		totalSize++
	}
	result := slices.Grow(dst, totalSize)
	result = append(result, prefix...)

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
