package cache

import (
	"testing"
	"time"
)

func BenchmarkMemoryCacheRead(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
		ttl  time.Duration
	}{
		{"Hit32", 32, time.Hour},
		{"Hit4096", 4096, time.Hour},
		{"Miss", -1, time.Hour},
		{"Expired", 32, time.Nanosecond},
	} {
		b.Run(sample.name, func(b *testing.B) {
			c := NewMemory()
			defer c.Close()
			if sample.size >= 0 {
				if err := c.Set(b.Context(), "key", make([]byte, sample.size), sample.ttl); err != nil {
					b.Fatal(err)
				}
			}
			if sample.name == "Expired" {
				time.Sleep(time.Millisecond)
			}
			want := sample.size
			if sample.name == "Expired" {
				want = 0
			}
			b.ReportAllocs()
			for b.Loop() {
				value, err := c.Get(b.Context(), "key")
				if err != nil || (want > 0 && len(value) != want) || (want <= 0 && value != nil) {
					b.Fatal("cache result changed", len(value), err)
				}
			}
		})
	}
}

func BenchmarkMemoryCacheWrite(b *testing.B) {
	for _, size := range []int{32, 4096} {
		name := "32"
		if size == 4096 {
			name = "4096"
		}
		b.Run(name, func(b *testing.B) {
			c := NewMemory()
			defer c.Close()
			value := make([]byte, size)
			b.ReportAllocs()
			for b.Loop() {
				if err := c.Set(b.Context(), "key", value, time.Hour); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryCacheRemove(b *testing.B) {
	for _, prefix := range []bool{false, true} {
		name := "Del"
		if prefix {
			name = "Invalidate"
		}
		b.Run(name, func(b *testing.B) {
			c := NewMemory()
			defer c.Close()
			if err := c.Set(b.Context(), "key", []byte{1}, time.Hour); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := c.Set(b.Context(), "key", []byte{1}, time.Hour); err != nil {
					b.Fatal(err)
				}
				var err error
				if prefix {
					err = c.Invalidate(b.Context(), "k")
				} else {
					err = c.Del(b.Context(), "key")
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryCacheClosedRead(b *testing.B) {
	c := NewMemory()
	if err := c.Set(b.Context(), "key", make([]byte, 32), time.Hour); err != nil {
		b.Fatal(err)
	}
	if err := c.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		value, err := c.Get(b.Context(), "key")
		if err == nil {
			if len(value) != 32 {
				b.Fatal("closed-read baseline returned an unexpected value", value)
			}
		} else if value != nil {
			b.Fatal("failed closed read returned data", value, err)
		}
	}
}

// Construction includes cleanup workers that may outlive the timed Close.
// Run it alone; this does not measure fully drained allocation counts.
func BenchmarkMemoryCacheCreateClose(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c := NewMemory()
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// Isolate the real Close operation and container allocation from asynchronous
// cleanup-worker creation. This is not a complete NewMemory lifecycle sample.
func BenchmarkMemoryCacheContainerClose(b *testing.B) {
	value := []byte("private cached value")
	b.ReportAllocs()
	for b.Loop() {
		c := &MemoryCache{items: map[string]entry{"key": {data: value}}, closeCh: make(chan struct{})}
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryCacheRepeatedClose(b *testing.B) {
	c := NewMemory()
	if err := c.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
