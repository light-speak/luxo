// Package rpc provides Luxo's internal RPC protocol for microservice communication.
// Uses a minimal framed TCP protocol with Luxo binary encoding — no HTTP overhead.
//
// Frame format:
//
//	[4 bytes: payload length, big-endian uint32]
//	[payload bytes]
//
// Request payload:
//
//	[API ID varint] [field mask len varint] [field mask bytes] [params binary (fieldID+value pairs, 0x00 terminator)]
//
// Response payload:
//
//	[status 1 byte: 0x00=ok, 0x01=error]
//	[body bytes]
//
// Error body (status=0x01):
//
//	[error code svarint] [error name string] [error message string]
package rpc

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	statusOK     = 0x00
	statusError  = 0x01
	maxFrameSize = 4 << 20 // 4MB max frame
)

// WriteFrame writes a length-prefixed frame to w.
func WriteFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
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
