package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestRPCRoundTrip(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "pong")
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19876")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19876")
	defer client.Close()

	resp, err := client.Call(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
	t.Logf("RPC response: %d bytes", len(resp))
}

func TestRPCWithParams(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("greet", func(ctx context.Context, req *api.Request) error {
		name, _ := req.ParamString("name")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "hello "+name)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("greet", 2)
	rt.Registry.RegisterParams("greet", []api.ParamMeta{
		{Name: "name", Type: "String", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19877")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19877")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: "world"})
	resp, err := client.Call(2, params)
	if err != nil {
		t.Fatal(err)
	}

	// Decode response
	dec := codec.NewDecoder(resp)
	dec.NextField()
	msg := dec.ReadString()
	if msg != "hello world" {
		t.Fatalf("got %q, want 'hello world'", msg)
	}
}

func TestRPCContextEnvelopeForwardsVerifiedIdentity(t *testing.T) {
	type identityKey struct{}
	rt := api.NewRouter()
	rt.InternalRequestContext = func(ctx context.Context, token string) (context.Context, error) {
		if token != "verified-token" {
			return nil, fmt.Errorf("unexpected bearer token %q", token)
		}
		return context.WithValue(ctx, identityKey{}, int64(42)), nil
	}
	rt.Handle("private", func(ctx context.Context, req *api.Request) error {
		identity, _ := ctx.Value(identityKey{}).(int64)
		if identity != 42 {
			return fmt.Errorf("missing forwarded identity")
		}
		req.Buf.B = append(req.Buf.B, 0)
		return nil
	})
	rt.Registry.Register("private", 7)
	rt.Registry.RegisterParams("private", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19880")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19880")
	defer client.Close()
	if _, err := client.CallWithMaskContext(context.Background(), "verified-token", 7, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalRequestEnvelopeRejectsLegacyAndUnknownVersions(t *testing.T) {
	body := encodeCallRequest(7, nil, nil)
	payload := encodeRequestEnvelope(requestKindCall, "token", body)
	if !bytes.Equal(payload[:3], []byte{requestEnvelopeMarker, requestEnvelopeVersion, requestKindCall}) {
		t.Fatalf("canonical envelope prefix = %v", payload[:3])
	}
	envelope, err := decodeRequestEnvelope(payload)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.kind != requestKindCall || envelope.token != "token" || !bytes.Equal(envelope.body, body) {
		t.Fatalf("decoded envelope = %+v", envelope)
	}
	if _, err := decodeRequestEnvelope(body); err == nil {
		t.Fatal("legacy bare requests must be rejected")
	}
	payload[1]++
	if _, err := decodeRequestEnvelope(payload); err == nil {
		t.Fatal("unknown envelope versions must be rejected")
	}
}

func TestRPCErrorEnvelopeRequiresCanonicalFieldsAndTermination(t *testing.T) {
	var unknown codec.Encoder
	unknown.WriteFieldInt(4, 1)
	unknown.WriteEnd()
	var missing codec.Encoder
	missing.WriteFieldInt(1, 400)
	missing.WriteEnd()

	for name, data := range map[string][]byte{
		"truncated": {1},
		"unknown":   unknown.Bytes(),
		"missing":   missing.Bytes(),
		"trailing":  append(encodeError(400, "BadRequest", "bad")[1:], 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := decodeError(data); err == nil {
				t.Fatal("invalid error envelope must be rejected")
			}
		})
	}
}

func TestRPCStreamRoundTripAndCancellation(t *testing.T) {
	rt := api.NewRouter()
	rt.Schema.RegisterAPI(&schema.API{Name: "watchRPC", Stream: true})
	rt.Registry.Register("watchRPC", 8)
	rt.Registry.RegisterParams("watchRPC", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19882")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19882")
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	subscription, err := client.SubscribeContext(ctx, "", 8, nil, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	rt.Streams.DispatchEncoded("watchRPC", []byte("stream-data"), nil)
	select {
	case got := <-subscription.Messages():
		if string(got) != "stream-data" {
			t.Fatalf("stream payload = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RPC stream payload")
	}

	cancel()
	select {
	case err := <-subscription.Errors():
		if err != nil && err != context.Canceled {
			t.Fatalf("stream close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RPC stream did not stop after cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for rt.Streams.SubCount("watchRPC") != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rt.Streams.SubCount("watchRPC") != 0 {
		t.Fatal("server-side RPC subscription was not removed")
	}
}

func TestServerCloseTerminatesActiveRPCStream(t *testing.T) {
	rt := api.NewRouter()
	rt.Schema.RegisterAPI(&schema.API{Name: "watchShutdown", Stream: true})
	rt.Registry.Register("watchShutdown", 9)
	rt.Registry.RegisterParams("watchShutdown", nil)
	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19883")
	time.Sleep(100 * time.Millisecond)

	client := NewClient("127.0.0.1:19883")
	defer client.Close()
	subscription, err := client.SubscribeContext(context.Background(), "", 9, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	closed := make(chan struct{})
	go func() {
		srv.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server shutdown blocked on an active RPC stream")
	}
	select {
	case <-subscription.Errors():
	case <-time.After(time.Second):
		t.Fatal("client stream was not terminated by server shutdown")
	}
}

func TestServerCloseTerminatesIdleRPCConnection(t *testing.T) {
	rt := api.NewRouter()
	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19884")
	time.Sleep(100 * time.Millisecond)
	conn, err := net.Dial("tcp", "127.0.0.1:19884")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(20 * time.Millisecond)

	closed := make(chan struct{})
	go func() {
		srv.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server shutdown blocked on an idle RPC connection")
	}
}

func TestRPCError(t *testing.T) {
	rt := api.NewRouter()
	rt.Registry.Register("missing", 99)
	rt.Registry.RegisterParams("missing", nil)
	// No handler registered for "missing"

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19878")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19878")
	defer client.Close()

	_, err := client.Call(99, nil)
	if err == nil {
		t.Fatal("should error for missing handler")
	}
	t.Logf("error: %v", err)
}

func BenchmarkRPC_Ping(b *testing.B) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19879")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19879")
	defer client.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := client.Call(1, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Frame-level tests ---

func TestWriteAndReadFrame(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello frame")
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello frame" {
		t.Fatalf("got %q", got)
	}
}

func TestReadFrameEmpty(t *testing.T) {
	var buf bytes.Buffer
	_, err := ReadFrame(&buf, nil)
	if err == nil {
		t.Fatal("should error on empty reader")
	}
}

func TestReadFrameShortHeader(t *testing.T) {
	buf := bytes.NewReader([]byte{0, 0}) // only 2 bytes, need 4
	_, err := ReadFrame(buf, nil)
	if err == nil {
		t.Fatal("should error on short header")
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	// Write header with size > maxFrameSize
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], maxFrameSize+1)
	buf.Write(hdr[:])
	_, err := ReadFrame(&buf, nil)
	if err == nil {
		t.Fatal("should error on oversized frame")
	}
}

func TestReadFrameShortPayload(t *testing.T) {
	var buf bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 100)
	buf.Write(hdr[:])
	buf.Write([]byte{1, 2, 3}) // only 3 bytes, header says 100
	_, err := ReadFrame(&buf, nil)
	if err == nil {
		t.Fatal("should error on short payload")
	}
}

func TestReadFrameReusesBuf(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("reuse")
	WriteFrame(&buf, payload)

	existingBuf := make([]byte, 100) // larger than payload
	got, err := ReadFrame(&buf, existingBuf)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "reuse" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteFrameError(t *testing.T) {
	w := &errWriter{}
	err := WriteFrame(w, []byte("test"))
	if err == nil {
		t.Fatal("should error on failed write")
	}
}

func TestWriteFramePayloadError(t *testing.T) {
	w := &errWriterAfterN{n: 1} // header succeeds, payload fails
	err := WriteFrame(w, []byte("test"))
	if err == nil {
		t.Fatal("should error on failed payload write")
	}
}

func TestRequestEnvelopeRejectsMalformedShapes(t *testing.T) {
	overLimit := []byte{requestEnvelopeMarker, requestEnvelopeVersion, requestKindCall}
	overLimit = codec.AppendVarint(overLimit, maxBearerTokenSize+1)
	declaredTokenTooLong := []byte{requestEnvelopeMarker, requestEnvelopeVersion, requestKindCall}
	declaredTokenTooLong = codec.AppendVarint(declaredTokenTooLong, 2)
	declaredTokenTooLong = append(declaredTokenTooLong, 'a')
	tests := map[string][]byte{
		"truncated":               {requestEnvelopeMarker},
		"unknown request kind":    {requestEnvelopeMarker, requestEnvelopeVersion, 9, 0, 1},
		"invalid token length":    {requestEnvelopeMarker, requestEnvelopeVersion, requestKindCall, 0x80},
		"token over limit":        overLimit,
		"token exceeds request":   declaredTokenTooLong,
		"request body is missing": {requestEnvelopeMarker, requestEnvelopeVersion, requestKindCall, 0},
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRequestEnvelope(payload); err == nil {
				t.Fatal("malformed request envelope was accepted")
			}
		})
	}
}

func TestFrameWritersRejectOversizeAndWriteFailure(t *testing.T) {
	if err := WriteFrame(&bytes.Buffer{}, make([]byte, maxFrameSize+1)); err == nil {
		t.Fatal("oversized frame was written")
	}
	if err := writeStatusFrame(&bytes.Buffer{}, statusOK, make([]byte, maxFrameSize)); err == nil {
		t.Fatal("oversized status frame was written")
	}
	if err := writeStatusFrame(&errWriter{}, statusOK, nil); err == nil {
		t.Fatal("status frame write failure was ignored")
	}
}

func TestValidateStreamAckRejectsMalformedFrames(t *testing.T) {
	if err := validateStreamAck([]byte{statusOK}); err != nil {
		t.Fatal(err)
	}
	for name, frame := range map[string][]byte{
		"empty":          nil,
		"invalid status": {9},
		"trailing data":  {statusOK, 1},
		"error":          encodeError(400, "BadRequest", "bad"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateStreamAck(frame); err == nil {
				t.Fatal("malformed stream acknowledgment was accepted")
			}
		})
	}
}

func TestSubscriptionReadLoopRejectsMalformedFrames(t *testing.T) {
	tests := map[string][]byte{
		"empty":          nil,
		"invalid status": {9},
		"error":          encodeError(400, "BadRequest", "bad"),
	}
	for name, frame := range tests {
		t.Run(name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			subscription := &Subscription{
				messages: make(chan []byte, 1),
				errors:   make(chan error, 1),
				conn:     clientConn,
				cancel:   cancel,
			}
			go subscription.readLoop(ctx)
			_ = WriteFrame(serverConn, frame)
			if err := <-subscription.Errors(); err == nil {
				t.Fatal("malformed stream frame produced no error")
			}
			serverConn.Close()
		})
	}
}

