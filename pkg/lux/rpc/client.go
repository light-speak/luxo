package rpc

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

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

// Subscription is a dedicated server-streaming RPC connection.
type Subscription struct {
	messages chan []byte
	errors   chan error
	conn     net.Conn
	cancel   context.CancelFunc
	stop     func() bool
	once     sync.Once
}

// Messages returns canonical Luxo binary stream payloads.
func (s *Subscription) Messages() <-chan []byte {
	return s.messages
}

// Errors reports the terminal stream result and then closes.
func (s *Subscription) Errors() <-chan error {
	return s.errors
}

// Close cancels the subscription and closes its dedicated connection.
func (s *Subscription) Close() {
	s.cancel()
	s.conn.Close()
}

// NewClient creates an RPC client for the given service address.
func NewClient(addr string) *Client {
	return &Client{pool: NewPool(addr, 32)}
}

// NewNamedClient creates a client whose debug traces identify the target service.
func NewNamedClient(name, addr string) *Client {
	return &Client{name: name, pool: NewPool(addr, 32)}
}

// Call sends a binary-encoded request and returns the response body.
// apiID: from luxo.lock. params: pre-encoded binary (fieldID+value pairs + 0x00).
// Returns the response body (without status byte) and any error.
func (c *Client) Call(apiID int, params []byte) ([]byte, error) {
	return c.CallWithMask(apiID, nil, params)
}

// CallWithMask sends a request with field mask for field selection.
func (c *Client) CallWithMask(apiID int, fieldMask []byte, params []byte) ([]byte, error) {
	request := encodeCallRequest(apiID, fieldMask, params)
	payload := encodeRequestEnvelope(requestKindCall, "", request)
	return c.callPayload(context.Background(), payload)
}

// CallWithMaskContext sends an authenticated request and propagates cancellation
// and deadlines to the network operation.
func (c *Client) CallWithMaskContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte) ([]byte, error) {
	return c.CallWithMaskTargetContext(ctx, bearerToken, apiID, fieldMask, params, TraceTarget{})
}

// CallWithMaskTargetContext adds service and field attribution when detailed
// tracing is explicitly enabled on ctx. The normal path keeps the v1 envelope.
func (c *Client) CallWithMaskTargetContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte, target TraceTarget) ([]byte, error) {
	request := encodeCallRequest(apiID, fieldMask, params)
	trace := api.DebugTrace(ctx)
	if trace == nil {
		payload := encodeRequestEnvelope(requestKindCall, bearerToken, request)
		return c.callPayload(ctx, payload)
	}
	fieldPath := target.FieldPath
	if fieldPath == "" {
		fieldPath = api.JoinTraceFieldPath(api.TraceFieldPath(ctx), target.Field)
	}
	target.FieldPath = fieldPath
	_, callSpan := trace.StartSpan(ctx, api.DebugSpanMeta{
		Name: "service.call", Category: "service", Service: c.traceName(), Operation: target.Operation,
		Field: target.Field, FieldPath: fieldPath, Selection: target.Selection, Dependency: target.Dependency,
	})
	payload := encodeTracedRequestEnvelope(requestKindCall, bearerToken, api.TraceID(ctx), fieldPath, trace.DatabaseDetails(), request)
	body, remote, err := c.callTracedPayload(ctx, payload)
	applyTraceDefaults(&remote, c.traceName(), target)
	trace.MergeSpan(callSpan, remote)
	trace.FinishSpan(callSpan)
	return body, err
}

// SubscribeContext opens a dedicated binary RPC stream. The connection is not
// returned to the unary pool because it remains owned by the subscription.
func (c *Client) SubscribeContext(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte) (*Subscription, error) {
	request := encodeCallRequest(apiID, fieldMask, params)
	payload := encodeRequestEnvelope(requestKindStream, bearerToken, request)
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpc: dial %s: %w", c.pool.addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("rpc: stream deadline: %w", err)
		}
	}
	streamCtx, cancel := context.WithCancel(ctx)
	subscription := &Subscription{
		messages: make(chan []byte, 64),
		errors:   make(chan error, 1),
		conn:     conn,
		cancel:   cancel,
	}
	subscription.stop = context.AfterFunc(streamCtx, func() { conn.Close() })
	if err := WriteFrame(conn, payload); err != nil {
		subscription.finish(fmt.Errorf("rpc: stream write: %w", err))
		return nil, err
	}
	ack, err := ReadFrame(conn, nil)
	if err != nil {
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
			if ctx.Err() != nil {
				s.finish(ctx.Err())
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
			select {
			case s.messages <- frame[1:]:
			case <-ctx.Done():
				s.finish(ctx.Err())
				return
			}
		case statusError:
			s.finish(decodeError(frame[1:]))
			return
		default:
			s.finish(fmt.Errorf("rpc: invalid stream frame status %d", frame[0]))
			return
		}
	}
}

