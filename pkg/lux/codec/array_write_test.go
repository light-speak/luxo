package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestArrayWritersRejectOversizedCountBeforeWriting(t *testing.T) {
	for _, sample := range []struct {
		name  string
		write func(*Encoder) error
	}{
		{"Int", func(e *Encoder) error { return e.WriteFieldIntArray(1, make([]int64, MaxArrayElements+1)) }},
		{"Float", func(e *Encoder) error { return e.WriteFieldFloatArray(1, make([]float64, MaxArrayElements+1)) }},
		{"String", func(e *Encoder) error { return e.WriteFieldStringArray(1, make([]string, MaxArrayElements+1)) }},
		{"Bool", func(e *Encoder) error { return e.WriteFieldBoolArray(1, make([]bool, MaxArrayElements+1)) }},
		{"Bytes", func(e *Encoder) error { return e.WriteFieldBytesArray(1, make([][]byte, MaxArrayElements+1)) }},
		{"UUID", func(e *Encoder) error { return e.WriteFieldUUIDArray(1, make([][16]byte, MaxArrayElements+1)) }},
		{"DateTime", func(e *Encoder) error { return e.WriteFieldDateTimeArray(1, make([]time.Time, MaxArrayElements+1)) }},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var enc Encoder
			enc.WriteBool(true)
			before := bytes.Clone(enc.Bytes())
			if err := sample.write(&enc); !errors.Is(err, ErrInvalidArrayCount) || !bytes.Equal(before, enc.Bytes()) {
				t.Fatal("invalid array changed output", err)
			}
		})
	}
}

func TestArrayHeaderWriteBounds(t *testing.T) {
	for _, count := range []int{-1, -int(^uint(0)>>1) - 1, MaxArrayElements + 1, int(^uint(0) >> 1)} {
		backing := [3]byte{42, 0xa5, 0x5a}
		original := backing
		prefix := backing[:1]
		actual, err := AppendArrayHeader(prefix, count)
		if err != ErrInvalidArrayCount || len(actual) != len(prefix) || cap(actual) != cap(prefix) ||
			&actual[0] != &prefix[0] || backing != original {
			t.Fatal("invalid header changed output", count, actual, err)
		}
		encoder := NewEncoder(prefix)
		err = encoder.WriteArrayHeader(count)
		actual = encoder.Bytes()
		if err != ErrInvalidArrayCount || len(actual) != len(prefix) || cap(actual) != cap(prefix) ||
			&actual[0] != &prefix[0] || backing != original {
			t.Fatal("invalid encoder header changed output", count, actual, err)
		}
	}
	for _, count := range []int{0, 1, 127, 128, MaxArrayElements} {
		actual, err := AppendArrayHeader(nil, count)
		got, consumed := ReadArrayHeader(actual, 0)
		if err != nil || got != count || consumed != len(actual) {
			t.Fatal("valid count changed", count, got, err)
		}
	}
}

func TestArrayHeaderWriteCapacity(t *testing.T) {
	writers := []struct {
		name  string
		write func([]byte, int) ([]byte, error)
	}{
		{"append", AppendArrayHeader},
		{"encoder", func(dst []byte, count int) ([]byte, error) {
			encoder := NewEncoder(dst)
			err := encoder.WriteArrayHeader(count)
			return encoder.Bytes(), err
		}},
	}
	reference := func(dst []byte, count int) ([]byte, error) {
		return AppendVarint(dst, uint64(count)), nil
	}
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			testArrayHeaderWriteCapacity(t, writer.write, reference)
		})
	}
}

// Both writers cross the same call boundary so empty-buffer growth has the same escape context.
func testArrayHeaderWriteCapacity(t *testing.T, write, reference func([]byte, int) ([]byte, error)) {
	t.Helper()
	for _, prefixLength := range []int{0, 1, 2, 7, 8, 255, 256, 1024} {
		for _, count := range []int{0, 1, 127, 128, 129, 16383, 16384, 16385, MaxArrayElements - 1, MaxArrayElements} {
			want := binary.AppendUvarint(bytes.Repeat([]byte{0xa5}, prefixLength), uint64(count))
			for spare := 0; spare <= len(want)-prefixLength+1; spare++ {
				original := bytes.Repeat([]byte{0xa5}, prefixLength+spare)
				referenceBacking := bytes.Clone(original)
				expected, referenceErr := reference(referenceBacking[:prefixLength:len(referenceBacking)], count)
				got, err := write(original[:prefixLength:len(original)], count)
				reused, referenceReused := false, false
				if len(original) != 0 {
					reused = &got[0] == &original[0]
					referenceReused = &expected[0] == &referenceBacking[0]
				}
				if err != nil || referenceErr != nil || !bytes.Equal(got, want) || cap(got) != cap(expected) ||
					reused != referenceReused || !bytes.Equal(original, referenceBacking) {
					t.Fatalf("prefix %d, count %d, spare %d: got %x with capacity %d, want %x with capacity %d, error %v",
						prefixLength, count, spare, got, cap(got), want, cap(expected), err)
				}
			}
		}
	}
}

func TestArrayHeaderRejectsEmptyDestinationsWithoutMutation(t *testing.T) {
	for _, write := range []func([]byte, int) ([]byte, error){
		AppendArrayHeader,
		func(dst []byte, count int) ([]byte, error) {
			encoder := NewEncoder(dst)
			err := encoder.WriteArrayHeader(count)
			return encoder.Bytes(), err
		},
	} {
		for _, count := range []int{-1, -int(^uint(0)>>1) - 1, MaxArrayElements + 1, int(^uint(0) >> 1)} {
			for _, destination := range [][]byte{nil, {}, make([]byte, 0, 4)} {
				backing := destination[:cap(destination)]
				for index := range backing {
					backing[index] = 0xa5
				}
				before := bytes.Clone(backing)
				got, err := write(destination, count)
				if err != ErrInvalidArrayCount || len(got) != len(destination) || cap(got) != cap(destination) ||
					(got == nil) != (destination == nil) || !bytes.Equal(backing, before) {
					t.Fatal("invalid count replaced or changed an empty destination", count, got, err)
				}
				if cap(got) != 0 && &got[:cap(got)][0] != &backing[0] {
					t.Fatal("invalid count replaced the original backing array", count)
				}
			}
		}
	}
}

func TestArrayHeaderPreallocatedWritesDoNotAllocate(t *testing.T) {
	for _, count := range []int{0, 127, 128, 16383, 16384, MaxArrayElements} {
		buffer := make([]byte, 0, 16)
		var got []byte
		var failure error
		allocations := testing.AllocsPerRun(100, func() {
			got, failure = AppendArrayHeader(buffer[:0], count)
		})
		if allocations != 0 || failure != nil || len(got) == 0 {
			t.Fatal("preallocated append allocated or failed", count, allocations, failure)
		}
		encoder := NewEncoder(buffer[:0])
		allocations = testing.AllocsPerRun(100, func() {
			encoder.Reset()
			failure = encoder.WriteArrayHeader(count)
		})
		if allocations != 0 || failure != nil || len(encoder.Bytes()) == 0 {
			t.Fatal("preallocated encoder allocated or failed", count, allocations, failure)
		}
	}
}
