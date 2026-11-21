package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

func TestMalformedRequestsDoNotDispatchOrPoisonConnection(t *testing.T) {
	router := api.NewRouter()
	if err := router.Registry.Register("echo", 1); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterParams("echo", []api.ParamMeta{{Name: "id", FieldID: 1, Type: "Int"}}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	router.Handle("echo", func(_ context.Context, req *api.Request) error {
		calls.Add(1)
		req.Buf.B = append(req.Buf.B, req.BinaryParams()...)
		return nil
	})
	server := NewServer(router)
	conn, err := net.DialTimeout("tcp", startTestRPCServer(t, server), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := initiateHandshake(t.Context(), conn, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for i, body := range [][]byte{
		{0, 0, 0}, {0x80}, {1, 0x80}, {1, 4, 1}, {1, 0},
		{1, 0, 1, 0x80}, {1, 0, 1, 2, 1, 4, 0}, {1, 0, 0, 1},
		append(codec.AppendVarint(nil, uint64(codec.MaxAPIID)+1), 0, 0),
	} {
		response := exchangeAuditFrame(t, conn, body)
		_, err := decodeCallResponse(response, nil)
		appErr, ok := errors.AsType[*luxerrors.AppError](err)
		if !ok || appErr.Code != 400 || calls.Load() != int32(i) {
			t.Fatalf("malformed body %x dispatched or lacked 400: %v; calls=%d", body, err, calls.Load())
		}
		response = exchangeAuditFrame(t, conn, []byte{1, 0, 1, 84, 0})
		if !bytes.Equal(response, []byte{statusOK, 1, 84, 0}) || calls.Load() != int32(i+1) {
			t.Fatalf("valid request after %x changed: %x; calls=%d", body, response, calls.Load())
		}
	}
}

func exchangeAuditFrame(t *testing.T, conn net.Conn, body []byte) []byte {
	t.Helper()
	if err := WriteFrame(conn, encodeRequestEnvelope(requestKindCall, "", body)); err != nil {
		t.Fatal(err)
	}
	response, err := ReadFrame(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestStreamAndTraceRejectOversizeBeforeBorrow(t *testing.T) {
	c := NewClient("127.0.0.1:1")
	c.Close()
	params := make([]byte, maxFrameSize)
	if sub, err := c.SubscribeContext(t.Context(), "", 1, nil, params); sub != nil || !errors.Is(err, errRequestFrameTooLarge) {
		t.Errorf("oversized stream reached pool: subscription=%v err=%v", sub, err)
	}
	ctx, _ := api.WithDebugTrace(t.Context(), "size-boundary")
	if body, err := c.CallWithMaskContext(ctx, "", 1, nil, params); body != nil || !errors.Is(err, errRequestFrameTooLarge) {
		t.Errorf("oversized traced call reached pool: body=%v err=%v", body, err)
	}
}

func TestCallFrameConnectionReuse(t *testing.T) {
	c := &poolConn{}
	for _, size := range []int{1, 4096, 128, 65536, 0} {
		params := bytes.Repeat([]byte{17}, size)
		r := callRequest{apiID: 42, token: "token", mask: []byte{1}, params: params}
		frame, err := r.prepare(c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(frame, prepareCallFrame(requestKindCall, r.token, r.apiID, r.mask, params)) {
			t.Fatal("wire changed")
		}
		if cap(c.requestBuf) > 64<<10 {
			t.Fatal("oversized buffer retained")
		}
		if size == 128 && cap(c.requestBuf) < 4096 {
			t.Fatal("buffer was not reused")
		}
	}
}

func TestReusedRequestsKeepResponsesOwned(t *testing.T) {
	router := api.NewRouter()
	router.Registry.Register("echo", 1)
	router.Registry.RegisterParams("echo", []api.ParamMeta{{Name: "id", FieldID: 1, Type: "Int"}})
	router.Handle("echo", func(ctx context.Context, req *api.Request) error {
		req.Buf.B = append(req.Buf.B, req.BinaryParams()...)
		return nil
	})
	server := NewServer(router)
	client := NewClient(startTestRPCServer(t, server))
	defer server.Close()
	defer client.Close()
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			var held []byte
			var first []byte
			for i := range 32 {
				var enc codec.Encoder
				enc.WriteFieldInt(1, int64(worker*32+i))
				enc.WriteEnd()
				body, err := client.CallWithMaskContext(t.Context(), "", 1, nil, enc.Bytes())
				if err != nil || !bytes.Equal(body, enc.Bytes()) {
					t.Errorf("echo changed: %x %v", body, err)
					return
				}
				if i == 0 {
					held, first = body, bytes.Clone(body)
				}
			}
			if !bytes.Equal(held, first) {
				t.Error("later calls overwrote response")
			}
		})
	}
	wg.Wait()
}

func TestReusedRequestCancellationClosesBorrowedConnection(t *testing.T) {
	for _, reading := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			conn, peer := net.Pipe()
			defer peer.Close()
			client := NewClient("unused")
			defer client.Close()
			client.pool.Put(conn)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := client.CallWithMaskContext(ctx, "", 1, nil, nil); done <- err }()
			if reading {
				if _, err := ReadFrame(peer, nil); err != nil {
					t.Fatal(err)
				}
			}
			synctest.Wait()
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			synctest.Wait()
			if client.pool.open.Load() != 0 {
				t.Fatal("cancelled connection retained")
			}
		})
	}
}

func TestCallFrameReuseRejectsOversizeBeforeAllocation(t *testing.T) {
	c := &poolConn{requestBuf: make([]byte, 0, 128)}
	r := callRequest{apiID: 1, params: make([]byte, maxFrameSize)}
	if _, err := r.prepare(c); err == nil {
		t.Fatal("oversize accepted")
	}
	if cap(c.requestBuf) != 128 {
		t.Fatal("oversize allocated retained buffer")
	}
}

func TestUntrackedRequestPreparation(t *testing.T) {
	r := callRequest{apiID: 1}
	frame, err := r.prepare(nil)
	if err != nil || !bytes.Equal(frame, prepareCallFrame(requestKindCall, "", 1, nil, nil)) {
		t.Fatalf("frame %x: %v", frame, err)
	}
}

func TestOversizedRequestReturnsConnection(t *testing.T) {
	client := NewClient("unused")
	defer client.Close()
	conn, peer := net.Pipe()
	defer peer.Close()
	client.pool.Put(conn)
	if _, err := client.Call(1, make([]byte, maxFrameSize)); err == nil {
		t.Fatal("oversize accepted")
	}
	if len(client.pool.conns) != 1 || client.pool.open.Load() != 1 {
		t.Fatal("preflight error leaked connection")
	}
}

func BenchmarkLRPCOwnedRequest(b *testing.B) {
	for _, size := range []int{0, 128, 4096, 65536} {
		params := make([]byte, size)
		b.Run(fmt.Sprintf("%d/before", size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := writePreparedFrame(io.Discard, prepareCallFrame(requestKindCall, "", 1, nil, params)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%d/owned", size), func(b *testing.B) {
			conn := &poolConn{}
			request := callRequest{apiID: 1, params: params}
			b.ReportAllocs()
			for b.Loop() {
				frame, err := request.prepare(conn)
				if err != nil {
					b.Fatal(err)
				}
				if err := writePreparedFrame(io.Discard, frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
