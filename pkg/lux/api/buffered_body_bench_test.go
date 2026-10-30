package api

import (
	"bytes"
	"io"
	"testing"
)

func BenchmarkTransportBody(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
	}{{"small", 32}, {"4KiB", 4096}, {"64KiB", 65536}} {
		b.Run(sample.name, func(b *testing.B) {
			data := make([]byte, sample.size)
			for _, mode := range []string{"previous", "ordinary", "borrowed"} {
				b.Run(mode, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						var body []byte
						var bp *[]byte
						var err error
						switch mode {
						case "previous":
							body, bp, err = readBody(io.NopCloser(bytes.NewReader(data)))
						case "ordinary":
							body, bp, err = readTransportBody(io.NopCloser(bytes.NewReader(data)))
						case "borrowed":
							body, bp, err = readTransportBody(NewBufferedBody(data))
						}
						if err != nil || len(body) != len(data) {
							b.Fatal("read failed", err)
						}
						if bp != nil {
							putBody(bp)
						}
					}
				})
			}
		})
	}
}
