package rpc

import (
	"fmt"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// decodeError rejects malformed envelopes before constructing an application error.
func decodeError(data []byte) error {
	fields, err := readRPCErrorFields(data)
	if err != nil {
		return err
	}
	// Only 32-bit hosts can narrow the signed wire value to a host int.
	if int64(int(fields.code)) != fields.code {
		return fmt.Errorf("rpc: error code exceeds host int range")
	}
	if !utf8.Valid(fields.name) || !utf8.Valid(fields.message) {
		return fmt.Errorf("rpc: error envelope contains invalid UTF-8")
	}
	if fields.data != nil || fields.text != nil {
		if err := validateRPCErrorData(fields.data, fields.text); err != nil {
			return err
		}
	}
	// Copy only validated text; each string owns its storage independently.
	value := &luxerrors.AppError{Code: int(fields.code), Name: string(fields.name), Message: string(fields.message)}
	if fields.code >= 500 || fields.internal {
		value.Cause = fmt.Errorf("rpc error %d %s: %s", fields.code, value.Name, value.Message)
		value.Message = "error.internal"
		value.Internal = true
	} else if fields.data != nil {
		value.Data = ownRPCErrorData(fields.data, fields.text)
	}
	return value
}

type rpcErrorEnvelope struct {
	code          int64
	name, message []byte
	data, text    []byte
	internal      bool
}

func readRPCErrorFields(data []byte) (rpcErrorEnvelope, error) {
	var value rpcErrorEnvelope
	var seen uint8
	offset := 0
	for {
		field, size := codec.ReadVarint(data, offset)
		if size <= 0 {
			return value, fmt.Errorf("rpc: invalid error field identifier at offset %d", offset)
		}
		offset += size
		if field == 0 {
			break
		}
		if field > 6 {
			return value, fmt.Errorf("rpc: unknown error field %d", field)
		}
		bit := uint8(1 << field)
		if seen&bit != 0 {
			return value, fmt.Errorf("rpc: duplicate error field %d", field)
		}
		seen |= bit
		size = value.readField(data, offset, field)
		if size <= 0 {
			return value, fmt.Errorf("rpc: invalid error field %d at offset %d", field, offset)
		}
		offset += size
	}
	if offset != len(data) {
		return value, fmt.Errorf("rpc: error envelope has trailing bytes")
	}
	if seen&0b1110 != 0b1110 {
		return value, fmt.Errorf("rpc: error envelope missing required fields")
	}
	return value, nil
}

// readField is called only for known field identifiers.
func (e *rpcErrorEnvelope) readField(data []byte, offset int, field uint64) (size int) {
	switch field {
	case 1:
		e.code, size = codec.ReadSvarint(data, offset)
	case 2:
		e.name, size = codec.ReadBytes(data, offset)
	case 3:
		e.message, size = codec.ReadBytes(data, offset)
	case 4:
		e.data, size = codec.ReadBytes(data, offset)
	case 5:
		e.text, size = codec.ReadBytes(data, offset)
	case 6:
		if offset < len(data) && data[offset] <= 1 {
			e.internal, size = data[offset] == 1, 1
		}
	}
	return size
}
