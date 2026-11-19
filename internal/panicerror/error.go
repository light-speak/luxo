// Package panicerror builds diagnostics without invoking panic payload methods.
package panicerror

import "errors"

var errNonStringPanic = errors.New("handler panicked with a non-string value")

// New preserves plain string diagnostics. Other values deliberately remain
// opaque: Error, String and Format methods may block, exit or panic again.
func New(prefix string, value any) error {
	if message, ok := value.(string); ok {
		return errors.New(prefix + message)
	}
	return errNonStringPanic
}
