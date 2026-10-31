package codec

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestFiniteFloatBenchmarkParity(t *testing.T) {
	values := []float64{1.25, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Copysign(0, -1)}
	var old, checked Encoder
	for index, value := range values {
		old.writeFieldFloatBaseline(index+1, value)
		if err := checked.WriteFieldFloat(index+1, value); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(old.Bytes(), checked.Bytes()) {
		t.Fatal("field benchmark bytes differ")
	}
	old.Reset()
	checked.Reset()
	old.writeFieldFloatArrayBaseline(1, values)
	if err := checked.WriteFieldFloatArray(1, values); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old.Bytes(), checked.Bytes()) {
		t.Fatal("array benchmark bytes differ")
	}
}

// Keep the former unchecked implementation only as a performance baseline.
func BenchmarkFiniteFloatFields(b *testing.B) {
	for _, count := range []int{1, 128} {
		name := "single"
		if count == 128 {
			name = "128-fields"
		}
		b.Run(name+"/old", func(b *testing.B) { benchmarkFloatFields(b, count, false) })
		b.Run(name+"/checked", func(b *testing.B) { benchmarkFloatFields(b, count, true) })
	}
}

func benchmarkFloatFields(b *testing.B, count int, checked bool) {
	values := []float64{1.25, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Copysign(0, -1)}
	encoder := Encoder{buf: make([]byte, 0, count*10)}
	b.ReportAllocs()
	for b.Loop() {
		encoder.Reset()
		for index := range count {
			value := values[index%len(values)]
			if checked {
				if err := encoder.WriteFieldFloat(index+1, value); err != nil {
					b.Fatal(err)
				}
			} else {
				encoder.writeFieldFloatBaseline(index+1, value)
			}
		}
	}
}

func BenchmarkFiniteFloatArray(b *testing.B) {
	values := make([]float64, 128)
	for index := range values {
		values[index] = float64(index) + 0.25
	}
	for _, checked := range []bool{false, true} {
		name := "old"
		if checked {
			name = "checked"
		}
		b.Run(name, func(b *testing.B) {
			encoder := Encoder{buf: make([]byte, 0, 2048)}
			b.ReportAllocs()
			for b.Loop() {
				encoder.Reset()
				if checked {
					if err := encoder.WriteFieldFloatArray(1, values); err != nil {
						b.Fatal(err)
					}
				} else {
					encoder.writeFieldFloatArrayBaseline(1, values)
				}
			}
		})
	}
}

func (e *Encoder) writeFieldFloatBaseline(fieldID int, v float64) {
	e.buf = AppendVarint(e.buf, uint64(fieldID))
	e.buf = appendFixed64Baseline(e.buf, v)
}

func (e *Encoder) writeFieldFloatArrayBaseline(fieldID int, v []float64) {
	e.buf = AppendVarint(e.buf, uint64(fieldID))
	e.buf = AppendArrayHeader(e.buf, len(v))
	for _, item := range v {
		e.buf = appendFixed64Baseline(e.buf, item)
	}
}

func appendFixed64Baseline(dst []byte, v float64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
	return append(dst, buf[:]...)
}
