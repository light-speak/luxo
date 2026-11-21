package rpc

// Frozen complete data handling before reader reuse. The unchanged TLV helpers
// are shared; no validation, copying, or text semantics are removed here.

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"unicode/utf8"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// rpcErrorDataBeforeReaderReuse owns validated JSON. Numbers are never decoded into host values.
// text contains only overrides of raw field values (String versus JSON String,
// for example); null means AppendText did not expose that field.
type rpcErrorDataBeforeReaderReuse struct{ raw, text []byte }

func ownRPCErrorDataBeforeReaderReuse(raw, text []byte) *rpcErrorDataBeforeReaderReuse {
	owned := make([]byte, len(raw)+len(text))
	copy(owned, raw)
	copy(owned[len(raw):], text)
	return &rpcErrorDataBeforeReaderReuse{raw: owned[:len(raw):len(raw)], text: owned[len(raw):]}
}

func (d *rpcErrorDataBeforeReaderReuse) AppendJSON(dst []byte) ([]byte, error) {
	return append(dst, d.raw...), nil
}

func (d *rpcErrorDataBeforeReaderReuse) AppendText(dst []byte, field string) ([]byte, bool) {
	if value := rpcJSONFieldBeforeReaderReuse(d.text, field); value != nil {
		if bytes.Equal(value, []byte("null")) {
			return dst, false
		}
		result, err := jsontext.AppendUnquote(dst, value)
		return result, err == nil
	}
	value := rpcJSONFieldBeforeReaderReuse(d.raw, field)
	return append(dst, value...), value != nil
}

// Iterate raw tokens only. Offsets retain the last value without keeping the
// decoder's temporary buffer or allocating a map; nested values stay opaque.
func rpcJSONFieldBeforeReaderReuse(raw []byte, field string) []byte {
	if jsontext.Value(raw).Kind() != '{' {
		return nil
	}
	var dec jsontext.Decoder
	dec.Reset(bytes.NewBuffer(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	_, _ = dec.ReadToken() // The envelope already validated this complete value.
	var found []byte
	for dec.PeekKind() != '}' {
		name, err := dec.ReadValue()
		if err != nil {
			return nil
		}
		matches := rpcJSONNameMatchesBeforeReaderReuse(name, field)
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
func rpcJSONNameMatchesBeforeReaderReuse(name []byte, field string) bool {
	if bytes.IndexByte(name, '\\') < 0 {
		return string(name[1:len(name)-1]) == field
	}
	var scratch [128]byte
	value, err := jsontext.AppendUnquote(scratch[:0], name)
	return err == nil && string(value) == field
}

func validateRPCErrorDataBeforeReaderReuse(raw, text []byte) error {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return fmt.Errorf("rpc: invalid error data JSON")
	}
	if text == nil {
		return nil
	}
	return validateRPCErrorTextBeforeReaderReuse(text)
}

func validateRPCErrorTextBeforeReaderReuse(text []byte) error {
	var dec jsontext.Decoder
	dec.Reset(bytes.NewBuffer(text))
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

func encodeRPCErrorDataBeforeReaderReuse(data luxerrors.ErrorData) (raw, text []byte, err error) {
	if remote, ok := data.(*rpcErrorDataBeforeReaderReuse); ok {
		return remote.raw, remote.text, nil
	}
	raw, err = data.AppendJSON(nil)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("rpc: empty error data JSON")
	}
	if err = validateRPCErrorDataBeforeReaderReuse(raw, nil); err != nil {
		return nil, nil, err
	}
	if jsontext.Value(raw).Kind() != '{' {
		return raw, nil, nil
	}
	text, err = rpcErrorTextOverridesBeforeReaderReuse(data, raw)
	if err == nil && len(text) > 0 {
		err = validateRPCErrorTextBeforeReaderReuse(text)
	}
	return raw, text, err
}

func rpcErrorTextOverridesBeforeReaderReuse(data luxerrors.ErrorData, raw []byte) ([]byte, error) {
	var dec jsontext.Decoder
	dec.Reset(bytes.NewBuffer(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
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

func encodeApplicationErrorBeforeReaderReuse(value *luxerrors.AppError) ([]byte, error) {
	message := value.Message
	if value.Internal {
		message = "error.internal"
	}
	var raw, text []byte
	if value.Data != nil && !value.Internal && value.Code < 500 {
		var err error
		raw, text, err = encodeRPCErrorDataBeforeReaderReuse(value.Data)
		if err != nil {
			return nil, err
		}
	}
	return encodeRPCError(value.Code, value.Name, message, raw, text, value.Internal)
}

func decodeErrorBeforeReaderReuse(data []byte) error {
	fields, err := readRPCErrorFields(data)
	if err != nil {
		return err
	}
	// Only 32-bit hosts can narrow the signed wire value to a host int.
	if int64(int(fields.code)) != fields.code {
		return fmt.Errorf("rpc: error code exceeds host int range")
	}
	if !utf8.Valid(fields.name) || !utf8.Valid(fields.message) {
		return fmt.Errorf("rpc: error envelope contains invalid UTF-8")
	}
	if fields.data != nil || fields.text != nil {
		if err := validateRPCErrorDataBeforeReaderReuse(fields.data, fields.text); err != nil {
			return err
		}
	}
	// Copy only validated text; each string owns its storage independently.
	value := &luxerrors.AppError{Code: int(fields.code), Name: string(fields.name), Message: string(fields.message)}
	if fields.code >= 500 || fields.internal {
		value.Cause = fmt.Errorf("rpc error %d %s: %s", fields.code, value.Name, value.Message)
		value.Message = "error.internal"
		value.Internal = true
	} else if fields.data != nil {
		value.Data = ownRPCErrorDataBeforeReaderReuse(fields.data, fields.text)
	}
	return value
}
