package rpc

import (
	"context"
	"runtime"
	"testing"
)

var poolBorrowErrorSink error

func BenchmarkRPCPoolClosedBorrow(b *testing.B) {
	pool := NewPool("unused", 1)
	pool.Close()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		conn, err := pool.GetContext(ctx)
		if conn != nil || err == nil {
			b.Fatal("closed pool accepted a borrow")
		}
		poolBorrowErrorSink = err
	}
}

// Both open pools perform the same borrow/return operations without dialing.
// Calls stay concrete so the benchmark does not add interface dispatch.
func BenchmarkRPCPoolCloseSafety(b *testing.B) {
	b.Run("serial/before", benchmarkPoolSerialBefore)
	b.Run("serial/after", benchmarkPoolSerialAfter)
	b.Run("parallel/before", benchmarkPoolParallelBefore)
	b.Run("parallel/after", benchmarkPoolParallelAfter)
}

// Reverse execution order without changing either measured implementation.
func BenchmarkRPCPoolCloseSafetyReverse(b *testing.B) {
	b.Run("serial/after", benchmarkPoolSerialAfter)
	b.Run("serial/before", benchmarkPoolSerialBefore)
	b.Run("parallel/after", benchmarkPoolParallelAfter)
	b.Run("parallel/before", benchmarkPoolParallelBefore)
}

// These controls measure pool bookkeeping only, not OS socket closing. Reusing
// a counting connection removes socket creation from each timed iteration.
func BenchmarkRPCPoolReturnRejection(b *testing.B) {
	for _, closed := range []bool{false, true} {
		name := "full"
		if closed {
			name = "closed"
		}
		b.Run(name+"/before", func(b *testing.B) {
			pool := newPoolBeforeConcurrentClose("unused", 1)
			pool.Put(&poolLifecycleConn{})
			b.Cleanup(pool.Close)
			if closed {
				pool.Close()
			}
			conn := &poolLifecycleConn{}
			b.ReportAllocs()
			for b.Loop() {
				pool.Put(conn)
			}
		})
		b.Run(name+"/after", func(b *testing.B) {
			pool := NewPool("unused", 1)
			pool.Put(&poolLifecycleConn{})
			b.Cleanup(pool.Close)
			if closed {
				pool.Close()
			}
			conn := &poolLifecycleConn{}
			b.ReportAllocs()
			for b.Loop() {
				pool.Put(conn)
			}
		})
	}
}

func benchmarkPoolSerialBefore(b *testing.B) {
	pool := newPoolBeforeConcurrentClose("unused", 1)
	pool.Put(&poolLifecycleConn{})
	b.Cleanup(pool.Close)
	b.ReportAllocs()
	for b.Loop() {
		conn, err := pool.Get()
		if err != nil {
			b.Fatal(err)
		}
		pool.Put(conn)
	}
}

func benchmarkPoolSerialAfter(b *testing.B) {
	pool := NewPool("unused", 1)
	pool.Put(&poolLifecycleConn{})
	b.Cleanup(pool.Close)
	b.ReportAllocs()
	for b.Loop() {
		conn, err := pool.Get()
		if err != nil {
			b.Fatal(err)
		}
		pool.Put(conn)
	}
}

func benchmarkPoolParallelBefore(b *testing.B) {
	workers := runtime.GOMAXPROCS(0)
	pool := newPoolBeforeConcurrentClose("unused", workers)
	for range workers {
		pool.Put(&poolLifecycleConn{})
	}
	b.Cleanup(pool.Close)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := pool.Get()
			if err != nil {
				b.Error(err)
				return
			}
			pool.Put(conn)
		}
	})
}

func benchmarkPoolParallelAfter(b *testing.B) {
	workers := runtime.GOMAXPROCS(0)
	pool := NewPool("unused", workers)
	for range workers {
		pool.Put(&poolLifecycleConn{})
	}
	b.Cleanup(pool.Close)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := pool.Get()
			if err != nil {
				b.Error(err)
				return
			}
			pool.Put(conn)
		}
	})
}
