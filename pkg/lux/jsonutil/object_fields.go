package jsonutil

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
)

var errNilObjectVisitor = errors.New("JSON object visitor is required")

// VisitObject validates one complete object and visits fields in input order.
// Names are unescaped and valid only during visit. Values borrow raw, never the
// pooled reader; raw must stay immutable while the caller uses those slices.
// Duplicate names are visited again, allowing generated fixed slots to retain
// the last value before performing type checks. All values, even overwritten
// ones, must pass strict JSON/Unicode validation. A failure may follow earlier
// visits: callers must discard collected slots on error, not publish partial data.
func VisitObject(raw []byte, visit func(name, value []byte) error) error {
	return visitObject(raw, visit, true)
}

// VisitUniqueObject rejects duplicate names, including in nested objects. This
// is intended for security metadata where last-wins fields are ambiguous.
func VisitUniqueObject(raw []byte, visit func(name, value []byte) error) error {
	return visitObject(raw, visit, false)
}

func visitObject(raw []byte, visit func(name, value []byte) error, duplicates bool) error {
	if visit == nil {
		return errNilObjectVisitor
	}
	reader := BorrowReader(raw, jsontext.AllowDuplicateNames(duplicates))
	defer reader.Release()
	start, err := reader.ReadToken()
	if err != nil {
		return err
	}
	if start.Kind() != '{' {
		return errExpectedObject
	}
	var scratch []byte
	for reader.PeekKind() != '}' {
		name, err := readObjectFieldName(reader, raw, &scratch)
		if err != nil {
			return err
		}
		value, err := reader.ReadValue()
		if err != nil {
			return err
		}
		end := int(reader.InputOffset())
		if err := visit(name, raw[end-len(value):end]); err != nil {
			return err
		}
	}
	if !bytes.Equal(bytes.Trim(raw[reader.InputOffset():], " \t\r\n"), []byte("}")) {
		return errTrailingObject
	}
	return nil
}

// ReadValue guarantees a valid object key. Borrow from the original document
// because reading its value may invalidate the decoder's returned key buffer.
func readObjectFieldName(reader *Reader, raw []byte, scratch *[]byte) ([]byte, error) {
	value, err := reader.ReadValue()
	if err != nil {
		return nil, err
	}
	end := int(reader.InputOffset())
	name := raw[end-len(value) : end]
	if bytes.IndexByte(name, '\\') < 0 {
		return name[1 : len(name)-1], nil
	}
	*scratch, err = jsontext.AppendUnquote((*scratch)[:0], name)
	return *scratch, err
}
