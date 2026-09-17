package rpc

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func rpcErrorFields(code int64, name, message string) [3][]byte {
	return [3][]byte{
		codec.AppendSvarint([]byte{1}, code),
		codec.AppendBytes([]byte{2}, []byte(name)),
		codec.AppendBytes([]byte{3}, []byte(message)),
	}
}

func rpcErrorBody(fields ...[]byte) []byte {
	return append(bytes.Join(fields, nil), 0)
}

func requireRPCEnvelopeFailure(t *testing.T, data []byte) {
	t.Helper()
	err := decodeError(data)
	requireRPCProtocolError(t, err)
}

func requireRPCProtocolError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("malformed envelope returned no error")
	}
	if value, ok := stderrors.AsType[*luxerrors.AppError](err); ok {
		t.Fatalf("malformed envelope reached the application: %+v", value)
	}
}

func TestRPCErrorRejectsDuplicateFields(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	for i, duplicate := range rpcErrorFields(403, "Different", "different message") {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			requireRPCEnvelopeFailure(t, rpcErrorBody(fields[0], fields[1], fields[2], duplicate))
		})
	}
}

func TestRPCErrorRejectsInvalidText(t *testing.T) {
	for _, invalid := range []string{"\xff", "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xe4\xb8"} {
		for _, field := range []int{2, 3} {
			t.Run(fmt.Sprintf("%d/%x", field, invalid), func(t *testing.T) {
				fields := rpcErrorFields(422, "Rejected", "error.rejected")
				fields[field-1] = codec.AppendBytes([]byte{byte(field)}, []byte(invalid))
				requireRPCEnvelopeFailure(t, rpcErrorBody(fields[:]...))
			})
		}
	}
}

func TestRPCErrorCanonicalOrdersAndOwnedText(t *testing.T) {
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			fields := rpcErrorFields(422, "\ufeffRejected\ufffd", "精度 café\n")
			body := rpcErrorBody(fields[order[0]], fields[order[1]], fields[order[2]])
			value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(body))
			clear(body)
			if !ok || value.Code != 422 || value.Name != "\ufeffRejected\ufffd" || value.Message != "精度 café\n" || value.Internal {
				t.Fatalf("canonical error changed: %+v", value)
			}
		})
	}
}

func TestRPCErrorTextBoundaries(t *testing.T) {
	for _, text := range []string{"", "\x00", "é", strings.Repeat("精度", 4096)} {
		for _, code := range []int64{422, 503} {
			t.Run(fmt.Sprintf("%d/%d", code, len(text)), func(t *testing.T) {
				fields := rpcErrorFields(code, text, text)
				body := rpcErrorBody(fields[2], fields[1], fields[0])
				value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(body))
				clear(body)
				if !ok || value.Name != text || value.Code != int(code) {
					t.Fatalf("text ownership changed: %+v", value)
				}
				if code < 500 && value.Message != text {
					t.Fatalf("message changed: %q", value.Message)
				}
				if code >= 500 && (!value.Internal || value.Message != "error.internal" ||
					value.Cause == nil || value.Cause.Error() != fmt.Sprintf("rpc error %d %s: %s", code, text, text)) {
					t.Fatalf("internal text changed: %+v", value)
				}
			})
		}
	}
}

func TestRPCErrorCanonicalCompletion(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	body := rpcErrorBody(fields[:]...)
	for length := range len(body) {
		t.Run("truncated/"+strconv.Itoa(length), func(t *testing.T) {
			requireRPCEnvelopeFailure(t, body[:length])
		})
	}
	for _, suffix := range [][]byte{{0}, {1}, {0xff}} {
		requireRPCEnvelopeFailure(t, append(bytes.Clone(body), suffix...))
	}
	for missing := range fields {
		var partial [][]byte
		for i, field := range fields {
			if i != missing {
				partial = append(partial, field)
			}
		}
		requireRPCEnvelopeFailure(t, rpcErrorBody(partial...))
	}
	for _, field := range []byte{7, 8, 127} {
		requireRPCEnvelopeFailure(t, rpcErrorBody(fields[0], fields[1], fields[2], []byte{field, 0}))
	}
}

func TestRPCErrorWideFieldIdentifiers(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	for _, field := range []uint64{1 << 32, 1<<32 + 1, 1<<32 + 2, 1<<32 + 3, math.MaxUint64} {
		t.Run(strconv.FormatUint(field, 10), func(t *testing.T) {
			wide := codec.AppendVarint(nil, field)
			requireRPCEnvelopeFailure(t, rpcErrorBody(fields[0], fields[1], fields[2], wide))
		})
	}
}

