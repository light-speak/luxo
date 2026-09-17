package errors

import (
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	stderrors "errors"
	"strconv"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/datetime"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// DataWriter appends statically typed error fields without reflection. Generated
// code owns the object syntax; only validated JSON may use JSON instead of Token.
// After an error Result returns the original prefix, never a partial value.
type DataWriter struct {
	dst          []byte
	start, depth int
	err          error
}

func NewDataWriter(dst []byte) DataWriter { return DataWriter{dst: dst, start: len(dst)} }
func (w *DataWriter) Err() error          { return w.err }
func (w *DataWriter) Reject(message string) {
	if w.err == nil {
		w.err = stderrors.New(message)
	}
}
func (w *DataWriter) Result() ([]byte, error) {
	if w.err != nil {
		return w.dst[:w.start], w.err
	}
	return w.dst, nil
}

// Token is only for trusted, generated JSON punctuation and field names.
func (w *DataWriter) Token(value string) { w.dst = append(w.dst, value...) }
func (w *DataWriter) Int(value int64)    { w.dst = strconv.AppendInt(w.dst, value, 10) }
func (w *DataWriter) Bool(value bool)    { w.dst = strconv.AppendBool(w.dst, value) }
func (w *DataWriter) String(value string) {
	if w.err == nil {
		w.dst, w.err = jsontext.AppendQuote(w.dst, value)
	}
}
func (w *DataWriter) Float(value float64) {
	if w.err != nil {
		return
	}
	if !codec.IsFiniteFloat(value) {
		w.err = stderrors.New("error data: Float must be finite")
		return
	}
	w.dst = strconv.AppendFloat(w.dst, value, 'g', -1, 64)
}
func (w *DataWriter) JSON(value json.RawMessage) {
	if w.err != nil {
		return
	}
	if value == nil {
		w.Token("null")
		return
	}
	if !jsonutil.ValidUTF8JSON(value) {
		w.err = stderrors.New("error data: invalid JSON")
		return
	}
	w.dst = append(w.dst, value...)
}
func (w *DataWriter) Bytes(value []byte) {
	if value == nil {
		w.Token("null")
		return
	}
	w.dst = append(w.dst, '"')
	w.dst = base64.StdEncoding.AppendEncode(w.dst, value)
	w.dst = append(w.dst, '"')
}
func (w *DataWriter) Time(value time.Time) {
	if w.err != nil {
		return
	}
	if !datetime.ValidSeconds(value.Unix()) {
		w.err = stderrors.New("error data: DateTime is outside RFC3339 range")
		return
	}
	w.dst = append(w.dst, '"')
	w.dst = value.UTC().AppendFormat(w.dst, time.RFC3339Nano)
	w.dst = append(w.dst, '"')
}

// Enter bounds recursive object traversal, including cycles in native values.
func (w *DataWriter) Enter() bool {
	if w.err != nil {
		return false
	}
	if w.depth == 64 {
		w.err = stderrors.New("error data: object nesting exceeds 64 levels")
		return false
	}
	w.depth++
	return true
}
func (w *DataWriter) Leave() { w.depth-- }
