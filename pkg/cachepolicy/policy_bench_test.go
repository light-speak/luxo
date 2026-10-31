package cachepolicy

import (
	"fmt"
	"strings"
	"testing"
)

// These are absolute build-time measurements, not a request-path comparison.
func BenchmarkParse(b *testing.B) {
	for _, count := range []int{1, 128} {
		var source strings.Builder
		source.WriteString(`{"version":1,"policies":{`)
		for i := range count {
			if i != 0 {
				source.WriteByte(',')
			}
			fmt.Fprintf(&source, `"p%d":{"mode":"ttl","ttl":"5m","scope":"private"}`, i)
		}
		source.WriteString(`}}`)
		raw := []byte(source.String())
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				catalog, err := Parse(raw)
				if err != nil {
					b.Fatal(err)
				}
				if _, ok := catalog.Lookup("p0"); !ok {
					b.Fatal("missing compiled policy")
				}
			}
		})
	}
}