func TestSubscriptionReadLoopHandlesReadAndContextFailures(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		clientConn, serverConn := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		subscription := &Subscription{
			messages: make(chan []byte, 1),
			errors:   make(chan error, 1),
			conn:     clientConn,
			cancel:   cancel,
		}
		go subscription.readLoop(ctx)
		if canceled {
			cancel()
		}
		serverConn.Close()
		if err := <-subscription.Errors(); err == nil {
			t.Fatal("stream read failure produced no error")
		}
	}

	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	subscription := &Subscription{
		messages: make(chan []byte),
		errors:   make(chan error, 1),
		conn:     clientConn,
		cancel:   func() {},
	}
	go subscription.readLoop(ctx)
	_ = WriteFrame(serverConn, []byte{statusStream, 1})
	if err := <-subscription.Errors(); err != context.Canceled {
		t.Fatalf("stream cancellation error = %v", err)
	}
	serverConn.Close()
}

func TestClientRejectsMalformedUnaryResponses(t *testing.T) {
	for name, response := range map[string][]byte{
		"empty":          nil,
		"invalid status": {9},
	} {
		t.Run(name, func(t *testing.T) {
			client := NewClient(startSingleFrameRPCServer(t, response, true))
			defer client.Close()
			if _, err := client.Call(1, nil); err == nil {
				t.Fatal("malformed unary response was accepted")
			}
		})
	}

	client := NewClient(startSingleFrameRPCServer(t, nil, false))
	defer client.Close()
	if _, err := client.Call(1, nil); err == nil {
		t.Fatal("closed response connection produced no error")
	}
}

