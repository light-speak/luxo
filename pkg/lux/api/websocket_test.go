package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

// testWSRouter creates a router with a test handler for WS tests.
func testWSRouter() *Router {
	rt := NewRouter()
	rt.Schema = schema.New()
	rt.Schema.RegisterModel(&schema.Model{
		Name: "User",
		Fields: []schema.Field{
			{ID: 1, Name: "id", Type: schema.FieldInt},
			{ID: 2, Name: "name", Type: schema.FieldString},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{
		ID: 10, Name: "getUser", Module: "user", ReturnType: "User",
	})

	// Register handler that writes a binary User (with arena header)
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
		req.Buf.B = codec.AppendVarint(req.Buf.B, 0) // arena header (totalStringLen=0 for test)
		var enc codec.Encoder
		enc.WriteFieldInt(1, 42)
		enc.WriteFieldString(2, "Alice")
		enc.WriteEnd()
		req.Buf.B = append(req.Buf.B, enc.Bytes()...)
		return nil
	})

	// Register handler that returns an error
	rt.Handle("failAPI", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.ResourceError{Resource: "test"})
	})

	// Register binary API
	rt.Registry.Register("getUser", 10)
	rt.Registry.RegisterParams("getUser", []ParamMeta{
		{Name: "id", Type: "Int", FieldID: 1},
	})
	rt.Schema.RegisterAPI(&schema.API{Name: "watchTest", Stream: true})

	return rt
}

func registerTestStream(rt *Router, name string) {
	definition := rt.Schema.APIs[name]
	if definition == nil {
		definition = &schema.API{Name: name}
		rt.Schema.RegisterAPI(definition)
	}
	definition.Stream = true
}

func TestWSRejectsSubscriptionsToNonStreamAPIs(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"getUser","id":42}`)); err != nil {
		t.Fatal(err)
	}
	binary := []byte{BinaryFrameSubscribe}
	binary = codec.AppendVarint(binary, 10)
	binary = codec.AppendVarint(binary, 0)
	var enc codec.Encoder
	enc.WriteFieldInt(1, 42)
	enc.WriteEnd()
	binary = append(binary, enc.Bytes()...)
	if err := conn.Write(ctx, websocket.MessageBinary, binary); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	if rt.Streams.SubCount("getUser") != 0 {
		t.Fatal("non-stream API must reject JSON and binary subscriptions")
	}
}

func TestWSJSONSubscriptionAcknowledgesSuccessAndErrors(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchAck")
	rt.Registry.Register("watchAck", 71)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"watchAck"}`)); err != nil {
		t.Fatal(err)
	}
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText || string(data) != `{"$sub":"watchAck","ok":true}` {
		t.Fatalf("unexpected subscribe acknowledgement: type=%v data=%s", messageType, data)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":"getUser","id":42}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if string(response["$sub"]) != `"getUser"` || string(response["error"]) != `"BadRequest"` {
		t.Fatalf("unexpected subscription error: %s", data)
	}
}

func TestWSBinarySubscriptionAcknowledgesSuccessAndErrors(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchAck")
	rt.Registry.Register("watchAck", 71)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	request := []byte{BinaryFrameSubscribe, 71, 0, 0}
	if err := conn.Write(ctx, websocket.MessageBinary, request); err != nil {
		t.Fatal(err)
	}
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary || !bytes.Equal(data, []byte{BinaryFrameSubscribeSuccess, 71}) {
		t.Fatalf("unexpected subscribe acknowledgement: type=%v data=%v", messageType, data)
	}

	request = []byte{BinaryFrameSubscribe, 10, 0, 0}
	if err := conn.Write(ctx, websocket.MessageBinary, request); err != nil {
		t.Fatal(err)
	}
	_, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 3 || data[0] != BinaryFrameSubscribeError || data[1] != 10 {
		t.Fatalf("unexpected subscription error frame: %v", data)
	}
	wireErr, err := DecodeBinaryError(data[2:], 0)
	if err != nil {
		t.Fatal(err)
	}
	if wireErr.Name != "BadRequest" || wireErr.Code != http.StatusBadRequest {
		t.Fatalf("unexpected subscription error: %+v", wireErr)
	}
}

