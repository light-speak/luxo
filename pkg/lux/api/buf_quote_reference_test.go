package api

import "unicode/utf8"

// Frozen manual quote writer, before strict UTF-8 migration. Invalid input
// replacement is historical behavior, not an acceptable correctness baseline.
const quoteHexBeforeStrict = "0123456789abcdef"

func (r *ResponseBuf) appendJSONStringBeforeStrict(s string) {
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
			r.B = append(r.B, '\\', 'u', '0', '0', quoteHexBeforeStrict[c>>4], quoteHexBeforeStrict[c&0x0f])
		}
		i++
	}
	r.B = append(r.B, '"')
}