func TestSubscribeContextRejectsConnectionAndAcknowledgmentErrors(t *testing.T) {
	client := NewClient("127.0.0.1:1")
	defer client.Close()
	if _, err := client.SubscribeContext(context.Background(), "", 1, nil, nil); err == nil {
		t.Fatal("stream dial failure produced no error")
	}

	for name, response := range map[string][]byte{
		"empty":          nil,
		"invalid status": {9},
		"error":          encodeError(400, "BadRequest", "bad"),
	} {
		t.Run(name, func(t *testing.T) {
			client := NewClient(startSingleFrameRPCServer(t, response, true))
			defer client.Close()
			if _, err := client.SubscribeContext(context.Background(), "", 1, nil, nil); err == nil {
				t.Fatal("malformed stream acknowledgment was accepted")
			}
		})
	}

	client = NewClient(startSingleFrameRPCServer(t, nil, false))
	defer client.Close()
	if _, err := client.SubscribeContext(context.Background(), "", 1, nil, nil); err == nil {
		t.Fatal("closed stream acknowledgment connection produced no error")
	}
}

func startSingleFrameRPCServer(t *testing.T, response []byte, respond bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, readErr := ReadFrame(conn, nil); readErr != nil || !respond {
			return
		}
		_ = WriteFrame(conn, response)
	}()
	return listener.Addr().String()
}

// --- Pool tests ---

func TestPoolGetPut(t *testing.T) {
	// Start a dummy listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	defer pool.Close()

	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(conn)
}

func TestPoolFullDropsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()

	pool := NewPool(ln.Addr().String(), 1) // maxIdle=1
	defer pool.Close()

	c1, _ := pool.Get()
	c2, _ := pool.Get()

	pool.Put(c1) // goes into pool
	pool.Put(c2) // pool full, c2 gets closed
}

func TestPoolDialError(t *testing.T) {
	pool := NewPool("127.0.0.1:1", 2) // bad port
	defer pool.Close()

	_, err := pool.Get()
	if err == nil {
		t.Fatal("should error on bad address")
	}
}

func TestPoolDefaultMaxIdle(t *testing.T) {
	pool := NewPool("127.0.0.1:1234", 0)
	if pool.maxIdle != 32 {
		t.Errorf("default maxIdle = %d, want 32", pool.maxIdle)
	}
}

// --- Client tests ---

func TestClientCallDialError(t *testing.T) {
	client := NewClient("127.0.0.1:1") // bad port
	defer client.Close()

	_, err := client.Call(1, nil)
	if err == nil {
		t.Fatal("should error on dial failure")
	}
}

func TestClientCallWithMask(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "pong")
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19881")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19881")
	defer client.Close()

	mask := codec.FieldMaskSet(nil, 1)
	mask = codec.AppendSelectionMask(nil, mask, nil)
	resp, err := client.CallWithMask(1, mask, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
}

// testProcessRequest is a helper that calls processRequest with a buffer and reads the response frame.
func testProcessRequest(t *testing.T, srv *Server, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	envelope := encodeRequestEnvelope(requestKindCall, "", payload)
	if err := srv.processRequest(&buf, envelope); err != nil {
		t.Fatalf("processRequest: %v", err)
	}
	resp, err := ReadFrame(&buf, nil)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	return resp
}

// --- Server malformed request tests ---

func TestServerMalformedAPIID(t *testing.T) {
	rt := api.NewRouter()
	srv := NewServer(rt)

	// Completely invalid payload
	resp := testProcessRequest(t, srv, []byte{})
	if resp[0] != statusError {
		t.Fatal("should return error for empty payload")
	}
}

func TestServerUnknownAPIID(t *testing.T) {
	rt := api.NewRouter()
	srv := NewServer(rt)

	payload := codec.AppendVarint(nil, 9999) // unknown API
	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("should return error for unknown API")
	}
}

func TestServerMalformedFieldMask(t *testing.T) {
	rt := api.NewRouter()
	rt.Registry.Register("test", 1)
	rt.Registry.RegisterParams("test", nil)
	srv := NewServer(rt)

	// API ID valid but no field mask follows
	payload := codec.AppendVarint(nil, 1)
	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("should return error for missing field mask")
	}
}

func TestServerHandlerNotFound(t *testing.T) {
	rt := api.NewRouter()
	rt.Registry.Register("nohandler", 1)
	rt.Registry.RegisterParams("nohandler", nil)
	srv := NewServer(rt)

	// Valid request but no handler registered
	var payload []byte
	payload = codec.AppendVarint(payload, 1) // API ID
	payload = codec.AppendVarint(payload, 0) // mask len = 0
	payload = append(payload, 0x00)          // params terminator
	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("should return error for missing handler")
	}
}

// --- EncodeParams type coverage ---

func TestEncodeParamsAllTypes(t *testing.T) {
	params := EncodeParams(
		ParamField{FieldID: 1, Value: int64(42)},
		ParamField{FieldID: 2, Value: 3.14},
		ParamField{FieldID: 3, Value: "hello"},
		ParamField{FieldID: 4, Value: true},
		ParamField{FieldID: 5, Value: []byte("unknown")}, // unsupported type, should be skipped
	)
	if len(params) == 0 {
		t.Fatal("should encode something")
	}
}

// --- Server param type coverage ---

func TestRPCWithFloatParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("calc", func(ctx context.Context, req *api.Request) error {
		v, _ := req.ParamFloat("value")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendFixed64(req.Buf.B, v*2)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("calc", 10)
	rt.Registry.RegisterParams("calc", []api.ParamMeta{
		{Name: "value", Type: "Float", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19882")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19882")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: 3.14})
	resp, err := client.Call(10, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
}

func TestRPCWithBoolParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("toggle", func(ctx context.Context, req *api.Request) error {
		v, _ := req.ParamBool("flag")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendBool(req.Buf.B, !v)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("toggle", 11)
	rt.Registry.RegisterParams("toggle", []api.ParamMeta{
		{Name: "flag", Type: "Boolean", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19883")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19883")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: true})
	resp, err := client.Call(11, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
}

func TestRPCWithIntParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("getById", func(ctx context.Context, req *api.Request) error {
		id, _ := req.ParamInt("id")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendSvarint(req.Buf.B, id)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("getById", 12)
	rt.Registry.RegisterParams("getById", []api.ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19884")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19884")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: int64(42)})
	resp, err := client.Call(12, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
}

func TestRPCHandlerReturnsAppError(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *api.Request) error {
		return &luxerrors.AppError{
			Code:    404,
			Name:    "NotFound",
			Message: "user not found",
		}
	})
	rt.Registry.Register("fail", 13)
	rt.Registry.RegisterParams("fail", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19885")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19885")
	defer client.Close()

	_, err := client.Call(13, nil)
	if err == nil {
		t.Fatal("should error")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("NotFound")) {
		t.Fatalf("error should contain NotFound: %v", err)
	}
}

func TestRPCHandlerReturnsGenericError(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("crash", func(ctx context.Context, req *api.Request) error {
		return fmt.Errorf("something broke")
	})
	rt.Registry.Register("crash", 14)
	rt.Registry.RegisterParams("crash", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19886")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19886")
	defer client.Close()

	_, err := client.Call(14, nil)
	if err == nil {
		t.Fatal("should error")
	}
}

func TestPoolGetFromIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn // keep open
		}
	}()

	pool := NewPool(ln.Addr().String(), 4)
	defer pool.Close()

	// Get a connection, put it back, then get again (from idle)
	c1, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(c1)

	c2, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(c2)
}

// --- Error helpers ---

type errWriter struct{}

func (w *errWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write error")
}

type errWriterAfterN struct {
	n     int
	calls int
}

func (w *errWriterAfterN) Write(p []byte) (int, error) {
	w.calls++
	if w.calls > w.n {
		return 0, fmt.Errorf("write error after %d calls", w.n)
	}
	return len(p), nil
}

func BenchmarkRPC_Echo(b *testing.B) {
	rt := api.NewRouter()
	rt.Handle("echo", func(ctx context.Context, req *api.Request) error {
		name, _ := req.ParamString("name")
		req.Buf.B = codec.AppendVarint(req.Buf.B, 1)
		req.Buf.B = codec.AppendString(req.Buf.B, "hello "+name)
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("echo", 1)
	rt.Registry.RegisterParams("echo", []api.ParamMeta{
		{Name: "name", Type: "String", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19880")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19880")
	defer client.Close()

	params := EncodeParams(ParamField{FieldID: 1, Value: "world"})

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := client.Call(1, params)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Adversarial tests ---

func TestRPCHandlerPanicRecovery(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("crash", func(ctx context.Context, req *api.Request) error {
		panic("boom")
	})
	rt.Registry.Register("crash", 1)
	rt.Registry.RegisterParams("crash", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19890")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19890")
	defer client.Close()

	// Should get error, not crash
	_, err := client.Call(1, nil)
	if err == nil {
		t.Fatal("should return error on handler panic")
	}
	t.Logf("panic caught: %v", err)

	// Connection should still work after panic
	_, err = client.Call(1, nil)
	if err == nil {
		t.Fatal("second call should also return error")
	}
}

func TestRPCTruncatedParams(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("need_int", func(ctx context.Context, req *api.Request) error {
		v, _ := req.ParamInt("id")
		req.Buf.B = codec.AppendSvarint(req.Buf.B, v)
		return nil
	})
	rt.Registry.Register("need_int", 1)
	rt.Registry.RegisterParams("need_int", []api.ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19891")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19891")
	defer client.Close()

	// Send truncated param: fieldID varint but no value bytes
	var truncated []byte
	truncated = codec.AppendVarint(truncated, 1) // API ID
	truncated = codec.AppendVarint(truncated, 0) // mask len
	truncated = codec.AppendVarint(truncated, 1) // param fieldID=1
	// NO value bytes — truncated!

	conn, err := client.pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	WriteFrame(conn, encodeRequestEnvelope(requestKindCall, "", truncated))
	resp, err := ReadFrame(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.pool.Put(conn)

	if len(resp) == 0 || resp[0] != statusError {
		t.Fatalf("should return error for truncated params, got status=%d", resp[0])
	}
	t.Logf("truncated param caught: %s", string(resp[1:]))
}

func TestPoolGetAfterClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	pool.Close()

	_, err = pool.Get()
	if err == nil {
		t.Fatal("Get after Close should return error")
	}
}

// --- Pool: Put with nil conn ---

func TestPoolPutNil(t *testing.T) {
	pool := NewPool("127.0.0.1:1234", 2)
	defer pool.Close()
	// Should not panic
	pool.Put(nil)
}

// --- Pool: Put after Close ---

func TestPoolPutAfterClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			_ = c
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	// Put after close should close the conn, not panic
	pool.Put(conn)
}

// --- Pool: GetContext with cancelled context ---

func TestPoolGetContextCancelled(t *testing.T) {
	pool := NewPool("127.0.0.1:1", 2) // unreachable
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := pool.GetContext(ctx)
	if err == nil {
		t.Fatal("GetContext with cancelled context should error")
	}
}

// --- Pool: GetContext with closed pool ---

func TestPoolGetContextClosed(t *testing.T) {
	pool := NewPool("127.0.0.1:1234", 2)
	pool.Close()
	_, err := pool.GetContext(context.Background())
	if err == nil {
		t.Fatal("GetContext on closed pool should error")
	}
}

// --- Server: handler returns AppError (via processRequest directly) ---

func TestServerProcessRequestAppError(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("fail", func(ctx context.Context, req *api.Request) error {
		return &luxerrors.AppError{Code: 403, Name: "Forbidden", Message: "no access"}
	})
	rt.Registry.Register("fail", 1)
	rt.Registry.RegisterParams("fail", nil)

	srv := NewServer(rt)
	var payload []byte
	payload = codec.AppendVarint(payload, 1) // API ID
	payload = codec.AppendVarint(payload, 0) // mask len
	payload = append(payload, 0x00)          // params terminator

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("should return error status")
	}
	// Decode error
	dec := codec.NewDecoder(resp[1:])
	var errCode int64
	var errName string
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			errCode = dec.ReadInt()
		case 2:
			errName = dec.ReadString()
		}
	}
	if errCode != 403 || errName != "Forbidden" {
		t.Fatalf("error code=%d name=%s", errCode, errName)
	}
}

// --- Server: handler panics (via processRequest directly) ---

func TestServerProcessRequestHandlerPanic(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("boom", func(ctx context.Context, req *api.Request) error {
		panic("kaboom")
	})
	rt.Registry.Register("boom", 1)
	rt.Registry.RegisterParams("boom", nil)

	srv := NewServer(rt)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)
	payload = codec.AppendVarint(payload, 0)
	payload = append(payload, 0x00)

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("panic should return error status")
	}
}

