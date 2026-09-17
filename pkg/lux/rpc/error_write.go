package rpc

import (
	"io"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func writeApplicationError(w io.Writer, value *luxerrors.AppError, trace *api.DebugTraceSession) error {
	encoded, err := encodeApplicationError(value)
	if err != nil {
		// Never publish a partially encoded data field or expose native values.
		return writeCallError(w, 500, "Internal", "error.internal", trace)
	}
	return writeEncodedError(w, encoded, trace)
}

func encodeApplicationError(value *luxerrors.AppError) ([]byte, error) {
	message := value.Message
	if value.Internal {
		message = "error.internal"
	}
	var raw, text []byte
	if value.Data != nil && !value.Internal && value.Code < 500 {
		var err error
		raw, text, err = encodeRPCErrorData(value.Data)
		if err != nil {
			return nil, err
		}
	}
	return encodeRPCError(value.Code, value.Name, message, raw, text, value.Internal)
}

func encodeError(code int, name, message string) []byte {
	encoded, err := encodeRPCError(code, name, message, nil, nil, false)
	if err == nil {
		return encoded
	}
	// Only fixed UTF-8 literals may use the raw bytes writer for this fallback.
	encoded = codec.AppendSvarint([]byte{statusError, 1}, 500)
	encoded = codec.AppendBytes(append(encoded, 2), []byte("Internal"))
	encoded = codec.AppendBytes(append(encoded, 3), []byte("error.internal"))
	return append(encoded, 0)
}

func encodeRPCError(code int, name, message string, raw, text []byte, internal bool) ([]byte, error) {
	// The old three mandatory fields keep exactly the same layout. Optional
	// data is copied directly from its one static writer, never decoded to a map.
	capacity := 35 + len(name) + len(message)
	if raw != nil {
		capacity += 11 + len(raw)
	}
	if len(text) > 0 {
		capacity += 11 + len(text)
	}
	if internal {
		capacity += 2
	}
	encoded := make([]byte, 0, capacity)
	encoded = append(encoded, statusError, 1)
	encoded = codec.AppendSvarint(encoded, int64(code))
	var err error
	encoded, err = codec.AppendString(append(encoded, 2), name)
	if err != nil {
		return nil, err
	}
	encoded, err = codec.AppendString(append(encoded, 3), message)
	if err != nil {
		return nil, err
	}
	if raw != nil {
		encoded = codec.AppendBytes(append(encoded, 4), raw)
	}
	if len(text) > 0 {
		encoded = codec.AppendBytes(append(encoded, 5), text)
	}
	if internal {
		encoded = append(encoded, 6, 1)
	}
	return append(encoded, 0), nil
}
