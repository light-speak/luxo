package api

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// The shared reference cannot implement identity-specific fields. These cases
// measure fan-out plus ownership copies, not generated model encoding or I/O.
func BenchmarkEventIdentityDispatch(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		for _, size := range []int{8, 4096} {
			for _, variant := range []string{"false", "true", "selection-public", "selection-private"} {
				b.Run(fmt.Sprintf("subscribers=%d/bytes=%d/identity=%s", count, size, variant), func(b *testing.B) {
					personalized := variant == "true" || variant == "selection-private"
					router := NewRouter()
					topic := RegisterEventStream(router, "watch", func(context.Context, *Request, any) (int, error) { return 0, nil })
					bits := byte(1)
					if personalized {
						bits = 3
					}
					mask, err := codec.AppendSelectionMask(nil, []byte{bits}, nil)
					if err != nil {
						b.Fatal(err)
					}
					subs := make([]*StreamSub, 0, count)
					for index := range count {
						sub := router.Streams.subscribePreparedMode("watch", nil, index, mask, true, nil, nil, preparedStream{bind: func(sub *StreamSub) { topic.attach(sub, 0) }})
						subs = append(subs, sub)
					}
					payload := bytes.Repeat([]byte{7}, size)
					encode := func([]byte, bool) ([]byte, error) { return bytes.Clone(payload), nil }
					encodeIdentity := func(_ []byte, _ bool, identity any) ([]byte, error) {
						data := bytes.Clone(payload)
						if identity != nil {
							data[0] = byte(identity.(int))
						}
						return data, nil
					}
					dispatch := func() error { return topic.Dispatch(nil, encode) }
					if personalized {
						dispatch = func() error { return topic.DispatchIdentity(nil, encodeIdentity) }
					}
					if variant == "selection-public" || variant == "selection-private" {
						independent := func(mask []byte) bool { return !codec.FieldMaskHas(codec.SelectionMaskFields(mask), 2) }
						dispatch = func() error { return topic.DispatchIdentitySelection(nil, encodeIdentity, independent) }
					}
					b.ReportAllocs()
					for b.Loop() {
						if err := dispatch(); err != nil {
							b.Fatal(err)
						}
						for index, sub := range subs {
							data := <-sub.Ch
							want := byte(7)
							if personalized {
								want = byte(index)
							}
							if len(data) != size || data[0] != want {
								b.Fatal("wrong identity output")
							}
						}
					}
					for _, sub := range subs {
						router.Streams.Unsubscribe("watch", sub)
					}
				})
			}
		}
	}
}

// Compare dispatch only: subscriptions and their owned input values already
// exist. No network, event decoding, registration or shutdown is timed here.
func BenchmarkEventSnapshotDispatch(b *testing.B) {
	for _, count := range []int{1, 64, 128} {
		for _, variant := range []string{"legacy", "legacy-safe", "typed"} {
			b.Run(fmt.Sprintf("subscribers=%d/%s", count, variant), func(b *testing.B) {
				typed := variant == "typed"
				router := NewRouter()
				topic := RegisterEventStream(router, "watch", func(context.Context, *Request, any) (int64, error) { return 42, nil })
				var subs []*StreamSub
				for range count {
					prepared := preparedStream{}
					var params map[string]any
					if typed {
						prepared.bind = func(sub *StreamSub) { topic.attach(sub, 42) }
					} else {
						params = map[string]any{"id": int64(42)}
					}
					sub := router.Streams.subscribePreparedMode("watch", params, nil, nil, true, nil, nil, prepared)
					subs = append(subs, sub)
				}
				payload := []byte{84}
				encode := func([]byte, bool) ([]byte, error) { return payload, nil }
				dispatch := func() error {
					return router.Streams.DispatchPreparedEvent("watch", func(params *StreamParams, _ any) bool { value, ok := params.LookupInt("id"); return ok && value == 42 }, encode)
				}
				if variant == "legacy" {
					dispatch = func() error {
						return legacyEventDispatchBenchmark(router.Streams, "watch", func(params *StreamParams, _ any) bool { value, ok := params.LookupInt("id"); return ok && value == 42 }, encode)
					}
				}
				if typed {
					dispatch = func() error {
						return topic.Dispatch(func(value *int64, _ any) (bool, error) { return *value == 42, nil }, encode)
					}
				}
				b.ReportAllocs()
				for b.Loop() {
					if err := dispatch(); err != nil {
						b.Fatal(err)
					}
					for _, sub := range subs {
						if data := <-sub.Ch; len(data) != 1 || data[0] != 84 {
							b.Fatal("bad payload")
						}
					}
				}
				for _, sub := range subs {
					router.Streams.Unsubscribe("watch", sub)
				}
			})
		}
	}
}

// Test-only reference of the pre-fix dispatcher, including its unsafe manual
// unlock. Never used by runtime routes or error/panic benchmarks.
func legacyEventDispatchBenchmark(h *StreamHub, name string, match StreamSubscriberMatcher, encode func([]byte, bool) ([]byte, error)) error {
	h.mu.RLock()
	var encodings streamEncodings
	var slow []*StreamSub
	for _, sub := range h.subs[name] {
		if match != nil && !match(sub.Params, sub.Identity) {
			continue
		}
		data, err := cachedStreamEncoding(&encodings, sub.FieldMask, sub.Binary, encode)
		if err != nil {
			h.mu.RUnlock()
			h.removeSlowSubscribers(name, slow)
			return err
		}
		if !enqueueStreamData(sub, data) {
			slow = append(slow, sub)
		}
	}
	h.mu.RUnlock()
	h.removeSlowSubscribers(name, slow)
	return nil
}
