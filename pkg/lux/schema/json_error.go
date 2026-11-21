package schema

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	jsonModelContext = -1
	jsonFieldContext = -2
)

// Contexts are collected while the private encoder unwinds. That encoder owns
// the error exclusively until JSONToBinary returns it; published errors are
// immutable. Frames retain metadata names, never input bytes or model values.
type jsonContextError struct {
	cause  error
	frames []jsonErrorContext
}

type jsonErrorContext struct {
	name  string
	field string
	index int // Negative for model/field contexts; otherwise a list element index.
}

func addJSONContext(err error, name, field string, index int, final bool) error {
	value, ok := err.(*jsonContextError)
	if final && !ok {
		if index == jsonFieldContext {
			return fmt.Errorf("%s.%s: %w", name, field, err)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	if !ok {
		value = &jsonContextError{cause: err}
	}
	value.frames = append(value.frames, jsonErrorContext{name: name, field: field, index: index})
	if final {
		return &jsonDiagnosticError{message: value.Error(), cause: value.cause}
	}
	return value
}

// The published diagnostic keeps only its final text and original cause.
// Transient context frames are released before JSONToBinary returns.
type jsonDiagnosticError struct {
	message string
	cause   error
}

func (e *jsonDiagnosticError) Error() string { return e.message }
func (e *jsonDiagnosticError) Unwrap() error { return e.cause }

// Materialize at the public boundary, rather than copying the growing message
// at every model/list boundary. Repeated consumers read the immutable diagnostic.
func (e *jsonContextError) Error() string {
	message := e.cause.Error()
	size := len(message)
	for i := range e.frames {
		size += e.frames[i].size()
	}
	var out strings.Builder
	out.Grow(size)
	for i := len(e.frames) - 1; i >= 0; i-- {
		e.frames[i].appendTo(&out)
	}
	out.WriteString(message)
	return out.String()
}

func (c jsonErrorContext) size() int {
	if c.index >= 0 {
		digits := 1
		for value := c.index; value >= 10; value /= 10 {
			digits++
		}
		return len("element ") + digits + len(": ")
	}
	size := len(c.name) + len(": ")
	if c.index == jsonFieldContext {
		size += 1 + len(c.field)
	}
	return size
}

func (c jsonErrorContext) appendTo(out *strings.Builder) {
	if c.index >= 0 {
		out.WriteString("element ")
		var digits [20]byte
		out.Write(strconv.AppendInt(digits[:0], int64(c.index), 10))
	} else {
		out.WriteString(c.name)
		if c.index == jsonFieldContext {
			out.WriteByte('.')
			out.WriteString(c.field)
		}
	}
	out.WriteString(": ")
}
