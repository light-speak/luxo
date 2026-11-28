package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/env"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

var errEmptyStreamPayload = errors.New("rpc: empty stream data payload")

// Client sends RPC requests to a remote Luxo service.
// Uses connection pooling for high throughput.
type Client struct {
	name string
	pool *Pool
}

// TraceTarget describes the logical operation and selected field behind an RPC call.
type TraceTarget struct {
	Operation  string
	Field      string
	FieldPath  string
	Selection  string
	Dependency api.TraceDependency
}

const (
	subscriptionActive uint32 = iota
	subscriptionFinishing
	subscriptionResultPublished
)

// Subscription is a dedicated server-streaming RPC connection.
type Subscription struct {
	messages  chan []byte
	errors    chan error
	conn      net.Conn
	cancel    context.CancelFunc
	stop      func() bool
	closeOnce sync.Once
	terminal  atomic.Uint32
	result    error // published by terminal; immutable afterward
}

// Messages returns canonical Luxo binary stream payloads.
func (s *Subscription) Messages() <-chan []byte {
	return s.messages
}

// Errors reports terminal failure and then closes. Drain Messages first when
// ordering matters; selecting Errors alongside Messages can skip buffered data.
func (s *Subscription) Errors() <-chan error {
	return s.errors
}

// Err returns the terminal result without consuming it. Drain Messages before
// interpreting nil as successful completion; an active stream also returns nil.
func (s *Subscription) Err() error {
	if s.terminal.Load() == subscriptionResultPublished {
		return s.result
	}
	return nil
}

// Close cancels the subscription and closes its dedicated connection.
func (s *Subscription) Close() {
	s.cancel()
	s.closeConnection()
}

func (s *Subscription) closeConnection() {
	// Wait for the first Close to finish releasing the pool's open slot.
	s.closeOnce.Do(func() { s.conn.Close() })
}

// NewClient creates an RPC client for the given service address.
func NewClient(addr string) *Client {
	return &Client{pool: NewPool(addr, 32)}
}

// NewNamedClient creates a client whose debug traces identify the target service.
func NewNamedClient(name, addr string) *Client {
	return &Client{name: name, pool: NewPool(addr, 32)}
}

// NewNamedClientWithOptions creates an LRPC client with explicit pool budgets.
func NewNamedClientWithOptions(name, addr string, options PoolOptions) (*Client, error) {
	pool, err := NewPoolWithOptions(addr, options)
	if err != nil {
		return nil, err
	}
	return &Client{name: name, pool: pool}, nil
}

// CheckReady verifies a fresh LRPC handshake within the shared connection
// budget, without invoking a business operation.
func (c *Client) CheckReady(ctx context.Context) error {
	return c.pool.checkReady(ctx)
}

// LoadGatewayClients loads an optional dotenv file and configures one named
// client and pool per target. Process values override dotenv values. Each
// default address can be overridden by the uppercase target's _SERVICE_ADDR key.
func LoadGatewayClients(envFile string, defaultAddresses map[string]string) (map[string]*Client, error) {
	if err := env.Load(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load %s: %w", envFile, err)
	}
	options, err := poolOptionsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("RPC pool configuration: %w", err)
	}
	clients := make(map[string]*Client, len(defaultAddresses))
	for name, fallback := range defaultAddresses {
		address := env.GetOrDefault(strings.ToUpper(name)+"_SERVICE_ADDR", fallback)
		clients[name] = &Client{name: name, pool: newPool(address, options)}
	}
	return clients, nil
}

// Call sends a binary-encoded request and returns the response body.
// apiID: from luxo.lock. params: pre-encoded binary (fieldID+value pairs + 0x00).
// Returns the response body (without status byte) and any error.
func (c *Client) Call(apiID int, params []byte) ([]byte, error) {
	return c.CallWithMask(apiID, nil, params)
}

// CallWithMask sends a request with field mask for field selection.
func (c *Client) CallWithMask(apiID int, fieldMask []byte, params []byte) ([]byte, error) {
	return c.callRequest(context.Background(), callRequest{apiID: apiID, mask: fieldMask, params: params})
}

// CallWithMaskContext sends an authenticated request and propagates cancellation
// and deadlines to the network operation.
func (c *Client) CallWithMaskContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte) ([]byte, error) {
	return c.CallWithMaskTargetContext(ctx, bearerToken, apiID, fieldMask, params, TraceTarget{})
}

