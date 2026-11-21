package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

type precisionErrorData struct {
	Value int64
	Label string
}

type uncheckedErrorData string

type panickingErrorData struct{}

func (panickingErrorData) AppendJSON([]byte) ([]byte, error)        { panic("private native detail") }
func (panickingErrorData) AppendText([]byte, string) ([]byte, bool) { panic("private native detail") }

func TestNativeErrorDataPanicIsIsolated(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Fatalf("native error writer escaped: %v", failure)
		}
	}()
	value := NewRouter().buildWireError(t.Context(), "", errors.BadRequest.WithData(panickingErrorData{}))
	if value.Code != 500 || value.Name != "Internal" || value.Data != nil || value.Cause != "" {
		t.Fatalf("unsafe panic response: %+v", value)
	}
}

func (d uncheckedErrorData) AppendJSON(dst []byte) ([]byte, error) {
	return append(dst, d...), nil
}

func (d uncheckedErrorData) AppendText(dst []byte, _ string) ([]byte, bool) {
	return dst, false
}

func TestNativeErrorDataRequiresValidJSON(t *testing.T) {
	for _, raw := range []string{"", "{", `{"secret":`, `null true`, `"\uD800"`, "\"\xff\""} {
		t.Run(raw, func(t *testing.T) {
			value := NewRouter().buildWireError(t.Context(), "", errors.BadRequest.WithData(uncheckedErrorData(raw)))
			if value.Code != 500 || value.Name != "Internal" || value.Data != nil || value.Message != "error.internal" {
				t.Fatalf("unchecked native JSON escaped: %+v", value)
			}
		})
	}
}

func TestNativeErrorDataPreservesValidJSON(t *testing.T) {
	for _, raw := range []string{`null`, `123456789012345678901234567890`, `{"n":1e+9999,"text":"\uD83D\uDE00"}`, `[]`} {
		value := NewRouter().buildWireError(t.Context(), "", errors.BadRequest.WithData(uncheckedErrorData(raw)))
		if value.Code != 400 || string(value.Data) != raw {
			t.Fatalf("valid native JSON changed: %+v", value)
		}
	}
}

func TestErrorDataIndependentJSONDepth(t *testing.T) {
	for _, depth := range []int{127, 128, 129} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		value := NewRouter().buildWireError(t.Context(), "", errors.BadRequest.WithData(uncheckedErrorData(raw)))
		if depth <= 128 {
			if value.Code != 400 || string(value.Data) != raw {
				t.Fatalf("depth %d changed: %+v", depth, value)
			}
		} else if value.Code != 500 || value.Name != "Internal" || len(value.Data) != 0 {
			t.Fatalf("deep error data exposed: %+v", value)
		}
	}
}

