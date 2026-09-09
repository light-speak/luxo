// Package rpc provides Luxo's internal RPC protocol for microservice communication.
// Uses a minimal framed TCP protocol with Luxo binary encoding — no HTTP overhead.
//
// Frame format:
//
//	[4 bytes: payload length, big-endian uint32]
//	[payload bytes]
//
// Request payload (canonical envelope, version 1):
//
//	[marker 0x00] [version 0x01] [kind 0x01=call, 0x02=stream]
//	[bearer token len varint] [bearer token bytes]
//	[API ID varint] [field mask len varint] [field mask bytes] [params binary (fieldID+value pairs, 0x00 terminator)]
//
// Traced requests use version 4 and insert trace ID, full field path, and a
// database-detail flag before the canonical binary request body.
//
// Response payload:
//
//	[status 1 byte: 0x00=ok, 0x01=error, 0x02=stream item]
//	[body bytes]
//
// Error body (status=0x01):
//
//	[field 1=error code svarint] [field 2=error name string]
//	[field 3=error message string] [0x00 terminator]
package rpc

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

const (
	statusOK         = 0x00
	statusError      = 0x01
	statusStream     = 0x02
	statusTraceOK    = 0x03
	statusTraceError = 0x04
	maxFrameSize     = 4 << 20 // 4MB max frame

	requestEnvelopeMarker        = 0x00
	requestEnvelopeVersion       = 0x01
	legacyTraceEnvelopeVersion   = 0x02
	fieldTraceEnvelopeVersion    = 0x03
	tracedRequestEnvelopeVersion = 0x04
	requestKindCall              = 0x01
	requestKindStream            = 0x02
	maxBearerTokenSize           = 16 << 10
	maxTraceIDSize               = 256
	maxTraceFieldPathSize        = 4 << 10
)

type requestEnvelope struct {
	kind            byte
	token           string
	traceID         string
	fieldPath       string
	databaseDetails bool
	body            []byte
}

func encodeRequestEnvelope(kind byte, token string, body []byte) []byte {
	payload := make([]byte, 0, 3+10+len(token)+len(body))
	payload = append(payload, requestEnvelopeMarker, requestEnvelopeVersion, kind)
	payload = codec.AppendVarint(payload, uint64(len(token)))
	payload = append(payload, token...)
	payload = append(payload, body...)
	return payload
}

func encodeTracedRequestEnvelope(kind byte, token, traceID, fieldPath string, databaseDetails bool, body []byte) []byte {
	payload := make([]byte, 0, 4+30+len(token)+len(traceID)+len(fieldPath)+len(body))
	payload = append(payload, requestEnvelopeMarker, tracedRequestEnvelopeVersion, kind)
	payload = codec.AppendVarint(payload, uint64(len(token)))
	payload = append(payload, token...)
	payload = codec.AppendVarint(payload, uint64(len(traceID)))
	payload = append(payload, traceID...)
	payload = codec.AppendVarint(payload, uint64(len(fieldPath)))
	payload = append(payload, fieldPath...)
	if databaseDetails {
		payload = append(payload, 1)
	} else {
		payload = append(payload, 0)
	}
	payload = append(payload, body...)
	return payload
}

func decodeRequestEnvelope(payload []byte) (requestEnvelope, error) {
	if len(payload) == 0 || payload[0] != requestEnvelopeMarker {
		return requestEnvelope{}, fmt.Errorf("rpc: missing canonical request envelope")
	}
	if len(payload) < 4 {
		return requestEnvelope{}, fmt.Errorf("rpc: truncated request envelope")
	}
	version := payload[1]
	if version != requestEnvelopeVersion && version != legacyTraceEnvelopeVersion && version != fieldTraceEnvelopeVersion && version != tracedRequestEnvelopeVersion {
		return requestEnvelope{}, fmt.Errorf("rpc: unsupported request envelope version %d", payload[1])
	}
	kind := payload[2]
	if kind != requestKindCall && kind != requestKindStream {
		return requestEnvelope{}, fmt.Errorf("rpc: unknown request kind %d", kind)
	}
	tokenLength, consumed := codec.ReadVarint(payload, 3)
	if consumed <= 0 {
		return requestEnvelope{}, fmt.Errorf("rpc: invalid bearer token length")
	}
	if tokenLength > maxBearerTokenSize {
		return requestEnvelope{}, fmt.Errorf("rpc: bearer token exceeds %d bytes", maxBearerTokenSize)
	}
	start := 3 + consumed
	if tokenLength > uint64(len(payload)-start) {
		return requestEnvelope{}, fmt.Errorf("rpc: bearer token length exceeds request")
	}
	tokenEnd := start + int(tokenLength)
	if tokenEnd == len(payload) {
		return requestEnvelope{}, fmt.Errorf("rpc: request envelope has no body")
	}
	envelope := requestEnvelope{kind: kind, token: string(payload[start:tokenEnd])}
	if version == legacyTraceEnvelopeVersion || version == fieldTraceEnvelopeVersion || version == tracedRequestEnvelopeVersion {
		traceID, bodyStart, err := decodeTraceID(payload, tokenEnd)
		if err != nil {
			return requestEnvelope{}, err
		}
		envelope.traceID = traceID
		if version == fieldTraceEnvelopeVersion || version == tracedRequestEnvelopeVersion {
			fieldPath, next, pathErr := decodeTraceFieldPath(payload, bodyStart)
			if pathErr != nil {
				return requestEnvelope{}, pathErr
			}
			envelope.fieldPath = fieldPath
			bodyStart = next
		}
		if version == tracedRequestEnvelopeVersion {
			if bodyStart >= len(payload) || payload[bodyStart] > 1 {
				return requestEnvelope{}, fmt.Errorf("rpc: invalid database trace detail flag")
			}
			envelope.databaseDetails = payload[bodyStart] == 1
			bodyStart++
		}
		if bodyStart >= len(payload) {
			return requestEnvelope{}, fmt.Errorf("rpc: request envelope has no body")
		}
		envelope.body = payload[bodyStart:]
		return envelope, nil
	}
	envelope.body = payload[tokenEnd:]
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
// Returns the payload bytes. Reuses buf if large enough.
func ReadFrame(r io.Reader, buf []byte) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("rpc: frame too large: %d bytes (max %d)", size, maxFrameSize)
	}
	if int(size) > len(buf) {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
