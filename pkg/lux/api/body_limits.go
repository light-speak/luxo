package api

import "errors"

// BodyLimits bounds public HTTP bodies and individual WebSocket messages.
// Zero selects the finite default. These are limits, not preallocations.
// Configure before serving; returned snapshots are independent values.
type BodyLimits struct {
	RequestBytes  int
	ResponseBytes int
	// ReceiveBytesTotal bounds active HTTP/WS raw buffers across this Router.
	// It excludes idle pools, transport buffers and decoded application objects.
	ReceiveBytesTotal int64
}

const DefaultResponseBodySize = 2 << 20

var errRequestBodyTooLarge = errors.New("request body exceeds size limit")
var errInvalidBodyLimits = errors.New("body limits must be positive and leave room for overflow detection")

func normalizeBodyLimits(l BodyLimits) BodyLimits {
	if l.ReceiveBytesTotal == 0 {
		l.ReceiveBytesTotal = 64 << 20
	}
	if l.RequestBytes == 0 {
		l.RequestBytes = MaxRequestBodySize
	}
	if l.ResponseBytes == 0 {
		l.ResponseBytes = DefaultResponseBodySize
	}
	return l
}

func (l BodyLimits) validate() error {
	maximum := int(^uint(0) >> 1)
	if l.RequestBytes <= 0 || l.RequestBytes >= maximum || l.ResponseBytes <= 0 || l.ResponseBytes >= maximum || l.ReceiveBytesTotal < int64(l.RequestBytes)+1 {
		return errInvalidBodyLimits
	}
	return nil
}

func (rt *Router) BodyLimits() BodyLimits { return rt.bodyLimits }

func (rt *Router) responseBodyLimit() int {
	if rt.bodyLimits.ResponseBytes == 0 {
		return DefaultResponseBodySize
	}
	return rt.bodyLimits.ResponseBytes
}

func responseBodySize(binary, json []byte, binaryMode bool) int {
	if binaryMode {
		return len(binary)
	}
	return len(json) + len(jsonDataPrefix) + len(jsonDataSuffix)
}
