package api

import (
	"encoding/json"
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Binary WebSocket frame types. Every binary frame starts with exactly one
// frame type byte so request sequence varints can never collide with control
// or stream frames.
const (
	BinaryFrameCallRequest      byte = 0x01
	BinaryFrameCallSuccess      byte = 0x02
	BinaryFrameCallError        byte = 0x03
	BinaryFrameSubscribe        byte = 0x04
	BinaryFrameUnsubscribe      byte = 0x05
	BinaryFrameStream           byte = 0x06
	BinaryFrameSubscribeSuccess byte = 0x07
	BinaryFrameSubscribeError   byte = 0x08
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

func appendBinaryError(dst []byte, e wireError) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(binaryErrorCodeField, int64(e.Code))
	enc.WriteFieldString(binaryErrorNameField, e.Name)
	enc.WriteFieldString(binaryErrorMessageField, e.Message)
	if e.TraceID != "" {
		enc.WriteFieldString(binaryErrorTraceIDField, e.TraceID)
	}
	if len(e.Data) > 0 {
		enc.WriteFieldBytes(binaryErrorDataField, e.Data)
	}
	if e.Cause != "" {
		enc.WriteFieldString(binaryErrorCauseField, e.Cause)
	}
	enc.WriteEnd()
	return append(dst, enc.Bytes()...)
}

// DecodeBinaryError decodes the canonical binary error envelope.
func DecodeBinaryError(data []byte, statusCode int) (*BinaryError, error) {
	dec := codec.NewDecoder(data)
	result := &BinaryError{Code: statusCode, Name: "Error", Message: fmt.Sprintf("HTTP %d", statusCode)}
	for dec.NextField() {
		switch dec.FieldID() {
		case binaryErrorCodeField:
			result.Code = int(dec.ReadInt())
		case binaryErrorNameField:
			result.Name = dec.ReadString()
		case binaryErrorMessageField:
			result.Message = dec.ReadString()
		case binaryErrorTraceIDField:
			result.TraceID = dec.ReadString()
		case binaryErrorDataField:
			result.Data = append(result.Data[:0], dec.ReadBytes()...)
		case binaryErrorCauseField:
			result.Cause = dec.ReadString()
		default:
			return nil, fmt.Errorf("unknown binary error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		return nil, fmt.Errorf("decode binary error: %w", err)
	}
	if dec.FieldID() != 0 {
		return nil, fmt.Errorf("decode binary error: missing end marker")
	}
	if dec.Offset() != len(data) {
		return nil, fmt.Errorf("decode binary error: trailing bytes")
	}
	if len(result.Data) > 0 && !json.Valid(result.Data) {
		return nil, fmt.Errorf("decode binary error: invalid JSON data")
	}
	return result, nil
}
