package codec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"testing"
)

type columnCapacityFixture struct {
	name    string
	write   func(*ColumnarWriter)
	payload []byte
}

func columnCapacityFixtures(rows int) []columnCapacityFixture {
	ints, wide := make([]int64, rows), make([]int64, rows)
	flags, ids := make([]bool, rows), make([][16]byte, rows)
	var smallData, wideData, boolData, uuidData []byte
	for i := range rows {
		ints[i], wide[i] = 42, math.MaxInt64
		if i%2 != 0 {
			wide[i] = math.MinInt64
		}
		flags[i], ids[i] = i%2 == 0, [16]byte{byte(i), 0x80, 0xff}
		smallData = AppendSvarint(smallData, ints[i])
		wideData = AppendSvarint(wideData, wide[i])
		boolData = AppendBool(boolData, flags[i])
		uuidData = AppendUUID(uuidData, ids[i])
	}
	return []columnCapacityFixture{
		{"int", func(w *ColumnarWriter) { w.WriteColumnInt(1, ints) }, smallData},
		{"wide-int", func(w *ColumnarWriter) { w.WriteColumnInt(1, wide) }, wideData},
		{"bool", func(w *ColumnarWriter) { w.WriteColumnBool(1, flags) }, boolData},
		{"uuid", func(w *ColumnarWriter) { w.WriteColumnUUID(1, ids) }, uuidData},
	}
}

func TestColumnCapacityPreservesWire(t *testing.T) {
	for _, rows := range []int{0, 1, 10, 128} {
		for _, fixture := range columnCapacityFixtures(rows) {
			t.Run(fmt.Sprintf("%s/rows=%d", fixture.name, rows), func(t *testing.T) {
				want := AppendVarint(nil, uint64(rows))
				want = append(want, 0)
				if rows > 0 {
					want = append(want, 1)
					want = append(want, fixture.payload...)
				}
				want = append(want, 0)
				var writer ColumnarWriter
				for range 2 {
					writer.Reset()
					writer.SetCount(rows)
					fixture.write(&writer)
					got, err := writer.Bytes()
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("column capacity changed wire: %x, want %x; %v", got, want, err)
					}
				}
			})
		}
	}
}

func TestFixedAndSmallColumnsAllocateOnePayload(t *testing.T) {
	for _, fixture := range columnCapacityFixtures(128) {
		if fixture.name == "wide-int" {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			writer := ColumnarWriter{columns: make([]column, 0, 1)}
			allocations := testing.AllocsPerRun(100, func() {
				writer.Reset()
				writer.SetCount(128)
				fixture.write(&writer)
			})
			if allocations != 1 || len(writer.columns) != 1 || !bytes.Equal(writer.columns[0].data, fixture.payload) {
				t.Fatalf("column did not use one owned payload allocation: %v", allocations)
			}
		})
	}
}

func TestSingleWideIntegerAvoidsTinyGrowth(t *testing.T) {
	writer := ColumnarWriter{columns: make([]column, 0, 1)}
	values := []int64{math.MaxInt64}
	allocations := testing.AllocsPerRun(100, func() {
		writer.Reset()
		writer.SetCount(1)
		writer.WriteColumnInt(1, values)
	})
	if allocations > 2 {
		t.Fatalf("one wide integer requires more growth than the original writer: %v", allocations)
	}
}

func TestFloatColumnCapacityAndWire(t *testing.T) {
	for _, rows := range []int{0, 1, 10, 128} {
		t.Run(fmt.Sprint(rows), func(t *testing.T) {
			values := floatCapacityValues(rows)
			writer := ColumnarWriter{columns: make([]column, 0, 1)}
			allocations := testing.AllocsPerRun(100, func() {
				writer.Reset()
				writer.SetCount(rows)
				if err := writer.WriteColumnFloat(1, values); err != nil {
					t.Fatal(err)
				}
			})
			wantAllocations := 0.0
			if rows > 0 {
				wantAllocations = 1
			}
			if allocations != wantAllocations {
				t.Fatalf("payload allocations = %v, want %v", allocations, wantAllocations)
			}
			var payload []byte
			for _, value := range values {
				payload = appendFixed64(payload, value)
			}
			if len(writer.columns) != 1 || !bytes.Equal(writer.columns[0].data, payload) {
				t.Fatal("Float payload bits changed")
			}
		})
	}
}

func floatCapacityValues(rows int) []float64 {
	boundaries := [...]float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, 1.25}
	values := make([]float64, rows)
	for i := range values {
		values[i] = boundaries[i%len(boundaries)]
	}
	return values
}