// CallWithMaskTargetContext adds service and field attribution when detailed
// tracing is explicitly enabled on ctx. Normal requests carry no trace metadata.
func (c *Client) CallWithMaskTargetContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte, target TraceTarget) ([]byte, error) {
	trace := api.DebugTrace(ctx)
	if trace == nil {
		return c.callRequest(ctx, callRequest{apiID: apiID, token: bearerToken, mask: fieldMask, params: params})
	}
	traceID := api.TraceID(ctx)
	if traceID == "" {
		return c.callRequest(ctx, callRequest{apiID: apiID, token: bearerToken, mask: fieldMask, params: params})
	}
	return c.callWithTrace(ctx, bearerToken, apiID, fieldMask, params, target, trace, traceID)
}

// Keep optional trace assembly out of the ordinary call stack frame.
func (c *Client) callWithTrace(ctx context.Context, bearerToken string, apiID int, fieldMask, params []byte, target TraceTarget, trace *api.DebugTraceSession, traceID string) ([]byte, error) {
	fieldPath := target.FieldPath
	if fieldPath == "" {
		fieldPath = api.JoinTraceFieldPath(api.TraceFieldPath(ctx), target.Field)
	}
	if size := tracedCallFrameSize(bearerToken, traceID, fieldPath, apiID, fieldMask, params); size > maxFrameSize || size < 0 {
		return nil, errRequestFrameTooLarge
	}
	target.FieldPath = fieldPath
	_, callSpan := trace.StartSpan(ctx, api.DebugSpanMeta{
		Name: "service.call", Category: "service", Service: c.traceName(), Operation: target.Operation,
		Field: target.Field, FieldPath: fieldPath, Selection: target.Selection, Dependency: target.Dependency,
	})
	payload := prepareTracedCallFrame(bearerToken, traceID, fieldPath, trace.DatabaseDetails(), apiID, fieldMask, params)
	body, remote, err := c.callTracedPayload(ctx, payload)
	applyTraceDefaults(&remote, c.traceName(), target)
	trace.MergeSpan(callSpan, remote)
	trace.FinishSpan(callSpan)
	return body, err
}

// SubscribeContext opens a dedicated binary RPC stream. The connection is not
// returned to the unary pool because it remains owned by the subscription.
func (c *Client) SubscribeContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte) (*Subscription, error) {
	if size := callFrameSize(bearerToken, apiID, fieldMask, params); size > maxFrameSize || size < 0 {
		return nil, errRequestFrameTooLarge
	}
	conn, err := c.pool.getStreamContext(ctx)
	if err != nil {
		if err == ErrPoolExhausted {
			return nil, err
		}
		return nil, fmt.Errorf("rpc: dial %s: %w", c.pool.addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("rpc: stream deadline: %w", err)
		}
	}
	payload := prepareCallFrame(requestKindStream, bearerToken, apiID, fieldMask, params)
	streamCtx, cancel := context.WithCancel(ctx)
	subscription := &Subscription{
		messages: make(chan []byte, 64),
		errors:   make(chan error, 1),
		conn:     conn,
		cancel:   cancel,
	}
	subscription.stop = context.AfterFunc(streamCtx, subscription.closeConnection)
	if err := writePreparedFrame(conn, payload); err != nil {
		if contextErr := contextErrorAfterIO(ctx, err); contextErr != nil {
			err = contextErr
		}
		subscription.finish(fmt.Errorf("rpc: stream write: %w", err))
		return nil, err
	}
	ack, err := ReadFrame(conn, nil)
	if err != nil {
		if contextErr := contextErrorAfterIO(ctx, err); contextErr != nil {
			err = contextErr
		}
		subscription.finish(fmt.Errorf("rpc: stream ack: %w", err))
		return nil, err
	}
	if err := validateStreamAck(ack); err != nil {
		subscription.finish(err)
		return nil, err
	}
	go subscription.readLoop(streamCtx)
	return subscription, nil
}

func validateStreamAck(frame []byte) error {
	if len(frame) == 0 {
		return fmt.Errorf("rpc: empty stream acknowledgment")
	}
	if frame[0] == statusError {
		return decodeError(frame[1:])
	}
	if frame[0] != statusOK || len(frame) != 1 {
		return fmt.Errorf("rpc: invalid stream acknowledgment")
	}
	return nil
}

