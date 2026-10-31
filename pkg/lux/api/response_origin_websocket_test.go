package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"nhooyr.io/websocket"
)

func TestResponseOriginWebSocketIsolation(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, waiting := range []bool{false, true} {
			t.Run(fmt.Sprintf("binary=%t/waiting=%t", binary, waiting), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testOriginWebSocket(t, binary, waiting) })
			})
		}
	}
}

func testOriginWebSocket(t *testing.T, binary, waiting bool) {
	t.Helper()
	options := ResponseOriginOptions{MaxConcurrent: 1}
	if waiting {
		options.MaxWaiting, options.WaitTimeout = 1, time.Second
	}
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	binding, err := NewResponseCache(backend, CacheNamespace{Project: "p", Environment: "e", Service: "s", Revision: "r"}, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, conn := newInMemoryWebSocket(t, originBudgetRouter(t, binding))
	call := func(name string, id int, sequence uint64, busy bool) {
		t.Helper()
		originWebSocketCall(t, ctx, conn, binary, name, id, sequence, busy)
	}
	call("first", 1, 1, false)
	if err := binding.origin.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	// Hold the only origin slot without changing the route or cached entry.
	func() {
		defer binding.origin.release()
		call("second", 2, ^uint64(0), true)
		call("first", 1, 2, false)
		call("plain", 3, 3, false)
		if len(binding.origin.slots) != 1 || len(binding.origin.waiters) != 0 {
			t.Fatal("rejection or bypass corrupted origin admission")
		}
	}()
	call("second", 2, 4, false)
	if len(binding.origin.slots) != 0 || len(binding.origin.waiters) != 0 {
		t.Fatal("completed WebSocket call leaked admission")
	}
	if waiting {
		testOriginWebSocketDisconnect(t, ctx, conn, binary, binding)
	}
}

func testOriginWebSocketDisconnect(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, binding *ResponseCache) {
	t.Helper()
	if err := binding.origin.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer binding.origin.release()
	// This fixture owns an isolated memory backend. Clear its warmed entries;
	// policy keys intentionally use a derived prefix, not binding.prefix.
	if err := binding.backend.Invalidate(ctx, ""); err != nil {
		t.Fatal(err)
	}
	kind, request := originWebSocketRequest(binary, "second", 2, 5)
	writeWSFrame(t, ctx, conn, kind, request)
	synctest.Wait()
	if len(binding.origin.waiters) != 1 {
		t.Fatal("WebSocket call did not enter the bounded queue")
	}
	// The reader loop must still dispatch unrelated calls while one waits.
	originWebSocketCall(t, ctx, conn, binary, "plain", 3, 6, false)
	if len(binding.origin.waiters) != 1 {
		t.Fatal("unrelated call consumed the waiting request")
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if len(binding.origin.waiters) != 0 || len(binding.origin.slots) != 1 {
		t.Fatal("disconnect leaked a waiter or released someone else's slot")
	}
}

func originWebSocketRequest(binary bool, name string, id int, sequence uint64) (websocket.MessageType, []byte) {
	kind := websocket.MessageText
	request := []byte(fmt.Sprintf(`{"$id":%d,"$api":%q}`, sequence, name))
	if binary {
		kind = websocket.MessageBinary
		request = codec.AppendVarint([]byte{BinaryFrameCallRequest}, sequence)
		request = codec.AppendVarint(request, uint64(id))
		request = append(request, 0, 0) // Empty selection and parameter terminator.
	}
	return kind, request
}

func originWebSocketCall(t *testing.T, ctx context.Context, conn *websocket.Conn, binary bool, name string, id int, sequence uint64, busy bool) {
	t.Helper()
	kind, request := originWebSocketRequest(binary, name, id, sequence)
	writeWSFrame(t, ctx, conn, kind, request)
	if !busy {
		want := []byte(fmt.Sprintf(`{"$id":%d,"data":42}`, sequence))
		if binary {
			want = codec.AppendVarint([]byte{BinaryFrameCallSuccess}, sequence)
			want = codec.AppendSvarint(want, 42)
		}
		requireWSFrame(t, ctx, conn, kind, want)
		return
	}
	gotKind, body, err := conn.Read(ctx)
	if err != nil || gotKind != kind {
		t.Fatalf("overload frame: kind=%v error=%v", gotKind, err)
	}
	requireOriginWebSocketError(t, body, binary, sequence)
}

func requireOriginWebSocketError(t *testing.T, body []byte, binary bool, sequence uint64) {
	t.Helper()
	var got wireError
	var actualID uint64
	if binary {
		if len(body) < 2 || body[0] != BinaryFrameCallError {
			t.Fatalf("expected call error, got %x", body)
		}
		var size int
		actualID, size = codec.ReadVarint(body, 1)
		if size <= 0 {
			t.Fatal("invalid overload request ID")
		}
		got = decodeWireError(t, body[1+size:])
	} else {
		var response struct {
			ID             uint64 `json:"$id"`
			Error, Message string
			Code           int
			Data           json.RawMessage
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		actualID = response.ID
		got = wireError{Code: response.Code, Name: response.Error, Message: response.Message, Data: response.Data}
	}
	if actualID != sequence || got.Code != 503 || got.Name != "CacheOriginBusy" ||
		got.Message != "Cache origin capacity exhausted" || len(got.Data) != 0 {
		t.Fatalf("overload changed identity or exposed success data: id=%d error=%+v", actualID, got)
	}
}
