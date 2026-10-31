package jsonutil

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"sync"
)

// Reader borrows input until Release. Tokens and values follow jsontext's
// lifetimes; copy any value that must survive the next read or Release.
// Only the fixed-size reader shell is reused, never payloads or parser caches.
type Reader struct {
	jsontext.Decoder
	input bytes.Buffer
}

var readers = sync.Pool{New: func() any { return new(Reader) }}

var errExpectedArray = errors.New("expected JSON array")
var errTrailingArray = errors.New("unexpected data after JSON array")

// ParseArray reads a complete non-null array without an intermediate value tree.
// parse must own any returned data before the reader advances to the next value.
// Failure never returns a partially decoded list; empty arrays remain non-nil.
func ParseArray[T any](raw []byte, parse func([]byte) (T, error)) ([]T, error) {
	return parseArrayReader(raw, func(reader *Reader) (T, error) {
		value, err := reader.ReadValue()
		if err != nil {
			var zero T
			return zero, err
		}
		return parse(value)
	})
}

// ParseRawArray preserves exact tokens without copying their contents. Results
// borrow raw, not the pooled reader, and remain valid until the caller changes
// raw. An excess item is rejected before allocating storage for that item.
func ParseRawArray(raw []byte, limit int) ([]json.RawMessage, error) {
	count := 0
	return parseArrayReader(raw, func(reader *Reader) (json.RawMessage, error) {
		if count >= limit {
			return nil, errArrayLimit
		}
		count++
		value, err := reader.ReadValue()
		if err != nil {
			return nil, err
		}
		end := int(reader.InputOffset())
		return raw[end-len(value) : end], nil
	})
}

func parseArrayReader[T any](raw []byte, read func(*Reader) (T, error)) ([]T, error) {
	reader := BorrowReader(raw, jsontext.AllowDuplicateNames(true))
	defer reader.Release()
	start, err := reader.ReadToken()
	if err != nil {
		return nil, err
	}
	if start.Kind() != '[' {
		return nil, errExpectedArray
	}
	values := make([]T, 0)
	for reader.PeekKind() != ']' {
		item, err := read(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, item)
	}
	if !bytes.Equal(bytes.Trim(raw[reader.InputOffset():], " \t\r\n"), []byte("]")) {
		return nil, errTrailingArray
	}
	return values, nil
}

// BorrowReader creates an exclusive reader with the supplied options. Without
// options, jsontext's strict Unicode and unique-name policy applies.
func BorrowReader(raw []byte, opts ...jsontext.Options) *Reader {
	reader := readers.Get().(*Reader)
	reader.input = *bytes.NewBuffer(raw)
	reader.Reset(&reader.input, opts...)
	return reader
}

func (r *Reader) clear() {
	// Reset alone retains internal name/namespace storage. Zero the entire
	// state so neither secrets nor arbitrarily large inputs stay in the pool.
	*r = Reader{}
}

// Release clears all input and parsing state. The reader must not be used again.
func (r *Reader) Release() {
	r.clear()
	readers.Put(r)
}
