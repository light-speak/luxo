package api

import (
	"context"
	"fmt"
	"testing"
)

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
					dispatch = func() error { return topic.Dispatch(func(value *int64, _ any) bool { return *value == 42 }, encode) }
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
