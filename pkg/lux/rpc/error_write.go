package rpc

import (
	"errors"
	"io"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func writeApplicationError(w io.Writer, value *luxerrors.AppError, trace *api.DebugTraceSession) error {
	encoded, err := encodeApplicationError(value)
	if err != nil {
		// Never publish a partially encoded data field or expose native values.
		return writeEncodedError(w, encodeInternalError(), trace)
	}
	return writeEncodedError(w, encoded, trace)
}

func encodeApplicationError(value *luxerrors.AppError) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded, err = nil, errors.New("rpc: error data encoder panicked")
		}
	}()
	if value == nil || value.Code < 400 || value.Code > 599 {
		return encodeInternalError(), nil
	}
	message := value.Message
	if value.Internal {
		message = "error.internal"
	}
	var raw, text []byte
	if value.Data != nil && !value.Internal {
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
	return encodeInternalError()
}

func encodeInternalError() []byte {
	// Only fixed UTF-8 literals may use the raw bytes writer for this fallback.
	encoded := codec.AppendSvarint([]byte{statusError, 1}, 500)
	encoded = codec.AppendBytes(append(encoded, 2), []byte("Internal"))
	encoded = codec.AppendBytes(append(encoded, 3), []byte("error.internal"))
	return append(encoded, 6, 1, 0)
}

func encodeRPCError(code int, name, message string, raw, text []byte, internal bool) ([]byte, error) {
	if code < 400 || code > 599 {
		return nil, errors.New("rpc: error status code must be between 400 and 599")
	}
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

var errSlowConsumer = luxerrors.New("SlowConsumer", 429, "stream subscriber is too slow")

// Error-chain callbacks are native code too. Normalize before writing any frame
// so an Unwrap/As/Is panic cannot strand a partially written response.
func normalizeHandlerError(err error, stream bool) (result *luxerrors.AppError) {
	defer func() {
		if recover() != nil {
			result = luxerrors.InternalError
		}
	}()
	if value, ok := errors.AsType[*luxerrors.AppError](err); ok {
		return value
	}
	if stream && errors.Is(err, api.ErrStreamFull) {
		return errSlowConsumer
	}
	// Never call Error on an unclassified native error or transmit its details.
	return luxerrors.InternalError
}

// Authentication callbacks are application code; never format their panic values.
var errAuthenticationPanic = errors.New("rpc: authentication callback panicked")

func encodeAuthenticationError(err error) (encoded []byte) {
	defer func() {
		if recover() != nil {
			encoded = encodeInternalError()
		}
	}()
	if err == errAuthenticationPanic {
		return encodeInternalError()
	}
	return encodeError(401, "Unauthorized", err.Error())
}
