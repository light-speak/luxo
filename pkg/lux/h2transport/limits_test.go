package h2transport

import (
	"net/http"
	"testing"
)

func TestExplicitLimitsValidation(t *testing.T) {
	for _, change := range []func(*Limits){
		func(l *Limits) { l.MaxRequestBodyBytes = 0 },
		func(l *Limits) { l.MaxResponseBodyBytes = 0 },
		func(l *Limits) { l.MaxRequestBodyBytes = int(l.MaxBufferedBytes) + 1 },
		func(l *Limits) { l.MaxResponseBodyBytes = int(l.MaxBufferedBytes) + 1 },
		func(l *Limits) { l.MaxConnections = 0 }, func(l *Limits) { l.MaxConnections = 65536 },
		func(l *Limits) { l.MaxInFlight = 0 }, func(l *Limits) { l.MaxInFlight = 65536 },
		func(l *Limits) { l.MaxBufferedBytes = 1 }, func(l *Limits) { l.MaxBufferedBytes = 1 << 41 },
		func(l *Limits) { l.ControlFramesPerSecond = 0 }, func(l *Limits) { l.HandshakeTimeout = 0 },
		func(l *Limits) { l.BodyTimeout = 0 }, func(l *Limits) { l.ResponseTimeout = 0 },
		func(l *Limits) { l.IdleTimeout = 0 }, func(l *Limits) { l.WriteTimeout = 0 },
	} {
		limits := DefaultLimits()
		change(&limits)
		if _, err := NewServerWithLimits(http.NotFoundHandler(), limits); err == nil {
			t.Fatal("invalid limits accepted", limits)
		}
	}
}
