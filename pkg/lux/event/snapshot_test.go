package event

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

var errProbeSnapshot = errors.New("rejected snapshot")

func TestImmutableSnapshotOwnsValue(t *testing.T) {
	value := struct {
		id   int64
		name string
	}{42, "immutable"}
	want := value
	snapshot := NewImmutableSnapshot(value)
	value.id = 99
	if snapshot.takePayload() != want || snapshot.copyPayload() != want {
		t.Fatal("immutable snapshot retained the publisher's mutable variable")
	}
}

// Used only with immutable scalars or immutable test structs, never containers.
type immutableTestSource[T any] struct{ value T }

func immutablePayload[T any](value T) immutableTestSource[T] { return immutableTestSource[T]{value} }
func (p immutableTestSource[T]) SnapshotEvent() (Snapshot, error) {
	return NewSnapshot(p.value, func(value T) T { return value })
}

type snapshotProbe struct {
	data   []byte
	reject bool
	copies *atomic.Int32
}

func (p *snapshotProbe) SnapshotEvent() (Snapshot, error) {
	if p == nil || p.reject {
		return nil, errProbeSnapshot
	}
	owned := *p
	owned.data = bytes.Clone(p.data)
	return NewSnapshot(owned, func(value snapshotProbe) snapshotProbe {
		if value.copies != nil {
			value.copies.Add(1)
		}
		value.data = bytes.Clone(value.data)
		return value
	})
}

func TestChanBusRejectsInvalidSnapshotBeforeDelivery(t *testing.T) {
	for _, subscribed := range []bool{false, true} {
		bus := NewChanBus(1)
		called := false
		if subscribed {
			bus.On("probe", func(context.Context, any) error { called = true; return nil })
		}
		for _, payload := range []*snapshotProbe{nil, {reject: true}} {
			if err := bus.Emit(t.Context(), "probe", payload); !errors.Is(err, errProbeSnapshot) {
				t.Errorf("subscribed=%t: invalid snapshot accepted: %v", subscribed, err)
			}
		}
		bus.Close()
		if called {
			t.Fatal("rejected event reached a listener")
		}
	}
}

func TestChanBusSnapshotOwnership(t *testing.T) {
	bus := NewChanBus(1)
	gate := make(chan struct{})
	var first, second snapshotProbe
	if err := bus.On("probe", func(_ context.Context, raw any) error {
		<-gate
		first = raw.(snapshotProbe)
		first.data[0] = 99
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.On("probe", func(_ context.Context, raw any) error {
		second = raw.(snapshotProbe)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var copies atomic.Int32
	source := &snapshotProbe{data: []byte{42}, copies: &copies}
	if err := bus.Emit(t.Context(), "probe", source); err != nil {
		t.Fatal(err)
	}
	source.data[0] = 7
	close(gate)
	bus.Close()
	if first.data[0] != 99 || second.data[0] != 42 || copies.Load() != 1 {
		t.Fatalf("shared payload or redundant copy: first=%v second=%v copies=%d", first.data, second.data, copies.Load())
	}
	first.data[0] = 8
	if second.data[0] != 42 || source.data[0] != 7 {
		t.Fatal("retained listener payload aliases another owner")
	}
}

func TestChanBusSingleListenerTransfersSnapshot(t *testing.T) {
	bus := NewChanBus(1)
	var copies atomic.Int32
	bus.On("probe", func(_ context.Context, raw any) error {
		if raw.(snapshotProbe).data[0] != 42 {
			t.Error("unexpected payload")
		}
		return nil
	})
	if err := bus.Emit(t.Context(), "probe", &snapshotProbe{data: []byte{42}, copies: &copies}); err != nil {
		t.Fatal(err)
	}
	bus.Close()
	if copies.Load() != 0 {
		t.Fatal("single listener requires no second copy")
	}
}

func TestSnapshotRequiresCloneContract(t *testing.T) {
	if owned, err := NewSnapshot(42, nil); owned != nil || !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("missing clone accepted: %v %v", owned, err)
	}
}

var snapshotTransferSink any

func BenchmarkSnapshotTransferShapes(b *testing.B) {
	benchmarkSnapshotTransfer(b, "int", int64(9223372036854775807), func(value int64) int64 { return value })
	benchmarkSnapshotTransfer(b, "string", "snapshot", func(value string) string { return value })
	value := 42
	benchmarkSnapshotTransfer(b, "pointer", &value, func(value *int) *int { return value })
	benchmarkSnapshotTransfer(b, "wide", [16]int64{42}, func(value [16]int64) [16]int64 { return value })
	benchmarkSnapshotTransfer[any](b, "nil", nil, func(value any) any { return value })
}

func benchmarkSnapshotTransfer[T any](b *testing.B, name string, value T, clone func(T) T) {
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			snapshot, err := NewSnapshot(value, clone)
			if err != nil {
				b.Fatal(err)
			}
			snapshotTransferSink = snapshot.takePayload()
		}
		snapshotTransferSink = nil
	})
}

