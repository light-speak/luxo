package api

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Freeze the former Send loop only for open subscriptions; racing its close is
// deliberately excluded because it can panic. This is not a correct equivalent.
func streamSendBaseline(s *Stream, data []byte) error {
	select {
	case s.sub.Ch <- data:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		return ErrStreamFull
	}
}

func BenchmarkNativeStreamSendSafety(b *testing.B) {
	for _, scenario := range []string{"serial", "full", "parallel-shared", "parallel-isolated"} {
		for _, checked := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/checked=%t", scenario, checked), func(b *testing.B) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				send := streamSendBaseline
				if checked {
					send = (*Stream).Send
				}
				stream := newBenchmarkStream(ctx)
				payload := []byte("message")
				b.ReportAllocs()
				if scenario == "full" {
					for range cap(stream.sub.Ch) {
						stream.sub.Ch <- payload
					}
				}
				if scenario == "parallel-shared" || scenario == "parallel-isolated" {
					benchmarkParallelStream(b, ctx, stream, send, payload, scenario == "parallel-isolated")
					return
				}
				for b.Loop() {
					err := send(stream, payload)
					if scenario == "full" {
						if err != ErrStreamFull {
							b.Fatalf("full queue send = %v", err)
						}
					} else {
						if err != nil {
							b.Fatal(err)
						}
						<-stream.sub.Ch
					}
				}
			})
		}
	}
}

func newBenchmarkStream(ctx context.Context) *Stream {
	return &Stream{sub: &StreamSub{Ch: make(chan []byte, 64)}, ctx: ctx}
}

// The former shared context interrupts accepted writes on native completion.
// Freeze only that setup for comparison, not as a correct equivalent. Both
// variants use the current StreamSub layout and exclude network/message work.
func subscribeSharedWSBaseline(h *StreamHub, parent context.Context) (*StreamSub, context.Context, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	sub := h.SubscribeMode("watch", nil, nil, nil, false, cancel)
	return sub, ctx, ctx
}

func subscribeIndependentWS(h *StreamHub, parent context.Context) (*StreamSub, context.Context, context.Context) {
	return h.subscribeWS(parent, "watch", nil, nil, nil, false)
}

func BenchmarkWSStreamLifetime(b *testing.B) {
	for _, waiting := range []bool{false, true} {
		for _, completed := range []bool{false, true} {
			for _, independent := range []bool{false, true} {
				b.Run(fmt.Sprintf("waiting=%t/complete=%t/independent=%t", waiting, completed, independent), func(b *testing.B) {
					benchmarkWSStreamLifetime(b, waiting, completed, independent)
				})
			}
		}
	}
}

func BenchmarkWSStreamSlowRemovalLifetime(b *testing.B) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := NewStreamHub()
	var producer, writer context.Context
	var sub *StreamSub
	var slow [1]*StreamSub
	b.ReportAllocs()
	for b.Loop() {
		sub, producer, writer = hub.subscribeWS(parent, "watch", nil, nil, nil, false)
		producer.Done()
		writer.Done()
		// Measure removal after a caller has classified the subscriber as slow;
		// enqueue/backpressure detection and network I/O are separate paths.
		slow[0] = sub
		hub.removeSlowSubscribers("watch", slow[:])
	}
	if producer.Err() == nil || writer.Err() == nil || hub.SubCount("watch") != 0 || sub.result != ErrStreamFull {
		b.Fatal("slow removal did not finish the subscription")
	}
}

func benchmarkWSStreamLifetime(b *testing.B, waiting, completed, independent bool) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := NewStreamHub()
	subscribe := subscribeSharedWSBaseline
	if independent {
		subscribe = subscribeIndependentWS
	}
	var producer, writer context.Context
	var sub *StreamSub
	b.ReportAllocs()
	for b.Loop() {
		sub, producer, writer = subscribe(hub, parent)
		if waiting {
			// Account separately for the lazy channels used by a waiting handler
			// and write pump, without scheduling goroutines or sending messages.
			producer.Done()
			writer.Done()
		}
		if completed {
			hub.finish("watch", sub, nil)
		}
		hub.Unsubscribe("watch", sub)
	}
	if producer.Err() == nil || writer.Err() == nil || hub.SubCount("watch") != 0 {
		b.Fatal("subscription did not release its contexts")
	}
}

