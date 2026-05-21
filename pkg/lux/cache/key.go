package cache

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

// BuildKey creates a deterministic cache key from an API name and parameters.
// Format: luxo:api:{name}:{paramHash}
// Params are sorted by key for determinism; values are FNV-hashed to keep keys short.
func BuildKey(apiName string, params map[string]any) string {
	if len(params) == 0 {
		return "luxo:api:" + apiName + ":"
	}
	h := fnv.New64a()
	keys := sortedKeys(params)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		writeValue(h, params[k])
		h.Write([]byte(";"))
	}
	return "luxo:api:" + apiName + ":" + strconv.FormatUint(h.Sum64(), 36)
}

// BuildKeyRaw creates a cache key from raw byte params (for binary protocol).
// Uses FNV hash of the raw bytes for determinism.
func BuildKeyRaw(apiName string, raw []byte) string {
	if len(raw) == 0 {
		return "luxo:api:" + apiName + ":"
	}
	h := fnv.New64a()
	h.Write(raw)
	return "luxo:api:" + apiName + ":" + strconv.FormatUint(h.Sum64(), 36)
}

// InvalidatePrefix returns the prefix to use when invalidating caches for a model.
// All API caches related to this model should use keys that start with this prefix.
func InvalidatePrefix(modelName string) string {
	return "luxo:api:" + strings.ToLower(modelName)
}

// sortedKeys returns map keys in sorted order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeValue writes a value's string representation to the hasher.
func writeValue(h hash.Hash64, v any) {
	switch val := v.(type) {
	case string:
		h.Write([]byte(val))
	case int64:
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(val))
		h.Write(buf[:])
	case float64:
		s := strconv.FormatFloat(val, 'f', -1, 64)
		h.Write([]byte(s))
	case bool:
		if val {
			h.Write([]byte("T"))
		} else {
			h.Write([]byte("F"))
		}
	case []byte:
		h.Write(val)
	case nil:
		h.Write([]byte("nil"))
	default:
		// Fallback: unknown type marker
		h.Write([]byte("?"))
	}
}