func TestNativeErrorDataFailureOverTransports(t *testing.T) {
	for _, data := range []errors.ErrorData{uncheckedErrorData(`{"private":`), panickingErrorData{}, uncheckedErrorData(strings.Repeat("[", 128) + "[]" + strings.Repeat("]", 128))} {
		for _, binary := range []bool{false, true} {
			synctest.Test(t, func(t *testing.T) {
				router := NewRouter()
				if err := router.Registry.Register("fail", 1); err != nil {
					t.Fatal(err)
				}
				router.Handle("fail", func(context.Context, *Request) error {
					return errors.BadRequest.WithData(data)
				})
				body := []byte(`{"$api":"fail"}`)
				if binary {
					body = []byte{1, 0, 0}
				}
				request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				if binary {
					request.Header.Set("X-Luxo-Mode", "binary")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != 500 {
					t.Fatal(response.Code)
				}
				assertSafeEncodingError(t, response.Body.Bytes(), binary, false)
				ctx, conn := newInMemoryWebSocket(t, router)
				kind, frame := websocket.MessageText, []byte(`{"$id":7,"$api":"fail"}`)
				if binary {
					kind, frame = websocket.MessageBinary, []byte{BinaryFrameCallRequest, 7, 1, 0, 0}
				}
				writeWSFrame(t, ctx, conn, kind, frame)
				_, frame, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if binary {
					if len(frame) < 2 || frame[0] != BinaryFrameCallError || frame[1] != 7 {
						t.Fatal(frame)
					}
					frame = frame[2:]
				}
				assertSafeEncodingError(t, frame, binary, false)
			})
		}
	}
}

func (d precisionErrorData) AppendJSON(dst []byte) ([]byte, error) {
	w := errors.NewDataWriter(dst)
	w.Token(`{"value":`)
	w.Int(d.Value)
	w.Token(`,"label":`)
	w.String(d.Label)
	w.Token("}")
	return w.Result()
}
func (d precisionErrorData) AppendText(dst []byte, field string) ([]byte, bool) {
	w := errors.NewDataWriter(dst)
	switch field {
	case "value":
		w.Int(d.Value)
	case "label":
		w.Token(d.Label)
	default:
		return dst, false
	}
	result, err := w.Result()
	return result, err == nil
}

func TestInvalidErrorDataIsNotPublished(t *testing.T) {
	router := NewRouter()
	err := errors.BadRequest.WithData(precisionErrorData{Label: string([]byte{0xff})})
	value := router.buildWireError(context.Background(), "", err)
	if value.Code != 500 || value.Name != "Internal" || value.Data != nil || value.Cause != "" {
		t.Fatalf("invalid data exposed or misclassified: %+v", value)
	}
}

func invalidErrorDataRouter(dev bool) *Router {
	router := NewRouter()
	router.SetDevMode(dev)
	router.Registry.Register("fail", 1)
	router.Handle("fail", func(context.Context, *Request) error {
		return errors.BadRequest.WithData(precisionErrorData{Label: string([]byte{0xff})})
	})
	return router
}

func assertSafeEncodingError(t *testing.T, body []byte, binary, dev bool) {
	t.Helper()
	var result wireError
	if binary {
		result = decodeWireError(t, body)
	} else {
		var value struct {
			Name  string `json:"error"`
			Code  int
			Data  json.RawMessage
			Cause string
		}
		if err := json.Unmarshal(body, &value); err != nil {
			t.Fatal(err)
		}
		result = wireError{Name: value.Name, Code: value.Code, Data: value.Data, Cause: value.Cause}
	}
	if result.Name != "Internal" || result.Code != 500 || len(result.Data) != 0 || (result.Cause != "") != dev {
		t.Fatalf("unsafe encoding failure: %+v", result)
	}
}

func TestErrorDataFailureOverHTTP(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, dev := range []bool{false, true} {
			router := invalidErrorDataRouter(dev)
			body := []byte(`{"$api":"fail"}`)
			if binary {
				body = []byte{1, 0, 0}
			}
			request := httptest.NewRequest(http.MethodPost, "/luxo", bytes.NewReader(body))
			if binary {
				request.Header.Set("X-Luxo-Mode", "binary")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 500 {
				t.Fatalf("HTTP status %d", response.Code)
			}
			assertSafeEncodingError(t, response.Body.Bytes(), binary, dev)
		}
	}
}

func TestErrorDataFailureOverWebSocket(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, dev := range []bool{false, true} {
			synctest.Test(t, func(t *testing.T) {
				ctx, conn := newInMemoryWebSocket(t, invalidErrorDataRouter(dev))
				kind, body := websocket.MessageText, []byte(`{"$id":7,"$api":"fail"}`)
				if binary {
					kind, body = websocket.MessageBinary, []byte{BinaryFrameCallRequest, 7, 1, 0, 0}
				}
				writeWSFrame(t, ctx, conn, kind, body)
				_, response, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if binary {
					if len(response) < 2 || response[0] != BinaryFrameCallError {
						t.Fatalf("invalid error frame: %x", response)
					}
					id, n := codec.ReadVarint(response, 1)
					if id != 7 || n == 0 {
						t.Fatal("lost request ID")
					}
					response = response[n+1:]
				}
				assertSafeEncodingError(t, response, binary, dev)
			})
		}
	}
}

type forbiddenErrorData struct{ t *testing.T }

