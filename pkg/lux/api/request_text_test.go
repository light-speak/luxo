package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestJSONRequestRejectsInvalidUTF8BeforeDecode(t *testing.T) {
	for _, invalid := range invalidQuoteInputs() {
		for _, frame := range []string{
			`{"$api":"text","value":"` + invalid + `"}`,
			`{"$api":"` + invalid + `"}`,
			`{"$api":"text","` + invalid + `":1}`,
			`{"$api":"text","raw":{"nested":["` + invalid + `"]}}`,
		} {
			req, err := ParseRequest(makeReq(frame))
			if err == nil || req != nil {
				t.Errorf("invalid text accepted: %x / %v", frame, err)
			}
		}
	}
}

func TestHTTPJSONInvalidTextDoesNotRunHandler(t *testing.T) {
	for _, traced := range []bool{false, true} {
		rt := NewRouter()
		rt.SetDevMode(traced)
		called := false
		rt.Handle("text", func(context.Context, *Request) error { called = true; return nil })
		r := makeReq("{\"$api\":\"text\",\"value\":\"\xff\"}")
		r.Header.Set("Content-Type", "application/json; charset=iso-8859-1")
		if traced {
			r.Header.Set(DebugTraceRequestHeader, "true")
		}
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || called {
			t.Fatalf("invalid JSON executed: status=%d called=%v", w.Code, called)
		}
		if !json.Valid(w.Body.Bytes()) || bytes.Contains(w.Body.Bytes(), []byte{0xff}) {
			t.Fatal("invalid error response")
		}
	}
}

type textBoundaryBody struct {
	*strings.Reader
	closed bool
}

func (b *textBoundaryBody) Close() error {
	b.closed = true
	return nil
}

func TestJSONRequestInvalidTextClosesBody(t *testing.T) {
	body := &textBoundaryBody{Reader: strings.NewReader("{\"$api\":\"text\",\"value\":\"\xff\"}")}
	req, err := ParseRequest(&http.Request{Body: body})
	if req != nil || err != errInvalidJSONUTF8 || !body.closed {
		t.Fatalf("text rejection did not close body: %v, closed=%v", err, body.closed)
	}
	valid, err := ParseRequest(makeReq(`{"$api":"text","value":"東京"}`))
	if err != nil || valid.API != "text" || string(valid.Params["value"]) != `"東京"` {
		t.Fatalf("request after rejection failed: %v", err)
	}
}

func TestJSONRequestPreservesValidTextAndNumbers(t *testing.T) {
	const raw = `{"integer":123456789012345678901234567890,"decimal":1.234567890123456789}`
	for _, value := range []string{"", "\ufeff東京", "\ufffd", "e\u0301", "\U0001f642", "<>&\u2028\u2029", "\x00\b\f"} {
		quoted, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		req, err := ParseRequest(makeReq(`{"$api":"text","value":` + string(quoted) + `,"raw":` + raw + `}`))
		if err != nil {
			t.Fatal(err)
		}
		got, err := req.ParamString("value")
		if err != nil || got != value || string(req.Params["raw"]) != raw {
			t.Fatal("JSON values changed")
		}
	}
}

func TestWSTextRejectsInvalidUnicodeBeforeDispatch(t *testing.T) {
	for _, frame := range []string{
		"{\"$id\":1,\"$api\":\"text\",\"value\":\"\xff\"}",
		"{\"$id\":1,\"$sub\":\"watch\",\"value\":\"\xff\"}",
		"{\"$unsub\":1,\"ignored\":\"\xff\"}",
		"{\"$id\":1,\"$api\":\"text\",\"\xff\":0}",
		`{"$id":1,"$api":"text","value":"\uD800"}`,
		`{"$id":1,"$sub":"watch","value":"\uD800"}`,
		`{"$unsub":1,"ignored":"\uDC00"}`,
		`{"$id":1,"$api":"text","\uDC00":0}`,
		`{"$id":1,"$api":"text","value":"\uD800","value":"valid"}`,
	} {
		t.Run(frame, func(t *testing.T) {
			rt := NewRouter()
			var calls atomic.Int64
			rt.Schema.RegisterAPI(&schema.API{Name: "text", ReturnType: "String"})
			rt.Handle("text", func(_ context.Context, req *Request) error {
				calls.Add(1)
				var err error
				req.Buf.B, err = codec.AppendString(nil, "ok")
				return err
			})
			registerTextBoundaryStream(rt)
			rt.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error {
				calls.Add(1)
				<-ctx.Done()
				return ctx.Err()
			})
			ctx, conn, done := newTextBoundarySocket(t, rt)
			writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(frame))
			_, data, err := conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusInvalidFramePayloadData {
				t.Fatalf("invalid frame did not close with 1007: %q / %v", data, err)
			}
			waitTextBoundaryDone(t, ctx, done)
			if calls.Load() != 0 || rt.Streams.SubCount("watch") != 0 {
				t.Fatal("invalid frame dispatched or leaked subscription")
			}
		})
	}
}

