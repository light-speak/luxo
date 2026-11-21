package rpc

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// rpcErrorData owns validated JSON. Numbers are never decoded into host values.
// text contains only overrides of raw field values (String versus JSON String,
// for example); null means AppendText did not expose that field.
type rpcErrorData struct{ raw, text []byte }

func ownRPCErrorData(raw, text []byte) *rpcErrorData {
	owned := make([]byte, len(raw)+len(text))
	copy(owned, raw)
	copy(owned[len(raw):], text)
	return &rpcErrorData{raw: owned[:len(raw):len(raw)], text: owned[len(raw):]}
}

func (d *rpcErrorData) AppendJSON(dst []byte) ([]byte, error) {
	return append(dst, d.raw...), nil
}

func (d *rpcErrorData) AppendText(dst []byte, field string) ([]byte, bool) {
	if value := rpcJSONField(d.text, field); value != nil {
		if bytes.Equal(value, []byte("null")) {
			return dst, false
		}
		result, err := jsontext.AppendUnquote(dst, value)
		return result, err == nil
	}
	value := rpcJSONField(d.raw, field)
	return append(dst, value...), value != nil
}

// Iterate raw tokens only. Offsets retain the last value without keeping the
// decoder's temporary buffer or allocating a map; nested values stay opaque.
func rpcJSONField(raw []byte, field string) []byte {
	if jsontext.Value(raw).Kind() != '{' {
		return nil
	}
	dec := jsonutil.BorrowReader(raw, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	defer dec.Release()
	_, _ = dec.ReadToken() // The envelope already validated this complete value.
	var found []byte
	for dec.PeekKind() != '}' {
		name, err := dec.ReadValue()
		if err != nil {
			return nil
		}
		matches := rpcJSONNameMatches(name, field)
		value, err := dec.ReadValue()
		if err != nil {
			return nil
		}
		if matches {
			end := int(dec.InputOffset())
			found = raw[end-len(value) : end]
		}
	}
	return found
}

// Most generated names need neither unquoting nor an owned string. Escaped
// native names still use the standard parser, with a small stack buffer.
func rpcJSONNameMatches(name []byte, field string) bool {
	if bytes.IndexByte(name, '\\') < 0 {
		return string(name[1:len(name)-1]) == field
	}
	var scratch [128]byte
	value, err := jsontext.AppendUnquote(scratch[:0], name)
	return err == nil && string(value) == field
}

func validateRPCErrorData(raw, text []byte) error {
	if !jsonutil.ValidUTF8JSON(raw) {
		return fmt.Errorf("rpc: invalid error data JSON")
	}
	if text == nil {
		return nil
	}
	return validateRPCErrorText(text)
}

func validateRPCErrorText(text []byte) error {
	dec := jsonutil.BorrowReader(text)
	defer dec.Release()
	start, err := dec.ReadToken()
	if err != nil || start.Kind() != '{' {
		return fmt.Errorf("rpc: invalid error text overrides")
	}
	for dec.PeekKind() != '}' {
		if _, err := dec.ReadToken(); err != nil {
			return fmt.Errorf("rpc: invalid error text name: %w", err)
		}
		value, err := dec.ReadValue()
		if err != nil {
			return fmt.Errorf("rpc: invalid error text value: %w", err)
		}
		if kind := value.Kind(); kind != '"' && kind != 'n' {
			return fmt.Errorf("rpc: error text override must be a string or null")
		}
	}
	_, _ = dec.ReadToken()
	if _, err := dec.ReadToken(); err != io.EOF {
		return fmt.Errorf("rpc: trailing error text bytes")
	}
	return nil
}

func encodeRPCErrorData(data luxerrors.ErrorData) (raw, text []byte, err error) {
	if remote, ok := data.(*rpcErrorData); ok {
		return remote.raw, remote.text, nil
	}
	raw, err = data.AppendJSON(nil)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("rpc: empty error data JSON")
	}
	if err = validateRPCErrorData(raw, nil); err != nil {
		return nil, nil, err
	}
	if jsontext.Value(raw).Kind() != '{' {
		return raw, nil, nil
	}
	text, err = rpcErrorTextOverrides(data, raw)
	if err == nil && len(text) > 0 {
		err = validateRPCErrorText(text)
	}
	return raw, text, err
}

func rpcErrorTextOverrides(data luxerrors.ErrorData, raw []byte) ([]byte, error) {
	dec := jsonutil.BorrowReader(raw, jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	defer dec.Release()
	_, _ = dec.ReadToken()
	w := luxerrors.NewDataWriter(nil)
	var scratch []byte
	started := false
	for dec.PeekKind() != '}' {
		name, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		field := name.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		var found bool
		scratch, found = data.AppendText(scratch[:0], field)
		if found && bytes.Equal(scratch, value) {
			continue
		}
		if !started {
			w.Token("{")
			started = true
		} else {
			w.Token(",")
		}
		w.String(field)
		w.Token(":")
		if found {
			w.String(string(scratch))
		} else {
			w.Token("null")
		}
	}
	if started {
		w.Token("}")
	}
	return w.Result()
}