func (d forbiddenErrorData) AppendJSON(dst []byte) ([]byte, error) {
	d.t.Fatal("internal error data must not be encoded")
	return dst, nil
}
func (d forbiddenErrorData) AppendText(dst []byte, field string) ([]byte, bool) {
	d.t.Fatal("internal error data must not be interpolated")
	return dst, false
}

func TestInternalErrorDataIsNeverAccessed(t *testing.T) {
	value := NewRouter().buildWireError(t.Context(), "en", errors.InternalError.WithData(forbiddenErrorData{t}))
	if value.Code != 500 || value.Data != nil {
		t.Fatalf("internal data exposed: %+v", value)
	}
}

func TestInternalErrorMessageIsPrivate(t *testing.T) {
	private := errors.Wrap(nil)
	private.Message = "private database diagnostic"
	for _, dev := range []bool{false, true} {
		router := NewRouter()
		router.SetDevMode(dev)
		value := router.buildWireError(t.Context(), "en", private)
		if value.Message != "error.internal" {
			t.Fatalf("private message exposed: %q", value.Message)
		}
	}
}

// A panic payload must never be formatted while recovering an error writer.
type hostileErrorPanic struct{ formatted *bool }

func (p hostileErrorPanic) Error() string {
	*p.formatted = true
	panic("panic value must not be formatted")
}

type textPanicErrorData struct{ payload hostileErrorPanic }

func (d textPanicErrorData) AppendJSON(dst []byte) ([]byte, error) {
	return append(dst, `{"secret":"private"}`...), nil
}

func (d textPanicErrorData) AppendText([]byte, string) ([]byte, bool) {
	panic(d.payload)
}

func TestErrorTranslationPanicDiscardsPartialData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "en.yaml"), []byte("error:\n  bad_request: 'invalid ${secret}'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	translator := i18n.New("en")
	if err := translator.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	router.SetTranslator(translator)
	formatted := false
	source := errors.BadRequest.WithData(textPanicErrorData{hostileErrorPanic{&formatted}})
	for _, dev := range []bool{false, true} {
		router.SetDevMode(dev)
		got := router.buildWireError(t.Context(), "en", source)
		if formatted || got.Code != 500 || got.Name != "Internal" || got.Message != "error.internal" || got.Data != nil || got.Cause != "" {
			t.Fatalf("translation panic leaked partial error: %+v, formatted=%v", got, formatted)
		}
	}
	// Recovery must not leave the translator locked or poison later responses.
	got := router.buildWireError(t.Context(), "en", errors.BadRequest.WithData(uncheckedErrorData(`{"ok":true}`)))
	if got.Code != 400 || string(got.Data) != `{"ok":true}` {
		t.Fatalf("subsequent error failed: %+v", got)
	}
}

func TestDebugCausePanicIsIsolated(t *testing.T) {
	router := NewRouter()
	formatted := false
	source := errors.BadRequest.WithCause(hostileErrorPanic{&formatted})
	got := router.buildWireError(t.Context(), "", source)
	if formatted || got.Code != 400 || got.Cause != "" {
		t.Fatal("production mode evaluated private cause")
	}
	router.SetDevMode(true)
	got = router.buildWireError(t.Context(), "", source)
	if !formatted || got.Code != 500 || got.Message != "error.internal" || got.Data != nil || got.Cause != "" {
		t.Fatalf("debug cause panic escaped isolation: %+v", got)
	}
}

func TestHandlerPanicDoesNotFormatPayload(t *testing.T) {
	formatted := false
	called := false
	router := NewRouter()
	err := router.callHandler(func(context.Context, *Request) error {
		called = true
		panic(hostileErrorPanic{&formatted})
	}, t.Context(), &Request{Buf: new(ResponseBuf)})
	if !called || formatted || err == nil {
		t.Fatal("handler recovery evaluated arbitrary panic payload")
	}
	got := router.buildWireError(t.Context(), "", err)
	if got.Code != 500 || got.Message != "error.internal" {
		t.Fatalf("unsafe handler failure: %+v", got)
	}
}

var responseBufferCases = []struct {
	mode       string
	statusCode int
	value      int64
}{
	{"unchanged", 200, 42}, {"replaceBytes", 200, 7}, {"zero", 200, 0},
	{"nil", 500, 0}, {"replace", 500, 0},
	{"empty", 500, 0},
	{"businessError", 404, 0}, {"panic", 500, 0},
}

