package api

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// Filter represents a single filter condition from $filters.
type Filter struct {
	Field    string `json:"field"`
	Operator string `json:"op"`
	Value    string `json:"value"`
}

// UnmarshalJSON accepts JSON scalar filter values while keeping the internal
// string representation consumed by typed FilterOp implementations.
func (f *Filter) UnmarshalJSON(data []byte) error {
	reader := jsonutil.BorrowReader(data, jsontext.AllowDuplicateNames(true))
	defer reader.Release()
	text := jsonutil.NewTextBuffer(len(data))
	metadata, err := readListMetadata(reader, &text, "op")
	if err != nil {
		return err
	}
	value, err := filterMetadata(metadata)
	if err != nil {
		return err
	}
	if err := metadataEnd(data, reader, ""); err != nil {
		return err
	}
	*f = value
	return nil
}

// Sorter represents a sort directive from $sorters.
type Sorter struct {
	Field string `json:"field"`
	Order string `json:"order"` // "asc" or "desc"
}

func parseListObjects[T any](raw []byte, limit int, action string, parse func(listMetadata) (T, error)) ([]T, error) {
	reader := jsonutil.BorrowReader(raw, jsontext.AllowDuplicateNames(true))
	defer reader.Release()
	start, err := reader.ReadToken()
	if err != nil {
		return nil, err
	}
	if start.Kind() == 'n' {
		return nil, metadataEnd(raw, reader, "") // Preserve null-as-unset metadata.
	}
	if start.Kind() != '[' {
		return nil, fmt.Errorf("expected metadata array")
	}
	text := jsonutil.NewTextBuffer(len(raw))
	items := make([]T, 0)
	for reader.PeekKind() != ']' {
		if len(items) >= limit {
			return nil, fmt.Errorf("metadata array exceeds limit %d", limit)
		}
		metadata, err := readListMetadata(reader, &text, action)
		if err != nil {
			return nil, err
		}
		item, err := parse(metadata)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", len(items), err)
		}
		items = append(items, item)
	}
	if err := metadataEnd(raw, reader, "]"); err != nil {
		return nil, err
	}
	return items, nil
}

func metadataEnd(raw []byte, reader *jsonutil.Reader, suffix string) error {
	if string(bytes.Trim(raw[reader.InputOffset():], " \t\r\n")) != suffix {
		return fmt.Errorf("unexpected data after JSON metadata")
	}
	return nil
}

func filterMetadata(metadata listMetadata) (Filter, error) {
	if !metadata.hasValue {
		return Filter{}, fmt.Errorf("filter value must be a string, number, or boolean")
	}
	return Filter{metadata.field, metadata.action, metadata.value}, nil
}

func sorterMetadata(metadata listMetadata) (Sorter, error) {
	return Sorter{metadata.field, metadata.action}, nil
}

type listMetadata struct {
	field, action, value string
	hasValue             bool
}

func readListMetadata(reader *jsonutil.Reader, text *jsonutil.TextBuffer, action string) (listMetadata, error) {
	start, err := reader.ReadToken()
	if err != nil {
		return listMetadata{}, err
	}
	if start.Kind() != '{' {
		return listMetadata{}, fmt.Errorf("expected metadata object")
	}
	var metadata listMetadata
	for reader.PeekKind() != '}' {
		name, err := reader.ReadToken()
		if err != nil {
			return listMetadata{}, err
		}
		// Classify before advancing: ReadValue invalidates the borrowed token.
		key := metadataMember(name.String(), action)
		if err := metadata.read(key, reader, text); err != nil {
			return listMetadata{}, err
		}
	}
	_, err = reader.ReadToken()
	return metadata, err
}

func metadataMember(name, action string) int {
	switch {
	case strings.EqualFold(name, "field"):
		return 1
	case strings.EqualFold(name, action):
		return 2
	case action == "op" && strings.EqualFold(name, "value"):
		return 3
	default:
		return 0
	}
}

func (m *listMetadata) read(key int, reader *jsonutil.Reader, text *jsonutil.TextBuffer) error {
	if key == 3 {
		return m.readValue(reader, text)
	}
	if key == 0 || reader.PeekKind() == 'n' {
		_, err := reader.ReadValue()
		return err // Unknown members and null string members retain prior behavior.
	}
	value, err := text.ReadString(reader)
	if key == 1 {
		m.field = value
	} else {
		m.action = value
	}
	return err
}

func (m *listMetadata) readValue(reader *jsonutil.Reader, text *jsonutil.TextBuffer) error {
	if reader.PeekKind() == '"' {
		value, err := text.ReadString(reader)
		m.value, m.hasValue = value, err == nil
		return err
	}
	value, err := reader.ReadValue()
	if err != nil {
		return err
	}
	switch value.Kind() {
	case '0', 't', 'f':
		m.value, m.hasValue = text.Copy(value), true
	default:
		m.hasValue = false // Only the final duplicate value determines its type.
	}
	return nil
}
