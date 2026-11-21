package rpc

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestRPCInvalidTraceIDDoesNotContaminateNextFrame(t *testing.T) {
	for name, field := range map[string]int{"ID": 8, "ParentID": 9} {
		t.Run(name, func(t *testing.T) {
			valid, err := api.EncodeDebugTrace(api.DebugTraceSnapshot{TraceID: "next"})
			if err != nil {
				t.Fatal(err)
			}
			frames := [][]byte{traceResponseForTest(invalidTraceIDForTest(t, field)), traceResponseForTest(valid)}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() { done <- serveTraceFramesForTest(listener, frames) }()
			client := NewClient(listener.Addr().String())
			defer client.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			body, snapshot, err := client.callTracedPayload(ctx, []byte{1, 0, 0, 1, 0, 0})
			if err == nil || body != nil || snapshot.TraceID != "" || snapshot.Spans != nil {
				t.Fatalf("invalid trace escaped: %x %+v %v", body, snapshot, err)
			}
			body, snapshot, err = client.callTracedPayload(ctx, []byte{1, 0, 0, 1, 0, 0})
			if err != nil || string(body) != "business" || snapshot.TraceID != "next" {
				t.Fatalf("next frame contaminated: %x %+v %v", body, snapshot, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func invalidTraceIDForTest(t *testing.T, field int) []byte {
	t.Helper()
	var span, trace codec.Encoder
	if err := span.WriteFieldString(1, "query"); err != nil {
		t.Fatal(err)
	}
	if err := span.WriteFieldString(2, "database"); err != nil {
		t.Fatal(err)
	}
	span.WriteFieldInt(field, 1<<32+1)
	span.WriteEnd()
	if err := trace.WriteFieldString(1, "invalid"); err != nil {
		t.Fatal(err)
	}
	trace.WriteFieldBytesArray(3, [][]byte{span.Bytes()})
	trace.WriteEnd()
	return append([]byte{3}, trace.Bytes()...)
}

func traceResponseForTest(trace []byte) []byte {
	frame := codec.AppendVarint([]byte{statusTraceOK}, uint64(len(trace)))
	frame = append(frame, trace...)
	return append(frame, "business"...)
}

func serveTraceFramesForTest(listener net.Listener, frames [][]byte) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := acceptHandshake(conn); err != nil {
		return err
	}
	for _, frame := range frames {
		if _, err := ReadFrame(conn, nil); err != nil {
			return err
		}
		if err := WriteFrame(conn, frame); err != nil {
			return err
		}
	}
	return nil
}

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