func TestRPCErrorPreservesAcceptedVarintForms(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	var body []byte
	for _, field := range fields {
		body = append(body, field[0]|0x80, 0)
		body = append(body, field[1:]...)
	}
	body = append(body, 0x80, 0)
	value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(body))
	if !ok || value.Code != 422 || value.Name != "Rejected" || value.Message != "error.rejected" {
		t.Fatalf("accepted varint representation changed: %+v", value)
	}
}

func TestRPCInvalidTextAvoidsStringCopies(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", strings.Repeat("a", 32<<10)+"\xff")
	body := rpcErrorBody(fields[:]...)
	var got error
	allocations := testing.AllocsPerRun(100, func() {
		got = decodeError(body)
	})
	requireRPCProtocolError(t, got)
	// Only the protocol error is allocated, never the rejected text.
	if allocations > 1 {
		t.Fatalf("invalid text decoding allocated %g times, want at most 1", allocations)
	}
}

func TestRPCErrorCodeDoesNotNarrow(t *testing.T) {
	for _, code := range []int64{math.MinInt64, math.MaxInt64, math.MinInt32 - 1, math.MaxInt32 + 1} {
		fields := rpcErrorFields(code, "Rejected", "error.rejected")
		body := rpcErrorBody(fields[:]...)
		if strconv.IntSize == 32 {
			requireRPCEnvelopeFailure(t, body)
			continue
		}
		value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(body))
		if !ok || int64(value.Code) != code {
			t.Fatalf("code %d changed: %+v", code, value)
		}
	}
}

func TestRPCErrorInternalRedactionIsPreserved(t *testing.T) {
	fields := rpcErrorFields(503, "Unavailable", "private diagnostic")
	value, ok := stderrors.AsType[*luxerrors.AppError](decodeError(rpcErrorBody(fields[:]...)))
	if !ok || value.Code != 503 || value.Name != "Unavailable" || value.Message != "error.internal" || !value.Internal ||
		value.Cause == nil || value.Cause.Error() != "rpc error 503 Unavailable: private diagnostic" {
		t.Fatalf("internal error redaction changed: %+v", value)
	}
}

func TestRPCMalformedErrorAtEveryResponseBoundary(t *testing.T) {
	fields := rpcErrorFields(422, "Rejected", "error.rejected")
	for name, body := range map[string][]byte{
		"duplicate": rpcErrorBody(fields[0], fields[1], fields[2], fields[0]),
		"bad text":  rpcErrorBody(fields[0], fields[1], codec.AppendBytes([]byte{3}, []byte("\xff"))),
	} {
		t.Run(name, func(t *testing.T) {
			frame := append([]byte{statusError}, body...)
			requireRPCProtocolError(t, validateStreamAck(frame))
			_, _, err := decodeTracedResponse(frame)
			requireRPCProtocolError(t, err)
			var traced bytes.Buffer
			_, trace := api.WithDebugTrace(context.Background(), "rpc-error-test")
			if err := writeTracedFrame(&traced, statusTraceError, body, trace); err != nil {
				t.Fatal(err)
			}
			tracedFrame, err := ReadFrame(&traced, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, snapshot, err := decodeTracedResponse(tracedFrame)
			requireRPCProtocolError(t, err)
			if snapshot.TraceID != "rpc-error-test" {
				t.Fatal("malformed error lost its trace context")
			}
			testRPCUnaryErrorFrame(t, frame)
			testRPCStreamErrorFrame(t, frame)
		})
	}
}

func testRPCUnaryErrorFrame(t *testing.T, frame []byte) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	client := NewClient("unused:0")
	client.pool.Put(clientConn)
	done := make(chan error, 1)
	t.Cleanup(func() {
		client.Close()
		serverConn.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	go func() {
		defer serverConn.Close()
		if err := serverConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			done <- err
			return
		}
		if _, err := ReadFrame(serverConn, nil); err != nil {
			done <- err
			return
		}
		done <- WriteFrame(serverConn, frame)
	}()
	_, err := client.Call(1, nil)
	requireRPCProtocolError(t, err)
	if want := decodeError(frame[1:]).Error(); err.Error() != want {
		t.Fatalf("unary error = %v, want %s", err, want)
	}
}

func testRPCStreamErrorFrame(t *testing.T, frame []byte) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	subscription := &Subscription{
		messages: make(chan []byte, 1), errors: make(chan error, 1),
		conn: clientConn, cancel: cancel,
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		subscription.Close()
		serverConn.Close()
		<-done
	})
	go func() {
		defer close(done)
		subscription.readLoop(ctx)
	}()
	if err := serverConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(serverConn, frame); err != nil {
		t.Fatal(err)
	}
	err := <-subscription.Errors()
	requireRPCProtocolError(t, err)
	if want := decodeError(frame[1:]).Error(); err.Error() != want {
		t.Fatalf("stream error = %v, want %s", err, want)
	}
	if _, ok := <-subscription.Messages(); ok {
		t.Fatal("malformed error delivered a stream value")
	}
}
