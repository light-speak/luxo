package h2transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestControlMailboxBounds(t *testing.T) {
	for _, size := range []int{0, 65536} {
		if _, err := newControlMailbox(size); err == nil {
			t.Fatal("invalid capacity")
		}
	}
	m, err := newControlMailbox(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint32{1, 2} {
		if err := m.put(controlCommand{value: n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.put(controlCommand{}); !errors.Is(err, errMailboxFull) {
		t.Fatal(err)
	}
	first, ok, err := m.poll()
	if err != nil || !ok || first.value != 1 {
		t.Fatal(first, err)
	}
	if err := m.put(controlCommand{value: 3}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint32{2, 3} {
		cmd, ok, err := m.poll()
		if err != nil || !ok || cmd.value != n {
			t.Fatal(cmd, err)
		}
	}
	if _, ok, err := m.poll(); ok || err != nil {
		t.Fatal(err)
	}
	m.close()
	m.close()
	if _, _, err := m.poll(); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
	if err := m.put(controlCommand{}); !errors.Is(err, errQueueClosed) {
		t.Fatal(err)
	}
}

func TestControlMailboxWait(t *testing.T) {
	for _, operation := range []string{"put", "close", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _ := newControlMailbox(2)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := m.wait(ctx); done <- err }()
				synctest.Wait()
				switch operation {
				case "put":
					_ = m.put(controlCommand{kind: pingCommand})
				case "close":
					m.close()
				case "cancel":
					cancel()
				}
				err := <-done
				if operation == "put" && err != nil {
					t.Fatal(err)
				}
				if operation == "close" && !errors.Is(err, errQueueClosed) {
					t.Fatal(err)
				}
				if operation == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestControlMailboxConcurrentClose(t *testing.T) {
	m, _ := newControlMailbox(8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 1000 {
				err := m.put(controlCommand{kind: pingCommand})
				if err != nil && !errors.Is(err, errMailboxFull) && !errors.Is(err, errQueueClosed) {
					t.Error(err)
				}
			}
		})
	}
	group.Go(func() {
		for range 1000 {
			_, _, _ = m.poll()
		}
	})
	group.Go(m.close)
	group.Wait()
}

func TestControlMailboxPreCancelled(t *testing.T) {
	m, _ := newControlMailbox(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func BenchmarkControlHandoff(b *testing.B) {
	m, err := newControlMailbox(128)
	if err != nil {
		b.Fatal(err)
	}
	command := controlCommand{kind: windowCommand, streamID: 1, value: 4096}
	b.ReportAllocs()
	for b.Loop() {
		if err := m.put(command); err != nil {
			b.Fatal(err)
		}
		if _, ok, err := m.poll(); err != nil || !ok {
			b.Fatal(err)
		}
	}
}
