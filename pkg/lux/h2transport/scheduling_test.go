package h2transport

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/http2"
)

func TestTaskDeadlineReplacement(t *testing.T) {
	base := time.Unix(100, 0)
	for _, sample := range []struct {
		name             string
		old, next, other time.Duration
	}{
		{"later-nonminimum", 2, 3, 1}, {"earlier-nonminimum", 2, 0, 1},
		{"replace-minimum", 1, 3, 2}, {"tied-minimum", 1, 3, 1},
		{"earlier-minimum", 1, 0, 2},
	} {
		t.Run(sample.name, func(t *testing.T) {
			task := &requestTask{deadline: base.Add(sample.old)}
			other := base.Add(sample.other)
			c := &candidateConnection{tasks: map[uint32]*requestTask{1: task, 3: {deadline: other}}}
			c.refreshDeadline()
			next := base.Add(sample.next)
			c.replaceTaskDeadline(task, next)
			want := next
			if other.Before(want) {
				want = other
			}
			if !c.deadline.Equal(want) || !task.deadline.Equal(next) {
				t.Fatal("incorrect earliest deadline")
			}
		})
	}
}

func TestIdleWriterDoesNotRenewDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &faultConn{}
		q, _ := testQueue(t, 1, 32)
		m, _ := newControlMailbox(8)
		w := newControlWriter(newDataWriter(q, conn, 4096))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.run(ctx, conn, m, time.Second) }()
		synctest.Wait()
		initial := conn.deadlines
		_ = m.put(controlCommand{kind: pingCommand})
		synctest.Wait()
		afterPing := conn.deadlines
		cancel()
		<-done
		if initial != 1 || afterPing != 2 {
			t.Fatalf("initial=%d afterPing=%d", initial, afterPing)
		}
	})
}

func TestWokenWriterBatchesReadyControls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn := &faultConn{}
		q, _ := testQueue(t, 1, 32)
		m, _ := newControlMailbox(16)
		w := newControlWriter(newDataWriter(q, conn, 4096))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.run(ctx, conn, m, time.Second) }()
		synctest.Wait()
		// Publish one complete burst before the waiting writer can poll it.
		m.mu.Lock()
		for i := range 8 {
			m.items[i] = controlCommand{kind: pingCommand, ping: [8]byte{byte(i)}}
		}
		m.count = 8
		m.wake <- struct{}{}
		m.mu.Unlock()
		synctest.Wait()
		writes := conn.writes
		cancel()
		<-done
		if writes != 2 {
			t.Fatalf("settings plus one ready batch: got %d writes, want 2", writes)
		}
		reader := http2.NewFramer(nil, &conn.Buffer)
		if _, err := reader.ReadFrame(); err != nil {
			t.Fatal(err)
		}
		for i := range 8 {
			frame, err := reader.ReadFrame()
			if err != nil {
				t.Fatal(err)
			}
			ping, ok := frame.(*http2.PingFrame)
			if !ok || !ping.IsAck() || ping.Data[0] != byte(i) {
				t.Fatalf("out-of-order control %d: %v", i, frame)
			}
		}
	})
}

func BenchmarkWakeControlBatch(b *testing.B) {
	for _, count := range []int{1, 8} {
		for _, combined := range []bool{false, true} {
			b.Run(strconv.Itoa(count)+"/combined="+strconv.FormatBool(combined), func(b *testing.B) {
				q, _ := newDataQueue(1, 32, func([]byte) {})
				m, _ := newControlMailbox(16)
				var wire flushCounter
				wire.Grow(4096)
				w := newControlWriter(newDataWriter(q, &wire, 4096))
				b.ReportAllocs()
				for b.Loop() {
					wire.Reset()
					for range count {
						_ = m.put(controlCommand{kind: pingCommand})
					}
					command, _, _ := m.poll()
					if combined {
						if _, err := w.writeCommands(m, command, true); err != nil {
							b.Fatal(err)
						}
					} else {
						if err := w.apply(command); err != nil {
							b.Fatal(err)
						}
						if err := w.flushReadyData(); err != nil {
							b.Fatal(err)
						}
						if count > 1 {
							if _, err := w.writeReady(m); err != nil {
								b.Fatal(err)
							}
						}
					}
				}
				b.ReportMetric(float64(wire.writes)/float64(b.N), "writes/op")
			})
		}
	}
}

func TestTaskDeadlineMatchesFullScan(t *testing.T) {
	for _, count := range []int{1, 8, 128} {
		c := &candidateConnection{tasks: make(map[uint32]*requestTask)}
		for i := range count {
			c.tasks[uint32(i)] = &requestTask{}
		}
		for i := range 1000 {
			next := time.Unix(int64(i%17), 0)
			if i%5 == 0 {
				next = time.Time{}
			}
			c.replaceTaskDeadline(c.tasks[uint32(i%count)], next)
			got := c.deadline
			c.refreshDeadline()
			if !got.Equal(c.deadline) {
				t.Fatalf("tasks=%d step=%d got=%v want=%v", count, i, got, c.deadline)
			}
		}
	}
}

func BenchmarkTaskDeadlineMinimum(b *testing.B) {
	for _, optimized := range []bool{false, true} {
		name := "scan"
		if optimized {
			name = "conditional"
		}
		b.Run(name, func(b *testing.B) {
			base := time.Unix(100, 0)
			c := &candidateConnection{tasks: make(map[uint32]*requestTask)}
			for i := range 8 {
				c.tasks[uint32(i)] = &requestTask{deadline: base.Add(time.Duration(i))}
			}
			task := c.tasks[0]
			next := base.Add(time.Second)
			b.ReportAllocs()
			for b.Loop() {
				task.deadline, c.deadline = base, base
				if optimized {
					c.replaceTaskDeadline(task, next)
				} else {
					task.deadline = next
					c.refreshDeadline()
				}
			}
		})
	}
}

func BenchmarkTaskDeadlineReplacement(b *testing.B) {
	for _, count := range []int{1, 8, 128} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			for _, optimized := range []bool{false, true} {
				name := "scan"
				if optimized {
					name = "conditional"
				}
				b.Run(name, func(b *testing.B) {
					base := time.Unix(100, 0)
					c := &candidateConnection{tasks: make(map[uint32]*requestTask)}
					for i := 0; i < count; i++ {
						c.tasks[uint32(i)] = &requestTask{deadline: base.Add(time.Duration(i))}
					}
					c.refreshDeadline()
					task := c.tasks[uint32(count-1)]
					next := base.Add(time.Second)
					b.ReportAllocs()
					for b.Loop() {
						next = next.Add(time.Nanosecond)
						if optimized {
							c.replaceTaskDeadline(task, next)
						} else {
							task.deadline = next
							c.refreshDeadline()
						}
					}
				})
			}
		})
	}
}
