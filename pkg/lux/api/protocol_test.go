package api

import (
	"bytes"
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestAppendBinaryErrorGolden(t *testing.T) {
	got := appendBinaryError(nil, wireError{
		Code:    400,
		Name:    "BadRequest",
		Message: "bad",
		TraceID: "t",
		Data:    []byte(`{}`),
		Cause:   "c",
	})
	want := []byte{
		1, 0xa0, 0x06,
		2, 10, 'B', 'a', 'd', 'R', 'e', 'q', 'u', 'e', 's', 't',
		3, 3, 'b', 'a', 'd',
		4, 1, 't',
		5, 2, '{', '}',
		6, 1, 'c',
		0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary error = %v, want %v", got, want)
	}
}

func TestDecodeBinaryErrorGolden(t *testing.T) {
	data := appendBinaryError(nil, wireError{
		Code: 422, Name: "InvalidInput", Message: "invalid", TraceID: "trace",
		Data: []byte(`{"param":"email"}`), Cause: "validation failed",
	})
	got, err := DecodeBinaryError(data, 500)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != 422 || got.Name != "InvalidInput" || got.Message != "invalid" ||
		got.TraceID != "trace" || string(got.Data) != `{"param":"email"}` || got.Cause != "validation failed" {
		t.Fatalf("decoded error = %+v", got)
	}
}

func TestDecodeBinaryErrorRejectsInvalidEnvelope(t *testing.T) {
	for _, data := range [][]byte{
		{1, 0x80},
		{7, 0},
		{1, 2},
		{5, 1, '{', 0},
		{0, 0},
	} {
		if _, err := DecodeBinaryError(data, 400); err == nil {
			t.Fatalf("accepted invalid envelope %v", data)
		}
	}
}

func TestRouterBinaryParseErrorUsesBinaryEnvelope(t *testing.T) {
	rt := NewRouter()
	handler := TraceMiddleware(rt)
	r := httptest.NewRequest(http.MethodPost, "/luvia", nil)
	r.Header.Set("X-Luxo-Mode", "binary")
	r.Header.Set("X-Request-Id", "trace-parse")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-luxo" {
		t.Fatalf("content type = %q", got)
	}
	got := decodeWireError(t, w.Body.Bytes())
	if got.Name != "BadRequest" || got.Code != 400 || got.Message != "empty binary request" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if got.TraceID != "trace-parse" {
		t.Fatalf("trace ID = %q", got.TraceID)
	}
}

func TestRouterBinaryAppErrorIncludesStructuredFields(t *testing.T) {
	rt := NewRouter()
	rt.SetDevMode(true)
	rt.Registry.Register("fail", 1)
	rt.Handle("fail", func(context.Context, *Request) error {
		return luxerrors.New("InvalidInput", 422, "invalid input").
			WithData(luxerrors.ParamError{Param: "email", Error: "invalid"}).
			WithCause(stderrors.New("validation failed"))
	})
	handler := TraceMiddleware(rt)
	r := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader([]byte{1, 0, 0}))
	r.Header.Set("X-Luxo-Mode", "binary")
	r.Header.Set("X-Request-Id", "trace-app")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	got := decodeWireError(t, w.Body.Bytes())
	if got.Name != "InvalidInput" || got.Code != 422 || got.Message != "invalid input" {
		t.Fatalf("unexpected error envelope: %+v", got)
	}
	if got.TraceID != "trace-app" || string(got.Data) != `{"param":"email","error":"invalid"}` {
		t.Fatalf("missing structured fields: %+v", got)
	}
	if got.Cause != "validation failed" {
		t.Fatalf("cause = %q", got.Cause)
	}
}

func decodeWireError(t *testing.T, data []byte) wireError {
	t.Helper()
	dec := codec.NewDecoder(data)
	var got wireError
	for dec.NextField() {
		switch dec.FieldID() {
		case binaryErrorCodeField:
			got.Code = int(dec.ReadInt())
		case binaryErrorNameField:
			got.Name = dec.ReadString()
		case binaryErrorMessageField:
			got.Message = dec.ReadString()
		case binaryErrorTraceIDField:
			got.TraceID = dec.ReadString()
		case binaryErrorDataField:
			got.Data = append([]byte(nil), dec.ReadBytes()...)
		case binaryErrorCauseField:
			got.Cause = dec.ReadString()
		default:
			t.Fatalf("unexpected error field %d", dec.FieldID())
		}
	}
	if err := dec.Err(); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return got
}