func responseBufferRouter(t *testing.T, mode string) *Router {
	t.Helper()
	router := NewRouter()
	for i, name := range []string{"buffer", "healthy"} {
		if err := router.Schema.RegisterAPI(&schema.API{ID: i + 1, Name: name, ReturnType: "Int"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("buffer", func(_ context.Context, req *Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		switch mode {
		case "nil":
			req.Buf = nil
		case "replace":
			req.Buf = &ResponseBuf{B: codec.AppendSvarint(nil, 7)}
		case "replaceBytes":
			req.Buf.B = codec.AppendSvarint(nil, 7)
		case "empty":
			req.Buf.B = req.Buf.B[:0]
		case "zero":
			req.Buf.B = codec.AppendSvarint(req.Buf.B[:0], 0)
		case "businessError":
			req.Buf = nil
			return errors.NotFound
		case "panic":
			req.Buf = nil
			panic("private handler buffer detail")
		}
		return nil
	})
	router.Handle("healthy", func(_ context.Context, req *Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 7)
		return nil
	})
	return router
}

func TestHTTPHandlerResponseBufferOwnership(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, sample := range responseBufferCases {
			t.Run(sample.mode+map[bool]string{false: "/JSON", true: "/Binary"}[binary], func(t *testing.T) {
				router := responseBufferRouter(t, sample.mode)
				metrics := &mockMetrics{}
				router.SetMetricsCollector(metrics)
				for i, name := range []string{"buffer", "healthy"} {
					body := []byte(`{"$api":"` + name + `"}`)
					if binary {
						body = []byte{byte(i + 1), 0, 0}
					}
					request := httptest.NewRequest("POST", "/", bytes.NewReader(body))
					if binary {
						request.Header.Set("X-Luxo-Mode", "binary")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					status, value := sample.statusCode, sample.value
					if i == 1 {
						status, value = 200, 7
					}
					if response.Code != status {
						t.Fatalf("status=%d want=%d body=%q", response.Code, status, response.Body.Bytes())
					}
					assertBufferContractReply(t, response.Body.Bytes(), binary, status, value)
				}
				if len(metrics.calls) != 2 || metrics.calls[0].isError != (sample.statusCode != 200) || metrics.calls[1].isError {
					t.Fatalf("incorrect response metrics: %+v", metrics.calls)
				}
			})
		}
	}
}

func TestWSHandlerResponseBufferOwnership(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, sample := range responseBufferCases {
			t.Run(sample.mode+map[bool]string{false: "/JSON", true: "/Binary"}[binary], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					router := responseBufferRouter(t, sample.mode)
					ctx, conn := newInMemoryWebSocket(t, router)
					for i, name := range []string{"buffer", "healthy"} {
						kind, frame := websocket.MessageText, []byte(`{"$id":1,"$api":"`+name+`"}`)
						if binary {
							kind, frame = websocket.MessageBinary, []byte{BinaryFrameCallRequest, 1, byte(i + 1), 0, 0}
						}
						writeWSFrame(t, ctx, conn, kind, frame)
						gotKind, frame, err := conn.Read(ctx)
						if err != nil || gotKind != kind {
							t.Fatal(gotKind, err)
						}
						status, value := sample.statusCode, sample.value
						if i == 1 {
							status, value = 200, 7
						}
						if binary {
							frameType := byte(BinaryFrameCallSuccess)
							if status != 200 {
								frameType = BinaryFrameCallError
							}
							if len(frame) < 2 || frame[0] != frameType || frame[1] != 1 {
								t.Fatalf("unexpected reply: %x", frame)
							}
							frame = frame[2:]
						}
						assertBufferContractReply(t, frame, binary, status, value)
					}
				})
			})
		}
	}
}

