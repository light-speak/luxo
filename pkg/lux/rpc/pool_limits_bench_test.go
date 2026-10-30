package rpc

import (
	"runtime"
	"testing"
)

func BenchmarkLRPCPoolLimits(b *testing.B) {
	b.Run("serial/before", func(b *testing.B) {
		p := newPoolBeforeLimits("unused", 1)
		p.Put(&poolLifecycleConn{})
		defer p.Close()
		b.ReportAllocs()
		for b.Loop() {
			c, err := p.Get()
			if err != nil {
				b.Fatal(err)
			}
			p.Put(c)
		}
	})
	b.Run("serial/after", benchmarkPoolSerialAfter)
	b.Run("parallel/before", func(b *testing.B) {
		p := newPoolBeforeLimits("unused", runtime.GOMAXPROCS(0))
		for range runtime.GOMAXPROCS(0) {
			p.Put(&poolLifecycleConn{})
		}
		defer p.Close()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				c, err := p.Get()
				if err != nil {
					b.Error(err)
					return
				}
				p.Put(c)
			}
		})
	})
	b.Run("parallel/after", benchmarkPoolParallelAfter)
}