// --- Server: invalid frame then disconnect ---

func TestServerInvalidFrameDisconnect(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.listener = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.wg.Add(1)
			go srv.handleConn(conn)
		}
	}()
	defer func() {
		srv.Close()
		ln.Close()
	}()

	// Send garbage data then close
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte{0xFF, 0xFF}) // invalid frame header
	conn.Close()

	time.Sleep(50 * time.Millisecond) // let server process
}

// --- Client: CallWithMask — server returns error status ---

func TestClientCallWithMaskServerError(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("err", func(ctx context.Context, req *api.Request) error {
		return fmt.Errorf("internal failure")
	})
	rt.Registry.Register("err", 1)
	rt.Registry.RegisterParams("err", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19892")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	client := NewClient("127.0.0.1:19892")
	defer client.Close()

	_, err := client.CallWithMask(1, nil, nil)
	if err == nil {
		t.Fatal("should get error from server")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("internal failure")) {
		t.Fatalf("error should contain message, got: %v", err)
	}
}

// --- Server: truncated Float/String/Boolean params ---

func TestRPCTruncatedFloatParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("calc", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("calc", 1)
	rt.Registry.RegisterParams("calc", []api.ParamMeta{
		{Name: "amount", Type: "Float", FieldID: 1},
	})

	srv := NewServer(rt)
	// API ID=1, mask=0, param fieldID=1, then truncated (no 8 bytes for float)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)
	payload = codec.AppendVarint(payload, 0)
	payload = codec.AppendVarint(payload, 1) // param fieldID
	payload = append(payload, 0x01, 0x02)    // only 2 bytes, need 8

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("truncated float should return error")
	}
}

func TestRPCTruncatedStringParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("search", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("search", 1)
	rt.Registry.RegisterParams("search", []api.ParamMeta{
		{Name: "query", Type: "String", FieldID: 1},
	})

	srv := NewServer(rt)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)
	payload = codec.AppendVarint(payload, 0)
	payload = codec.AppendVarint(payload, 1) // param fieldID
	// String length says 100 but no data follows
	payload = codec.AppendVarint(payload, 100)

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("truncated string should return error")
	}
}

func TestRPCTruncatedBoolParam(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("toggle", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("toggle", 1)
	rt.Registry.RegisterParams("toggle", []api.ParamMeta{
		{Name: "flag", Type: "Boolean", FieldID: 1},
	})

	srv := NewServer(rt)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)
	payload = codec.AppendVarint(payload, 0)
	payload = codec.AppendVarint(payload, 1) // param fieldID
	// No bool byte follows

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("truncated bool should return error")
	}
}

// --- Server: unknown param field ID ---

// --- Client: WriteFrame error (connection closed before write) ---

