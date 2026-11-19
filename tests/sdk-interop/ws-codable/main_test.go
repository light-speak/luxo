package main

import "testing"

func TestUnsubscribeProbeCountsEveryReceivedFrame(t *testing.T) {
	c := &peerConnection{streams: map[uint64]string{7: "watch"}}
	id := uint64(7)
	for range 2 {
		if err := c.handle(request{Unsubscribe: &id}); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.streams) != 0 || c.unsubscriptions != 2 {
		t.Fatalf("active=%d unsubscribe frames=%d, want 0 and 2", len(c.streams), c.unsubscriptions)
	}
}
