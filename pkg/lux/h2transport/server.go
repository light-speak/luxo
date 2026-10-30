package h2transport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server implements bounded HTTP/2 unary transport for Luxo handlers.
// HTTP/1.1 upgrades and WebSocket connections need a separate HTTP/1.1 handler.
// Gateway integration and production acceptance are separate from this engine.
type Server struct {
	limits      Limits
	budget      byteBudget
	streams     byteBudget
	handler     http.Handler
	mu          sync.Mutex
	listener    net.Listener
	connections map[net.Conn]*serverConnection
	workers     sync.WaitGroup
	closed      bool
}

type serverConnection struct {
	cancel context.CancelFunc
	active *candidateConnection
}

func NewServer(handler http.Handler) (*Server, error) {
	return NewServerWithLimits(handler, DefaultLimits())
}

func NewServerWithLimits(handler http.Handler, limits Limits) (*Server, error) {
	if handler == nil {
		return nil, errors.New("HTTP/2 candidate requires a handler")
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &Server{handler: handler, limits: limits, budget: byteBudget{limit: limits.MaxBufferedBytes}, streams: byteBudget{limit: int64(limits.MaxInFlight)}, connections: make(map[net.Conn]*serverConnection)}, nil
}

// ServeTLS retains the standard library handshake and requires h2 ALPN.
// The caller owns certificates and configuration; neither is mutated.
func (s *Server) ServeTLS(listener net.Listener, config *tls.Config) error {
	if config == nil || config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS12 {
		return errors.New("HTTP/2 requires a TLS configuration supporting TLS 1.2 or later")
	}
	if config.GetConfigForClient != nil {
		return errors.New("experimental HTTP/2 does not support replacing TLS configuration during handshake")
	}
	cloned := config.Clone()
	cloned.MinVersion = max(cloned.MinVersion, tls.VersionTLS12)
	cloned.NextProtos = []string{"h2"}
	return s.Serve(tls.NewListener(listener, cloned))
}

func (s *Server) Serve(listener net.Listener) error {
	s.mu.Lock()
	if s.closed || s.listener != nil {
		s.mu.Unlock()
		_ = listener.Close()
		return net.ErrClosed
	}
	s.listener = listener
	s.mu.Unlock()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		ctx, cancel, err := s.admitConnection(context.Background(), conn)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			continue
		}
		go s.runConnection(ctx, cancel, conn)
	}
}

// ServeConn takes ownership of one accepted HTTP/2 connection and blocks until
// it ends. Connections share this server's admission, buffer and shutdown bounds.
// The caller must not have consumed the HTTP/2 connection preface.
func (s *Server) ServeConn(ctx context.Context, conn net.Conn) error {
	if ctx == nil || conn == nil {
		if conn != nil {
			_ = conn.Close()
		}
		return errors.New("HTTP/2 requires a context and connection")
	}
	ctx, cancel, err := s.admitConnection(ctx, conn)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Time{}); err != nil {
		cancel()
		_ = conn.Close()
		s.mu.Lock()
		delete(s.connections, conn)
		s.mu.Unlock()
		s.workers.Done()
		return err
	}
	return s.runConnection(ctx, cancel, conn)
}

var errConnectionLimit = errors.New("HTTP/2 connection limit reached")

func (s *Server) admitConnection(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	switch {
	case s.closed:
		err = net.ErrClosed
	case parent.Err() != nil:
		err = parent.Err()
	case len(s.connections) >= s.limits.MaxConnections:
		err = errConnectionLimit
	}
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s.connections[conn] = &serverConnection{cancel: cancel}
	s.workers.Add(1)
	return ctx, cancel, nil
}

func (s *Server) runConnection(ctx context.Context, cancel context.CancelFunc, conn net.Conn) error {
	defer s.workers.Done()
	defer func() {
		cancel()
		_ = conn.Close()
		s.mu.Lock()
		delete(s.connections, conn)
		s.mu.Unlock()
	}()
	var tlsState *tls.ConnectionState
	if secured, ok := conn.(*tls.Conn); ok {
		handshake, stop := context.WithTimeout(ctx, s.limits.HandshakeTimeout)
		err := secured.HandshakeContext(handshake)
		stop()
		if err != nil || secured.ConnectionState().NegotiatedProtocol != "h2" {
			return errors.Join(err, errors.New("HTTP/2 TLS negotiation failed"))
		}
		state := secured.ConnectionState()
		tlsState = &state
	}
	c := newCandidateConnection(ctx, conn, s.handler)
	c.budget, c.receiver.budget = &s.budget, &s.budget
	c.slots = &s.streams
	c.limits = s.limits
	c.receiver.maxBody = s.limits.MaxRequestBodyBytes
	c.receiver.limit = int(s.limits.MaxBufferedBytes)
	c.configureReceiveWindows()
	c.writer.data.queue.limit = int(s.limits.MaxBufferedBytes)
	c.rate.burst = s.limits.ControlFramesPerSecond
	c.tlsState = tlsState
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.connections[conn].active = c
	s.mu.Unlock()
	return c.run()
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for conn, entry := range s.connections {
		entry.cancel()
		_ = conn.Close()
	}
	return nil
}

// Shutdown stops admission and drains accepted streams. A deadline forces
// cancellation; application handlers must honor context cancellation to exit.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for conn, entry := range s.connections {
		if entry.active == nil {
			entry.cancel()
			_ = conn.Close()
		} else if err := entry.active.beginDrain(); err != nil {
			entry.cancel()
			_ = conn.Close()
		}
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = s.Close()
		return ctx.Err()
	}
}
