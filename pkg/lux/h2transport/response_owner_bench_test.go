package h2transport

import (
	"net/http"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
)

func BenchmarkResponseHandoff(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
	}{{"small", 32}, {"4KiB", 4096}, {"64KiB", 65536}} {
		b.Run(sample.name, func(b *testing.B) {
			data := make([]byte, sample.size)
			for _, transfer := range []bool{false, true} {
				name := "copy"
				if transfer {
					name = "transfer"
				}
				b.Run(name, func(b *testing.B) {
					budget := &byteBudget{limit: 64 << 20}
					b.ReportAllocs()
					for b.Loop() {
						buf := api.GetBuf()
						buf.B = append(buf.B, data...)
						r := &response{header: make(http.Header), limit: candidateBody, budget: budget}
						if transfer {
							if !r.TakeBinaryResponse(buf) {
								b.Fatal("transfer failed")
							}
						} else {
							if _, err := r.Write(buf.B); err != nil {
								b.Fatal(err)
							}
							api.PutBuf(buf)
						}
						r.release()
					}
					if budget.used.Load() != 0 {
						b.Fatal("budget leak")
					}
				})
			}
		})
	}
}
