package rpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	stderrors "errors"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// MaxRPCConnections limits concurrent RPC connections to prevent goroutine exhaustion.
const MaxRPCConnections = 10000

// Server handles internal RPC connections using Luxo binary protocol.
// Dispatches requests to the same handlers as the HTTP router.
type Server struct {
	router                 *api.Router
	handlers               map[string]api.HandlerFunc
	registry               *api.APIRegistry
	internalRequestContext func(context.Context, string) (context.Context, error)
	listener               net.Listener
	wg                     sync.WaitGroup
	done                   chan struct{}
	sem                    chan struct{} // connection semaphore
	connMu                 sync.Mutex
	connections            map[net.Conn]struct{}
	closing                bool
	closeOnce              sync.Once
}

// NewServer creates an RPC server that shares handlers with the HTTP router.
func NewServer(router *api.Router) *Server {
	return &Server{
		router:                 router,
		handlers:               router.ExportHandlers(),
		registry:               router.Registry,
		internalRequestContext: router.InternalRequestContext,
		done:                   make(chan struct{}),
		sem:                    make(chan struct{}, MaxRPCConnections),
		connections:            make(map[net.Conn]struct{}),
	}
}

// ListenAndServe starts the RPC server on the given TCP address.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.connMu.Lock()
	if s.closing {
		s.connMu.Unlock()
		ln.Close()
		return nil
	}
	s.listener = ln
	s.connMu.Unlock()
	fmt.Printf("  Luxo RPC on %s\n", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
			}
			continue
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		// Limit concurrent connections via semaphore
		select {
		case s.sem <- struct{}{}:
		default:
			conn.Close() // at capacity, reject
			continue
		}
		if !s.registerConnection(conn) {
			<-s.sem
			continue
		}
		go func() {
			defer func() { <-s.sem }()
			s.handleConn(conn)
		}()
	}
}

// Close stops accepting new connections and waits for active ones to finish.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.connMu.Lock()
		s.closing = true
		if s.listener != nil {
			s.listener.Close()
		}
		for conn := range s.connections {
			conn.Close()
		}
		s.connMu.Unlock()
	})
	s.wg.Wait()
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer s.unregisterConnection(conn)
	defer conn.Close()

	var readBuf []byte
	for {
		// Read request frame
		payload, err := ReadFrame(conn, readBuf)
		if err != nil {
			if err != io.EOF {
				// Connection error — close
			}
			return
		}
		readBuf = payload[:0] // reuse buffer
		envelope, envelopeErr := decodeRequestEnvelope(payload)
		if envelopeErr != nil {
			if WriteFrame(conn, encodeError(400, "BadRequest", envelopeErr.Error())) != nil {
				return
			}
			continue
		}
		if envelope.kind == requestKindStream {
			s.processStream(conn, envelope)
			return
		}

		if err := s.processCall(conn, envelope); err != nil {
			return
		}
	}
}

func (s *Server) registerConnection(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closing {
		conn.Close()
		return false
	}
	s.connections[conn] = struct{}{}
	s.wg.Add(1)
	return true
}

func (s *Server) unregisterConnection(conn net.Conn) {
	s.connMu.Lock()
	delete(s.connections, conn)
	s.connMu.Unlock()
}

func (s *Server) processRequest(conn io.Writer, payload []byte) error {
	envelope, err := decodeRequestEnvelope(payload)
	if err != nil {
		return WriteFrame(conn, encodeError(400, "BadRequest", err.Error()))
	}
	if envelope.kind != requestKindCall {
		return WriteFrame(conn, encodeError(400, "BadRequest", "stream request sent to unary dispatcher"))
	}
	return s.processCall(conn, envelope)
}