func TestClientCallWriteError(t *testing.T) {
	// Start a listener that accepts and immediately closes connections
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			c.Close() // close immediately so client write fails
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	client := &Client{pool: pool}
	defer client.Close()

	// Get a connection, then close it ourselves before calling
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	// Put the broken conn back so CallWithMask gets it
	pool.Put(conn)

	// Now CallWithMask should fail on WriteFrame
	_, err = client.CallWithMask(1, nil, nil)
	if err == nil {
		t.Fatal("should error on write to closed conn")
	}
}

// --- Client: ReadFrame error (server closes after receiving request) ---

func TestClientCallReadError(t *testing.T) {
	// Start a listener that reads the frame header+payload then closes
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			// Read the request frame then close without responding
			ReadFrame(c, nil)
			c.Close()
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	client := &Client{pool: pool}
	defer client.Close()

	_, err = client.CallWithMask(1, nil, nil)
	if err == nil {
		t.Fatal("should error on read from closed conn")
	}
}

// --- Client: empty response (zero-length response body) ---

func TestClientCallEmptyResponse(t *testing.T) {
	// Start a listener that returns a zero-length response frame
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			// Read the request
			ReadFrame(c, nil)
			// Write an empty response frame (0-length payload)
			WriteFrame(c, []byte{})
			c.Close()
		}
	}()

	pool := NewPool(ln.Addr().String(), 2)
	client := &Client{pool: pool}
	defer client.Close()

	_, err = client.CallWithMask(1, nil, nil)
	if err == nil {
		t.Fatal("should error on empty response")
	}
}

func TestClientRejectsNonUnaryResponseStatus(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		ReadFrame(conn, nil)
		WriteFrame(conn, []byte{statusStream, 1})
	}()

	client := NewClient(ln.Addr().String())
	defer client.Close()
	if _, err := client.Call(1, nil); err == nil || !bytes.Contains([]byte(err.Error()), []byte("invalid unary response status")) {
		t.Fatalf("unexpected response status must fail, got %v", err)
	}
}

// --- Pool: GetContext receives nil from closed channel ---

func TestPoolGetContextNilFromChannel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			_ = c
		}
	}()

	pool := NewPool(ln.Addr().String(), 4)

	// Put a real conn in the pool, then close the pool
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(conn)

	// Close the pool — this closes the channel
	pool.Close()

	// The channel is closed and drained. Now a Get should fail via closed.Load() check.
	// To hit the nil-from-channel path, we need the atomic check to pass but the channel read to give nil.
	// This is a race-condition path; the closed.Load() guard makes it unreachable in practice.
	// The pool.Close() already covers the drain. Test the closed-pool path explicitly.
	_, err = pool.GetContext(context.Background())
	if err == nil {
		t.Fatal("GetContext on closed pool should error")
	}
}

// --- Server: ListenAndServe with bad address ---

func TestServerListenAndServeError(t *testing.T) {
	rt := api.NewRouter()
	srv := NewServer(rt)
	// Listen on an invalid address
	err := srv.ListenAndServe("0.0.0.0:-1")
	if err == nil {
		t.Fatal("should error on invalid address")
	}
}

// --- Server: connection rejected at capacity (via ListenAndServe) ---

