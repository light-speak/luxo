package event

import "testing"

func BenchmarkChanBusEmptyLifecycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if err := NewChanBus(1).Close(); err != nil {
			b.Fatal(err)
		}
	}
}
