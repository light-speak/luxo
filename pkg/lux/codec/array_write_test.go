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
	const prefixLength = 2
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			for _, count := range []int{0, 127, 128, 16383, 16384, MaxArrayElements} {
				want := binary.AppendUvarint([]byte{0xa5, 0xa5}, uint64(count))
				for spare := 0; spare <= len(want)-prefixLength+1; spare++ {
					original := bytes.Repeat([]byte{0xa5}, prefixLength+spare)
					referenceBacking := make([]byte, len(original))
					copy(referenceBacking, original)
					reference := AppendVarint(referenceBacking[:prefixLength], uint64(count))

					got, err := writer.write(original[:prefixLength:len(original)], count)
					if err != nil || !bytes.Equal(got, want) || cap(got) != cap(reference) ||
						(&got[0] == &original[0]) != (&reference[0] == &referenceBacking[0]) ||
						!bytes.Equal(original, referenceBacking) {
						t.Fatalf("count %d, spare %d: got %x with capacity %d, want %x with capacity %d, error %v",
							count, spare, got, cap(got), want, cap(reference), err)
					}
				}
			}
		})
	}
}