func assertBufferContractReply(t *testing.T, body []byte, binary bool, status int, value int64) {
	t.Helper()
	if status == 500 {
		assertSafeEncodingError(t, body, binary, false)
		return
	}
	if binary {
		if status == 200 {
			if !bytes.Equal(body, codec.AppendSvarint(nil, value)) {
				t.Fatalf("wrong business value: %x", body)
			}
			return
		}
		got, err := DecodeBinaryError(body, 0)
		if err != nil || got.Code != status {
			t.Fatal(got, err)
		}
		return
	}
	var envelope struct {
		Data int64
		Code int
	}
	if err := json.Unmarshal(body, &envelope); err != nil || status == 200 && envelope.Data != value || status != 200 && envelope.Code != status {
		t.Fatalf("unexpected response %s: %v", body, err)
	}
}

func TestConcurrentHandlerBufferFailureDoesNotLeakPrivateCause(t *testing.T) {
	router := NewRouter()
	handler := func(_ context.Context, req *Request) error {
		req.Buf = nil
		return nil
	}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			buf := new(ResponseBuf)
			req := &Request{}
			for range 32 {
				req.Buf = buf
				err := router.callHandler(handler, t.Context(), req)
				if err == nil {
					t.Error("detached transport buffer returned success")
					return
				}
				wire := router.buildWireError(t.Context(), "", err)
				if wire.Name != "Internal" || wire.Code != 500 || wire.Message != "error.internal" || wire.Cause != "" || len(wire.Data) != 0 {
					t.Errorf("unsafe concurrent handler failure: %+v", wire)
				}
			}
		})
	}
	workers.Wait()
}

func TestSchemaLessJSONHandlerKeepsNullResponse(t *testing.T) {
	for _, noSchema := range []bool{false, true} {
		router := NewRouter()
		if noSchema {
			router.Schema = nil
		}
		router.Handle("noop", func(context.Context, *Request) error { return nil })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/", strings.NewReader(`{"$api":"noop"}`)))
		if response.Code != 200 || response.Body.String() != `{"data":null}` {
			t.Fatalf("schema=%v: %d %s", !noSchema, response.Code, response.Body.String())
		}
	}
}

func TestTypedEmptyResponseRealHTTPAndWebSocket(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "Binary"}[binary], func(t *testing.T) {
			server := httptest.NewServer(responseBufferRouter(t, "empty"))
			defer server.Close()
			server.Client().Timeout = 5 * time.Second
			for i, name := range []string{"buffer", "healthy"} {
				body := []byte(`{"$api":"` + name + `"}`)
				if binary {
					body = []byte{byte(i + 1), 0, 0}
				}
				request := httptest.NewRequest("POST", server.URL, bytes.NewReader(body))
				request.RequestURI = ""
				if binary {
					request.Header.Set("X-Luxo-Mode", "binary")
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := io.ReadAll(response.Body)
				response.Body.Close()
				status, value := 500, int64(0)
				if i == 1 {
					status, value = 200, 7
				}
				if err != nil || response.StatusCode != status {
					t.Fatal(response.StatusCode, err)
				}
				assertBufferContractReply(t, payload, binary, status, value)
			}
			checkRealEmptyWSResponse(t, server.URL, binary)
		})
	}
}

