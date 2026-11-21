package api

import (
	"context"
	"math"
	"sync"
	"testing"
)

func TestConnectionSubscriptionsDoNotRemoveReplacement(t *testing.T) {
	var items connectionSubscriptions
	if _, ok := items.take(1); ok {
		t.Fatal("empty connection has a subscription")
	}
	old, next := &StreamSub{}, &StreamSub{}
	items.add(1, "watch", old)
	if item, ok := items.take(1); !ok || item.sub != old {
		t.Fatal("unsubscribe did not remove its own registration")
	}
	items.add(2, "watch", next)
	items.remove(1, old)
	if len(items.items) != 1 || items.items[0].sub != next {
		t.Fatal("old writer removed a new subscription")
	}
	items.remove(2, next)
	items.remove(2, next)
	if len(items.items) != 0 {
		t.Fatal("finished writer retained a registration")
	}
}

func TestConnectionSubscriptionsCloseReleasesBeforeCancel(t *testing.T) {
	var items connectionSubscriptions
	hub := NewStreamHub()
	ctx, cancel := context.WithCancel(t.Context())
	sub := hub.Subscribe("watch", nil, nil, nil, func() {
		if _, ok := items.take(1); ok {
			t.Error("connection retained registration during cancellation")
		}
		cancel()
	})
	items.add(1, "watch", sub)
	items.close(hub)
	items.close(hub)
	if ctx.Err() == nil || hub.SubCount("watch") != 0 || items.items != nil {
		t.Fatal("connection close retained its registration or producer")
	}
}

func TestConnectionSubscriptionsRemovalClearsReferences(t *testing.T) {
	var items connectionSubscriptions
	first, middle, last := &StreamSub{}, &StreamSub{}, &StreamSub{}
	items.add(1, "first", first)
	items.add(2, "middle", middle)
	items.add(3, "last", last)
	stored := items.items
	if item, ok := items.take(2); !ok || item.sub != middle || len(items.items) != 2 || items.items[0].sub != first || items.items[1].sub != last {
		t.Fatal("removal affected an unrelated registration")
	}
	if stored[2] != (connectionSubscription{}) {
		t.Fatal("removal retained a tail reference")
	}
	items.remove(9, last)
	items.remove(1, last)
	if len(items.items) != 2 {
		t.Fatal("mismatched identity removed a registration")
	}
}

func TestConnectionSubscriptionsConcurrentCloseAndWriterExit(t *testing.T) {
	for range 1000 {
		var items connectionSubscriptions
		hub := NewStreamHub()
		sub, producer, writer := hub.subscribeWS(t.Context(), "watch", nil, nil, nil, false)
		items.add(1, "watch", sub)
		var wg sync.WaitGroup
		wg.Go(func() { items.close(hub) })
		wg.Go(func() {
			hub.Unsubscribe("watch", sub)
			items.remove(1, sub)
		})
		wg.Go(func() {
			if active, ok := items.take(1); ok {
				hub.Unsubscribe(active.apiName, active.sub)
			}
		})
		wg.Wait()
		if producer.Err() == nil || writer.Err() == nil || hub.SubCount("watch") != 0 || len(items.items) != 0 {
			t.Fatal("concurrent cleanup retained subscription state")
		}
	}
}

func TestConnectionSubscriptionsNeverReuseIDs(t *testing.T) {
	var items connectionSubscriptions
	for _, id := range []uint64{0, 1, 1 << 53, math.MaxUint64} {
		if !items.reserve(id) || items.reserve(id) {
			t.Fatalf("ID reservation/reuse failed for %d", id)
		}
	}
	if items.reserve(0) || items.reserve(math.MaxInt64) {
		t.Fatal("IDs wrapped or moved backwards")
	}
}
