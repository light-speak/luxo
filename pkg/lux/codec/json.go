package codec

import (
	"bytes"
	"errors"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// ErrInvalidJSON rejects malformed JSON text, including invalid Unicode.
var ErrInvalidJSON = errors.New("codec: JSON must contain one complete valid Unicode JSON value")

// IsJSONNull recognizes only the JSON null token and JSON whitespace.
// Empty input and non-JSON Unicode whitespace are not null.
func IsJSONNull(raw []byte) bool {
	if len(raw) == 4 {
		return string(raw) == "null"
	}
	if len(raw) < 4 || raw[0] > ' ' && raw[0] != 'n' {
		return false
	}
	return bytes.Equal(bytes.Trim(raw, " \t\r\n"), []byte("null"))
}

// AppendJSON appends a length-prefixed JSON value without decoding its numbers.
// Invalid input leaves dst unchanged. JSON null is distinct from empty input.
func AppendJSON(dst, raw []byte) ([]byte, error) {
	if !jsonutil.ValidUTF8JSON(raw) {
		return dst, ErrInvalidJSON
	}
	return AppendBytes(dst, raw), nil
}

// WriteJSON appends a JSON value without a field header.
func (e *Encoder) WriteJSON(raw []byte) error {
	var err error
	e.buf, err = AppendJSON(e.buf, raw)
	return err
}

// ReadJSON reads a validated JSON value borrowing the decoder's input storage.
// It does not normalize whitespace, keys, escapes or numeric tokens.
func (d *Decoder) ReadJSON() []byte {
	if d.err != nil {
		return nil
	}
	raw := d.ReadBytes()
	if d.err != nil {
		return nil
	}
	if !jsonutil.ValidUTF8JSON(raw) {
		d.err = ErrInvalidJSON
		return nil
	}
	return raw
}

// ReadJSONPtr distinguishes an absent nullable value from a present JSON null.
func (d *Decoder) ReadJSONPtr() *[]byte {
	if d.err != nil || !d.ReadBool() {
		return nil
	}
	raw := d.ReadJSON()
	if d.err != nil {
		return nil
	}
	return &raw
}

// ReadNullableJSON reads a public JSON? value, normalizing the former
// present-null representation only after validating its entire payload.
// ReadJSONPtr remains available for internal snapshots that preserve both states.
func (d *Decoder) ReadNullableJSON() []byte {
	if d.err != nil || !d.ReadBool() {
		return nil
	}
	raw := d.ReadJSON()
	// Syntax is already validated. Only null starts with 'n', so inspect the
	// first token without rescanning trailing whitespace or the JSON payload.
	for _, char := range raw {
		if char > ' ' {
			if char == 'n' {
				return nil
			}
			break
		}
	}
	return raw
}
