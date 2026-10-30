package h2transport

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"golang.org/x/net/http2"
)

func TestReceiveWindowsFollowBudgets(t *testing.T) {
	for _, tc := range []struct {
		body, budget       int
		connection, stream uint32
	}{
		{2 << 20, 64 << 20, 8 << 20, 2 << 20},
		{1 << 20, 4 << 20, 4 << 20, 1 << 20},
		{16 << 20, 32 << 20, 8 << 20, 2 << 20},
		{1, 1, 65535, 65535},
	} {
		c := newCandidateConnection(context.Background(), nil, http.NotFoundHandler())
		c.receiver.maxBody, c.receiver.limit = tc.body, tc.budget
		c.configureReceiveWindows()
		if c.receiver.window.available != tc.connection || c.receiver.initialWindow != tc.stream {
			t.Fatalf("budget %+v: receiver %d/%d", tc, c.receiver.window.available, c.receiver.initialWindow)
		}
		if c.writer.receiveConnection != tc.connection || c.writer.receiveStream != tc.stream {
			t.Fatal("writer and receiver disagree")
		}
		if _, err := c.receiver.open(receiveHead(t, 1, "", false)); err != nil {
			t.Fatal(err)
		}
		if c.receiver.streams[1].window.available != tc.stream || c.receiver.buffered != 0 {
			t.Fatal("window must not preallocate body storage")
		}
	}
}

func TestReceiveWindowAnnouncement(t *testing.T) {
	var wire bytes.Buffer
	q, _ := newDataQueue(2, 1024, func([]byte) {})
	w := newControlWriter(newDataWriter(q, &wire, 1024))
	w.receiveConnection, w.receiveStream = 8<<20, 2<<20
	if err := w.writeInitialSettings(); err != nil {
		t.Fatal(err)
	}
	if err := w.data.buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	f := http2.NewFramer(nil, &wire)
	frame, err := f.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	size, ok := frame.(*http2.SettingsFrame).Value(http2.SettingInitialWindowSize)
	if !ok || size != 2<<20 {
		t.Fatal("missing bounded stream credit")
	}
	frame, err = f.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	update := frame.(*http2.WindowUpdateFrame)
	if update.StreamID != 0 || update.Increment != (8<<20)-65535 {
		t.Fatal(update)
	}
}

func TestReceiveCreditDoesNotBypassStorageBudget(t *testing.T) {
	c := newCandidateConnection(context.Background(), nil, http.NotFoundHandler())
	c.receiver.maxBody, c.receiver.limit = 2<<20, 4<<20
	budget := &byteBudget{limit: 4 << 20}
	c.receiver.budget = budget
	c.configureReceiveWindows()
	if !budget.take(4 << 20) {
		t.Fatal("reserve shared budget")
	}
	if _, err := c.receiver.open(receiveHead(t, 1, "", false)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.receiver.data(receiveData(t, 1, true, []byte("x"), nil))
	if err == nil || c.receiver.buffered != 0 || budget.used.Load() != 4<<20 {
		t.Fatal("flow credit bypassed shared budget", err)
	}
	budget.give(4 << 20)
	c.receiver.maxBody = 1
	if _, err := c.receiver.open(receiveHead(t, 3, "", false)); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.receiver.data(receiveData(t, 3, true, []byte("xx"), nil))
	if err == nil || budget.used.Load() != 0 {
		t.Fatal("flow credit bypassed body limit", err)
	}
}

// Each receiver is owned by one reader; only the process budget is shared.
// Keep uploads incomplete so returned flow credit cannot imply free storage.
func TestReceiveWindowsConcurrentIncompleteUploads(t *testing.T) {
	budget := &byteBudget{limit: 320 << 10}
	var receivers [4]*requestReceiver
	for i := range receivers {
		c := newCandidateConnection(t.Context(), nil, http.NotFoundHandler())
		c.receiver.budget = budget
		c.configureReceiveWindows()
		receivers[i] = c.receiver
		if _, err := c.receiver.open(receiveHead(t, 1, "", false)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.receiver.close)
	}
	frame := receiveData(t, 1, false, make([]byte, 16<<10), nil)
	// Sequential growth avoids making admission depend on temporary copy peaks.
	for _, r := range receivers {
		for range 4 {
			if ready, _, err := r.data(frame); err != nil || ready != nil {
				t.Fatal("incomplete upload", ready, err)
			}
		}
	}
	// Reserve the remaining space for another live response after buffer growth.
	if !budget.take(64 << 10) {
		t.Fatal("reserve response storage")
	}
	if budget.used.Load() != budget.limit {
		t.Fatal("fixture must fill the shared storage budget", budget.used.Load())
	}
	results := make(chan error, len(receivers))
	var wg sync.WaitGroup
	for _, r := range receivers {
		wg.Go(func() {
			_, _, err := r.data(frame)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		var streamError http2.StreamError
		if !errors.As(err, &streamError) || streamError.Code != http2.ErrCodeEnhanceYourCalm {
			t.Fatal("must reject storage exhaustion, not flow credit", err)
		}
	}
	if budget.used.Load() != budget.limit {
		t.Fatal("retained idle buffers must remain charged", budget.used.Load())
	}
	for _, r := range receivers {
		if len(r.streams) != 0 || r.buffered != 0 {
			t.Fatal("rejected upload remained active")
		}
		r.close()
	}
	budget.give(64 << 10)
	if budget.used.Load() != 0 {
		t.Fatal("connection cleanup leaked shared capacity", budget.used.Load())
	}
	assertReceiveBudgetReusable(t, budget)
}

func assertReceiveBudgetReusable(t *testing.T, budget *byteBudget) {
	t.Helper()
	r, err := newRequestReceiver(1, 128<<10, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	r.budget = budget
	defer r.close()
	if _, err := r.open(receiveHead(t, 1, "", false)); err != nil {
		t.Fatal(err)
	}
	if ready, _, err := r.data(receiveData(t, 1, true, []byte("ok"), nil)); err != nil || ready == nil || string(ready.body) != "ok" {
		t.Fatal("released budget cannot serve a new request", ready, err)
	}
}
