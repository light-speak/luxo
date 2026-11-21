package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
)

func TestFloatWritersRejectWithoutPublishingPartialFields(t *testing.T) {
	for _, bits := range []uint64{0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000000, 0x7ff0000000000001, 0xfff0000000000001} {
		value := math.Float64frombits(bits)
		for name, write := range map[string]func(*Encoder) error{
			"value":    func(e *Encoder) error { return e.WriteFloat(value) },
			"field":    func(e *Encoder) error { return e.WriteFieldFloat(2, value) },
			"nullable": func(e *Encoder) error { return e.WriteFieldFloatPtr(2, &value) },
			"array":    func(e *Encoder) error { return e.WriteFieldFloatArray(2, []float64{1, value, 2}) },
		} {
			t.Run(name, func(t *testing.T) {
				var encoder Encoder
				encoder.WriteFieldInt(1, 42)
				before := bytes.Clone(encoder.Bytes())
				if err := write(&encoder); !errors.Is(err, ErrNonFiniteFloat) {
					t.Fatalf("expected non-finite error for %016x, got %v", bits, err)
				}
				if !bytes.Equal(before, encoder.Bytes()) {
					t.Fatalf("failed field changed the output: %x → %x", before, encoder.Bytes())
				}
				if err := encoder.WriteFieldFloat(2, 1.5); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestFloatColumnsRejectWithoutPublishingPartialColumns(t *testing.T) {
	for _, value := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		for name, write := range map[string]func(*ColumnarWriter) error{
			"values":   func(w *ColumnarWriter) error { return w.WriteColumnFloat(2, []float64{1, value}) },
			"nullable": func(w *ColumnarWriter) error { return w.WriteColumnFloatPtr(2, []*float64{nil, &value}) },
		} {
			t.Run(name, func(t *testing.T) {
				var writer ColumnarWriter
				writer.SetCount(2)
				writer.WriteColumnInt(1, []int64{1, 2})
				before := bytes.Clone(testutil.Bytes(t)(writer.Bytes()))
				if err := write(&writer); !errors.Is(err, ErrNonFiniteFloat) {
					t.Fatalf("expected non-finite error, got %v", err)
				}
				if !bytes.Equal(before, testutil.Bytes(t)(writer.Bytes())) {
					t.Fatal("failed write published a partial column")
				}
			})
		}
	}
}

func TestFloatWriterValidBoundaries(t *testing.T) {
	for _, bits := range []uint64{0, 1 << 63, 1, 1<<63 | 1, 0x000fffffffffffff, 0x0010000000000000, 0x7fefffffffffffff, 0xffefffffffffffff} {
		value := math.Float64frombits(bits)
		var encoder Encoder
		if err := encoder.WriteFloat(value); err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint64(encoder.Bytes()); got != bits {
			t.Fatalf("bits changed: %016x → %016x", bits, got)
		}
	}
	var encoder Encoder
	if err := encoder.WriteFieldFloatPtr(1, nil); err != nil {
		t.Fatal(err)
	}
	if err := encoder.WriteFieldFloatArray(2, nil); err != nil {
		t.Fatal(err)
	}
	var writer ColumnarWriter
	if err := writer.WriteColumnFloat(1, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteColumnFloatPtr(2, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFloatEncoderNoAllocation(t *testing.T) {
	encoder := Encoder{buf: make([]byte, 0, 128)}
	values := []float64{1, math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, value := range []float64{1.25, math.NaN(), math.Inf(-1)} {
		allocations := testing.AllocsPerRun(100, func() {
			encoder.Reset()
			_ = encoder.WriteFieldFloat(1, value)
			_ = encoder.WriteFieldFloatPtr(2, &value)
			_ = encoder.WriteFieldFloatArray(3, values)
		})
		if allocations != 0 {
			t.Fatalf("Float encoder allocated %v times", allocations)
		}
	}
}

func FuzzFiniteFloatWriter(f *testing.F) {
	for _, bits := range []uint64{0, 1 << 63, 1, 0x7fefffffffffffff, 0x7ff0000000000000, 0x7ff8000000000001, 0xfff0000000000000} {
		f.Add(bits)
	}
	f.Fuzz(func(t *testing.T, bits uint64) {
		var encoder Encoder
		encoder.WriteFieldInt(1, 42)
		prefix := bytes.Clone(encoder.Bytes())
		value := math.Float64frombits(bits)
		err := encoder.WriteFieldFloat(2, value)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			if !errors.Is(err, ErrNonFiniteFloat) || !bytes.Equal(prefix, encoder.Bytes()) {
				t.Fatalf("invalid write: %016x → %x, %v", bits, encoder.Bytes(), err)
			}
			return
		}
		want := append(prefix, 2)
		want = binary.LittleEndian.AppendUint64(want, bits)
		if err != nil || !bytes.Equal(want, encoder.Bytes()) {
			t.Fatalf("finite write changed bytes: %016x → %x, %v", bits, encoder.Bytes(), err)
		}
	})
}

func TestFiniteFloatWireBoundaries(t *testing.T) {
	for _, bits := range []uint64{0, 1 << 63, 1, 1<<63 | 1, 0x000fffffffffffff, 0x0010000000000000, 0x7fefffffffffffff, 0xffefffffffffffff} {
		wire := binary.LittleEndian.AppendUint64(nil, bits)
		value, n := ReadFixed64(wire, 0)
		if n != 8 || math.Float64bits(value) != bits {
			t.Fatalf("finite value lost bits: %016x → %016x (%d bytes)", bits, math.Float64bits(value), n)
		}
		if got, err := AppendFixed64(nil, value); err != nil || string(got) != string(wire) {
			t.Fatalf("finite value changed encoding: %016x", bits)
		}
	}
}

func TestAppendFixed64RejectsWithoutChangingPrefix(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		prefix := []byte{1, 2, 3}
		got, err := AppendFixed64(prefix, value)
		if !errors.Is(err, ErrNonFiniteFloat) || !bytes.Equal(got, prefix) {
			t.Fatalf("invalid Float changed output: %x, %v", got, err)
		}
	}
}

func TestNonFiniteFloatReadersReject(t *testing.T) {
	for _, bits := range []uint64{0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000000, 0x7ff0000000000001, 0xfff0000000000001, 0x7fffffffffffffff, 0xffffffffffffffff} {
		wire := binary.LittleEndian.AppendUint64(nil, bits)
		if _, n := ReadFixed64(wire, 0); n != 0 {
			t.Errorf("ReadFixed64 accepted %016x", bits)
		}
		for _, nullable := range []bool{false, true} {
			value := wire
			if nullable {
				value = append([]byte{1}, wire...)
			}
			checkNonFiniteRowFloat(t, value, nullable)
			checkNonFiniteColumnFloat(t, value, nullable)
		}
		decoder := NewDecoder(append([]byte{1}, wire...))
		decoder.ReadFloatArray()
		if decoder.Err() == nil {
			t.Errorf("array reader accepted %016x", bits)
		}
	}
}

func checkNonFiniteRowFloat(t *testing.T, wire []byte, nullable bool) {
	t.Helper()
	for _, skip := range []bool{false, true} {
		decoder := NewDecoder(wire)
		switch {
		case skip && nullable:
			decoder.SkipNullableFixed64()
		case skip:
			decoder.SkipFixed64()
		case nullable:
			decoder.ReadFloatPtr()
		default:
			decoder.ReadFloat()
		}
		if decoder.Err() == nil {
			t.Errorf("row reader accepted non-finite value: nullable=%v skip=%v", nullable, skip)
		}
	}
}

func checkNonFiniteColumnFloat(t *testing.T, wire []byte, nullable bool) {
	t.Helper()
	for _, skip := range []bool{false, true} {
		reader := NewColumnarReader(append([]byte{1, 0, 1}, wire...))
		reader.NextColumn()
		switch {
		case skip && nullable:
			reader.SkipColumnFloatPtr()
		case skip:
			reader.SkipColumnFloat()
		case nullable:
			reader.ReadColumnFloatPtr()
		default:
			reader.ReadColumnFloat()
		}
		if reader.Err() == nil {
			t.Errorf("column reader accepted non-finite value: nullable=%v skip=%v", nullable, skip)
		}
	}
}