func (s *Subscription) readLoop(ctx context.Context) {
	for {
		frame, err := ReadFrame(s.conn, nil)
		if err != nil {
			if contextErr := contextErrorAfterIO(ctx, err); contextErr != nil {
				s.finish(contextErr)
			} else {
				s.finish(fmt.Errorf("rpc: stream read: %w", err))
			}
			return
		}
		if len(frame) == 0 {
			s.finish(fmt.Errorf("rpc: empty stream frame"))
			return
		}
		switch frame[0] {
		case statusStream:
			if len(frame) == 1 {
				s.finish(errEmptyStreamPayload)
				return
			}
			select {
			case s.messages <- frame[1:]:
			case <-ctx.Done():
				s.finish(ctx.Err())
				return
			}
		case statusError:
			s.finish(decodeError(frame[1:]))
			return
		case statusStreamComplete:
			var err error
			if len(frame) != 1 {
				err = fmt.Errorf("rpc: trailing stream completion data")
			}
			s.finish(err)
			return
		default:
			s.finish(fmt.Errorf("rpc: invalid stream frame status %d", frame[0]))
			return
		}
	}
}

func (s *Subscription) finish(err error) {
	if !s.terminal.CompareAndSwap(subscriptionActive, subscriptionFinishing) {
		return
	}
	s.result = err
	s.terminal.Store(subscriptionResultPublished)
	if s.stop != nil {
		s.stop()
	}
	s.cancel()
	s.closeConnection()
	if err != nil {
		s.errors <- err
	}
	close(s.messages)
	close(s.errors)
}

func (c *Client) callRequest(ctx context.Context, request callRequest) ([]byte, error) {
	reply, err := c.exchangeRequest(ctx, nil, &request)
	body, err := decodeCallResponse(reply.frame, err)
	c.releaseReply(reply.conn, err)
	return body, err
}

func decodeCallResponse(resp []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if len(resp) == 0 {
		return nil, fmt.Errorf("rpc: empty response")
	}

	status := resp[0]
	body := resp[1:]

	if status == statusError {
		return nil, decodeError(body)
	}
	if status != statusOK {
		return nil, fmt.Errorf("rpc: invalid unary response status %d", status)
	}

	return body, nil
}

// A complete frame is still leased until its caller validates the response.
// conn is nil if I/O, deadline cleanup, or cancellation closed the lease.
type exchangeReply struct {
	frame []byte
	conn  net.Conn
}

func (c *Client) exchangeRequest(ctx context.Context, payload []byte, request *callRequest) (exchangeReply, error) {
	// Get pooled connection
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return exchangeReply{}, fmt.Errorf("rpc: dial %s: %w", c.pool.addr, err)
	}
	if request != nil {
		payload, err = request.prepare(conn)
		if err != nil {
			c.pool.Put(conn)
			return exchangeReply{}, err
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return exchangeReply{}, fmt.Errorf("rpc: deadline: %w", err)
		}
	}
	var stopCancel func() bool
	if ctx.Done() != nil {
		stopCancel = context.AfterFunc(ctx, func() { conn.Close() })
	}
	closeConnection := func() {
		if stopCancel != nil {
			stopCancel()
		}
		conn.Close()
	}

	// Send request frame
	if err := writePreparedFrame(conn, payload); err != nil {
		closeConnection()
		if contextErr := contextErrorAfterIO(ctx, err); contextErr != nil {
			return exchangeReply{}, contextErr
		}
		return exchangeReply{}, fmt.Errorf("rpc: write: %w", err)
	}

	scratch := payload[:4]
	if request != nil {
		// Only frames prepared by this exchange have an owned request body.
		scratch = payload[:cap(payload)]
	}
	resp, err := readClientFrame(conn, scratch)
	if err != nil {
		closeConnection()
		if contextErr := contextErrorAfterIO(ctx, err); contextErr != nil {
			return exchangeReply{}, contextErr
		}
		return exchangeReply{}, fmt.Errorf("rpc: read: %w", err)
	}

	reusable := true
	if stopCancel != nil && !stopCancel() {
		reusable = false
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		reusable = false
	}
	if !reusable {
		conn.Close()
		return exchangeReply{frame: resp}, nil
	}
	return exchangeReply{frame: resp, conn: conn}, nil
}

func (c *Client) releaseReply(conn net.Conn, err error) {
	if conn == nil {
		return
	}
	if err != nil {
		switch err.(type) {
		case *luxerrors.AppError, tracePayloadError:
			// A valid application error or a bounded trace payload still
			// belongs to this exchange and consumes the complete response.
		default:
			conn.Close()
			return
		}
	}
	c.pool.Put(conn)
}

