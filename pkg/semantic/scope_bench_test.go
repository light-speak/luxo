package semantic

import (
	"fmt"
	"testing"
)

var scopeLookupBenchmarkSink *Symbol

func BenchmarkScopeLookup(b *testing.B) {
	benchmarkScopeLookup(b, false)
}

func BenchmarkScopeLookupReadOnly(b *testing.B) {
	benchmarkScopeLookup(b, true)
}

func benchmarkScopeLookup(b *testing.B, readOnly bool) {
	for _, depth := range []int{0, 4, 32} {
		root := NewScope()
		root.Define(&Symbol{Name: "parent", Kind: SymVariable})
		leaf := root
		for range depth {
			leaf = leaf.Child()
		}
		leaf.Define(&Symbol{Name: "local", Kind: SymVariable})
		for _, name := range []string{"local", "parent", "missing"} {
			b.Run(fmt.Sprintf("depth%d/%s", depth, name), func(b *testing.B) {
				b.ReportAllocs()
				if readOnly {
					for b.Loop() {
						scopeLookupBenchmarkSink = leaf.LookupReadOnly(name)
					}
				} else {
					for b.Loop() {
						scopeLookupBenchmarkSink = leaf.Lookup(name)
					}
				}
			})
		}
	}
}
