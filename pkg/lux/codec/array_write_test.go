package codec

import (
	"bytes"
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
		prefix := []byte{42}
		actual, err := AppendArrayHeader(prefix, count)
		if !errors.Is(err, ErrInvalidArrayCount) || !bytes.Equal(actual, prefix) {
			t.Fatal("invalid header changed output", count, actual, err)
		}
		var encoder Encoder
		encoder.WriteBool(true)
		if err := encoder.WriteArrayHeader(count); !errors.Is(err, ErrInvalidArrayCount) || !bytes.Equal(encoder.Bytes(), []byte{1}) {
			t.Fatal("invalid encoder header changed output", count, err)
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
