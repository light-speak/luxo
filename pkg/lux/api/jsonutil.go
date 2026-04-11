package api

import (
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// JSONWriter is implemented by generated model types to support
// single-pass JSON serialization with field selection.
// Uses ResponseBuf for zero-allocation direct append.
type JSONWriter interface {
	WriteJSON(buf *ResponseBuf, fields []*selection.Field)
}