func TestFloatColumnRejectsBeforePayloadAllocation(t *testing.T) {
	for _, index := range []int{0, 63, 127} {
		for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			t.Run(fmt.Sprintf("%d/%v", index, invalid), func(t *testing.T) {
				values := floatCapacityValues(128)
				values[index] = invalid
				writer := ColumnarWriter{columns: make([]column, 0, 2)}
				writer.SetCount(128)
				writer.WriteColumnInt(1, make([]int64, 128))
				before, err := writer.Bytes()
				if err != nil {
					t.Fatal(err)
				}
				before = bytes.Clone(before)
				allocations := testing.AllocsPerRun(100, func() {
					if err := writer.WriteColumnFloat(2, values); err != ErrNonFiniteFloat {
						t.Fatalf("invalid Float accepted: %v", err)
					}
				})
				after, err := writer.Bytes()
				if allocations != 0 || err != nil || !bytes.Equal(before, after) || len(writer.columns) != 1 {
					t.Fatalf("failed write allocated/published payload: %v allocs, %v", allocations, err)
				}
			})
		}
	}
}

func TestColumnarBytesRejectsInvalidState(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*ColumnarWriter)
		want    error
	}{
		{"zero columns", func(w *ColumnarWriter) { w.SetCount(1) }, ErrEmptyColumnarProjection},
		{"negative count", func(w *ColumnarWriter) { w.SetCount(-1) }, ErrInvalidColumnarCount},
		{"excess count", func(w *ColumnarWriter) { w.SetCount(MaxColumnarRecords + 1) }, ErrInvalidColumnarCount},
		{"maximum count without columns", func(w *ColumnarWriter) { w.SetCount(MaxColumnarRecords) }, ErrEmptyColumnarProjection},
		{"short column", func(w *ColumnarWriter) { w.SetCount(2); w.WriteColumnInt(1, []int64{1}) }, ErrColumnarRowCount},
		{"long column", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(1, []int64{1, 2}) }, ErrColumnarRowCount},
		{"empty with values", func(w *ColumnarWriter) { w.WriteColumnInt(1, []int64{1}) }, ErrColumnarRowCount},
		{"zero field", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(0, []int64{1}) }, ErrInvalidColumnarField},
		{"negative field", func(w *ColumnarWriter) { w.SetCount(1); w.WriteColumnInt(-1, []int64{1}) }, ErrInvalidColumnarField},
		{"invalid field is sticky", func(w *ColumnarWriter) {
			w.SetCount(1)
			w.WriteColumnInt(0, []int64{1})
			w.WriteColumnBool(1, []bool{true})
		}, ErrInvalidColumnarField},
		{"negative arena", func(w *ColumnarWriter) { w.totalStringLen = -1 }, ErrInvalidColumnarArena},
		{"excess arena", func(w *ColumnarWriter) { w.totalStringLen = MaxArenaSize + 1 }, ErrInvalidColumnarArena},
		{"inconsistent columns", func(w *ColumnarWriter) {
			w.SetCount(2)
			w.WriteColumnInt(1, []int64{1, 2})
			w.WriteColumnBool(2, []bool{true})
			w.WriteColumnInt(3, []int64{3, 4})
		}, ErrColumnarRowCount},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := ColumnarWriter{buf: []byte{42, 43}}
			test.prepare(&writer)
			for range 2 {
				data, err := writer.Bytes()
				if data != nil || !errors.Is(err, test.want) {
					t.Fatalf("invalid output = %x, %v; want nil, %v", data, err, test.want)
				}
				if !bytes.Equal(writer.buf, []byte{42, 43}) {
					t.Fatal("failed validation modified the previous output")
				}
			}
			writer.Reset()
			if data, err := writer.Bytes(); err != nil || !bytes.Equal(data, []byte{0, 0, 0}) {
				t.Fatalf("reset did not restore an empty writer: %x, %v", data, err)
			}
		})
	}
}

func TestColumnarBytesRetryAndNullColumn(t *testing.T) {
	var writer ColumnarWriter
	writer.SetCount(2)
	if data, err := writer.Bytes(); data != nil || !errors.Is(err, ErrEmptyColumnarProjection) {
		t.Fatalf("missing projection: %x, %v", data, err)
	}
	writer.WriteColumnIntPtr(1, []*int64{nil, nil})
	want := []byte{2, 0, 1, 0, 0, 0}
	for range 2 {
		data, err := writer.Bytes()
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("null column rejected: %x, %v", data, err)
		}
	}
	writer.SetCount(1)
	if data, err := writer.Bytes(); data != nil || !errors.Is(err, ErrColumnarRowCount) {
		t.Fatalf("changed row count accepted: %x, %v", data, err)
	}
	writer.SetCount(2)
	if data, err := writer.Bytes(); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("restored count rejected: %x, %v", data, err)
	}
}
