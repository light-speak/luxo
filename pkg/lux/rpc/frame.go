// Package rpc provides Luxo's internal RPC protocol for microservice communication.
// Uses a minimal framed TCP protocol with Luxo binary encoding — no HTTP overhead.
//
// Frame format:
//
//	[4 bytes: payload length, big-endian uint32]
//	[payload bytes]
//
// A connection preface verifies ProtocolVersion before the first frame.
// Request payload:
//
//	[kind 0x01=call, 0x02=stream] [flags: 0x01=traced, otherwise 0]
//	[bearer token len varint] [bearer token bytes]
//	[API ID varint] [field mask len varint] [field mask bytes] [params binary (fieldID+value pairs, 0x00 terminator)]
//
// Traced requests insert trace ID, full field path, and a
// database-detail flag before the canonical binary request body.
//
// Response payload:
//
//	[status 1 byte: 0x00=ok, 0x01=error, 0x02=stream item]
//	[body bytes]
//
// Streams end with status 0x05 and no body, or a canonical error frame. EOF
// without a terminal frame is a transport failure, never successful completion.
//
// Error body (status=0x01):
//
//	[field 1=error code svarint] [field 2=error name string]
//	[field 3=error message string] [0x00 terminator]
//
// All three fields are required exactly once, in any order. Text must be valid
// UTF-8. Unknown fields, duplicate fields, truncation and trailing bytes fail.
package rpc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

const (
	statusOK             = 0x00
	statusError          = 0x01
	statusStream         = 0x02
	statusTraceOK        = 0x03
	statusTraceError     = 0x04
	statusStreamComplete = 0x05
	maxFrameSize         = 4 << 20 // 4MB max frame

	requestFlagTraced     = 0x01
	requestKindCall       = 0x01
	requestKindStream     = 0x02
	maxBearerTokenSize    = 16 << 10
	maxTraceIDSize        = 256
	maxTraceFieldPathSize = 4 << 10
)

type requestEnvelope struct {
	kind            byte
	token           string
	traceID         string
	fieldPath       string
	databaseDetails bool
	body            []byte
}

func decodeRequestEnvelope(payload []byte) (requestEnvelope, error) {
	if len(payload) < 4 {
		return requestEnvelope{}, fmt.Errorf("rpc: truncated request envelope")
	}
	flags := payload[1]
	if flags & ^byte(requestFlagTraced) != 0 {
		return requestEnvelope{}, fmt.Errorf("rpc: unsupported request flags %d", flags)
	}
	kind := payload[0]
	if kind != requestKindCall && kind != requestKindStream {
		return requestEnvelope{}, fmt.Errorf("rpc: unknown request kind %d", kind)
	}
	tokenLength, consumed := codec.ReadVarint(payload, 2)
	if consumed <= 0 {
		return requestEnvelope{}, fmt.Errorf("rpc: invalid bearer token length")
	}
	if tokenLength > maxBearerTokenSize {
		return requestEnvelope{}, fmt.Errorf("rpc: bearer token exceeds %d bytes", maxBearerTokenSize)
	}
	start := 2 + consumed
	if tokenLength > uint64(len(payload)-start) {
		return requestEnvelope{}, fmt.Errorf("rpc: bearer token length exceeds request")
	}
	tokenEnd := start + int(tokenLength)
	if tokenEnd == len(payload) {
		return requestEnvelope{}, fmt.Errorf("rpc: request envelope has no body")
	}
	envelope := requestEnvelope{kind: kind, token: string(payload[start:tokenEnd])}
	if flags&requestFlagTraced != 0 {
		return decodeRequestTrace(payload, tokenEnd, envelope)
	}
	envelope.body = payload[tokenEnd:]
	return envelope, nil
}

func decodeRequestTrace(payload []byte, offset int, envelope requestEnvelope) (requestEnvelope, error) {
	traceID, next, err := decodeTraceID(payload, offset)
	if err != nil {
		return requestEnvelope{}, err
	}
	fieldPath, next, err := decodeTraceFieldPath(payload, next)
	if err != nil {
		return requestEnvelope{}, err
	}
	if next >= len(payload) || payload[next] > 1 {
		return requestEnvelope{}, fmt.Errorf("rpc: invalid database trace detail flag")
	}
	if next+1 >= len(payload) {
		return requestEnvelope{}, fmt.Errorf("rpc: request envelope has no body")
	}
	envelope.traceID, envelope.fieldPath = traceID, fieldPath
	envelope.databaseDetails = payload[next] == 1
	envelope.body = payload[next+1:]
	return envelope, nil
}

func decodeTraceFieldPath(payload []byte, offset int) (string, int, error) {
	pathLength, consumed := codec.ReadVarint(payload, offset)
	if consumed <= 0 {
		return "", 0, fmt.Errorf("rpc: invalid trace field path length")
	}
	if pathLength > maxTraceFieldPathSize {
		return "", 0, fmt.Errorf("rpc: trace field path exceeds %d bytes", maxTraceFieldPathSize)
	}
	start := offset + consumed
	if pathLength > uint64(len(payload)-start) {
		return "", 0, fmt.Errorf("rpc: trace field path length exceeds request")
	}
	end := start + int(pathLength)
	return string(payload[start:end]), end, nil
}

func decodeTraceID(payload []byte, offset int) (string, int, error) {
	traceLength, consumed := codec.ReadVarint(payload, offset)
	if consumed <= 0 {
		return "", 0, fmt.Errorf("rpc: invalid trace ID length")
	}
	if traceLength == 0 || traceLength > maxTraceIDSize {
		return "", 0, fmt.Errorf("rpc: trace ID length must be between 1 and %d bytes", maxTraceIDSize)
	}
	start := offset + consumed
	if traceLength >= uint64(len(payload)-start) {
		return "", 0, fmt.Errorf("rpc: trace ID length exceeds request")
	}
	end := start + int(traceLength)
	return string(payload[start:end]), end, nil
}

// WriteFrame writes a length-prefixed frame to w.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrameSize {
		return fmt.Errorf("rpc: frame too large: %d bytes (max %d)", len(payload), maxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// Internal callers reserve the prefix before encoding, avoiding both a copy
// and the temporary scatter/gather containers needed by separate payloads.
func writePreparedFrame(w io.Writer, frame []byte) error {
	if len(frame) < 4 || len(frame)-4 > maxFrameSize {
		return fmt.Errorf("rpc: invalid prepared frame length: %d", len(frame))
	}
	binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)-4))
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}

func writeStatusFrame(w io.Writer, status byte, payload []byte) error {
	if len(payload) >= maxFrameSize {
		return fmt.Errorf("rpc: frame too large: %d bytes (max %d)", len(payload)+1, maxFrameSize)
	}
	var prefix [5]byte
	binary.BigEndian.PutUint32(prefix[:4], uint32(len(payload)+1))
	prefix[4] = status
	buffers := net.Buffers{prefix[:], payload}
	_, err := buffers.WriteTo(w)
	return err
}

// ReadFrame reads a length-prefixed frame from r.
// Returns the payload bytes. Reuses buf's capacity, including zero-length
// slices. The caller owns the buffer and controls its reuse lifetime.
func ReadFrame(r io.Reader, buf []byte) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("rpc: frame too large: %d bytes (max %d)", size, maxFrameSize)
	}
	if int(size) > cap(buf) {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Bound per-connection idle retention without lowering the accepted frame size.
// A large frame stays alive through dispatch but is not kept for the next call.
func retainReadFrame(buf []byte) []byte {
	if cap(buf) > 64<<10 {
		return nil
	}
	return buf[:0]
}
