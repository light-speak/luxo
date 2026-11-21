package api

import (
	"strconv"
	"sync"
	"time"
)

var requestLogBuffers = sync.Pool{New: func() any {
	buf := make([]byte, 0, 256)
	return &buf
}}

// appendRequestLog formats a captured duration without generic formatting.
func appendRequestLog(dst []byte, at time.Time, module, moduleColor, name string, duration time.Duration, failure string) []byte {
	dst = append(dst, colorDim...)
	dst = at.AppendFormat(dst, "15:04:05")
	dst = append(dst, colorReset...)
	dst = append(dst, ' ')
	dst = append(dst, moduleColor...)
	dst = append(dst, '[')
	dst = append(dst, module...)
	dst = append(dst, ']')
	dst = append(dst, colorReset...)
	dst = append(dst, ' ')
	dst = append(dst, name...)
	dst = append(dst, ' ')
	ms := float64(duration.Microseconds()) / 1000
	color := colorDim
	if ms > 500 {
		color = colorRed
	} else if ms > 100 {
		color = "\033[33m"
	}
	dst = append(dst, color...)
	dst = strconv.AppendFloat(dst, ms, 'f', 1, 64)
	dst = append(dst, "ms"...)
	dst = append(dst, colorReset...)
	dst = append(dst, ' ')
	if failure == "" {
		dst = append(dst, colorGreen...)
		dst = append(dst, "✓"...)
	} else {
		dst = append(dst, colorRed...)
		dst = append(dst, "✗ "...)
		dst = append(dst, failure...)
	}
	dst = append(dst, colorReset...)
	return append(dst, '\n')
}

// putRequestLogBuffer bounds retained memory independently of log line size.
func putRequestLogBuffer(buf *[]byte) {
	if cap(*buf) <= 4096 {
		*buf = (*buf)[:0]
		requestLogBuffers.Put(buf)
	}
}

func (rt *Router) logParseFailure(mode, message string) {
	if !rt.allowLogParts(mode, message) {
		return
	}
	buf := requestLogBuffers.Get().(*[]byte)
	*buf = append((*buf)[:0], colorDim...)
	*buf = time.Now().AppendFormat(*buf, "15:04:05")
	*buf = append(*buf, colorReset...)
	*buf = append(*buf, ' ')
	*buf = append(*buf, colorRed...)
	*buf = append(*buf, "[parse]"...)
	*buf = append(*buf, colorReset...)
	*buf = append(*buf, ' ')
	*buf = append(*buf, mode...)
	*buf = append(*buf, ' ')
	*buf = append(*buf, colorRed...)
	*buf = append(*buf, "✗ "...)
	*buf = append(*buf, message...)
	*buf = append(*buf, colorReset...)
	*buf = append(*buf, '\n')
	rt.writeLogBuffer(buf)
}
