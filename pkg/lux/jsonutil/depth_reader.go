package jsonutil

import (
	"encoding/json/jsontext"
	"errors"
	"io"
)

var errIncompleteDepthValue = errors.New("JSON depth visitor did not consume a value")
var errTrailingDepthValue = errors.New("unexpected data after JSON depth value")
var errNilDepthCheck = errors.New("JSON depth check is required")

// DepthReader checks resources with statically generated field dispatch. One
// reader advances through the whole value; nested models are never constructed.
// Callbacks consume exactly one value. Names borrow input or transient scratch.
// The reader and borrowed names are valid only during the callback.
type DepthReader struct {
	reader  *Reader
	raw     []byte
	scratch []byte
}

// CheckJSONValueDepth visits one complete value. Nil is an absent previous
// field, not JSON null. It retains neither input nor a partially built model.
func CheckJSONValueDepth(raw []byte, check func(*DepthReader) error) error {
	if raw == nil {
		return nil
	}
	if check == nil {
		return errNilDepthCheck
	}
	reader := BorrowReader(raw, jsontext.AllowDuplicateNames(true))
	defer reader.Release()
	value := DepthReader{reader: reader, raw: raw}
	if err := check(&value); err != nil {
		return err
	}
	if reader.InputOffset() == 0 {
		return errIncompleteDepthValue
	}
	_, err := reader.ReadToken()
	if err == nil {
		return errTrailingDepthValue
	}
	if err == io.EOF {
		return nil
	}
	return err
}

// Kind reports the next value's kind without consuming it.
func (r *DepthReader) Kind() byte { return byte(r.reader.PeekKind()) }

// JSON consumes one generic JSON value with its own container budget.
func (r *DepthReader) JSON() error {
	value, err := r.reader.ReadValue()
	if err != nil {
		return err
	}
	return CheckJSONDepth(value)
}

// Object visits every member, including earlier duplicate names. Mismatched
// shapes use a generic JSON budget rather than raising business type errors.
func (r *DepthReader) Object(visit func([]byte) error) error {
	start, err := r.reader.ReadToken()
	if err != nil {
		return err
	}
	if start.Kind() != '{' {
		return r.checkOpened(start.Kind())
	}
	if visit == nil {
		return errNilDepthCheck
	}
	for r.reader.PeekKind() != '}' {
		name, err := readObjectFieldName(r.reader, r.raw, &r.scratch)
		if err != nil {
			return err
		}
		if err := visit(name); err != nil {
			return err
		}
	}
	_, err = r.reader.ReadToken()
	return err
}

// List does not charge its container to an element's independent budget. Its
// callback checks static model edges or JSON, without intermediate list storage.
func (r *DepthReader) List(limit int, check func(*DepthReader) error) error {
	start, err := r.reader.ReadToken()
	if err != nil {
		return err
	}
	if start.Kind() != '[' {
		return r.checkOpened(start.Kind())
	}
	if check == nil {
		return errNilDepthCheck
	}
	count := 0
	for r.reader.PeekKind() != ']' {
		if count >= limit {
			return errArrayLimit
		}
		count++
		if err := check(r); err != nil {
			return err
		}
	}
	_, err = r.reader.ReadToken()
	return err
}

func (r *DepthReader) checkOpened(kind jsontext.Kind) error {
	if kind != '{' && kind != '[' {
		return nil
	}
	depth := 1
	for depth > 0 {
		token, err := r.reader.ReadToken()
		if err != nil {
			return err
		}
		switch token.Kind() {
		case '{', '[':
			depth++
			if depth > MaxJSONDepth {
				return errJSONDepth
			}
		case '}', ']':
			depth--
		}
	}
	return nil
}
