package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

type precisionErrorData struct {
	Value int64
	Label string
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