func (s *Server) processCall(conn io.Writer, envelope requestEnvelope) error {
	var ctx context.Context = context.Background()
	var trace *api.DebugTraceSession
	if envelope.traceID != "" {
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = requestCtx
		ctx, trace = api.WithRemoteTrace(ctx, envelope.traceID, envelope.fieldPath, envelope.databaseDetails)
	}
	authStarted := trace.Start()
	ctx, err := s.requestContext(ctx, envelope.token)
	trace.Finish(authStarted, api.DebugSpanMeta{Name: "service.authenticate", Category: "service"})
	if err != nil {
		return writeCallError(conn, 401, "Unauthorized", err.Error(), trace)
	}
	req := api.GetRequest()
	defer api.PutRequest(req)
	decodeStarted := trace.Start()
	if parseErr := s.registry.ParseBinaryRequestInto(envelope.body, req); parseErr != nil {
		trace.Finish(decodeStarted, api.DebugSpanMeta{Name: "service.decode", Category: "service"})
		return writeCallError(conn, 400, "BadRequest", parseErr.Error(), trace)
	}
	trace.Finish(decodeStarted, api.DebugSpanMeta{Name: "service.decode", Category: "service", Operation: req.API})
	req.Internal = true

	// Dispatch to handler
	fn, ok := s.handlers[req.API]
	if !ok {
		return writeCallError(conn, 404, "NotFound", "handler not found: "+req.API, trace)
	}

	buf := api.GetBuf()
	// Reserve 1 byte for status prefix — handler appends after it
	buf.B = append(buf.B, statusOK)
	req.Buf = buf

	metrics := s.router.RequestMetricsRecorder()
	var handlerStarted time.Time
	if metrics != nil {
		handlerStarted = time.Now()
	}
	handlerCtx, handlerSpan := trace.StartSpan(ctx, api.DebugSpanMeta{Name: "service.handler", Category: "service", Operation: req.API})
	herr := s.callHandler(handlerCtx, fn, req)
	trace.FinishSpan(handlerSpan)
	if metrics != nil {
		metrics.Record(req.API, time.Since(handlerStarted), herr != nil)
	}
	if herr != nil {
		api.PutBuf(buf)
		var appErr *luxerrors.AppError
		if stderrors.As(herr, &appErr) {
			return writeCallError(conn, appErr.Code, appErr.Name, appErr.Message, trace)
		}
		return writeCallError(conn, 500, "Internal", herr.Error(), trace)
	}

	// Zero-copy: write [frame header][statusOK + handler payload] directly from pooled buf
	if trace == nil {
		err = WriteFrame(conn, buf.B)
	} else {
		err = writeTracedFrame(conn, statusTraceOK, buf.B[1:], trace)
	}
	api.PutBuf(buf)
	return err
}

func (s *Server) processStream(conn net.Conn, envelope requestEnvelope) {
	ctx, err := s.requestContext(context.Background(), envelope.token)
	if err != nil {
		WriteFrame(conn, encodeError(401, "Unauthorized", err.Error()))
		return
	}
	req := api.GetRequest()
	defer api.PutRequest(req)
	if err := s.registry.ParseBinaryRequestInto(envelope.body, req); err != nil {
		WriteFrame(conn, encodeError(400, "BadRequest", err.Error()))
		return
	}
	req.Internal = true
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := s.router.OpenInternalStream(streamCtx, req)
	if err != nil {
		WriteFrame(conn, encodeError(400, "BadRequest", err.Error()))
		return
	}
	defer stream.Close()
	if err := writeStatusFrame(conn, statusOK, nil); err != nil {
		return
	}

	disconnected := make(chan struct{})
	go func() {
		var probe [1]byte
		conn.Read(probe[:])
		close(disconnected)
	}()
	for {
		select {
		case data, ok := <-stream.Messages():
			if !ok || writeStatusFrame(conn, statusStream, data) != nil {
				return
			}
		case <-disconnected:
			return
		case <-s.done:
			return
		}
	}
}

func (s *Server) requestContext(ctx context.Context, token string) (context.Context, error) {
	if token == "" {
		return ctx, nil
	}
	if s.internalRequestContext == nil {
		return nil, fmt.Errorf("RPC bearer verification is not configured")
	}
	return s.internalRequestContext(ctx, token)
}

// callHandler calls the handler with panic recovery so a panicking handler
// does not crash the connection goroutine.
func (s *Server) callHandler(ctx context.Context, fn api.HandlerFunc, req *api.Request) (herr error) {
	defer func() {
		if p := recover(); p != nil {
			herr = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(ctx, req)
}

func encodeError(code int, name, message string) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(1, int64(code))
	enc.WriteFieldString(2, name)
	enc.WriteFieldString(3, message)
	enc.WriteEnd()

	resp := make([]byte, 0, 1+len(enc.Bytes()))
	resp = append(resp, statusError)
	resp = append(resp, enc.Bytes()...)
	return resp
}

func writeCallError(w io.Writer, code int, name, message string, trace *api.DebugTraceSession) error {
	encoded := encodeError(code, name, message)
	if trace == nil {
		return WriteFrame(w, encoded)
	}
	return writeTracedFrame(w, statusTraceError, encoded[1:], trace)
}

func writeTracedFrame(w io.Writer, status byte, body []byte, trace *api.DebugTraceSession) error {
	encodedTrace := api.EncodeDebugTrace(trace.Snapshot())
	payload := make([]byte, 0, 1+10+len(encodedTrace)+len(body))
	payload = append(payload, status)
	payload = codec.AppendVarint(payload, uint64(len(encodedTrace)))
	payload = append(payload, encodedTrace...)
	payload = append(payload, body...)
	return WriteFrame(w, payload)
}
