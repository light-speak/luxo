package h2transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestControlFloodBound(t *testing.T) {
	start := time.Now()
	gate := controlRate{burst: 2, interval: time.Second}
	if !gate.allow(start) || !gate.allow(start) || gate.allow(start.Add(time.Millisecond)) {
		t.Fatal("burst not bounded")
	}
	if !gate.allow(start.Add(time.Second)) {
		t.Fatal("budget did not renew")
	}
}

func TestWindowUpdatesFollowDataProgress(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := newCandidateConnection(context.Background(), a, http.NotFoundHandler())
	c.rate.interval = time.Hour
	for range 4096 {
		c.windowCredit.sentData()
		for range 2 {
			if err := c.dispatchFrame(&http2.WindowUpdateFrame{Increment: 4096}); err != nil {
				t.Fatal("useful flow control throttled", err)
			}
			_, _, _ = c.inbox.poll()
		}
	}
	for range c.rate.burst {
		if err := c.dispatchFrame(&http2.WindowUpdateFrame{Increment: 1}); err != nil {
			t.Fatal(err)
		}
		_, _, _ = c.inbox.poll()
	}
	if err := c.dispatchFrame(&http2.WindowUpdateFrame{Increment: 1}); err == nil {
		t.Fatal("no-progress flood accepted")
	}
	c.windowCredit.sentData()
	if err := c.dispatchFrame(&http2.PingFrame{}); err == nil {
		t.Fatal("DATA credit authorized PING flood")
	}
	if err := c.dispatchFrame(&http2.WindowUpdateFrame{Increment: 0}); err != nil {
		t.Fatal(err)
	}
	command, _, _ := c.inbox.poll()
	if err := c.writer.apply(command); err == nil {
		t.Fatal("credit bypassed zero increment validation")
	}
}

func TestWindowCreditBound(t *testing.T) {
	var credit windowCredit
	for range 2048 {
		credit.sentData()
	}
	for range 1024 {
		if !credit.take() {
			t.Fatal("missing credit")
		}
	}
	if credit.take() {
		t.Fatal("unbounded credit")
	}
}

func BenchmarkWindowProgressCredit(b *testing.B) {
	var credit windowCredit
	b.ReportAllocs()
	for b.Loop() {
		credit.sentData()
		credit.take()
		credit.take()
	}
}

func TestActiveDeadlineNotExtendedByTraffic(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := newCandidateConnection(context.Background(), a, http.NotFoundHandler())
	expires := time.Now().Add(time.Second)
	c.tasks[1] = &requestTask{deadline: expires}
	c.refreshDeadline()
	if got := c.readDeadline(time.Now()); !got.Equal(expires) {
		t.Fatal("active deadline lost", got)
	}
	if got := c.readDeadline(time.Now().Add(time.Minute)); !got.Equal(expires) {
		t.Fatal("traffic extended deadline", got)
	}
}

func TestDispatchResetCancelsOnlyMatchingTask(t *testing.T) {
	c := newCandidateConnection(t.Context(), &faultConn{}, http.NotFoundHandler())
	first, cancelFirst := context.WithCancel(t.Context())
	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelFirst()
	defer cancelSecond()
	c.tasks[1] = &requestTask{id: 1, ctx: first, cancel: cancelFirst}
	c.tasks[3] = &requestTask{id: 3, ctx: second, cancel: cancelSecond}
	frame := &http2.RSTStreamFrame{FrameHeader: http2.FrameHeader{StreamID: 1}, ErrCode: http2.ErrCodeCancel}
	if err := c.dispatchFrame(frame); err != nil {
		t.Fatal(err)
	}
	if first.Err() == nil || second.Err() != nil || len(c.tasks) != 2 {
		t.Fatal("stream reset canceled another task or released active ownership")
	}
	command, ok, err := c.inbox.poll()
	if err != nil || !ok || command.kind != resetCommand || command.streamID != 1 || command.code != http2.ErrCodeCancel {
		t.Fatal("reset was not forwarded to writer", command, err)
	}
}

func TestDispatchRejectsPushAndIgnoresAdvisoryFrames(t *testing.T) {
	c := newCandidateConnection(t.Context(), &faultConn{}, http.NotFoundHandler())
	if err := c.dispatchFrame(&http2.PushPromiseFrame{}); !errors.Is(err, http2.ConnectionError(http2.ErrCodeProtocol)) {
		t.Fatal("client push promise accepted", err)
	}
	for _, frame := range []http2.Frame{&http2.PriorityFrame{}, &http2.UnknownFrame{}} {
		if err := c.dispatchFrame(frame); err != nil {
			t.Fatal("advisory or extension frame rejected", err)
		}
	}
	if _, ok, err := c.inbox.poll(); ok || err != nil || len(c.tasks) != 0 {
		t.Fatal("ignored frame created work", err)
	}
}
