package api

import (
	"fmt"
	"testing"
)

// Freeze the old read-loop-only storage, which cannot safely remove entries from
// completing writers. It is not an equivalent correct implementation.
type connectionEntryBaseline struct {
	name string
	sub  *StreamSub
}

type connectionSliceBaseline struct {
	items []connectionEntryBaseline
}

func (s *connectionSliceBaseline) has(name string) bool {
	for _, item := range s.items {
		if item.name == name {
			return true
		}
	}
	return false
}

func (s *connectionSliceBaseline) add(name string, sub *StreamSub) {
	s.items = append(s.items, connectionEntryBaseline{name, sub})
}

func (s *connectionSliceBaseline) remove(name string, sub *StreamSub) {
	for i, item := range s.items {
		if item.name == name && item.sub == sub {
			copy(s.items[i:], s.items[i+1:])
			s.items[len(s.items)-1] = connectionEntryBaseline{}
			s.items = s.items[:len(s.items)-1]
			return
		}
	}
}

func BenchmarkWSConnectionRegistrations(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		for _, impl := range []string{"former", "checked"} {
			b.Run(fmt.Sprintf("size=%d/%s", count, impl), func(b *testing.B) {
				benchmarkConnectionRegistrations(b, count, impl)
			})
		}
	}
}

func benchmarkConnectionRegistrations(b *testing.B, count int, impl string) {
	var checked connectionSubscriptions
	var slice connectionSliceBaseline
	sub := &StreamSub{}
	for i := range count - 1 {
		name := fmt.Sprintf("watch%d", i)
		checked.add(uint64(i), name, sub)
		slice.items = append(slice.items, connectionEntryBaseline{name, sub})
	}
	// Reuse established connection capacity, not the first-subscription allocation.
	id := uint64(count)
	checked.add(id, "target", sub)
	checked.remove(id, sub)
	slice.add("target", sub)
	slice.remove("target", sub)
	b.ReportAllocs()
	for b.Loop() {
		if impl == "checked" {
			id++
			if !checked.reserve(id) {
				b.Fatal("benchmark subscription already present")
			}
			checked.add(id, "target", sub)
			checked.remove(id, sub)
		} else {
			if slice.has("target") {
				b.Fatal("benchmark subscription already present")
			}
			slice.add("target", sub)
			slice.remove("target", sub)
		}
	}
}

var connectionRegistrySink *connectionSubscriptions
var connectionBaselineSink *connectionSliceBaseline

func BenchmarkWSConnectionRegistrationCold(b *testing.B) {
	for _, checked := range []bool{false, true} {
		b.Run(fmt.Sprint(checked), func(b *testing.B) {
			sub := &StreamSub{}
			b.ReportAllocs()
			for b.Loop() {
				// Both containers escape. This measures storage only, not complete
				// WebSocket setup or the old handler's compiler escape decisions.
				if checked {
					items := &connectionSubscriptions{}
					items.add(1, "watch", sub)
					items.remove(1, sub)
					connectionRegistrySink = items
				} else {
					items := &connectionSliceBaseline{}
					items.add("watch", sub)
					items.remove("watch", sub)
					connectionBaselineSink = items
				}
			}
		})
	}
}
