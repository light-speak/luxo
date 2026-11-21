package api

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
)

type bodyReadResult struct {
	data []byte
	err  error
}

type invalidBodyReader int

func (r invalidBodyReader) Read(p []byte) (int, error) {
	if r < 0 {
		return -1, nil
	}
	return len(p) + 1, nil
}

func TestBodyBufferInvalidCounts(t *testing.T) {
	for _, r := range []invalidBodyReader{-1, 1} {
		if _, err := readBodyBuffer(r, nil, 16); err == nil {
			t.Fatal("invalid count accepted")
		}
	}
}

// BenchmarkBodyRead compares the previous pooled implementation with direct reads.
func BenchmarkBodyRead(b *testing.B) {
	benchmarkBodyRead(b, false)
}

func BenchmarkBodyReadReverse(b *testing.B) {
	benchmarkBodyRead(b, true)
}

func benchmarkBodyRead(b *testing.B, reverse bool) {
	for _, size := range []int{0, 128, 4096, 4097, 65536} {
		data := bytes.Repeat([]byte{17}, size)
		// Both warm paths copy into the same backing storage to avoid address-dependent copy bias.
		storage := make([]byte, 0, max(4096, size*2))
		for _, direct := range []bool{reverse, !reverse} {
			name := "previous"
			if direct {
				name = "direct"
			}
			b.Run(strconv.Itoa(size)+"/"+name, func(b *testing.B) {
				pool := sync.Pool{New: func() any { buf := storage[:0]; return &buf }}
				reader := bytes.NewReader(data)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					reader.Reset(data)
					bp := pool.Get().(*[]byte)
					if direct {
						var err error
						*bp, err = readBodyBuffer(reader, (*bp)[:0], MaxRequestBodySize)
						if err != nil {
							b.Fatal(err)
						}
					} else {
						buf := bytes.NewBuffer((*bp)[:0])
						if _, err := buf.ReadFrom(io.LimitReader(reader, MaxRequestBodySize+1)); err != nil {
							b.Fatal(err)
						}
						*bp = buf.Bytes()
					}
					pool.Put(bp)
				}
			})
		}
	}
}

type bodyReadScript struct{ steps []bodyReadResult }

func (r *bodyReadScript) Read(p []byte) (int, error) {
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	s := r.steps[0]
	r.steps = r.steps[1:]
	return copy(p, s.data), s.err
}

func TestBodyBufferBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 4095, 4096, 4097, 65536} {
		want := bytes.Repeat([]byte{17}, size)
		got, err := readBodyBuffer(bytes.NewReader(want), make([]byte, 0, 4096), size)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("size %d: %v", size, err)
		}
		if _, err = readBodyBuffer(bytes.NewReader(append(want, 1)), nil, size); err == nil {
			t.Fatalf("limit %d exceeded", size)
		}
	}
}

func TestBodyBufferPreservesAllocationCapacity(t *testing.T) {
	buf := make([]byte, 0, 8192)
	got, err := readBodyBuffer(bytes.NewReader([]byte("abc")), buf, 3)
	// Keep the allocation visible to the next pool borrower's memory budget.
	// The writable length, not the slice capacity, bounds reader input.
	if err != nil || string(got) != "abc" || cap(got) != cap(buf) {
		t.Fatalf("length=%d capacity=%d error=%v", len(got), cap(got), err)
	}
}

func TestBodyBufferReaderResults(t *testing.T) {
	failure := errors.New("read failed")
	for _, sample := range []struct {
		steps []bodyReadResult
		want  error
	}{
		{[]bodyReadResult{{[]byte("abc"), io.EOF}}, nil},
		{[]bodyReadResult{{[]byte("abc"), failure}}, failure},
		{make([]bodyReadResult, 100), io.ErrNoProgress},
		{[]bodyReadResult{{nil, nil}, {[]byte("abc"), nil}, {nil, io.EOF}}, nil},
	} {
		_, err := readBodyBuffer(&bodyReadScript{steps: sample.steps}, nil, 16)
		if !errors.Is(err, sample.want) {
			t.Fatalf("error %v, want %v", err, sample.want)
		}
	}
}
