package api

import (
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// hex lookup for JSON \u00XX escape without fmt.Sprintf.
const hexDigits = "0123456789abcdef"

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
func (r *ResponseBuf) AppendFloat(v float64) { r.B = strconv.AppendFloat(r.B, v, 'f', -1, 64) }
func (r *ResponseBuf) AppendBool(v bool)     { r.B = strconv.AppendBool(r.B, v) }
func (r *ResponseBuf) AppendBytes(b []byte)  { r.B = append(r.B, b...) }

// AppendJSONString writes a JSON-escaped string directly into the buffer.
// Uses a byte-level fast path for ASCII — bulk-appends runs of safe bytes.
// Falls through to rune handling only for bytes >= 0x80 or special chars.
func (r *ResponseBuf) AppendJSONString(s string) {
	r.B = append(r.B, '"')
	i := 0
	for i < len(s) {
		// Fast path: scan for longest run of safe ASCII bytes.
		start := i
		for i < len(s) {
			c := s[i]
			if c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
				break
			}
			i++
		}
		if start < i {
			r.B = append(r.B, s[start:i]...)
		}
		if i >= len(s) {
			break
		}
		c := s[i]
		if c >= 0x80 {
			// Multi-byte UTF-8: decode rune and append.
			ru, size := utf8.DecodeRuneInString(s[i:])
			r.B = utf8.AppendRune(r.B, ru)
			i += size
			continue
		}
		// Special ASCII byte requiring escape.
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
			r.B = append(r.B, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0f])
		}
		i++
	}
	r.B = append(r.B, '"')
}

// AppendTime writes a time value as a JSON string using the given layout.
// Uses time.AppendFormat to avoid intermediate string allocation.
func (r *ResponseBuf) AppendTime(t time.Time, layout string) {
	r.B = append(r.B, '"')
	r.B = t.AppendFormat(r.B, layout)
	r.B = append(r.B, '"')
}
