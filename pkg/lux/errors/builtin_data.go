package errors

import (
	"encoding/hex"
	"strconv"
)

// ResourceID preserves the wire type without a dynamically typed ID field.
// The zero value means no ID; use a constructor to represent zero or empty IDs.
type ResourceID struct {
	kind    uint8
	integer int64
	text    string
}

func IntID(value int64) ResourceID     { return ResourceID{kind: 1, integer: value} }
func StringID(value string) ResourceID { return ResourceID{kind: 2, text: value} }
func UUIDID(value [16]byte) ResourceID {
	var text [36]byte
	hex.Encode(text[:8], value[:4])
	hex.Encode(text[9:13], value[4:6])
	hex.Encode(text[14:18], value[6:8])
	hex.Encode(text[19:23], value[8:10])
	hex.Encode(text[24:], value[10:])
	text[8], text[13], text[18], text[23] = '-', '-', '-', '-'
	return StringID(string(text[:]))
}

func (p ParamError) AppendJSON(dst []byte) ([]byte, error) {
	w := NewDataWriter(dst)
	w.Token(`{"param":`)
	w.String(p.Param)
	w.Token(`,"error":`)
	w.String(p.Error)
	w.Token("}")
	return w.Result()
}
func (p ParamError) AppendText(dst []byte, field string) ([]byte, bool) {
	switch field {
	case "param":
		return append(dst, p.Param...), true
	case "error":
		return append(dst, p.Error...), true
	default:
		return dst, false
	}
}
func (r ResourceError) AppendJSON(dst []byte) ([]byte, error) {
	w := NewDataWriter(dst)
	w.Token(`{"resource":`)
	w.String(r.Resource)
	switch r.ID.kind {
	case 1:
		w.Token(`,"id":`)
		w.Int(r.ID.integer)
	case 2:
		w.Token(`,"id":`)
		w.String(r.ID.text)
	}
	w.Token("}")
	return w.Result()
}
func (r ResourceError) AppendText(dst []byte, field string) ([]byte, bool) {
	if field == "resource" {
		return append(dst, r.Resource...), true
	}
	if field == "id" {
		switch r.ID.kind {
		case 1:
			return strconv.AppendInt(dst, r.ID.integer, 10), true
		case 2:
			return append(dst, r.ID.text...), true
		}
	}
	return dst, false
}