func (c *Client) callTracedPayload(ctx context.Context, payload []byte) ([]byte, api.DebugTraceSnapshot, error) {
	reply, err := c.exchangeRequest(ctx, payload, nil)
	if err != nil {
		return nil, api.DebugTraceSnapshot{}, err
	}
	body, trace, err := decodeTracedResponse(reply.frame)
	c.releaseReply(reply.conn, err)
	return body, trace, err
}

type tracePayloadError struct{ error }

func (e tracePayloadError) Error() string {
	return fmt.Sprintf("rpc: decode traced response: %v", e.error)
}

func (e tracePayloadError) Unwrap() error { return e.error }

// An invalid business error body retires the lease and preserves the trace failure.
type invalidTracedErrorBody struct{ tracePayloadError }

func decodeTracedResponse(resp []byte) ([]byte, api.DebugTraceSnapshot, error) {
	if len(resp) == 0 {
		return nil, api.DebugTraceSnapshot{}, fmt.Errorf("rpc: empty traced response")
	}
	status := resp[0]
	if status != statusTraceOK && status != statusTraceError {
		if status == statusError {
			return nil, api.DebugTraceSnapshot{}, decodeError(resp[1:])
		}
		return nil, api.DebugTraceSnapshot{}, fmt.Errorf("rpc: invalid traced response status %d", status)
	}
	traceLength, consumed := codec.ReadVarint(resp, 1)
	if consumed <= 0 {
		return nil, api.DebugTraceSnapshot{}, fmt.Errorf("rpc: invalid traced response length")
	}
	start := 1 + consumed
	if traceLength > uint64(len(resp)-start) {
		return nil, api.DebugTraceSnapshot{}, fmt.Errorf("rpc: traced response length exceeds frame")
	}
	end := start + int(traceLength)
	body := resp[end:]
	trace, err := api.DecodeDebugTrace(resp[start:end])
	if err != nil {
		traceErr := tracePayloadError{err}
		if status == statusTraceError {
			fields, bodyErr := readRPCErrorFields(body)
			if bodyErr != nil || fields.validate() != nil {
				return nil, api.DebugTraceSnapshot{}, invalidTracedErrorBody{traceErr}
			}
		}
		return nil, api.DebugTraceSnapshot{}, traceErr
	}
	if status == statusTraceError {
		return nil, trace, decodeError(body)
	}
	return body, trace, nil
}

func (c *Client) traceName() string {
	if c.name != "" {
		return c.name
	}
	return c.pool.addr
}

func applyTraceDefaults(snapshot *api.DebugTraceSnapshot, service string, target TraceTarget) {
	for i := range snapshot.Spans {
		span := &snapshot.Spans[i]
		if span.Service == "" {
			span.Service = service
		}
		if span.Operation == "" {
			span.Operation = target.Operation
		}
		if span.Field == "" {
			span.Field = target.Field
		}
		if span.FieldPath == "" {
			span.FieldPath = target.FieldPath
		}
	}
}

// Close closes the connection pool.
func (c *Client) Close() {
	c.pool.Close()
}

// EncodeParams builds binary params from typed values.
// Helper for constructing RPC call params.
func EncodeParams(fields ...ParamField) ([]byte, error) {
	var enc codec.Encoder
	for _, f := range fields {
		switch v := f.Value.(type) {
		case int64:
			enc.WriteFieldInt(f.FieldID, v)
		case float64:
			if err := enc.WriteFieldFloat(f.FieldID, v); err != nil {
				return nil, fmt.Errorf("RPC parameter %d: %w", f.FieldID, err)
			}
		case string:
			if err := enc.WriteFieldString(f.FieldID, v); err != nil {
				return nil, fmt.Errorf("RPC parameter %d: %w", f.FieldID, err)
			}
		case bool:
			enc.WriteFieldBool(f.FieldID, v)
		default:
			return nil, fmt.Errorf("RPC parameter %d: unsupported value type %T", f.FieldID, f.Value)
		}
	}
	enc.WriteEnd()
	return enc.Bytes(), nil
}

// ParamField represents a single parameter for EncodeParams.
type ParamField struct {
	FieldID int
	Value   any
}

// Socket and context timers can become ready independently. Only reclassify an
// actual I/O timeout after the caller's deadline; preserve unrelated failures.
// Called on failed I/O only, so successful requests need no extra clock read.
func contextErrorAfterIO(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return nil
	}
	timeout, ok := errors.AsType[net.Error](err)
	if ok && timeout.Timeout() && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}
