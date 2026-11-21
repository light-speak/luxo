package codec

import (
	"strconv"
	"testing"
)

func BenchmarkArrayHeaderWrite(b *testing.B) {
	for _, count := range []int{0, 128, MaxArrayElements, MaxArrayElements + 1, -1} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			encoder := NewEncoder(make([]byte, 0, 16))
			b.ReportAllocs()
			for b.Loop() {
				encoder.Reset()
				metadataError = encoder.WriteArrayHeader(count)
				metadataOffset = len(encoder.Bytes())
			}
		})
	}
}
