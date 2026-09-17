package api

import (
	"encoding/json"
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// Binary WebSocket frame types. Every binary frame starts with exactly one
// frame type byte so request sequence varints can never collide with control
// or stream frames.
const (
	BinaryFrameCallRequest byte = 0x01
	BinaryFrameCallSuccess byte = 0x02
	BinaryFrameCallError   byte = 0x03
	// The 0x10 range identifies streams by subscription UInt64, not API ID.
	// Retired 0x04..0x08 frames are rejected, never parsed as the new layout.
	BinaryFrameSubscribe        byte = 0x10
	BinaryFrameUnsubscribe      byte = 0x11
	BinaryFrameStream           byte = 0x12
	BinaryFrameSubscribeSuccess byte = 0x13
	BinaryFrameSubscribeError   byte = 0x14
	BinaryFrameStreamComplete   byte = 0x15
	BinaryFrameStreamError      byte = 0x16
)

const (
	binaryErrorCodeField = iota + 1
	binaryErrorNameField
	binaryErrorMessageField
	binaryErrorTraceIDField
	binaryErrorDataField
	binaryErrorCauseField
)

// BinaryError is the canonical error representation shared by binary HTTP and
// binary WebSocket responses.
type BinaryError struct {
	Code    int
	Name    string
	Message string
	TraceID string
	Data    json.RawMessage
	Cause   string
}

// Error implements error.
func (e *BinaryError) Error() string {
	if e.Message == "" {
		return e.Name
	}
	return e.Name + ": " + e.Message
}

type wireError = BinaryError

func appendBinaryError(dst []byte, e wireError) ([]byte, error) {
	prefix := dst
	dst = codec.AppendSvarint(append(dst, binaryErrorCodeField), int64(e.Code))
	for _, field := range []struct {
		id    byte
		value string
	}{
		{binaryErrorNameField, e.Name}, {binaryErrorMessageField, e.Message},
		{binaryErrorTraceIDField, e.TraceID},
	} {
		if field.id == binaryErrorTraceIDField && field.value == "" {
			continue
		}
		var err error
		dst, err = codec.AppendString(append(dst, field.id), field.value)
		if err != nil {
			return prefix, err
		}
	}
	if len(e.Data) > 0 {
		dst = codec.AppendBytes(append(dst, binaryErrorDataField), e.Data)
	}
	if e.Cause != "" {
		var err error
		dst, err = codec.AppendString(append(dst, binaryErrorCauseField), e.Cause)
		if err != nil {
			return prefix, err
		}
	}
	return append(dst, 0), nil
}

// DecodeBinaryError decodes the canonical binary error envelope.
// The legacy statusCode argument cannot supply missing required wire fields.
func DecodeBinaryError(data []byte, _ int) (*BinaryError, error) {
	dec := codec.NewDecoder(data)
	result := &BinaryError{}
	seen, err := readBinaryErrorFields(dec, result)
	if err != nil {
		return nil, err
	}
	if err := dec.Finish(); err != nil {
		return nil, fmt.Errorf("decode binary error: %w", err)
	}
	if err := validateBinaryError(result, seen); err != nil {
		return nil, err
	}
	result.Data = append(json.RawMessage(nil), result.Data...)
	return result, nil
}

func readBinaryErrorFields(dec *codec.Decoder, result *BinaryError) (uint8, error) {
	var seen uint8
	for dec.NextField() {
		field := dec.FieldID()
		if field < binaryErrorCodeField || field > binaryErrorCauseField {
			return 0, fmt.Errorf("unknown binary error field %d", field)
		}
		bit := uint8(1 << field)
		if seen&bit != 0 {
			return 0, fmt.Errorf("duplicate binary error field %d", field)
		}
		seen |= bit
		switch field {
		case binaryErrorCodeField:
			value := dec.ReadInt()
			result.Code = int(value)
			// Only 32-bit targets can narrow the signed wire value to a host int.
			if int64(result.Code) != value {
				return 0, fmt.Errorf("binary error code exceeds host int range")
			}
		case binaryErrorNameField:
			result.Name = dec.ReadString()
		case binaryErrorMessageField:
			result.Message = dec.ReadString()
		case binaryErrorTraceIDField:
			result.TraceID = dec.ReadString()
		case binaryErrorDataField:
			result.Data = dec.ReadBytes()
		case binaryErrorCauseField:
			result.Cause = dec.ReadString()
		}
	}
	return seen, nil
}

func validateBinaryError(result *BinaryError, seen uint8) error {
	const required = 1<<binaryErrorCodeField | 1<<binaryErrorNameField | 1<<binaryErrorMessageField
	if seen&required != required {
		return fmt.Errorf("decode binary error: missing required fields")
	}
	// String fields have already passed Decoder's UTF-8 validation and Finish.
	if seen&(1<<binaryErrorDataField) != 0 && !jsonutil.ValidUTF8JSON(result.Data) {
		return fmt.Errorf("decode binary error: invalid JSON data")
	}
	return nil
}
