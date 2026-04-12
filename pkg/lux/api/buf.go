package api

import (
	"strconv"
	"sync"
	"unicode/utf8"
)

// hex lookup for JSON \u00XX escape without fmt.Sprintf.
const hexDigits = "0123456789abcdef"

// ResponseBuf is a pooled, zero-allocation response buffer.
// Uses []byte with direct append — no bytes.Buffer overhead.
type ResponseBuf struct {
	B []byte
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
	return buf
}

// PutBuf returns a ResponseBuf to the pool.
func PutBuf(buf *ResponseBuf) {
	if cap(buf.B) > 1<<20 { // don't pool buffers > 1MB
		return
	}
	bufPool.Put(buf)
}

func (r *ResponseBuf) AppendByte(c byte)     { r.B = append(r.B, c) }
func (r *ResponseBuf) AppendString(s string) { r.B = append(r.B, s...) }
func (r *ResponseBuf) AppendInt(v int64)     { r.B = strconv.AppendInt(r.B, v, 10) }
func (r *ResponseBuf) AppendFloat(v float64) { r.B = strconv.AppendFloat(r.B, v, 'f', -1, 64) }
func (r *ResponseBuf) AppendBool(v bool)     { r.B = strconv.AppendBool(r.B, v) }
func (r *ResponseBuf) AppendBytes(b []byte)  { r.B = append(r.B, b...) }

// AppendJSONString writes a JSON-escaped string directly into the buffer.
// Zero allocation for strings without control characters.
func (r *ResponseBuf) AppendJSONString(s string) {
	r.B = append(r.B, '"')
	for _, c := range s {
		switch c {
		case '"':
			r.B = append(r.B, '\\', '"')
		case '\\':
			r.B = append(r.B, '\\', '\\')
		case '\n':
			r.B = append(r.B, '\\', 'n')
		case '\r':
			r.B = append(r.B, '\\', 'r')
		case '\t':
			r.B = append(r.B, '\\', 't')
		default:
			if c < 0x20 {
				r.B = append(r.B, '\\', 'u', '0', '0', hexDigits[byte(c)>>4], hexDigits[byte(c)&0x0f])
			} else {
				r.B = utf8.AppendRune(r.B, c)
			}
		}
	}
	r.B = append(r.B, '"')
}
