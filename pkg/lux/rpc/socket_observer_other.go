//go:build !linux

package rpc

import "io"

type socketObservation struct{}
type socketObserverPool struct{}

func (*Server) closeReadObservers() {}

func (*connectionReader) noteExchange()               {}
func (*connectionReader) closeObservation()           {}
func (*connectionReader) hasBufferedInput() bool      { return false }
func (r *connectionReader) observePeer(bool)          { r.ready.Signal() }
func (r *connectionReader) responseWriter() io.Writer { return r.conn }
