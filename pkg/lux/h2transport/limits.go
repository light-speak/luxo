package h2transport

import (
	"errors"
	"github.com/light-speak/luxo/pkg/lux/api"
	"time"
)

// Limits are explicit per-server bounds. Buffered bytes exclude application
// allocations, metadata, TLS/kernel buffers and the runtime heap.
type Limits struct {
	MaxRequestBodyBytes, MaxResponseBodyBytes                                 int
	MaxConnections, MaxInFlight, ControlFramesPerSecond                       int
	MaxBufferedBytes                                                          int64
	HandshakeTimeout, BodyTimeout, ResponseTimeout, IdleTimeout, WriteTimeout time.Duration
}

// DefaultLimits are benchmark candidate settings, not production sizing advice.
func DefaultLimits() Limits {
	return Limits{MaxRequestBodyBytes: api.MaxRequestBodySize, MaxResponseBodyBytes: api.DefaultResponseBodySize, MaxConnections: 128, MaxInFlight: 256, MaxBufferedBytes: 64 << 20, ControlFramesPerSecond: 1024,
		HandshakeTimeout: 5 * time.Second, BodyTimeout: 10 * time.Second, ResponseTimeout: 30 * time.Second,
		IdleTimeout: 30 * time.Second, WriteTimeout: 5 * time.Second}
}

func (l Limits) validate() error {
	if l.MaxRequestBodyBytes < 1 || l.MaxResponseBodyBytes < 1 ||
		int64(l.MaxRequestBodyBytes) > l.MaxBufferedBytes || int64(l.MaxResponseBodyBytes) > l.MaxBufferedBytes ||
		l.MaxBufferedBytes > int64(^uint(0)>>1) {
		return errors.New("invalid HTTP/2 body or buffer limits")
	}
	if l.MaxConnections < 1 || l.MaxConnections > 65535 || l.MaxInFlight < 1 || l.MaxInFlight > 65535 ||
		l.MaxBufferedBytes > 1<<40 || l.ControlFramesPerSecond < 1 ||
		l.HandshakeTimeout <= 0 || l.BodyTimeout <= 0 || l.ResponseTimeout <= 0 || l.IdleTimeout <= 0 || l.WriteTimeout <= 0 {
		return errors.New("invalid experimental HTTP/2 resource limits")
	}
	return nil
}
