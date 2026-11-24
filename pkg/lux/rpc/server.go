package rpc

import (
	"context"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/light-speak/luxo/internal/panicerror"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// MaxRPCConnections limits concurrent RPC connections to prevent goroutine exhaustion.
const MaxRPCConnections = 10000

// This private sentinel is replaced by the frame length before network I/O.
const handlerResponsePrefix = 0x4c525043

var errHandlerResponsePrefix = stderrors.New("rpc: handler overwrote the response buffer or its transport prefix")
var errHandlerEmptyResponse = luxerrors.InternalError.WithCause(stderrors.New("rpc: handler did not write its declared response value"))
var errBearerVerifierNotConfigured = stderrors.New("RPC bearer verification is not configured")
var errBearerVerifierMissingContext = stderrors.New("RPC bearer verifier returned no context")

// Server handles internal RPC connections using Luxo binary protocol.
// Dispatches both public API and RPC-only handlers from a startup snapshot.
type Server struct {
	ctx                    context.Context
	cancel                 context.CancelFunc
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
	readObservers          socketObserverPool
	readObserversStarted   bool
	closing                bool
}

// NewServer exports public and internal handlers after registration is complete.
func NewServer(router *api.Router) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:                    ctx,
		cancel:                 cancel,
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
	if err := s.router.Prepare(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Printf("  Luxo RPC on %s\n", ln.Addr())
	return s.Serve(ln)
}

// Serve accepts RPC connections from an already allocated listener.
// It owns and closes the listener, including when the server is closed or busy.
// Only one listener may serve at a time. Close makes this server permanently closed.
// A permanent Accept error is returned; Close causes Serve to return nil.
func (s *Server) Serve(ln net.Listener) error {
	if ln == nil {
		return fmt.Errorf("rpc: nil listener")
	}
	defer ln.Close()
	if err := s.router.Prepare(); err != nil {
		return err
	}
	s.connMu.Lock()
	if s.closing {
		s.connMu.Unlock()
		return nil
	}
	if s.listener != nil {
		s.connMu.Unlock()
		return fmt.Errorf("rpc: server is already serving")
	}
	s.listener = ln
	s.connMu.Unlock()
	defer func() {
		s.connMu.Lock()
		s.listener = nil
		s.connMu.Unlock()
	}()
	return s.acceptConnections(ln)
}

func (s *Server) acceptConnections(ln net.Listener) error {
	var retryDelay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			retry, serveErr := s.retryAccept(err, &retryDelay)
			if !retry {
				return serveErr
			}
			continue
		}
		retryDelay = 0
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

// Back off only transient listener failures; shutdown must interrupt the wait.
func (s *Server) retryAccept(err error, delay *time.Duration) (bool, error) {
	select {
	case <-s.done:
		return false, nil
	default:
	}
	temporary, ok := err.(interface{ Temporary() bool })
	if !ok || !temporary.Temporary() {
		return false, err
	}
	if *delay == 0 {
		*delay = 5 * time.Millisecond
	} else {
		*delay = min(*delay*2, time.Second)
	}
	timer := time.NewTimer(*delay)
	defer timer.Stop()
	select {
	case <-s.done:
		return false, nil
	case <-timer.C:
		return true, nil
	}
}

// Close stops accepting new connections and waits for active ones to finish.
func (s *Server) Close() {
	s.stopAccepting()
	s.wg.Wait()
	s.closeReadObservers()
}

func (s *Server) stopAccepting() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closing {
		return
	}
	s.closing = true
	s.cancel()
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	for conn := range s.connections {
		conn.Close()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer s.unregisterConnection(conn)
	if err := acceptHandshake(conn); err != nil {
		conn.Close()
		return
	}

	s.serveConnection(conn)

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
	return s.processCallContext(s.ctx, conn, envelope)
}

func (s *Server) processCallContext(ctx context.Context, conn io.Writer, envelope requestEnvelope) error {
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
		return writeEncodedError(conn, encodeAuthenticationError(err), trace)
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
	defer api.PutBuf(buf)
	// Reserve the frame header and status; the handler appends its body once.
	buf.B = append(buf.B, 'L', 'R', 'P', 'C', statusOK)
	req.Buf = buf

	metrics := s.router.RequestMetricsRecorder()
	var handlerStarted time.Time
	recorded := false
	if metrics != nil {
		handlerStarted = time.Now()
		defer func() {
			if !recorded {
				metrics.Record(req.API, time.Since(handlerStarted), true)
			}
		}()
	}
	var herr error
	if trace == nil {
		herr = s.callHandler(ctx, fn, req)
	} else {
		herr = s.callTracedHandler(ctx, fn, req, trace)
	}
	if herr == nil {
		if req.Buf != buf || len(buf.B) < 5 || binary.BigEndian.Uint32(buf.B[:4]) != handlerResponsePrefix || buf.B[4] != statusOK {
			herr = errHandlerResponsePrefix
		} else if s.missingResponseValue(req, buf) {
			herr = errHandlerEmptyResponse
		}
	}
	if metrics != nil {
		recorded = true
		metrics.Record(req.API, time.Since(handlerStarted), herr != nil)
	}
	if herr != nil {
		return writeApplicationError(conn, normalizeHandlerError(herr, false), trace)
	}

	// Zero-copy: write [frame header][statusOK + handler payload] directly from pooled buf
	if trace == nil {
		err = writePreparedFrame(conn, buf.B)
	} else {
		err = writeTracedFrame(conn, statusTraceOK, buf.B[5:], trace)
	}
	return err
}

func (s *Server) callTracedHandler(ctx context.Context, fn api.HandlerFunc, req *api.Request, trace *api.DebugTraceSession) error {
	ctx, span := trace.StartSpan(ctx, api.DebugSpanMeta{Name: "service.handler", Category: "service", Operation: req.API})
	defer trace.FinishSpan(span)
	return s.callHandler(ctx, fn, req)
}

func (s *Server) missingResponseValue(req *api.Request, buf *api.ResponseBuf) bool {
	if len(buf.B) != 5 || s.router.Schema == nil {
		return false
	}
	definition := s.router.Schema.APIs[req.API]
	return definition != nil && definition.ReturnType != ""
}

func (s *Server) processStream(parent context.Context, conn net.Conn, envelope requestEnvelope) {
	ctx, err := s.requestContext(parent, envelope.token)
	if err != nil {
		WriteFrame(conn, encodeAuthenticationError(err))
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
	stream, err := s.router.PrepareInternalStream(streamCtx, req)
	if err != nil {
		writeApplicationError(conn, normalizeHandlerError(err, false), nil)
		return
	}
	defer stream.Close()
	if err := writeStatusFrame(conn, statusOK, nil); err != nil {
		return
	}
	stream.Start()
	s.pumpInternalStream(streamCtx, conn, stream)
}

func (s *Server) pumpInternalStream(ctx context.Context, conn net.Conn, stream *api.InternalStream) {
	for {
		select {
		case data, ok := <-stream.Messages():
			if !ok {
				writeStreamResult(conn, stream.Err())
				return
			}
			if writeStatusFrame(conn, statusStream, data) != nil {
				return
			}
		case <-ctx.Done():
			return
		case <-s.done:
			return
		}
	}
}

func writeStreamResult(w io.Writer, err error) error {
	if err == nil {
		return writeStatusFrame(w, statusStreamComplete, nil)
	}
	return writeApplicationError(w, normalizeHandlerError(err, true), nil)
}

func (s *Server) requestContext(ctx context.Context, token string) (context.Context, error) {
	if token == "" {
		return ctx, nil
	}
	return s.verifyRequestContext(ctx, token)
}

func (s *Server) verifyRequestContext(ctx context.Context, token string) (verified context.Context, err error) {
	verify := s.internalRequestContext
	if verify == nil {
		return nil, errBearerVerifierNotConfigured
	}
	defer func() {
		if recover() != nil {
			verified, err = nil, errAuthenticationPanic
		}
	}()
	verified, err = verify(ctx, token)
	if err == nil && verified == nil {
		return nil, errBearerVerifierMissingContext
	}
	return verified, err
}

// callHandler calls the handler with panic recovery so a panicking handler
// does not crash the connection goroutine.
func (s *Server) callHandler(ctx context.Context, fn api.HandlerFunc, req *api.Request) (herr error) {
	defer func() {
		if p := recover(); p != nil {
			herr = panicerror.New("panic: ", p)
		}
	}()
	return fn(ctx, req)
}

func writeCallError(w io.Writer, code int, name, message string, trace *api.DebugTraceSession) error {
	encoded := encodeError(code, name, message)
	return writeEncodedError(w, encoded, trace)
}

func writeEncodedError(w io.Writer, encoded []byte, trace *api.DebugTraceSession) error {
	if trace == nil {
		return WriteFrame(w, encoded)
	}
	return writeTracedFrame(w, statusTraceError, encoded[1:], trace)
}

func writeTracedFrame(w io.Writer, status byte, body []byte, trace *api.DebugTraceSession) error {
	encodedTrace := api.EncodeDebugTraceResult(trace.Snapshot())
	payload := make([]byte, 0, 1+10+len(encodedTrace)+len(body))
	payload = append(payload, status)
	payload = codec.AppendVarint(payload, uint64(len(encodedTrace)))
	payload = append(payload, encodedTrace...)
	payload = append(payload, body...)
	return WriteFrame(w, payload)
}
