package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strings"
)

var errExpectedObject = errors.New("expected JSON object")
var errTrailingObject = errors.New("unexpected data after JSON object")

const ownedTextBlockSize = 256

// TextBuffer stores independently owned immutable strings in bounded blocks.
// Its zero value is ready to use; do not copy it after the first write.
type TextBuffer struct {
	text            strings.Builder
	initialCapacity int
}

// NewTextBuffer caps the initial size hint; later blocks remain bounded too.
func NewTextBuffer(sizeHint int) TextBuffer {
	return TextBuffer{initialCapacity: min(max(sizeHint, 16), ownedTextBlockSize)}
}

// ReadString consumes one strict JSON string without retaining reader storage.
func (b *TextBuffer) ReadString(reader *Reader) (string, error) {
	return readOwnedString(reader, &b.text, max(b.initialCapacity, 16))
}

// Copy retains no reference to value and never modifies earlier results.
func (b *TextBuffer) Copy(value []byte) string {
	return storeOwnedText(&b.text, value, max(b.initialCapacity, 16))
}

// ParseObject reads one complete object without reflection or numeric coercion.
// Keys are unescaped; values retain their original JSON text. Both are owned by
// the result. Duplicate names keep the last value, but every value is validated.
func ParseObject(raw []byte) (map[string]json.RawMessage, error) {
	reader := BorrowReader(raw, jsontext.AllowDuplicateNames(true))
	defer reader.Release()
	start, err := reader.ReadToken()
	if err != nil {
		return nil, err
	}
	if start.Kind() != '{' {
		return nil, errExpectedObject
	}
	fields := make(map[string]json.RawMessage)
	var names strings.Builder
	for reader.PeekKind() != '}' {
		key, err := readOwnedString(reader, &names, 16)
		if err != nil {
			return nil, err
		}
		value, err := reader.ReadValue()
		if err != nil {
			return nil, err
		}
		fields[key] = bytes.Clone(value)
	}
	// PeekKind already validated the closing delimiter. Nothing except that
	// delimiter and JSON whitespace may remain in the immutable input.
	if !bytes.Equal(bytes.Trim(raw[reader.InputOffset():], " \t\r\n"), []byte("}")) {
		return nil, errTrailingObject
	}
	return fields, nil
}

// Pack decoded names into owned immutable prefixes, without retaining the
// request body or allocating a separate string for each small field name.
func readOwnedString(reader *Reader, names *strings.Builder, initialCapacity int) (string, error) {
	raw, err := reader.ReadValue()
	if err != nil {
		return "", err
	}
	name := []byte(raw)
	if raw.Kind() == '"' && bytes.IndexByte(raw, '\\') < 0 {
		// ReadValue validated the complete string; no unescaping is needed.
		name = raw[1 : len(raw)-1]
	} else {
		var scratch [128]byte
		name, err = jsontext.AppendUnquote(scratch[:0], raw)
		if err != nil {
			return "", err
		}
	}
	return storeOwnedText(names, name, initialCapacity), nil
}

func storeOwnedText(names *strings.Builder, name []byte, initialCapacity int) string {
	if len(name) > ownedTextBlockSize {
		return string(name)
	}
	// Reuse an equal immutable block without a lookup table or retaining
	// input. The length check avoids scanning longer blocks for every value.
	if names.Len() == len(name) && names.String() == string(name) {
		return names.String()
	}
	// Duplicate keys must not pin an unbounded sequence of discarded names.
	// Reset never mutates prefixes already owned by keys in the result map.
	if names.Len()+len(name) > ownedTextBlockSize {
		names.Reset()
	}
	start := names.Len()
	if names.Cap() == 0 && len(name) != 0 {
		names.Grow(max(initialCapacity, len(name)))
	}
	names.Write(name)
	return names.String()[start:]
}