func (s *Subscription) finish(err error) {
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
		}
		s.cancel()
		s.conn.Close()
		if err != nil {
			s.errors <- err
		}
		close(s.messages)
		close(s.errors)
	})
}

func encodeCallRequest(apiID int, fieldMask []byte, params []byte) []byte {
	// Build request payload: [API ID varint] [mask len varint] [mask] [params]
	var payload []byte
	payload = codec.AppendVarint(payload, uint64(apiID))
	payload = codec.AppendVarint(payload, uint64(len(fieldMask)))
	if len(fieldMask) > 0 {
		payload = append(payload, fieldMask...)
	}
	if len(params) > 0 {
		payload = append(payload, params...)
	} else {
		payload = append(payload, 0x00) // empty params terminator
	}
	return payload
}

func (c *Client) callPayload(ctx context.Context, payload []byte) ([]byte, error) {
	resp, err := c.exchange(ctx, payload)
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

func (c *Client) exchange(ctx context.Context, payload []byte) ([]byte, error) {
	// Get pooled connection
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpc: dial %s: %w", c.pool.addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("rpc: deadline: %w", err)
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
	if err := WriteFrame(conn, payload); err != nil {
		closeConnection()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("rpc: write: %w", err)
	}

	// Read response frame
	resp, err := ReadFrame(conn, nil)
	if err != nil {
		closeConnection()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("rpc: read: %w", err)
	}

	reusable := true
	if stopCancel != nil && !stopCancel() {
		reusable = false
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		reusable = false
	}
	if reusable {
		c.pool.Put(conn)
	} else {
		conn.Close()
	}

	return resp, nil
}

func (c *Client) callTracedPayload(ctx context.Context, payload []byte) ([]byte, api.DebugTraceSnapshot, error) {
	resp, err := c.exchange(ctx, payload)
	if err != nil {
		return nil, api.DebugTraceSnapshot{}, err
	}
	return decodeTracedResponse(resp)
}

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
	trace, err := api.DecodeDebugTrace(resp[start:end])
	if err != nil {
		return nil, api.DebugTraceSnapshot{}, fmt.Errorf("rpc: decode traced response: %w", err)
	}
	body := resp[end:]
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

// decodeError decodes a binary error response.
func decodeError(data []byte) error {
	dec := codec.NewDecoder(data)
	var code int64
	var name, message string
	var seen uint8
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			code = dec.ReadInt()
			seen |= 1 << 0
		case 2:
			name = dec.ReadString()
			seen |= 1 << 1
		case 3:
			message = dec.ReadString()
			seen |= 1 << 2
		default:
			return fmt.Errorf("rpc: unknown error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		return fmt.Errorf("rpc: decode error envelope: %w", err)
	}
	if dec.FieldID() != 0 {
		return fmt.Errorf("rpc: error envelope missing end marker")
	}
	if dec.Offset() != len(data) {
		return fmt.Errorf("rpc: error envelope has trailing bytes")
	}
	if seen != 0b111 {
		return fmt.Errorf("rpc: error envelope missing required fields")
	}
	appErr := &luxerrors.AppError{Name: name, Code: int(code), Message: message}
	if code >= 500 {
		appErr.Message = "error.internal"
		appErr.Internal = true
		appErr.Cause = fmt.Errorf("rpc error %d %s: %s", code, name, message)
	}
	return appErr
}

// EncodeParams builds binary params from typed values.
// Helper for constructing RPC call params.
func EncodeParams(fields ...ParamField) []byte {
	var enc codec.Encoder
	for _, f := range fields {
		switch v := f.Value.(type) {
		case int64:
			enc.WriteFieldInt(f.FieldID, v)
		case float64:
			enc.WriteFieldFloat(f.FieldID, v)
		case string:
			enc.WriteFieldString(f.FieldID, v)
		case bool:
			enc.WriteFieldBool(f.FieldID, v)
		}
	}
	enc.WriteEnd()
	return enc.Bytes()
}

// ParamField represents a single parameter for EncodeParams.
type ParamField struct {
	FieldID int
	Value   any
}