func benchmarkParallelStream(b *testing.B, ctx context.Context, shared *Stream, send func(*Stream, []byte) error, payload []byte, isolated bool) {
	b.RunParallel(func(pb *testing.PB) {
		stream := shared
		if isolated {
			streamCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			stream = newBenchmarkStream(streamCtx)
		}
		for pb.Next() {
			if err := send(stream, payload); err != nil {
				b.Error(err)
				return
			}
			<-stream.sub.Ch
		}
	})
}

// Frozen dispatch/cache loops from before the error-return migration. Keep the
// callback boundary and cache allocations comparable; only valid data is timed.
func dispatchPreparedBaseline(h *StreamHub, name string, matcher StreamSubscriberMatcher, encode func([]byte, bool) []byte) {
	h.mu.RLock()
	var encodings []streamEncoding
	var slow []*StreamSub
	for _, sub := range h.subs[name] {
		if matcher != nil && !matcher(sub.Params, sub.Identity) {
			continue
		}
		data := cachedEncodingBaseline(&encodings, sub.FieldMask, sub.Binary, encode)
		if !enqueueStreamData(sub, data) {
			slow = append(slow, sub)
		}
	}
	h.mu.RUnlock()
	h.removeSlowSubscribers(name, slow)
}

func cachedEncodingBaseline(encodings *[]streamEncoding, mask []byte, binary bool, encode func([]byte, bool) []byte) []byte {
	for i := range *encodings {
		cached := &(*encodings)[i]
		if cached.binary == binary && bytes.Equal(cached.mask, mask) {
			return cached.data
		}
	}
	data := encode(mask, binary)
	*encodings = append(*encodings, streamEncoding{mask: mask, binary: binary, data: data})
	return data
}

func BenchmarkStreamEncodingErrors(b *testing.B) {
	for _, count := range []int{1, 64} {
		for _, checked := range []bool{false, true} {
			b.Run(fmt.Sprintf("subscribers=%d/checked=%t", count, checked), func(b *testing.B) {
				hub := NewStreamHub()
				subs := make([]*StreamSub, count)
				for i := range subs {
					subs[i] = hub.SubscribeMode("watch", nil, nil, []byte{1}, true, nil)
				}
				payload := []byte{0, 1, 2, 0}
				baseline := func([]byte, bool) []byte { return payload }
				encode := func([]byte, bool) ([]byte, error) { return payload, nil }
				b.ReportAllocs()
				for b.Loop() {
					if checked {
						if err := hub.DispatchPreparedEvent("watch", nil, encode); err != nil {
							b.Fatal(err)
						}
					} else {
						dispatchPreparedBaseline(hub, "watch", nil, baseline)
					}
					for _, sub := range subs {
						if got := <-sub.Ch; !bytes.Equal(got, payload) {
							b.Fatal("invalid stream payload")
						}
					}
				}
			})
		}
	}
}

// Compare only framing into a reused buffer, excluding sockets, pooling and
// channel scheduling. The old API identity cannot isolate stream instances.
func BenchmarkWSStreamIdentityEnvelope(b *testing.B) {
	for _, mode := range []string{"json", "binary"} {
		for _, identity := range []string{"former-api", "small-id", "max-id"} {
			b.Run(mode+"/"+identity, func(b *testing.B) {
				buf := &ResponseBuf{B: make([]byte, 0, 128)}
				id := uint64(71)
				if identity == "max-id" {
					id = ^uint64(0)
				}
				b.ReportAllocs()
				for b.Loop() {
					buf.B = buf.B[:0]
					if mode == "json" {
						buf.AppendString(`{"$stream":`)
						if identity == "former-api" {
							if err := buf.AppendJSONString("watchPrecision"); err != nil {
								b.Fatal(err)
							}
						} else {
							buf.B = strconv.AppendUint(buf.B, id, 10)
						}
						buf.AppendString(`,"data":42}`)
					} else {
						buf.AppendByte(BinaryFrameStream)
						buf.B = codec.AppendVarint(buf.B, id)
						buf.AppendByte(84)
					}
				}
				if len(buf.B) == 0 {
					b.Fatal("missing envelope")
				}
			})
		}
	}
}
