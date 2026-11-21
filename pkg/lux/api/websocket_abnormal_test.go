package api

import (
	"bytes"
	"context"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

type exitingWSErrorData struct{}

func (exitingWSErrorData) AppendJSON([]byte) ([]byte, error) { runtime.Goexit(); return nil, nil }
func (exitingWSErrorData) AppendText(dst []byte, _ string) ([]byte, bool) {
	return dst, false
}

type exitingWSCause struct{}

func (exitingWSCause) Error() string { runtime.Goexit(); return "" }

func TestWSUnaryAbnormalExitCompletesOnlyItsCall(t *testing.T) {
	for _, mode := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, fault := range []string{"handler-goexit", "error-data-goexit", "debug-cause-goexit", "panic-control"} {
			t.Run(mode.String()+"/"+fault, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) { testWSUnaryAbnormalExit(t, mode, fault) })
			})
		}
	}
}

func testWSUnaryAbnormalExit(t *testing.T, mode websocket.MessageType, fault string) {
	t.Helper()
	router := testWSRouter()
	original := router.handlers["getUser"].call
	buffer := make(chan *ResponseBuf, 1)
	router.devMode = fault == "debug-cause-goexit"
	router.Handle("getUser", wsAbnormalHandler(original, fault, buffer))
	ctx, conn := newInMemoryWebSocket(t, router)
	writeWSFrame(t, ctx, conn, mode, sequenceRequest(mode, "getUser", 1))
	originalBuffer := <-buffer
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	_, payload, err := conn.Read(readCtx)
	cancel()
	if err != nil {
		t.Error("abnormal call never received a terminal response", err)
	} else {
		requireWSCallError(t, mode, payload, 1, 500)
		if bytes.Contains(payload, []byte("private-response")) {
			t.Fatal("private response leaked", string(payload))
		}
		writeWSFrame(t, ctx, conn, mode, sequenceRequest(mode, "getUser", 2))
		_, payload, err = conn.Read(ctx)
		if err != nil {
			t.Fatal("abnormal call damaged the shared connection", err)
		}
		assertSequenceResponse(t, mode, "getUser", 2, payload)
	}
	synctest.Wait()
	if originalBuffer.Identity != nil || router.wsCalls.Load() != 0 || router.receiveBytes.Load() != 0 {
		t.Fatal("abnormal call retained response ownership or capacity", originalBuffer.Identity, router.wsCalls.Load(), router.receiveBytes.Load())
	}
}

func wsAbnormalHandler(original HandlerFunc, fault string, buffer chan *ResponseBuf) HandlerFunc {
	var calls atomic.Int64
	return func(ctx context.Context, req *Request) error {
		if calls.Add(1) != 1 {
			return original(ctx, req)
		}
		req.Buf.B = append(req.Buf.B, "private-response"...)
		req.Buf.Identity = "private-owner"
		buffer <- req.Buf
		switch fault {
		case "handler-goexit":
			runtime.Goexit()
		case "error-data-goexit":
			return errors.BadRequest.WithData(exitingWSErrorData{})
		case "debug-cause-goexit":
			return errors.BadRequest.WithCause(exitingWSCause{})
		default:
			panic("private-response")
		}
		return nil
	}
}

type abortingWSOutputContext struct {
	context.Context
	panic bool
}

func (c abortingWSOutputContext) Err() error {
	if c.panic {
		panic("private-output")
	}
	runtime.Goexit()
	return nil
}

func TestWSUnaryFallbackFailureClosesConnection(t *testing.T) {
	for _, panicOutput := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := testWSRouter()
			ctx, connection := newInMemoryWebSocket(t, router)
			call := wsUnaryCall{ctx: abortingWSOutputContext{ctx, panicOutput}, ws: &wsConn{conn: connection}, id: 1}
			done := make(chan struct{})
			go func() { defer close(done); call.finish() }()
			<-done
			if _, _, err := connection.Read(ctx); err == nil {
				t.Fatal("failed fallback left the connection open")
			}
			synctest.Wait()
			if router.wsConnections.Load() != 0 {
				t.Fatal("failed fallback retained connection capacity")
			}
		})
	}
}

func TestWSUnaryExitAfterWriteClosesConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, connection := newInMemoryWebSocket(t, testWSRouter())
		call := wsUnaryCall{ws: &wsConn{conn: connection}, writing: true}
		call.finish()
		if _, _, err := connection.Read(ctx); err == nil {
			t.Fatal("partial terminal write must close the connection")
		}
	})
	(&wsUnaryCall{returned: true}).finish()
}

func TestWSUnaryUntrustedBinaryHeaderIsIgnored(t *testing.T) {
	router := testWSRouter()
	for _, data := range [][]byte{nil, {BinaryFrameCallError, 1, 0}, {BinaryFrameCallRequest, 0x80, 0x80}} {
		router.handleWSBinary(t.Context(), nil, data)
	}
}

func TestWSUnaryAbnormalExitRealNetwork(t *testing.T) {
	for _, mode := range []websocket.MessageType{websocket.MessageText, websocket.MessageBinary} {
		for _, fault := range []string{"handler-goexit", "error-data-goexit"} {
			t.Run(mode.String()+"/"+fault, func(t *testing.T) {
				router := testWSRouter()
				buffers := make(chan *ResponseBuf, 1)
				router.Handle("getUser", wsAbnormalHandler(router.handlers["getUser"].call, fault, buffers))
				server := httptest.NewServer(router)
				defer server.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				connection, _, err := websocket.Dial(ctx, "ws"+server.URL[4:], nil)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.CloseNow()
				writeWSFrame(t, ctx, connection, mode, sequenceRequest(mode, "getUser", 1))
				kind, payload, err := connection.Read(ctx)
				if err != nil || kind != mode {
					t.Fatal("abnormal request lost its result", kind, err)
				}
				requireWSCallError(t, mode, payload, 1, 500)
				if bytes.Contains(payload, []byte("private-response")) {
					t.Fatal("fallback leaked private data")
				}
				writeWSFrame(t, ctx, connection, mode, sequenceRequest(mode, "getUser", 2))
				kind, payload, err = connection.Read(ctx)
				if err != nil || kind != mode {
					t.Fatal("shared connection was damaged", kind, err)
				}
				assertSequenceResponse(t, mode, "getUser", 2, payload)
			})
		}
	}
}
