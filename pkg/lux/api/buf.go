package api

import (
	"encoding/json/jsontext"
	"strconv"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// ResponseBuf is a pooled, zero-allocation response buffer.
// Uses []byte with direct append — no bytes.Buffer overhead.
type ResponseBuf struct {
	B        []byte
	Identity any // authenticated user, set by handler for @visible/@mask checks
}

// bufPool reuses ResponseBuf across requests. Pre-allocates 4KB.
var bufPool = sync.Pool{
	New: func() any {
		return &ResponseBuf{B: make([]byte, 0, 4096)}
	},
}

// GetBuf gets a ResponseBuf from the pool.
func GetBuf() *ResponseBuf {
	buf := bufPool.Get().(*ResponseBuf)
	buf.B = buf.B[:0]
	buf.Identity = nil
	return buf
}

// Reset clears the buffer for reuse, keeping the underlying capacity.
// Used by generated handlers that write multiple items into one pooled buffer.
func (r *ResponseBuf) Reset() {
	r.B = r.B[:0]
	r.Identity = nil
}

// PutBuf returns a ResponseBuf to the pool.
func PutBuf(buf *ResponseBuf) {
	buf.Identity = nil // clear PII before pooling
	if cap(buf.B) > 1<<20 {
		return
	}
	bufPool.Put(buf)
}

func (r *ResponseBuf) AppendByte(c byte)     { r.B = append(r.B, c) }
func (r *ResponseBuf) AppendString(s string) { r.B = append(r.B, s...) }
func (r *ResponseBuf) AppendInt(v int64)     { r.B = strconv.AppendInt(r.B, v, 10) }
func (r *ResponseBuf) AppendBool(v bool)     { r.B = strconv.AppendBool(r.B, v) }
func (r *ResponseBuf) AppendBytes(b []byte)  { r.B = append(r.B, b...) }

// AppendFloat writes a finite business Float without changing the buffer on error.
func (r *ResponseBuf) AppendFloat(v float64) error {
	if !codec.IsFiniteFloat(v) {
		return codec.ErrNonFiniteFloat
	}
	r.B = strconv.AppendFloat(r.B, v, 'f', -1, 64)
	return nil
}

// AppendJSONString writes a UTF-8 JSON string without publishing partial output
// or replacement characters on failure. The input must not alias the buffer.
func (r *ResponseBuf) AppendJSONString(s string) error {
	dst, err := jsontext.AppendQuote(r.B, s)
	if err != nil {
		return err
	}
	r.B = dst
	return nil
}

// AppendTime writes a time value as a JSON string using the given layout.
// Uses time.AppendFormat to avoid intermediate string allocation.
func (r *ResponseBuf) AppendTime(t time.Time, layout string) {
	r.B = append(r.B, '"')
	r.B = t.AppendFormat(r.B, layout)
	r.B = append(r.B, '"')
}
