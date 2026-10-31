package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"nhooyr.io/websocket"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestTransportParserWithoutSharedLease(t *testing.T) {
	rt := NewRouterWithOptions(RouterOptions{})
	rt.bodyLimits.RequestBytes = 64
	if err := rt.Registry.Register("health", 1); err != nil {
		t.Fatal(err)
	}
	binary, err := EncodeBinaryRequest(1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		binary bool
		body   []byte
		valid  bool
	}{
		{"json", false, []byte(`{"$api":"health"}`), true},
		{"binary", true, binary, true},
		{"bad-json", false, []byte(`{"$api":`), false},
		{"bad-binary", true, []byte{0x80}, false},
		{"oversize-json", false, bytes.Repeat([]byte("x"), 65), false},
		{"oversize-binary", true, bytes.Repeat([]byte("x"), 65), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpRequest := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(tc.body))
			req, buf, err := rt.parseTransportRequest(httpRequest, tc.binary)
			if !tc.valid {
				if err == nil || req != nil || buf != nil {
					t.Fatal("invalid request returned success or retained pooled storage", req, buf, err)
				}
				return
			}
			if err != nil || req == nil || req.API != "health" {
				t.Fatal(req, err)
			}
			PutRequest(req)
			if buf != nil {
				putBody(buf)
			}
		})
	}
}

func TestEmptyWSMessageReleasesReceiveLease(t *testing.T) {
	rt := testWSRouter()
	var used atomic.Int64
	lease := receiveLease{counter: &used, limit: 64}
	if err := lease.acquire(64); err != nil {
		t.Fatal(err)
	}
	if !rt.handleWSMessage(t.Context(), nil, websocket.MessageBinary, nil, nil, lease) {
		t.Fatal("empty message unexpectedly ended connection")
	}
	if used.Load() != 0 || rt.wsCalls.Load() != 0 {
		t.Fatal("empty message retained memory or started a call")
	}
}

func TestMalformedWSBinaryDoesNotDispatch(t *testing.T) {
	rt := testWSRouter()
	for _, data := range [][]byte{nil, {BinaryFrameCallRequest}, {0, 1, 1}, {BinaryFrameCallRequest, 0x80, 0x80}} {
		// No connection is needed: these frames must return before any response or handler.
		rt.handleWSBinary(t.Context(), nil, data)
	}
	rt.dispatchWSBinary(t.Context(), nil, []byte{BinaryFrameCallRequest, 0x80, 0x80})
	if rt.wsCalls.Load() != 0 {
		t.Fatal("malformed frame acquired a call slot")
	}
}

func TestReceiveBudgetBeforeAllocationAndGrowth(t *testing.T) {
	var used atomic.Int64
	lease := receiveLease{counter: &used, limit: 8192}
	data := bytes.Repeat([]byte{'x'}, 4097)
	buf, err := readBodyBufferBudget(bytes.NewReader(data), nil, 10000, &lease)
	if !errors.Is(err, errReceiveCapacity) || cap(buf) != 4096 || used.Load() != 4096 {
		t.Fatalf("growth exceeded overlap budget: cap=%d used=%d err=%v", cap(buf), used.Load(), err)
	}
	lease.release()
	lease.release()
	if used.Load() != 0 {
		t.Fatal("budget leaked or released twice")
	}
	lease = receiveLease{counter: &used, limit: 16384}
	buf, err = readBodyBufferBudget(bytes.NewReader(data), nil, 10000, &lease)
	if err != nil || len(buf) != len(data) || used.Load() != int64(cap(buf)) {
		t.Fatal("successful growth charged incorrectly", err)
	}
	lease.release()
}

func TestReceiveBudgetHTTPRejectsBeforeReading(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{})
	router.receiveBytes.Store(router.bodyLimits.ReceiveBytesTotal)
	body := &countedBody{Reader: bytes.NewReader([]byte(`{"$api":"test"}`))}
	req := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	req.Body = body
	req.ContentLength = -1
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 503 || body.read != 0 || router.receiveBytes.Load() != router.bodyLimits.ReceiveBytesTotal {
		t.Fatalf("status=%d read=%d used=%d", res.Code, body.read, router.receiveBytes.Load())
	}
}

func TestReceiveBudgetInvalidWSDispatchReleases(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{})
	for _, binary := range []bool{false, true} {
		lease := router.newReceiveLease()
		if err := lease.acquire(4096); err != nil {
			t.Fatal(err)
		}
		if binary {
			router.dispatchWSBinaryOwned(context.Background(), &wsConn{}, []byte{BinaryFrameCallRequest}, lease)
		} else {
			router.dispatchWSJSONOwned(context.Background(), &wsConn{}, map[string]json.RawMessage{}, lease)
		}
		if router.receiveBytes.Load() != 0 {
			t.Fatal("invalid frame retained reservation")
		}
	}
}

