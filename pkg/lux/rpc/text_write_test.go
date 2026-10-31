package rpc

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestRPCTraceEncodingFailurePreservesBusinessResult(t *testing.T) {
	_, trace := api.WithDebugTrace(context.Background(), "diagnostic")
	trace.Finish(time.Now(), api.DebugSpanMeta{Name: "query", Category: "database", Statement: "private\xff"})
	for _, failedBusiness := range []bool{false, true} {
		body, status := []byte("business"), byte(statusTraceOK)
		if failedBusiness {
			body, status = encodeError(409, "Conflict", "conflict")[1:], statusTraceError
		}
		var output bytes.Buffer
		if err := writeTracedFrame(&output, status, body, trace); err != nil {
			t.Fatal(err)
		}
		frame, err := ReadFrame(&output, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, snapshot, err := decodeTracedResponse(frame)
		if !snapshot.EncodingFailed || len(snapshot.Spans) != 0 {
			t.Fatalf("missing failure marker: %+v", snapshot)
		}
		if !failedBusiness {
			if err != nil || string(got) != "business" {
				t.Fatalf("success changed: %s / %v", got, err)
			}
			continue
		}
		var business *luxerrors.AppError
		if !errors.As(err, &business) || business.Code != 409 || business.Name != "Conflict" {
			t.Fatalf("business error replaced: %v", err)
		}
	}
}

func TestRPCStringWritersPropagateErrors(t *testing.T) {
	if encoded, err := EncodeParams(ParamField{FieldID: 1, Value: "bad\xff"}); encoded != nil || !errors.Is(err, codec.ErrInvalidUTF8) {
		t.Fatalf("invalid RPC parameter: %x / %v", encoded, err)
	}
	for _, value := range []*luxerrors.AppError{
		luxerrors.New("bad\xff", 422, "valid"), luxerrors.New("Valid", 422, "bad\xff"),
	} {
		if encoded, err := encodeApplicationError(value); encoded != nil || !errors.Is(err, codec.ErrInvalidUTF8) {
			t.Fatalf("invalid error: %x / %v", encoded, err)
		}
		encoded := encodeError(value.Code, value.Name, value.Message)
		checkRPCTextFallback(t, encoded)
		var output bytes.Buffer
		if err := writeApplicationError(&output, value, nil); err != nil {
			t.Fatal(err)
		}
		frame, err := ReadFrame(&output, nil)
		if err != nil {
			t.Fatal(err)
		}
		checkRPCTextFallback(t, frame)
	}
}

func checkRPCTextFallback(t *testing.T, encoded []byte) {
	t.Helper()
	if len(encoded) == 0 || encoded[0] != statusError {
		t.Fatalf("missing error frame: %x", encoded)
	}
	value, ok := errors.AsType[*luxerrors.AppError](decodeError(encoded[1:]))
	if !ok || value.Code != 500 || value.Name != "Internal" || value.Message != "error.internal" || value.Data != nil {
		t.Fatalf("invalid fallback: %+v", value)
	}
}