func checkRealEmptyWSResponse(t *testing.T, endpoint string, binary bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(endpoint, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for i, name := range []string{"buffer", "healthy"} {
		kind, frame := websocket.MessageText, []byte(`{"$id":1,"$api":"`+name+`"}`)
		if binary {
			kind, frame = websocket.MessageBinary, []byte{BinaryFrameCallRequest, 1, byte(i + 1), 0, 0}
		}
		writeWSFrame(t, ctx, conn, kind, frame)
		gotKind, frame, err := conn.Read(ctx)
		if err != nil || gotKind != kind {
			t.Fatal(gotKind, err)
		}
		status, value := 500, int64(0)
		if i == 1 {
			status, value = 200, 7
		}
		if binary {
			wantType := byte(BinaryFrameCallError)
			if i == 1 {
				wantType = BinaryFrameCallSuccess
			}
			if len(frame) < 2 || frame[0] != wantType || frame[1] != 1 {
				t.Fatalf("bad frame header: %x", frame)
			}
			frame = frame[2:]
		}
		assertBufferContractReply(t, frame, binary, status, value)
	}
}

func TestUnaryIdentityPanicAcrossHTTPAndWebSocket(t *testing.T) {
	for _, binary := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			router := NewRouter()
			if err := router.Registry.Register("checkIdentity", 1); err != nil {
				t.Fatal(err)
			}
			var extracted, handled int
			router.IdentityExtractor = func(context.Context) any {
				extracted++
				if extracted%2 == 1 {
					panic("private identity detail")
				}
				return int64(42)
			}
			router.Handle("checkIdentity", func(_ context.Context, req *Request) error {
				handled++
				if req.Buf.Identity != int64(42) {
					t.Error("identity not bound")
				}
				return errors.New("IdentityAccepted", 422, "identity.accepted")
			})
			for _, code := range []int{500, 422} {
				body := []byte(`{"$api":"checkIdentity"}`)
				if binary {
					body = []byte{1, 0, 0}
				}
				request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				if binary {
					request.Header.Set("X-Luxo-Mode", "binary")
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != code {
					t.Fatalf("HTTP status: %d", response.Code)
				}
				assertIdentityFailureEnvelope(t, response.Body.Bytes(), binary, code)
			}
			ctx, conn := newInMemoryWebSocket(t, router)
			for index, code := range []int{500, 422} {
				kind, frame := websocket.MessageText, []byte(`{"$id":7,"$api":"checkIdentity"}`)
				if binary {
					kind, frame = websocket.MessageBinary, []byte{BinaryFrameCallRequest, byte(7 + index), 1, 0, 0}
				}
				writeWSFrame(t, ctx, conn, kind, frame)
				_, frame, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if binary {
					if len(frame) < 2 || frame[0] != BinaryFrameCallError || frame[1] != byte(7+index) {
						t.Fatal("wrong error identity")
					}
					frame = frame[2:]
				}
				assertIdentityFailureEnvelope(t, frame, binary, code)
			}
			conn.CloseNow()
			synctest.Wait()
			if extracted != 4 || handled != 2 {
				t.Fatalf("callbacks: extracted=%d handled=%d", extracted, handled)
			}
		})
	}
}

func assertIdentityFailureEnvelope(t *testing.T, body []byte, binary bool, code int) {
	t.Helper()
	if code == 500 {
		assertSafeEncodingError(t, body, binary, false)
		return
	}
	if binary {
		value, err := DecodeBinaryError(body, 0)
		if err != nil || value.Code != code || value.Message != "identity.accepted" {
			t.Fatalf("unexpected Binary result: %+v %v", value, err)
		}
		return
	}
	var value struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &value); err != nil || value.Code != code || value.Message != "identity.accepted" {
		t.Fatalf("unexpected JSON result: %s %v", body, err)
	}
}

func TestWebSocketInvalidErrorStatusKeepsConnectionUsable(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, invalidCode := range []int{0, 200, 399, 600, 1000} {
			synctest.Test(t, func(t *testing.T) {
				router := NewRouter()
				if err := router.Registry.Register("reject", 1); err != nil {
					t.Fatal(err)
				}
				calls := 0
				router.Handle("reject", func(context.Context, *Request) error {
					calls++
					if calls == 1 {
						return errors.New("Private", invalidCode, "private diagnostic").WithData(uncheckedErrorData(`{"secret":true}`))
					}
					return errors.New("Accepted", 422, "identity.accepted")
				})
				ctx, conn := newInMemoryWebSocket(t, router)
				for index, code := range []int{500, 422} {
					kind, frame := websocket.MessageText, []byte(`{"$id":1,"$api":"reject"}`)
					if binary {
						kind, frame = websocket.MessageBinary, []byte{BinaryFrameCallRequest, byte(index + 1), 1, 0, 0}
					}
					writeWSFrame(t, ctx, conn, kind, frame)
					_, response, err := conn.Read(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if binary {
						if len(response) < 2 || response[0] != BinaryFrameCallError || response[1] != byte(index+1) {
							t.Fatalf("invalid error frame: %x", response)
						}
						response = response[2:]
					}
					assertIdentityFailureEnvelope(t, response, binary, code)
				}
				conn.CloseNow()
				synctest.Wait()
			})
		}
	}
}
