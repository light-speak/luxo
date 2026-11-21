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

func TestSlowPeerCannotExtendActiveDeadlineWithPing(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "body", true: "response-window"}[complete], func(t *testing.T) {
			server, peer := net.Pipe()
			defer server.Close()
			defer peer.Close()
			c := newCandidateConnection(context.Background(), server, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !complete {
					t.Error("incomplete request dispatched")
				}
				_, _ = w.Write([]byte("response"))
			}))
			c.limits.BodyTimeout, c.limits.ResponseTimeout = 150*time.Millisecond, 150*time.Millisecond
			done := make(chan error, 1)
			go func() { done <- c.run() }()
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				f := http2.NewFramer(nil, peer)
				for {
					if _, err := f.ReadFrame(); err != nil {
						return
					}
				}
			}()
			defer func() { _ = peer.Close(); <-readerDone }()
			_ = peer.SetWriteDeadline(time.Now().Add(time.Second))
			if _, err := peer.Write([]byte(http2.ClientPreface)); err != nil {
				t.Fatal(err)
			}
			f := http2.NewFramer(peer, nil)
			if err := f.WriteSettings(http2.Setting{ID: http2.SettingInitialWindowSize, Val: 0}); err != nil {
				t.Fatal(err)
			}
			if err := f.WriteSettingsAck(); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			if _, err := peer.Write(headerWire(t, requestFields(), complete)); err != nil {
				t.Fatal(err)
			}
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			timeout := time.NewTimer(time.Second)
			defer timeout.Stop()
			for {
				select {
				case err := <-done:
					var timed net.Error
					if !errors.As(err, &timed) || !timed.Timeout() || time.Since(started) < 100*time.Millisecond {
						t.Fatal("not closed by active deadline", err)
					}
					return
				case <-timeout.C:
					t.Fatal("peer traffic extended active deadline")
				case <-tick.C:
					_ = f.WritePing(false, [8]byte{})
				}
			}
		})
	}
}