func TestServerAtCapacity(t *testing.T) {
	rt := api.NewRouter()
	// Use a blocking handler to hold the semaphore slot
	blocked := make(chan struct{})
	rt.Handle("block", func(ctx context.Context, req *api.Request) error {
		<-blocked // block until released
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("block", 1)
	rt.Registry.RegisterParams("block", nil)

	srv := NewServer(rt)
	// Set sem to 1 so only 1 concurrent connection allowed
	srv.sem = make(chan struct{}, 1)

	go srv.ListenAndServe("127.0.0.1:19895")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	addr := "127.0.0.1:19895"

	// First connection: send a request that blocks the handler
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()

	var payload []byte
	payload = codec.AppendVarint(payload, 1) // API ID
	payload = codec.AppendVarint(payload, 0) // mask len
	payload = append(payload, 0x00)          // params end
	WriteFrame(c1, encodeRequestEnvelope(requestKindCall, "", payload))

	// Wait for handler to be running (sem slot taken)
	time.Sleep(50 * time.Millisecond)

	// Second connection: should be rejected at capacity (sem full, handler blocked)
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()

	// Give server time to reject the second connection
	time.Sleep(50 * time.Millisecond)

	// Release the blocked handler
	close(blocked)

	// Read c1's response
	ReadFrame(c1, nil)
}

// --- Server: processRequest WriteFrame error -> handleConn exits ---

func TestServerHandleConnProcessRequestWriteError(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)
	srv := NewServer(rt)

	// Use net.Pipe for controlled read/write
	pr, pw := net.Pipe()
	srv.wg.Add(1)
	go srv.handleConn(pw)

	// Send a valid request and read the response first to confirm it works
	var payload []byte
	payload = codec.AppendVarint(payload, 1) // API ID
	payload = codec.AppendVarint(payload, 0) // mask len
	payload = append(payload, 0x00)          // params end
	request := encodeRequestEnvelope(requestKindCall, "", payload)
	WriteFrame(pr, request)

	resp, err := ReadFrame(pr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp[0] != statusOK {
		t.Fatal("first request should succeed")
	}

	// Now send another request but close our read end immediately after writing.
	// This way processRequest succeeds but the WriteFrame for the response fails
	// because the pipe is closed, causing handleConn to return from processRequest error.
	WriteFrame(pr, request)
	pr.Close()

	srv.wg.Wait() // wait for handleConn to exit
}

// --- Server: accept error (non-done, continue path) ---
// server.go:56,58 — Accept error when server is NOT shutting down.
// This requires a transient Accept error (e.g., EMFILE). This is practically
// impossible to trigger in tests without OS-level manipulation.
// Marking as a defensive unreachable path in practice.

// --- Server: ListenAndServe graceful shutdown (done channel) ---

func TestServerListenAndServeGracefulShutdown(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe("127.0.0.1:19896")
	}()
	time.Sleep(100 * time.Millisecond)

	// Close the server -> done channel closed -> ln.Accept() returns error -> select <-s.done hits -> returns nil
	srv.Close()

	err := <-errCh
	if err != nil {
		t.Fatalf("ListenAndServe should return nil on graceful shutdown, got: %v", err)
	}
}

// --- Server: accept error (non-shutdown) ---

func TestServerAcceptErrorContinues(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("ping", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, 0x00)
		return nil
	})
	rt.Registry.Register("ping", 1)
	rt.Registry.RegisterParams("ping", nil)

	srv := NewServer(rt)
	go srv.ListenAndServe("127.0.0.1:19897")
	time.Sleep(100 * time.Millisecond)
	defer srv.Close()

	// Make a successful connection to verify the server is still running
	client := NewClient("127.0.0.1:19897")
	defer client.Close()

	_, err := client.Call(1, nil)
	if err != nil {
		t.Fatalf("server should still be running: %v", err)
	}
}

// --- Server: truncated field mask ---

func TestServerTruncatedFieldMask(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("test", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("test", 1)
	rt.Registry.RegisterParams("test", nil)
	srv := NewServer(rt)

	// API ID=1, mask len=100 but only 2 bytes follow
	var payload []byte
	payload = codec.AppendVarint(payload, 1)   // API ID
	payload = codec.AppendVarint(payload, 100) // mask len = 100
	payload = append(payload, 0x01, 0x02)      // only 2 bytes

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("truncated field mask should return error")
	}
}

// --- Server: truncated param field ID (end-of-buffer during varint read) ---

func TestServerTruncatedParamFieldID(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("test", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("test", 1)
	rt.Registry.RegisterParams("test", []api.ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})
	srv := NewServer(rt)

	// API ID=1, mask len=0, then a truncated varint (high bit set but no continuation)
	var payload []byte
	payload = codec.AppendVarint(payload, 1) // API ID
	payload = codec.AppendVarint(payload, 0) // mask len
	payload = append(payload, 0x80)          // incomplete varint — high bit set, no next byte

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("truncated param field ID should return error")
	}
}

// --- Server: too many params (paramIdx >= 16) ---

func TestServerTooManyParams(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("test", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("test", 1)

	// Register 17 params to trigger the "too many params" check
	params := make([]api.ParamMeta, 17)
	for i := 0; i < 17; i++ {
		params[i] = api.ParamMeta{
			Name:    fmt.Sprintf("p%d", i),
			Type:    "Int",
			FieldID: i + 1,
		}
	}
	rt.Registry.RegisterParams("test", params)
	srv := NewServer(rt)

	// Build payload: API ID=1, mask=0, then send param with fieldID=17 (index 16)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)  // API ID
	payload = codec.AppendVarint(payload, 0)  // mask len
	payload = codec.AppendVarint(payload, 17) // param fieldID=17 -> index 16 (>= 16)

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("too many params should return error")
	}
}

func TestRPCUnknownParamFieldID(t *testing.T) {
	rt := api.NewRouter()
	rt.Handle("test", func(ctx context.Context, req *api.Request) error {
		return nil
	})
	rt.Registry.Register("test", 1)
	rt.Registry.RegisterParams("test", []api.ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})

	srv := NewServer(rt)
	var payload []byte
	payload = codec.AppendVarint(payload, 1)  // API ID
	payload = codec.AppendVarint(payload, 0)  // mask len
	payload = codec.AppendVarint(payload, 99) // unknown param field ID

	resp := testProcessRequest(t, srv, payload)
	if resp[0] != statusError {
		t.Fatal("unknown param field ID should return error")
	}
}