func TestWSJSON_Success(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send JSON request
	req := `{"$id":1,"$api":"getUser","id":42}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Check $id
	var respID int
	json.Unmarshal(resp["$id"], &respID)
	if respID != 1 {
		t.Errorf("$id = %d, want 1", respID)
	}

	// Check data exists
	if _, ok := resp["data"]; !ok {
		t.Errorf("missing data in response: %s", data)
	}

	got := string(data)
	if !strings.Contains(got, "Alice") {
		t.Errorf("response should contain Alice: %s", got)
	}
}

func TestWSJSON_NotFound(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Request non-existent API
	req := `{"$id":2,"$api":"nonExistent"}`
	conn.Write(ctx, websocket.MessageText, []byte(req))

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, `"error"`) {
		t.Errorf("should contain error: %s", got)
	}
	if !strings.Contains(got, `"$id":2`) {
		t.Errorf("should contain $id:2: %s", got)
	}
}

func TestWSJSON_MissingAPI(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Missing $api
	req := `{"$id":3}`
	conn.Write(ctx, websocket.MessageText, []byte(req))

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, "missing $api") {
		t.Errorf("should report missing $api: %s", got)
	}
}

func TestWSJSON_HandlerError(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	req := `{"$id":4,"$api":"failAPI"}`
	conn.Write(ctx, websocket.MessageText, []byte(req))

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, "NotFound") {
		t.Errorf("should contain NotFound error: %s", got)
	}
}

func TestWSJSON_Concurrent(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send 3 requests rapidly
	for i := 1; i <= 3; i++ {
		req := `{"$id":` + string(rune('0'+i)) + `,"$api":"getUser","id":` + string(rune('0'+i)) + `}`
		conn.Write(ctx, websocket.MessageText, []byte(req))
	}

	// Read 3 responses (order may vary)
	received := 0
	for received < 3 {
		_, _, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", received, err)
		}
		received++
	}
	if received != 3 {
		t.Errorf("expected 3 responses, got %d", received)
	}
}

func TestWSBinary_Success(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Build binary call request.
	reqBuf := []byte{BinaryFrameCallRequest}
	reqBuf = codec.AppendVarint(reqBuf, 1)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 10) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask len
	// Param: field 1 = int 42
	var enc codec.Encoder
	enc.WriteFieldInt(1, 42)
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	if err := conn.Write(ctx, websocket.MessageBinary, reqBuf); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if data[0] != BinaryFrameCallSuccess {
		t.Fatalf("frame type = %x, want success", data[0])
	}
	seq, n := codec.ReadVarint(data, 1)
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	// Decode payload (skip arena header first)
	payload := data[n+1:]
	dec := codec.NewDecoder(payload)
	dec.SkipArenaHeader()
	if !dec.NextField() || dec.FieldID() != 1 {
		t.Fatal("expected field 1 (id)")
	}
	id := dec.ReadInt()
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
	if !dec.NextField() || dec.FieldID() != 2 {
		t.Fatal("expected field 2 (name)")
	}
	name := dec.ReadString()
	if name != "Alice" {
		t.Errorf("name = %q, want Alice", name)
	}
}

func TestWSBinary_SequenceBoundaryDoesNotCollideWithFrameTypes(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for seq := uint64(1); seq <= 300; seq++ {
		req := []byte{BinaryFrameCallRequest}
		req = codec.AppendVarint(req, seq)
		req = codec.AppendVarint(req, 10)
		req = codec.AppendVarint(req, 0)
		req = codec.AppendVarint(req, 1)
		req = codec.AppendSvarint(req, int64(seq))
		req = append(req, 0)
		if err := conn.Write(ctx, websocket.MessageBinary, req); err != nil {
			t.Fatalf("write sequence %d: %v", seq, err)
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read sequence %d: %v", seq, err)
		}
		if len(data) == 0 || data[0] != BinaryFrameCallSuccess {
			t.Fatalf("sequence %d frame = %v", seq, data)
		}
		gotSeq, n := codec.ReadVarint(data, 1)
		if n <= 0 || gotSeq != seq {
			t.Fatalf("sequence = %d, want %d", gotSeq, seq)
		}
	}
}

func TestWSBinary_NotFound(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Unknown API ID = 999
	reqBuf := []byte{BinaryFrameCallRequest}
	reqBuf = codec.AppendVarint(reqBuf, 5)   // seq
	reqBuf = codec.AppendVarint(reqBuf, 999) // unknown API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)   // mask
	reqBuf = append(reqBuf, 0x00)            // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if data[0] != BinaryFrameCallError {
		t.Fatalf("frame type = %x, want error", data[0])
	}
	seq, n := codec.ReadVarint(data, 1)
	if seq != 5 {
		t.Errorf("seq = %d, want 5", seq)
	}
	werr := decodeWireError(t, data[n+1:])
	if werr.Code != 400 || werr.Name != "BadRequest" {
		t.Errorf("error = %+v, want BadRequest 400", werr)
	}
}

func TestWSBinary_TooShort(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send 1 byte — too short, should be silently dropped
	conn.Write(ctx, websocket.MessageBinary, []byte{0x01})

	// Server should not crash, connection should stay open
	// Send a valid JSON request to verify connection alive
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should still be alive: %v", err)
	}
}

func TestWSJSON_WithSelect(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Request with $select
	req := `{"$id":1,"$api":"getUser","id":1,"$select":"id,name","page":1,"pageSize":10}`
	conn.Write(ctx, websocket.MessageText, []byte(req))

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `"$id":1`) {
		t.Errorf("missing $id: %s", data)
	}
}

func TestWSJSONRejectsMalformedSelectionAndListControls(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	requests := []string{
		`{"$id":7,"$api":"getUser","$select":"name{"}`,
		`{"$id":8,"$api":"getUser","$filters":[{"field":"name","op":"invalid","value":"x"}]}`,
	}
	for _, request := range requests {
		if err := conn.Write(ctx, websocket.MessageText, []byte(request)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !strings.Contains(string(data), `"error":"BadRequest"`) {
			t.Fatalf("response = %s", data)
		}
	}
}

func TestWSJSONSelectionSupportsTypeReturn(t *testing.T) {
	rt := testWSRouter()
	rt.Schema.RegisterType(&schema.TypeDecl{
		Name: "AuthPayload",
		Fields: []schema.Field{
			{ID: 1, Name: "token", Type: schema.FieldString},
			{ID: 2, Name: "expiresAt", Type: schema.FieldDateTime},
		},
	})
	rt.Schema.RegisterAPI(&schema.API{ID: 11, Name: "login", Module: "auth", ReturnType: "AuthPayload"})
	maskApplied := make(chan bool, 1)
	rt.Handle("login", func(ctx context.Context, req *Request) error {
		maskApplied <- bytes.Equal(req.FieldMask, []byte{1, 1})
		req.Buf.B = codec.AppendVarint(req.Buf.B, 0)
		var enc codec.Encoder
		enc.WriteFieldString(1, "token")
		enc.WriteEnd()
		req.Buf.B = append(req.Buf.B, enc.Bytes()...)
		return nil
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"login","$select":"token"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !<-maskApplied {
		t.Fatal("type return selection was not converted to a field mask")
	}
}

func TestWSBinary_HandlerError(t *testing.T) {
	rt := testWSRouter()
	rt.Handle("failBinary", func(ctx context.Context, req *Request) error {
		return errors.NotFound.WithData(errors.ResourceError{Resource: "user"})
	})
	rt.Registry.Register("failBinary", 99)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	reqBuf := []byte{BinaryFrameCallRequest}
	reqBuf = codec.AppendVarint(reqBuf, 7)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 99) // API ID = failBinary
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	reqBuf = append(reqBuf, 0x00)           // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if data[0] != BinaryFrameCallError {
		t.Fatalf("frame type = %x, want error", data[0])
	}
	seq, n := codec.ReadVarint(data, 1)
	if seq != 7 {
		t.Errorf("seq = %d, want 7", seq)
	}
	if got := decodeWireError(t, data[n+1:]); got.Code != 404 || got.Name != "NotFound" {
		t.Errorf("error = %+v", got)
	}
}

func TestWSBinary_InvalidVarint(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send bytes that form an invalid/overflowing varint (all continuation bits set)
	conn.Write(ctx, websocket.MessageBinary, []byte{BinaryFrameCallRequest, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02})

	// Should silently drop. Verify connection alive with a valid request.
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive invalid varint: %v", err)
	}
}

func TestWSBinary_APIRegisteredButNoHandler(t *testing.T) {
	rt := testWSRouter()
	// Register API ID but don't register handler
	rt.Registry.Register("ghostAPI", 77)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	reqBuf := []byte{BinaryFrameCallRequest}
	reqBuf = codec.AppendVarint(reqBuf, 8)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 77) // ghostAPI
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	reqBuf = append(reqBuf, 0x00)           // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Should get NotFound error
	if data[0] != BinaryFrameCallError {
		t.Fatalf("frame type = %x, want error", data[0])
	}
	seq, n := codec.ReadVarint(data, 1)
	if seq != 8 {
		t.Errorf("seq = %d, want 8", seq)
	}
	if got := decodeWireError(t, data[n+1:]); got.Code != 404 || got.Name != "NotFound" {
		t.Errorf("error = %+v", got)
	}
}

func TestWSBinary_PlainError(t *testing.T) {
	rt := testWSRouter()
	// Handler returns plain error (not AppError)
	rt.Handle("plainFail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("something broke")
	})
	rt.Registry.Register("plainFail", 88)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	reqBuf := []byte{BinaryFrameCallRequest}
	reqBuf = codec.AppendVarint(reqBuf, 9)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 88) // plainFail
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	reqBuf = append(reqBuf, 0x00)           // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if data[0] != BinaryFrameCallError {
		t.Fatalf("frame type = %x, want error", data[0])
	}
	seq, n := codec.ReadVarint(data, 1)
	if seq != 9 {
		t.Errorf("seq = %d, want 9", seq)
	}
	if got := decodeWireError(t, data[n+1:]); got.Code != 500 || got.Name != "Internal" {
		t.Errorf("error = %+v, want Internal 500", got)
	}
}

func TestWSJSON_MalformedJSON(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send invalid JSON
	conn.Write(ctx, websocket.MessageText, []byte(`{broken json`))

	// Malformed JSON is silently dropped. Verify alive.
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive malformed JSON: %v", err)
	}
}

func TestWSJSON_PlainError(t *testing.T) {
	rt := testWSRouter()
	rt.Handle("jsonFail", func(ctx context.Context, req *Request) error {
		return fmt.Errorf("plain error")
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":5,"$api":"jsonFail"}`))

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, `"code":500`) {
		t.Errorf("plain error should wrap to 500: %s", got)
	}
}

