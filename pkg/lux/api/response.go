package api

import (
	"encoding/json"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// FilterFields serializes data and filters by $select in a single pass.
// If fields is nil, returns the full serialization (no filtering).
func FilterFields(data any, fields []*selection.Field) ([]byte, error) {
	raw, err := sonic.Marshal(data)
	if err != nil {
		return nil, err
	}
	if fields == nil {
		return raw, nil
	}
	return filterRaw(raw, fields)
}

// filterRaw applies field selection to raw JSON bytes.
func filterRaw(raw []byte, fields []*selection.Field) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	switch raw[0] {
	case '[':
		return filterArray(raw, fields)
	case '{':
		return filterObject(raw, fields)
	default:
		return raw, nil
	}
}

// filterObject keeps only the selected fields from a JSON object.
// Uses sonic for fast unmarshal into raw map, then selectively re-serializes.
func filterObject(raw []byte, fields []*selection.Field) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := sonic.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}

	result := make(map[string]json.RawMessage, len(fields))
	for _, f := range fields {
		val, ok := obj[f.Name]
		if !ok {
			continue
		}
		if f.Children != nil {
			filtered, err := filterRaw(val, f.Children)
			if err != nil {
				return nil, err
			}
			result[f.Name] = filtered
		} else {
			result[f.Name] = val
		}
	}

	return sonic.Marshal(result)
}

// filterArray applies field selection to each element of a JSON array.
func filterArray(raw []byte, fields []*selection.Field) ([]byte, error) {
	var arr []json.RawMessage
	if err := sonic.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}

	result := make([]json.RawMessage, 0, len(arr))
	for _, item := range arr {
		filtered, err := filterRaw(item, fields)
		if err != nil {
			return nil, err
		}
		result = append(result, filtered)
	}

	return sonic.Marshal(result)
}
