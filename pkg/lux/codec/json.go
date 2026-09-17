package codec

import (
	"errors"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// ErrInvalidJSON rejects malformed JSON text, including invalid Unicode.
var ErrInvalidJSON = errors.New("codec: JSON must contain one complete valid Unicode JSON value")

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