func TestWSAcceptFail(t *testing.T) {
	rt := testWSRouter()

	// Send Upgrade: websocket but missing required WS headers → Accept fails
	r := httptest.NewRequest(http.MethodGet, "/luvia", nil)
	r.Header.Set("Upgrade", "websocket")
	// Missing Connection, Sec-WebSocket-Key, Sec-WebSocket-Version
	w := httptest.NewRecorder()

	// Should not panic, just return
	rt.ServeHTTP(w, r)

	// Accept failed → connection not upgraded, httptest.Recorder gets error response
	if w.Code == http.StatusSwitchingProtocols {
		t.Error("should NOT upgrade without proper WS headers")
	}
}

func TestWSUpgradeViaHTTP(t *testing.T) {
	rt := testWSRouter()

	// Regular HTTP POST should still work (not upgrade)
	r := httptest.NewRequest(http.MethodPost, "/luvia", strings.NewReader(`{"$api":"getUser","id":1}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	rt.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HTTP POST should work, got %d", w.Code)
	}
}

func TestIsStreamMsg(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		// Top-level key detection
		{"top-level $sub", `{"$sub":"watchDanmaku","roomId":1}`, true},
		{"top-level $unsub", `{"$unsub":"watchDanmaku"}`, true},
		{"second key after comma", `{"roomId":1,"$sub":"watch"}`, true},

		// Must NOT match $sub/$unsub inside string values (injection/false positive)
		{"$sub as string value", `{"action":"$sub","target":"x"}`, false},
		{"$sub embedded in nested object value", `{"data":{"$sub":"x"}}`, false},
		{"$sub in array value", `{"list":["$sub"]}`, false},

		// Whitespace tolerance (real JSON formatters add spaces/newlines)
		{"spaces around colon", `{ "$sub" : "watch" }`, true},
		{"newline before key", "{\n\"$sub\": \"watch\"}", true},
		{"tab indented", "{\t\"$sub\":\t\"x\"}", true},

		// Edge cases that previously caused false positives
		{"key ending with $sub suffix", `{"my$sub":"val"}`, false},
		{"partial match $su", `{"$su":"val"}`, false},

		// Minimal/boundary inputs
		{"too short to match", `{"$s"}`, false},
		{"exact minimum $sub", `{"$sub":"x"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStreamMsg([]byte(tt.data))
			if got != tt.want {
				t.Errorf("isStreamMsg(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

// --- identityFromCtx ---

func TestIdentityFromCtx_WithExtractor(t *testing.T) {
	old := IdentityExtractor
	defer func() { IdentityExtractor = old }()

	IdentityExtractor = func(ctx context.Context) any {
		return "user-42"
	}

	got := identityFromCtx(context.Background())
	if got != "user-42" {
		t.Errorf("expected user-42, got %v", got)
	}
}

func TestIdentityFromCtx_NilExtractor(t *testing.T) {
	old := IdentityExtractor
	defer func() { IdentityExtractor = old }()

	IdentityExtractor = nil
	got := identityFromCtx(context.Background())
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// --- WSBinary stream with different param types ---

func TestWSBinary_SubscribeWithFloatParam(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchFloat")
	rt.Registry.Register("watchFloat", 61)
	rt.Registry.RegisterParams("watchFloat", []ParamMeta{
		{Name: "rate", Type: "Float", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 61) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	var enc codec.Encoder
	enc.WriteFieldFloat(1, 3.14)
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchFloat") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchFloat"))
	}
}

func TestWSBinary_SubscribeWithStringParam(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchStr")
	rt.Registry.Register("watchStr", 62)
	rt.Registry.RegisterParams("watchStr", []ParamMeta{
		{Name: "channel", Type: "String", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 62) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	var enc codec.Encoder
	enc.WriteFieldString(1, "general")
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchStr") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchStr"))
	}
}

func TestWSBinary_SubscribeWithBoolParam(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchBool")
	rt.Registry.Register("watchBool", 63)
	rt.Registry.RegisterParams("watchBool", []ParamMeta{
		{Name: "active", Type: "Boolean", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 63) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	var enc codec.Encoder
	enc.WriteFieldBool(1, true)
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchBool") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchBool"))
	}
}

// --- WSBinary stream with bytes-compatible JSON param ---

func TestWSBinary_SubscribeWithUnsupportedParamType(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchUnsup")
	rt.Registry.Register("watchUnsup", 64)
	rt.Registry.RegisterParams("watchUnsup", []ParamMeta{
		{Name: "data", Type: "JSON", FieldID: 1},
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 64)
	reqBuf = codec.AppendVarint(reqBuf, 0)
	var enc codec.Encoder
	enc.WriteFieldString(1, "{}") // some data for the unsupported field
	enc.WriteEnd()
	reqBuf = append(reqBuf, enc.Bytes()...)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchUnsup") != 1 {
		t.Errorf("JSON bytes param should subscribe, got %d", rt.Streams.SubCount("watchUnsup"))
	}
}

// --- WSBinary stream with native handler ---

func TestWSBinary_SubscribeNativeHandler(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchNativeBin", 65)
	rt.Registry.RegisterParams("watchNativeBin", nil)

	handlerCalled := make(chan struct{}, 1)
	rt.HandleStreamNative("watchNativeBin", func(ctx context.Context, params *StreamParams, identity any, stream *Stream) {
		handlerCalled <- struct{}{}
	})

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 65) // API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)  // no mask
	reqBuf = append(reqBuf, 0x00)           // end params

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	select {
	case <-handlerCalled:
		// good
	case <-time.After(1 * time.Second):
		t.Fatal("native handler was not called for binary stream subscribe")
	}
}

// --- WSBinary stream with field mask ---

func TestWSBinary_SubscribeWithFieldMask(t *testing.T) {
	rt := testWSRouter()
	registerTestStream(rt, "watchMask")
	rt.Registry.Register("watchMask", 66)
	rt.Registry.RegisterParams("watchMask", nil)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 66) // API ID
	mask := codec.AppendSelectionMask(nil, []byte{0xFF, 0x01}, nil)
	reqBuf = codec.AppendVarint(reqBuf, uint64(len(mask)))
	reqBuf = append(reqBuf, mask...)
	reqBuf = append(reqBuf, 0x00) // end params

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	if rt.Streams.SubCount("watchMask") != 1 {
		t.Errorf("sub count = %d, want 1", rt.Streams.SubCount("watchMask"))
	}
}

// --- WSBinary stream: too short data ---

func TestWSBinary_StreamTooShort(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Send stream message with only 1 byte (too short)
	conn.Write(ctx, websocket.MessageBinary, []byte{BinaryFrameSubscribe})
	time.Sleep(50 * time.Millisecond)

	// Should not crash, connection alive
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- WSBinary stream: unknown API ID ---

func TestWSBinary_StreamUnknownAPIID(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var reqBuf []byte
	reqBuf = append(reqBuf, BinaryFrameSubscribe)
	reqBuf = codec.AppendVarint(reqBuf, 999) // unknown API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)
	reqBuf = append(reqBuf, 0x00)

	conn.Write(ctx, websocket.MessageBinary, reqBuf)
	time.Sleep(50 * time.Millisecond)

	// Should not crash, no subscription created
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- WSBinary stream: invalid varint in stream message ---

func TestWSBinary_StreamInvalidVarint(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(ctx, websocket.MessageBinary, []byte{BinaryFrameSubscribe, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02})
	time.Sleep(50 * time.Millisecond)

	// Should not crash
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- WSBinary unsubscribe non-existent ---

func TestWSBinary_UnsubscribeNonExistent(t *testing.T) {
	rt := testWSRouter()
	rt.Registry.Register("watchGhost", 67)

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Unsubscribe without subscribing first
	var unsubBuf []byte
	unsubBuf = append(unsubBuf, BinaryFrameUnsubscribe)
	unsubBuf = codec.AppendVarint(unsubBuf, 67)
	conn.Write(ctx, websocket.MessageBinary, unsubBuf)
	time.Sleep(50 * time.Millisecond)

	// Should not crash
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- isStreamMsg with escape in string ---

func TestIsStreamMsg_EscapeInString(t *testing.T) {
	// String value with escaped quote shouldn't break parsing
	got := isStreamMsg([]byte(`{"key":"val\"ue","$sub":"x"}`))
	if !got {
		t.Error("should find $sub after escaped quote in string value")
	}
}

// --- WSJSON subscribe with empty $sub name ---

func TestWSJSON_SubscribeEmptyName(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Empty $sub name — should be ignored
	conn.Write(ctx, websocket.MessageText, []byte(`{"$sub":""}`))
	time.Sleep(50 * time.Millisecond)

	// Should not crash, and no subscription created
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- WSJSON unsubscribe non-existent ---

func TestWSJSON_UnsubscribeNonExistent(t *testing.T) {
	rt := testWSRouter()
	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.Write(ctx, websocket.MessageText, []byte(`{"$unsub":"nonexistent"}`))
	time.Sleep(50 * time.Millisecond)

	// Should not crash
	conn.Write(ctx, websocket.MessageText, []byte(`{"$id":1,"$api":"getUser"}`))
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("connection should survive: %v", err)
	}
}

// --- WS origins config ---

func TestWSOrigins(t *testing.T) {
	rt := testWSRouter()
	rt.WSOrigins = []string{"http://example.com"}

	srv := httptest.NewServer(rt)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// This should fail because origin doesn't match
	_, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.com"}},
	})
	if err == nil {
		t.Error("should reject connection with wrong origin")
	}
}

func TestWSAllowAllOrigins(t *testing.T) {
	rt := testWSRouter()
	rt.WSAllowAllOrigins = true

	srv := httptest.NewServer(rt)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://example.com"}},
	})
	if err != nil {
		t.Fatalf("wildcard origin policy rejected connection: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

func TestHasColonAfter(t *testing.T) {
	tests := []struct {
		name string
		data string
		pos  int
		want bool
	}{
		// Normal JSON key-value separator
		{"immediate colon", `"key":val`, 5, true},
		{"colon after spaces", `"key" : val`, 5, true},
		{"colon after tab", "\"key\"\t:", 5, true},

		// No colon — means the match was inside a value, not a key
		{"no more data after pos", `"key"`, 5, false},
		{"next char is comma (value context)", `"key",`, 5, false},
		{"next char is closing brace", `"key"}`, 5, false},

		// Pos at end of buffer
		{"pos equals len", `abc`, 3, false},
		{"pos beyond len", `ab`, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasColonAfter([]byte(tt.data), tt.pos)
			if got != tt.want {
				t.Errorf("hasColonAfter(%q, %d) = %v, want %v", tt.data, tt.pos, got, tt.want)
			}
		})
	}
}