func TestReceiveBudgetWSRejectsExhaustedCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := testWSRouter()
		router.receiveBytes.Store(router.bodyLimits.ReceiveBytesTotal)
		ctx, conn := newInMemoryWebSocket(t, router)
		writeWSFrame(t, ctx, conn, websocket.MessageText, sequenceRequest(websocket.MessageText, "getUser", 1))
		_, _, err := conn.Read(ctx)
		if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
			t.Fatalf("unexpected overload close: %v", err)
		}
		synctest.Wait()
		if router.receiveBytes.Load() != router.bodyLimits.ReceiveBytesTotal || router.wsCalls.Load() != 0 {
			t.Fatal("rejected message altered existing reservations or dispatched")
		}
	})
}

func TestWSConnectionCapacityRejectsBeforeUpgrade(t *testing.T) {
	router := NewRouterWithOptions(RouterOptions{})
	router.wsConnections.Store(router.wsLimits.ConnectionsTotal)
	response := httptest.NewRecorder()
	router.handleWebSocket(response, httptest.NewRequest(http.MethodGet, "/luvia", nil))
	if response.Code != 503 || router.wsConnections.Load() != router.wsLimits.ConnectionsTotal {
		t.Fatal("connection admission exceeded capacity")
	}
}

func BenchmarkReceiveBudget(b *testing.B) {
	var used atomic.Int64
	lease := receiveLease{counter: &used, limit: 64 << 20}
	b.ReportAllocs()
	for b.Loop() {
		if err := lease.acquire(4096); err != nil {
			b.Fatal(err)
		}
		lease.release()
	}
}

func TestReceiveBudgetFailureOwnership(t *testing.T) {
	for _, reader := range []io.Reader{bytes.NewReader(make([]byte, 33)), invalidBodyReader(-1)} {
		var used atomic.Int64
		lease := receiveLease{counter: &used, limit: 4096}
		_, err := readBodyBufferBudget(reader, nil, 32, &lease)
		if err == nil {
			t.Fatal("expected read failure")
		}
		lease.release()
		if used.Load() != 0 {
			t.Fatal("failed read retained budget")
		}
	}
}

func TestReceiveBudgetPreservesPooledCapacity(t *testing.T) {
	var used atomic.Int64
	lease := receiveLease{counter: &used, limit: 8192}
	buf, err := readBodyBufferBudget(bytes.NewReader([]byte("ok")), make([]byte, 0, 4096), 32, &lease)
	if err != nil || cap(buf) != 4096 || used.Load() != 4096 {
		t.Fatalf("pooled backing storage hidden: cap=%d used=%d err=%v", cap(buf), used.Load(), err)
	}
	lease.release()
}

func TestBufferedReceiveBudgetRejectsWithoutConsuming(t *testing.T) {
	var used atomic.Int64
	used.Store(4)
	lease := receiveLease{counter: &used, limit: 8}
	storage := make([]byte, 3, 8)
	copy(storage, "abc")
	body := NewBufferedBody(storage)
	data, pooled, err := readTransportBodyBudget(body, 8, &lease)
	if !errors.Is(err, errReceiveCapacity) || data != nil || pooled != nil {
		t.Fatal("capacity exhaustion accepted buffered body", err)
	}
	if body.offset != 0 || used.Load() != 4 || lease.bytes != 0 {
		t.Fatal("rejection consumed body or changed another reservation")
	}
	lease.release()
	if used.Load() != 4 || string(storage) != "abc" {
		t.Fatal("rejected borrow changed transport ownership")
	}
}

func TestBufferedReceiveFailureRetainsLeaseUntilRelease(t *testing.T) {
	for _, closed := range []bool{false, true} {
		var used atomic.Int64
		lease := receiveLease{counter: &used, limit: 16}
		body := NewBufferedBody(make([]byte, 3, 8))
		want := errRequestBodyTooLarge
		if closed {
			_ = body.Close()
			want = io.ErrClosedPipe
		}
		data, pooled, err := readTransportBodyBudget(body, 2, &lease)
		if !errors.Is(err, want) || data != nil || pooled != nil || used.Load() != 8 {
			t.Fatal("failed borrow lost error or backing capacity accounting", err)
		}
		lease.release()
		lease.release()
		if used.Load() != 0 {
			t.Fatal("failed borrow leaked or double-released budget")
		}
	}
}

func TestInvalidBodyLimitRejectsBeforeReadingOrReserving(t *testing.T) {
	for _, limit := range []int{-1, int(^uint(0) >> 1)} {
		var used atomic.Int64
		lease := receiveLease{counter: &used, limit: 16}
		reader := &countedBody{Reader: bytes.NewReader([]byte("payload"))}
		buf, err := readBodyBufferBudget(reader, make([]byte, 0, 8), limit, &lease)
		if !errors.Is(err, errInvalidBodyLimits) || len(buf) != 0 || cap(buf) != 8 {
			t.Fatal("invalid limit accepted or changed storage", err)
		}
		if reader.read != 0 || used.Load() != 0 || lease.bytes != 0 {
			t.Fatal("invalid limit performed IO or reserved capacity")
		}
	}
}
