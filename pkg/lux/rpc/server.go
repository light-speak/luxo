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
// Dispatches both public API and RPC-only handlers from a startup snapshot.
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

// NewServer exports public and internal handlers after registration is complete.
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
	if err := acceptHandshake(conn); err != nil {
		return
	}

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
		readBuf = retainReadFrame(payload)
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
	// Reserve the frame header and status; the handler appends its body once.
	buf.B = append(buf.B, 0, 0, 0, 0, statusOK)
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
		if appErr, ok := stderrors.AsType[*luxerrors.AppError](herr); ok {
			return writeApplicationError(conn, appErr, trace)
		}
		return writeCallError(conn, 500, "Internal", herr.Error(), trace)
	}

	// Zero-copy: write [frame header][statusOK + handler payload] directly from pooled buf
	if trace == nil {
		err = writePreparedFrame(conn, buf.B)
	} else {
		err = writeTracedFrame(conn, statusTraceOK, buf.B[5:], trace)
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
	stream, err := s.router.PrepareInternalStream(streamCtx, req)
	if err != nil {
		if appErr, ok := stderrors.AsType[*luxerrors.AppError](err); ok {
			writeApplicationError(conn, appErr, nil)
			return
		}
		writeApplicationError(conn, luxerrors.Wrap(err), nil)
		return
	}
	defer stream.Close()
	if err := writeStatusFrame(conn, statusOK, nil); err != nil {
		return
	}
	stream.Start()
	s.pumpInternalStream(conn, stream)
}

func (s *Server) pumpInternalStream(conn net.Conn, stream *api.InternalStream) {
	disconnected := make(chan struct{})
	go func() {
		var probe [1]byte
		conn.Read(probe[:])
		close(disconnected)
	}()
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
		case <-disconnected:
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
	if appErr, ok := stderrors.AsType[*luxerrors.AppError](err); ok {
		return writeApplicationError(w, appErr, nil)
	}
	if stderrors.Is(err, api.ErrStreamFull) {
		return writeCallError(w, 429, "SlowConsumer", "stream subscriber is too slow", nil)
	}
	return writeApplicationError(w, luxerrors.InternalError, nil)
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