func TestWSEscapedStreamKeysAreRoutedAndCancelled(t *testing.T) {
	rt := NewRouter()
	registerTextBoundaryStream(rt)
	started, stopped := make(chan struct{}), make(chan struct{})
	rt.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	})
	ctx, conn, _ := newTextBoundarySocket(t, rt)
	writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"\u0024sub":"watch","value":"\ud800\udc00"}`))
	requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":1,"ok":true}`))
	waitTextBoundaryDone(t, ctx, started)
	writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"\u0024unsub":1}`))
	waitTextBoundaryDone(t, ctx, stopped)
	if rt.Streams.SubCount("watch") != 0 {
		t.Fatal("escaped unsubscribe leaked the subscription")
	}
}

func registerTextBoundaryStream(rt *Router) {
	rt.Schema.RegisterAPI(&schema.API{ID: 9, Name: "watch", Stream: true,
		Params: []schema.Param{{ID: 1, Name: "value", Type: schema.FieldString}}})
	rt.Registry.Register("watch", 9)
	rt.Registry.RegisterParams("watch", []ParamMeta{{Name: "value", Type: "String", FieldID: 1}})
}

func waitTextBoundaryDone(t *testing.T, ctx context.Context, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("connection or subscription did not finish")
	}
}

func TestWSTextInvalidFrameCleansOnlyItsSubscriptions(t *testing.T) {
	for _, invalid := range []string{"\xff", `\uD800`} {
		t.Run(invalid, func(t *testing.T) { assertWSTextInvalidFrameIsolation(t, invalid) })
	}
}

func assertWSTextInvalidFrameIsolation(t *testing.T, invalid string) {
	t.Helper()
	rt := NewRouter()
	registerTextBoundaryStream(rt)
	started, stopped := make(chan struct{}, 2), make(chan struct{}, 2)
	rt.HandleStreamNative("watch", func(ctx context.Context, _ *StreamParams, _ any, _ *Stream) error {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	})
	ctx, first, firstDone := newTextBoundarySocket(t, rt)
	_, second, secondDone := newTextBoundarySocket(t, rt)
	for _, conn := range []*websocket.Conn{first, second} {
		writeWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":1,"$sub":"watch","value":"東京"}`))
		requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":1,"ok":true}`))
		waitTextBoundaryDone(t, ctx, started)
	}
	if rt.Streams.SubCount("watch") != 2 {
		t.Fatal("valid subscriptions did not start")
	}
	writeWSFrame(t, ctx, first, websocket.MessageText, []byte(`{"$unsub":1,"ignored":"`+invalid+`"}`))
	_, _, err := first.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInvalidFramePayloadData {
		t.Fatalf("invalid text close: %v", err)
	}
	waitTextBoundaryDone(t, ctx, firstDone)
	waitTextBoundaryDone(t, ctx, stopped)
	if rt.Streams.SubCount("watch") != 1 {
		t.Fatal("other connection subscription lost or rejected connection leaked")
	}
	second.CloseNow()
	waitTextBoundaryDone(t, ctx, secondDone)
	waitTextBoundaryDone(t, ctx, stopped)
	if rt.Streams.SubCount("watch") != 0 {
		t.Fatal("subscription leaked after both connections closed")
	}
}

func TestWSBinaryBytesRemainOpaque(t *testing.T) {
	rt := NewRouter()
	meta := []ParamMeta{{Name: "value", Type: "Bytes", FieldID: 1}}
	rt.Schema.RegisterAPI(&schema.API{ID: 9, Name: "bytes", ReturnType: "Bytes"})
	rt.Registry.Register("bytes", 9)
	rt.Registry.RegisterParams("bytes", meta)
	rt.Handle("bytes", func(_ context.Context, req *Request) error {
		raw, present := req.findParam("value")
		value, ok := raw.([]byte)
		if !present || !ok {
			return fmt.Errorf("missing binary Bytes parameter")
		}
		req.Buf.B = codec.AppendBytes(nil, value)
		return nil
	})
	ctx, conn, _ := newTextBoundarySocket(t, rt)
	for _, value := range invalidQuoteInputs() {
		payload := mustEncodeBinaryRequest(t, 9, nil, map[string]any{"value": []byte(value)}, meta)
		frame := append(codec.AppendVarint([]byte{BinaryFrameCallRequest}, 42), payload...)
		writeWSFrame(t, ctx, conn, websocket.MessageBinary, frame)
		want := codec.AppendBytes(codec.AppendVarint([]byte{BinaryFrameCallSuccess}, 42), []byte(value))
		requireWSFrame(t, ctx, conn, websocket.MessageBinary, want)
	}
}

func newTextBoundarySocket(t *testing.T, rt *Router) (context.Context, *websocket.Conn, <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); rt.ServeHTTP(w, r) }))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws://example.com", &websocket.DialOptions{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(1 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return ctx, conn, done
}

func TestWSTextPreservesValidUTF8(t *testing.T) {
	rt := NewRouter()
	rt.Schema.RegisterAPI(&schema.API{Name: "text", ReturnType: "String"})
	rt.Handle("text", func(_ context.Context, req *Request) error {
		value, err := req.ParamString("value")
		if err != nil {
			return err
		}
		req.Buf.B, err = codec.AppendString(nil, value)
		return err
	})
	ctx, conn, _ := newTextBoundarySocket(t, rt)
	for _, value := range []string{"\ufeff東京", "\ufffd", strings.Repeat("東京", 12000)} {
		quoted, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		frame := []byte(`{"$id":18446744073709551615,"$api":"text","value":` + string(quoted) + `}`)
		writeFragmentedText(t, ctx, conn, frame)
		requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$id":18446744073709551615,"data":`+string(quoted)+`}`))
	}
}

func writeFragmentedText(t *testing.T, ctx context.Context, conn *websocket.Conn, frame []byte) {
	t.Helper()
	writer, err := conn.Writer(ctx, websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	// Conn.Writer.Write sends a non-final frame. Split inside a UTF-8 sequence.
	boundary := bytes.IndexFunc(frame, func(r rune) bool { return r > 127 }) + 1
	for _, part := range [][]byte{frame[:boundary], frame[boundary:]} {
		if _, err := writer.Write(part); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
