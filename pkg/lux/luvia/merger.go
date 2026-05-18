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
// Each extend response is also a WriteLuxo output from batchLoad, wrapped
// with its parent field ID so the client can decode it correctly.
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
	Data    []byte // encoded value (list: [count][items...], single: [arenaHeader][fields][0x00])
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
	switch st {
	case codec.SkipVarint:
		dec.SkipVarint()
	case codec.SkipFixed64:
		dec.SkipFixed64()
	case codec.SkipBytes:
		dec.SkipLenPrefixed()
	case codec.SkipNullVarint:
		dec.SkipNullableVarint()
	case codec.SkipNullFixed64:
		dec.SkipNullableFixed64()
	case codec.SkipNullBytes:
		dec.SkipNullableLenPrefixed()
	}
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

// skipColumnarColumn advances past all values in a column without allocating.
func skipColumnarColumn(r *codec.ColumnarReader, f *schema.Field) {
	switch f.Type {
	case schema.FieldInt, schema.FieldDateTime, schema.FieldDuration:
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
	case schema.FieldString, schema.FieldEnum:
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
	case schema.FieldModel:
		r.SkipColumnBytes()
	default:
		r.SkipColumnBytes()
	}
}

// MergeColumnar appends extend field columns to a columnar-encoded response.
// Each ExtendColumnResult represents one extend field with per-record blob data.
//
// Format of extend column: [fieldID varint][blob0][blob1]...[blobN]
// Each blob is length-prefixed: [varint len][binary data]
// The binary data is the WriteLuxo output for that extend field value
// (for lists: [count varint][model1][model2]..., for single: full WriteLuxo).
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
			result = codec.AppendBytes(result, blob)
		}
	}

	// New end marker
	result = append(result, 0x00)
	return result
}

// ExtendColumnResult holds per-record blob data for one extend field.
type ExtendColumnResult struct {
	FieldID int      // field ID in the parent model
	Blobs   [][]byte // one blob per record (index = record index)
}

// ParseGroupedResponse parses a svc:resolve grouped response into per-record blobs.
//
// Response format (from svc:resolve handler):
//
//	[key_count varint]
//	For each key (in request order):
//	  [item_count varint]
//	  [item1_len varint][item1 bytes]   ← length-prefixed
//	  [item2_len varint][item2 bytes]
//	  ...
//
// For list extend fields: returns per-key blob = [count svarint][item1 bytes][item2 bytes]...
// For single extend fields: returns per-key blob = the single item bytes (or nil).
func ParseGroupedResponse(resp []byte, isList bool) [][]byte {
	if len(resp) == 0 {
		return nil
	}
	keyCount, n := codec.ReadVarint(resp, 0)
	if n <= 0 {
		return nil
	}
	off := n

	blobs := make([][]byte, keyCount)
	for i := 0; i < int(keyCount); i++ {
		itemCount, cn := codec.ReadVarint(resp, off)
		if cn <= 0 {
			return blobs
		}
		off += cn

		if isList {
			// Collect all items for this key into a list blob:
			// [count svarint][item1 raw bytes][item2 raw bytes]...
			var listBuf []byte
			listBuf = codec.AppendSvarint(listBuf, int64(itemCount))
			for j := 0; j < int(itemCount); j++ {
				item, rn := codec.ReadBytes(resp, off)
				if rn == 0 {
					return blobs
				}
				off += rn
				listBuf = append(listBuf, item...)
			}
			blobs[i] = listBuf
		} else {
			if itemCount == 0 {
				blobs[i] = nil
			} else {
				item, rn := codec.ReadBytes(resp, off)
				if rn == 0 {
					return blobs
				}
				off += rn
				blobs[i] = item
				// Skip remaining items (shouldn't exist for single field)
				for j := 1; j < int(itemCount); j++ {
					_, rn := codec.ReadBytes(resp, off)
					if rn == 0 {
						return blobs
					}
					off += rn
				}
			}
		}
	}
	return blobs
}
