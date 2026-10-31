package rpc

import (
	"errors"
	"math/bits"
	"net"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

type callRequest struct {
	apiID        int
	token        string
	mask, params []byte
}

var errRequestFrameTooLarge = errors.New("rpc: request exceeds maximum frame size")

func (r callRequest) prepare(conn net.Conn) ([]byte, error) {
	size := 2 + varintSize(uint64(len(r.token))) + len(r.token) + callBodySize(r.apiID, r.mask, r.params)
	if size > maxFrameSize || size < 0 {
		return nil, errRequestFrameTooLarge
	}
	var dst []byte
	tracked, ok := conn.(*poolConn)
	if ok {
		dst = tracked.requestBuf
	}
	if cap(dst) < size+4 {
		dst = make([]byte, 4, size+4)
	} else {
		dst = dst[:4]
		clear(dst)
	}
	dst = appendEnvelopePrefix(dst, requestKindCall, 0, r.token)
	dst = appendCallBody(dst, r.apiID, r.mask, r.params)
	if ok {
		tracked.requestBuf = retainReadFrame(dst)
	}
	return dst, nil
}

func varintSize(value uint64) int { return (bits.Len64(value|1) + 6) / 7 }

func callBodySize(apiID int, mask, params []byte) int {
	return varintSize(uint64(apiID)) + varintSize(uint64(len(mask))) + len(mask) + max(1, len(params))
}

func appendCallBody(dst []byte, apiID int, mask, params []byte) []byte {
	dst = codec.AppendVarint(dst, uint64(apiID))
	dst = codec.AppendVarint(dst, uint64(len(mask)))
	dst = append(dst, mask...)
	if len(params) == 0 {
		return append(dst, 0)
	}
	return append(dst, params...)
}

func appendEnvelopePrefix(dst []byte, kind, flags byte, token string) []byte {
	dst = append(dst, kind, flags)
	dst = codec.AppendVarint(dst, uint64(len(token)))
	return append(dst, token...)
}

// Reserve the frame prefix and compose into one owned buffer. The writer
// fills the prefix after checking the final size; no body is encoded twice.
func prepareCallFrame(kind byte, token string, apiID int, mask, params []byte) []byte {
	size := 2 + varintSize(uint64(len(token))) + len(token) + callBodySize(apiID, mask, params)
	dst := make([]byte, 4, size+4)
	dst = appendEnvelopePrefix(dst, kind, 0, token)
	return appendCallBody(dst, apiID, mask, params)
}

func prepareTracedCallFrame(token, traceID, path string, details bool, apiID int, mask, params []byte) []byte {
	size := 3 + varintSize(uint64(len(token))) + len(token) + callBodySize(apiID, mask, params)
	size += varintSize(uint64(len(traceID))) + len(traceID) + varintSize(uint64(len(path))) + len(path)
	dst := make([]byte, 4, size+4)
	dst = appendEnvelopePrefix(dst, requestKindCall, requestFlagTraced, token)
	dst = codec.AppendVarint(dst, uint64(len(traceID)))
	dst = append(dst, traceID...)
	dst = codec.AppendVarint(dst, uint64(len(path)))
	dst = append(dst, path...)
	flag := byte(0)
	if details {
		flag = 1
	}
	dst = append(dst, flag)
	return appendCallBody(dst, apiID, mask, params)
}
