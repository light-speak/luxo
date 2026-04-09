package api

import (
	"encoding/json"

	"github.com/light-speak/luxo/pkg/lux/selection"
)

// FilterFields filters a JSON-serializable value by the given field selection.
// If fields is nil, returns the full value (no filtering).
// Marshals data to JSON once, then filters — one marshal + one unmarshal.
func FilterFields(data any, fields []*selection.Field) (json.RawMessage, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if fields == nil {
		return raw, nil
	}
	return filterRaw(raw, fields)
}

// filterRaw applies field selection to a raw JSON value.
func filterRaw(raw json.RawMessage, fields []*selection.Field) (json.RawMessage, error) {
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
func filterObject(raw json.RawMessage, fields []*selection.Field) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
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

	return json.Marshal(result)
}

// filterArray applies field selection to each element of a JSON array.
func filterArray(raw json.RawMessage, fields []*selection.Field) (json.RawMessage, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
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

	return json.Marshal(result)
}
