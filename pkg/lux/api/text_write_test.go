package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"nhooyr.io/websocket"
)

func invalidWireTextErrors() []wireError {
	return []wireError{
		{Code: 422, Name: "bad\xff", Message: "valid"},
		{Code: 422, Name: "Valid", Message: "bad\xff"},
		{Code: 422, Name: "Valid", Message: "valid", TraceID: "bad\xff"},
		{Code: 422, Name: "Valid", Message: "valid", Data: []byte(`{"private":1}`), Cause: "bad\xff"},
	}
}

func TestErrorStringWritersRejectAndRollback(t *testing.T) {
	for _, value := range invalidWireTextErrors() {
		prefix := []byte("prefix")
		if encoded, err := appendBinaryError(bytes.Clone(prefix), value); err == nil || !bytes.Equal(encoded, prefix) {
			t.Fatalf("invalid binary error published: %x / %v", encoded, err)
		}
		buf := &ResponseBuf{B: bytes.Clone(prefix)}
		if err := appendJSONErrorFields(buf, value); err == nil {
			t.Fatal("invalid JSON error text accepted")
		}
		for _, binaryMode := range []bool{false, true} {
			buf.B = bytes.Clone(prefix)
			got := appendWireErrorFields(buf, value, binaryMode)
			if got.Code != 500 || got.Name != "Internal" || got.Data != nil || got.Cause != "" || got.TraceID != "" {
				t.Fatalf("unsafe error fallback: %+v", got)
			}
			if !bytes.Equal(buf.B[:len(prefix)], prefix) {
				t.Fatal("frame prefix changed")
			}
			body := buf.B[len(prefix):]
			if !binaryMode {
				body = append(append([]byte{'{'}, body...), '}')
			}
			checkSafeTextError(t, body, binaryMode)
		}
	}
}

func checkSafeTextError(t *testing.T, body []byte, binaryMode bool) {
	t.Helper()
	if binaryMode {
		value, err := DecodeBinaryError(body, 500)
		if err != nil || value.Code != 500 || value.Name != "Internal" || value.Message != "error.internal" || value.Data != nil || value.Cause != "" {
			t.Fatalf("invalid binary fallback: %+v / %v", value, err)
		}
		return
	}
	var value struct {
		Code           int
		Error, Message string
		Data           json.RawMessage
		Cause          string
	}
	if err := json.Unmarshal(body, &value); err != nil || value.Code != 500 || value.Error != "Internal" || value.Message != "error.internal" || value.Data != nil || value.Cause != "" {
		t.Fatalf("invalid JSON fallback: %s / %v", body, err)
	}
}

func TestHTTPStringEncodingFailureIsNotSuccess(t *testing.T) {
	for _, fromError := range []bool{false, true} {
		for _, binaryMode := range []bool{false, true} {
			router := NewRouter()
			router.Registry.Register("fail", 1)
			router.Handle("fail", func(_ context.Context, req *Request) error {
				req.Buf.AppendString("must-not-escape")
				if fromError {
					return luxerrors.New("bad\xff", 422, "invalid")
				}
				var err error
				req.Buf.B, err = codec.AppendString(req.Buf.B, "invalid\xff")
				return err
			})
			body := []byte(`{"$api":"fail"}`)
			if binaryMode {
				body = []byte{1, 0, 0}
			}
			r := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if binaryMode {
				r.Header.Set("X-Luxo-Mode", "binary")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != 500 {
				t.Fatalf("failed writer reported HTTP %d: %s", w.Code, w.Body)
			}
			checkSafeTextError(t, w.Body.Bytes(), binaryMode)
		}
	}
}

func TestSubscriptionControlsUseNumericIdentity(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		router := NewRouter()
		ws := &wsConn{conn: conn, ctx: r.Context()}
		if err := router.writeWSJSONSubscriptionSuccess(r.Context(), ws, ^uint64(0)); err != nil {
			t.Error(err)
			return
		}
		if err := router.writeWSJSONSubscriptionError(r.Context(), ws, ^uint64(0), luxerrors.New("bad\xff", 422, "invalid")); err != nil {
			t.Error(err)
			return
		}
		sub := &StreamSub{Ch: make(chan []byte, 1)}
		sub.Ch <- []byte(`"ok"`)
		close(sub.Ch)
		WritePumpJSON(r.Context(), ws, ^uint64(0), sub)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$sub":18446744073709551615,"ok":true}`))
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkSafeTextError(t, payload, false)
	requireWSFrame(t, ctx, conn, websocket.MessageText, []byte(`{"$stream":18446744073709551615,"data":"ok"}`))
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("write pump leaked after completion")
	}
}

func TestStringFilterAndSorterErrors(t *testing.T) {
	for _, filter := range []Filter{{Field: "bad\xff", Operator: "eq", Value: "ok"}, {Field: "valid", Operator: "eq", Value: "bad\xff"}} {
		var enc codec.Encoder
		if err := writeBinaryFilters(&enc, []Filter{filter}); err == nil {
			t.Fatal("invalid filter text accepted")
		}
	}
	var enc codec.Encoder
	if err := writeBinarySorters(&enc, []Sorter{{Field: "bad\xff", Order: "asc"}}); err == nil {
		t.Fatal("invalid sorter text accepted")
	}
}

func TestBinaryRequestRejectsInvalidStringValues(t *testing.T) {
	for _, kind := range []string{"String", "Enum"} {
		for _, list := range []bool{false, true} {
			var value any = "bad\xff"
			if list {
				value = []string{"valid", "bad\xff"}
			}
			meta := []ParamMeta{{Name: "value", FieldID: 1, Type: kind, IsList: list}}
			encoded, err := EncodeBinaryRequest(1, nil, map[string]any{"value": value}, meta)
			if err == nil || encoded != nil {
				t.Fatalf("invalid %s list=%v: %x / %v", kind, list, encoded, err)
			}
		}
	}
}
