package api

import (
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

	// Register handler that writes a binary User
	rt.Handle("getUser", func(ctx context.Context, req *Request) error {
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

	return rt
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

	// Build binary request: [seq=1][API ID=10][mask=0][param field1=42][0x00]
	var reqBuf []byte
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

	// Parse response: [seq varint][0x01=ok][payload]
	seq, n := codec.ReadVarint(data, 0)
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	if data[n] != 0x01 {
		t.Errorf("status = %x, want 0x01 (ok)", data[n])
	}

	// Decode payload
	payload := data[n+1:]
	dec := codec.NewDecoder(payload)
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
	var reqBuf []byte
	reqBuf = codec.AppendVarint(reqBuf, 5)   // seq
	reqBuf = codec.AppendVarint(reqBuf, 999) // unknown API ID
	reqBuf = codec.AppendVarint(reqBuf, 0)   // mask
	reqBuf = append(reqBuf, 0x00)            // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Parse error: [seq][0x00=error][code svarint][name string][msg string]
	seq, n := codec.ReadVarint(data, 0)
	if seq != 5 {
		t.Errorf("seq = %d, want 5", seq)
	}
	if data[n] != 0x00 {
		t.Errorf("status = %x, want 0x00 (error)", data[n])
	}
	off := n + 1
	code, cn := codec.ReadSvarint(data, off)
	if code != 400 {
		t.Errorf("code = %d, want 400", code)
	}
	off += cn
	errName, _ := codec.ReadString(data, off)
	if errName != "BadRequest" {
		t.Errorf("error = %q, want BadRequest", errName)
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

	var reqBuf []byte
	reqBuf = codec.AppendVarint(reqBuf, 7)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 99) // API ID = failBinary
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	reqBuf = append(reqBuf, 0x00)           // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Error response: [seq=7][0x00=error][code][name][msg]
	seq, n := codec.ReadVarint(data, 0)
	if seq != 7 {
		t.Errorf("seq = %d, want 7", seq)
	}
	if data[n] != 0x00 {
		t.Errorf("status = %x, want 0x00 (error)", data[n])
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
	conn.Write(ctx, websocket.MessageBinary, []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02})

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

	var reqBuf []byte
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
	seq, n := codec.ReadVarint(data, 0)
	if seq != 8 {
		t.Errorf("seq = %d, want 8", seq)
	}
	if data[n] != 0x00 {
		t.Errorf("status should be error (0x00), got %x", data[n])
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

	var reqBuf []byte
	reqBuf = codec.AppendVarint(reqBuf, 9)  // seq
	reqBuf = codec.AppendVarint(reqBuf, 88) // plainFail
	reqBuf = codec.AppendVarint(reqBuf, 0)  // mask
	reqBuf = append(reqBuf, 0x00)           // end

	conn.Write(ctx, websocket.MessageBinary, reqBuf)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	seq, n := codec.ReadVarint(data, 0)
	if seq != 9 {
		t.Errorf("seq = %d, want 9", seq)
	}
	if data[n] != 0x00 {
		t.Errorf("status should be error, got %x", data[n])
	}
	// Wrapped error should have InternalServerError code (500)
	off := n + 1
	code, _ := codec.ReadSvarint(data, off)
	if code != 500 {
		t.Errorf("code = %d, want 500 (plain error wraps to InternalServerError)", code)
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
