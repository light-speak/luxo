package rpc

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Exact prior method body; only the method name differs.
func (c *Client) callBeforeEmptyTraceID(ctx context.Context, bearerToken string, apiID int, fieldMask []byte, params []byte, target TraceTarget) ([]byte, error) {
	trace := api.DebugTrace(ctx)
	if trace == nil {
		return c.callRequest(ctx, callRequest{apiID: apiID, token: bearerToken, mask: fieldMask, params: params})
	}
	fieldPath := target.FieldPath
	if fieldPath == "" {
		fieldPath = api.JoinTraceFieldPath(api.TraceFieldPath(ctx), target.Field)
	}
	if size := tracedCallFrameSize(bearerToken, api.TraceID(ctx), fieldPath, apiID, fieldMask, params); size > maxFrameSize || size < 0 {
		return nil, errRequestFrameTooLarge
	}
	target.FieldPath = fieldPath
	_, callSpan := trace.StartSpan(ctx, api.DebugSpanMeta{
		Name: "service.call", Category: "service", Service: c.traceName(), Operation: target.Operation,
		Field: target.Field, FieldPath: fieldPath, Selection: target.Selection, Dependency: target.Dependency,
	})
	payload := prepareTracedCallFrame(bearerToken, api.TraceID(ctx), fieldPath, trace.DatabaseDetails(), apiID, fieldMask, params)
	body, remote, err := c.callTracedPayload(ctx, payload)
	applyTraceDefaults(&remote, c.traceName(), target)
	trace.MergeSpan(callSpan, remote)
	trace.FinishSpan(callSpan)
	return body, err
}

type traceLocalConn struct {
	net.Conn
	response []byte
	offset   int
	writes   int
}

func (c *traceLocalConn) Read(data []byte) (int, error) {
	if c.offset == len(c.response) && len(data) != 0 {
		return 0, io.EOF
	}
	n := copy(data, c.response[c.offset:])
	c.offset += n
	return n, nil
}
func (c *traceLocalConn) Write(data []byte) (int, error) {
	c.offset = 0
	c.writes++
	return len(data), nil
}
func (*traceLocalConn) Close() error                { return nil }
func (*traceLocalConn) SetDeadline(time.Time) error { return nil }

func traceLocalClient(t testing.TB, traced bool) (*Client, *traceLocalConn) {
	t.Helper()
	payload := []byte{statusOK, 14}
	if traced {
		trace, err := api.EncodeDebugTrace(api.DebugTraceSnapshot{TraceID: "valid"})
		if err != nil {
			t.Fatal(err)
		}
		payload = codec.AppendVarint([]byte{statusTraceOK}, uint64(len(trace)))
		payload = append(payload, trace...)
		payload = append(payload, 14)
	}
	var response bytes.Buffer
	if err := WriteFrame(&response, payload); err != nil {
		t.Fatal(err)
	}
	conn := &traceLocalConn{response: response.Bytes()}
	client := NewClient("unused")
	client.pool.Put(conn)
	t.Cleanup(client.Close)
	return client, conn
}

func BenchmarkRPCTraceLocal(b *testing.B) {
	for _, traced := range []bool{false, true} {
		for _, before := range []bool{true, false} {
			version := "current"
			if before {
				version = "before"
			}
			b.Run(strconv.FormatBool(traced)+"/"+version, func(b *testing.B) {
				client, _ := traceLocalClient(b, traced)
				ctx := b.Context()
				if traced {
					ctx, _ = api.WithDebugTrace(ctx, "valid")
				}
				call := client.CallWithMaskTargetContext
				if before {
					call = client.callBeforeEmptyTraceID
				}
				for range 256 {
					if _, err := call(ctx, "", 1, nil, nil, TraceTarget{}); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := call(ctx, "", 1, nil, nil, TraceTarget{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestTraceLocalClientResponseAndReuse(t *testing.T) {
	for _, traced := range []bool{false, true} {
		t.Run(strconv.FormatBool(traced), func(t *testing.T) {
			client, raw := traceLocalClient(t, traced)
			ctx := t.Context()
			if traced {
				ctx, _ = api.WithDebugTrace(ctx, "valid")
			}
			initial, err := client.pool.Get()
			if err != nil {
				t.Fatal(err)
			}
			client.pool.Put(initial)
			for _, before := range []bool{true, false} {
				call := client.CallWithMaskTargetContext
				if before {
					call = client.callBeforeEmptyTraceID
				}
				body, err := call(ctx, "", 1, nil, nil, TraceTarget{})
				if err != nil || !bytes.Equal(body, []byte{14}) {
					t.Fatal(body, err)
				}
				reused, err := client.pool.Get()
				if err != nil || reused != initial {
					t.Fatal("connection changed", err)
				}
				client.pool.Put(reused)
			}
			if raw.writes != 2 {
				t.Fatal("unexpected request count", raw.writes)
			}
		})
	}
}
