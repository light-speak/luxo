package luxolog

import (
	"os"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// These limits bound diagnostic output, never business values or wire payloads.
const (
	MaxValueBytes = 4096
	MaxValueDepth = 64
	logPrefixSize = 64
	truncatedText = "<truncated>"
)

// Record is exclusively owned until WriteDebug/Info/Warn/Error releases it.
// Generated formatters append static values without reflection or object codecs.
type Record struct {
	data      [logPrefixSize + MaxValueBytes + len(truncatedText) + 1]byte
	size      int
	truncated bool
}

var records = sync.Pool{New: func() any { return new(Record) }}

// NewRecord borrows a bounded buffer. Call it only after checking the log level.
func NewRecord() *Record {
	r := records.Get().(*Record)
	r.size, r.truncated = 0, false
	return r
}

// Full lets generated collection/object loops stop visiting additional values.
func (r *Record) Full() bool { return r.truncated || r.size == MaxValueBytes }

// Truncate records that additional diagnostic content was deliberately omitted.
func (r *Record) Truncate() { r.truncated = true }

func boundedLogPrefix[T ~string | ~[]byte](value T, available int) int {
	if len(value) <= available {
		return len(value)
	}
	for available > 0 && !utf8.RuneStart(value[available]) {
		available--
	}
	return available
}

// Text appends already formatted text, retaining complete UTF-8 boundaries.
func (r *Record) Text(value string) {
	if r.truncated {
		return
	}
	count := boundedLogPrefix(value, MaxValueBytes-r.size)
	copy(r.data[logPrefixSize+r.size:], value[:count])
	r.size += count
	r.truncated = count != len(value)
}

// Raw appends a bounded textual view, without parsing or re-encoding JSON.
func (r *Record) Raw(value []byte) {
	if r.truncated {
		return
	}
	count := boundedLogPrefix(value, MaxValueBytes-r.size)
	copy(r.data[logPrefixSize+r.size:], value[:count])
	r.size += count
	r.truncated = count != len(value)
}

// String quotes bounded chunks without allocating an input-sized buffer.
func (r *Record) String(value string) {
	if r.truncated {
		return
	}
	available := MaxValueBytes - r.size
	// Six output bytes per input byte is a conservative escape bound.
	if available >= 2 && len(value) <= (available-2)/6 {
		quoted := strconv.AppendQuote(r.data[:logPrefixSize+r.size], value)
		r.size = len(quoted) - logPrefixSize
		return
	}
	r.Text(`"`)
	for len(value) > 0 && !r.Full() {
		value = r.quoteChunk(value)
	}
	if len(value) > 0 {
		r.Truncate()
	}
	r.Text(`"`)
}

func (r *Record) quoteChunk(value string) string {
	count := boundedLogPrefix(value, (MaxValueBytes-r.size)/6)
	if count == 0 {
		var scratch [16]byte
		_, count = utf8.DecodeRuneInString(value)
		quoted := strconv.AppendQuote(scratch[:0], value[:count])
		r.Raw(quoted[1 : len(quoted)-1])
		return value[count:]
	}
	start := logPrefixSize + r.size
	quoted := strconv.AppendQuote(r.data[:start], value[:count])
	// The surrounding quotes belong to String, not to each individual chunk.
	written := copy(r.data[start:], quoted[start+1:len(quoted)-1])
	r.size += written
	return value[count:]
}

func (r *Record) Int(value int64) {
	var scratch [32]byte
	r.Raw(strconv.AppendInt(scratch[:0], value, 10))
}

func (r *Record) Float(value float64) {
	var scratch [32]byte
	r.Raw(strconv.AppendFloat(scratch[:0], value, 'g', -1, 64))
}

func (r *Record) Boolean(value bool) {
	if value {
		r.Text("true")
	} else {
		r.Text("false")
	}
}

// Hex visits only bytes that fit; it never builds a complete intermediate string.
func (r *Record) Hex(value []byte) {
	const digits = "0123456789abcdef"
	r.Text("0x")
	for _, value := range value {
		if r.Full() {
			r.Truncate()
			break
		}
		pair := [2]byte{digits[value>>4], digits[value&15]}
		r.Raw(pair[:])
	}
}

func (r *Record) WriteDebug() { r.write(LevelDebug) }
func (r *Record) WriteInfo()  { r.write(LevelInfo) }
func (r *Record) WriteWarn()  { r.write(LevelWarn) }
func (r *Record) WriteError() { r.write(LevelError) }

func (r *Record) write(level int) {
	if GetLevel() <= level {
		var scratch [logPrefixSize]byte
		prefix := appendLogPrefix(scratch[:0], time.Now(), level)
		start := logPrefixSize - len(prefix)
		copy(r.data[start:logPrefixSize], prefix)
		end := logPrefixSize + r.size
		if r.truncated {
			end += copy(r.data[end:], truncatedText)
		}
		r.data[end] = '\n'
		_, _ = os.Stderr.Write(r.data[start : end+1])
	}
	records.Put(r)
}

func appendLogPrefix(dst []byte, at time.Time, level int) []byte {
	colors := [...]string{blue, cyan, yellow, red}
	names := [...]string{"DEBUG", "INFO", "WARN", "ERROR"}
	dst = append(dst, dim...)
	dst = at.AppendFormat(dst, "15:04:05")
	dst = append(dst, reset...)
	dst = append(dst, ' ')
	dst = append(dst, colors[level]...)
	dst = append(dst, names[level]...)
	dst = append(dst, reset...)
	dst = append(dst, ' ')
	if level == LevelInfo || level == LevelWarn {
		dst = append(dst, ' ')
	}
	return dst
}