func TestSnapshotPreservesNilInterfaceValue(t *testing.T) {
	owned, err := NewSnapshot[any](nil, func(value any) any {
		if value != nil {
			t.Fatal("nil interface changed during copying")
		}
		return "copied"
	})
	if err != nil || owned.takePayload() != nil {
		t.Fatalf("nil interface changed during ownership transfer: %v", err)
	}
	if owned.copyPayload() != "copied" {
		t.Fatal("nil interface bypassed the supplied clone")
	}
}

func TestLargeSnapshotFinalListenerDoesNotReboxPayload(t *testing.T) {
	owned, err := NewSnapshot(snapshotProbe{data: []byte{42}}, func(value snapshotProbe) snapshotProbe {
		value.data = bytes.Clone(value.data)
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		snapshotTransferSink = owned.takePayload()
	})
	defer func() { snapshotTransferSink = nil }()
	if allocations != 0 {
		t.Fatalf("final listener boxed an already owned value: %g allocations", allocations)
	}
	copy := owned.copyPayload().(snapshotProbe)
	copy.data[0] = 99
	if snapshotTransferSink.(snapshotProbe).data[0] != 42 {
		t.Fatal("earlier listener's clone aliases the final listener's value")
	}
}

func TestSnapshotRetainsInlineStorageForSmallValues(t *testing.T) {
	assertInlineSnapshot(t, int64(9223372036854775807))
	assertInlineSnapshot(t, "snapshot")
	assertInlineSnapshot(t, new(int))
	assertInlineSnapshot[any](t, nil)
}

func assertInlineSnapshot[T any](t *testing.T, value T) {
	t.Helper()
	owned, err := NewSnapshot(value, func(value T) T { return value })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := owned.(inlineSnapshot[T]); !ok {
		t.Fatal("small snapshot unnecessarily uses a larger boxed wrapper")
	}
}

type emptySnapshotSource struct{}

func (emptySnapshotSource) SnapshotEvent() (Snapshot, error) { return nil, nil }

func TestChanBusRejectsMissingSnapshotContract(t *testing.T) {
	bus := NewChanBus(1)
	defer bus.Close()
	for _, payload := range []any{nil, map[string]int{"id": 1}, "untyped", emptySnapshotSource{}} {
		if err := bus.Emit(t.Context(), "probe", payload); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("unsafe payload accepted: %T %v", payload, err)
		}
	}
}

func TestChanBusOwnsWireBytes(t *testing.T) {
	bus := NewChanBus(1)
	gate := make(chan struct{})
	var got []byte
	bus.On("wire", func(_ context.Context, raw any) error { <-gate; raw.([]byte)[0] = 99; return nil })
	bus.On("wire", func(_ context.Context, raw any) error { got = raw.([]byte); return nil })
	source := []byte{42}
	if err := bus.Emit(t.Context(), "wire", source); err != nil {
		t.Fatal(err)
	}
	source[0] = 7
	close(gate)
	bus.Close()
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("wire alias: %v", got)
	}
}
