package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func TestDateTimeSharedTextBoundaries(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, Input string
			Canonical   *string
		} `json:"dateTimeBoundaryCases"`
	}
	data, err := os.ReadFile("testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 34 {
		t.Fatal("missing boundary corpus")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			value, err := jsonutil.ParseDateTime([]byte(`"` + tc.Input + `"`))
			if tc.Canonical == nil {
				if err == nil {
					t.Fatalf("accepted invalid DateTime %q", tc.Input)
				}
				return
			}
			if err != nil || value.UTC().Format(time.RFC3339Nano) != *tc.Canonical {
				t.Fatalf("%v / %v", value, err)
			}
		})
	}
}

func TestDateTimeRejectsBinaryRangeBeforeMaterialization(t *testing.T) {
	for _, seconds := range []int64{-62167219201, 253402300800, math.MinInt64, math.MaxInt64} {
		wire := AppendVarint(AppendSvarint(nil, seconds), 0)
		if _, n := ReadDateTime(wire, 0); n != 0 {
			t.Fatalf("accepted seconds %d", seconds)
		}
		decoder := NewDecoder(wire)
		decoder.SkipDateTime()
		if decoder.Err() == nil {
			t.Fatalf("skipped invalid seconds %d", seconds)
		}
	}
}

func TestDateTimeWritersRejectRangeWithoutPartialOutput(t *testing.T) {
	for _, seconds := range []int64{-62167219201, 253402300800} {
		value := time.Unix(seconds, 0).UTC()
		got, err := AppendDateTime([]byte{42}, value)
		if !errors.Is(err, ErrInvalidDateTime) || !bytes.Equal(got, []byte{42}) {
			t.Fatal(got, err)
		}
		for _, write := range []func(*Encoder) error{
			func(e *Encoder) error { return e.WriteDateTime(value) },
			func(e *Encoder) error { return e.WriteFieldDateTime(1, value) },
			func(e *Encoder) error { return e.WriteFieldDateTimePtr(1, &value) },
			func(e *Encoder) error { return e.WriteFieldDateTimeArray(1, []time.Time{{}, value}) },
		} {
			encoder := Encoder{buf: []byte{42}}
			if err := write(&encoder); !errors.Is(err, ErrInvalidDateTime) || !bytes.Equal(encoder.Bytes(), []byte{42}) {
				t.Fatal(encoder.Bytes(), err)
			}
		}
		for _, nullable := range []bool{false, true} {
			var writer ColumnarWriter
			writer.SetCount(2)
			if nullable {
				err = writer.WriteColumnDateTimePtr(1, []*time.Time{nil, &value})
			} else {
				err = writer.WriteColumnDateTime(1, []time.Time{{}, value})
			}
			if !errors.Is(err, ErrInvalidDateTime) || len(writer.columns) != 0 {
				t.Fatal(writer.columns, err)
			}
		}
	}
}

func TestDateTimeWireNanoseconds(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		nanos   int
		wire    []byte
	}{
		{0, 0, []byte{0, 0}},
		{0, 1, []byte{0, 1}},
		{-1, 1, []byte{1, 1}},
		{1, 127, []byte{2, 127}},
		{1, 128, []byte{2, 128, 1}},
		{-1, 999999999, []byte{1, 255, 147, 235, 220, 3}},
	} {
		value := time.Unix(tc.seconds, int64(tc.nanos)).UTC()
		wire := testutil.Bytes(t)(AppendDateTime([]byte{42}, value))
		if !bytes.Equal(wire[1:], tc.wire) || wire[0] != 42 {
			t.Fatalf("%v: wire %x, want %x", value, wire, tc.wire)
		}
		got, n := ReadDateTime(wire, 1)
		if n != len(tc.wire) || !got.Equal(value) || got.Location() != time.UTC {
			t.Fatalf("%v: got %v / %d", value, got, n)
		}
	}
}

func invalidDateTimeValues() [][]byte {
	overflow := bytes.Repeat([]byte{255}, 10)
	return [][]byte{
		nil, {0}, {128}, {0, 128}, overflow, append([]byte{0}, overflow...),
		AppendVarint([]byte{0}, 1_000_000_000), AppendVarint([]byte{0}, math.MaxUint64),
	}
}

func TestDateTimeReadersRejectInvalidPairs(t *testing.T) {
	for _, wire := range invalidDateTimeValues() {
		if value, n := ReadDateTime(wire, 0); n != 0 || !value.IsZero() {
			t.Fatalf("accepted invalid pair %x: %v / %d", wire, value, n)
		}
		decoder := NewDecoder(wire)
		if value := decoder.ReadDateTime(); !value.IsZero() || decoder.Err() == nil {
			t.Fatalf("accepted invalid scalar %x", wire)
		}
		decoder = NewDecoder(append([]byte{1}, wire...))
		if value := decoder.ReadDateTimePtr(); value != nil || decoder.Err() == nil {
			t.Fatalf("accepted invalid nullable %x", wire)
		}
	}
	for _, off := range []int{-1, 2, math.MaxInt} {
		if _, n := ReadDateTime([]byte{0, 0}, off); n != 0 {
			t.Fatalf("accepted invalid offset %d", off)
		}
	}
	for _, wire := range [][]byte{nil, {2}, {255}} {
		decoder := NewDecoder(wire)
		if decoder.ReadDateTimePtr() != nil || decoder.Err() == nil {
			t.Fatalf("accepted nullable marker %x", wire)
		}
	}
}

func TestDateTimeEncoderAndCollections(t *testing.T) {
	value := time.Unix(-1, 900_000_000).UTC()
	values := []time.Time{value, {}, time.Unix(253402300799, 999999999).UTC()}
	var encoder Encoder
	encoder.WriteDateTime(value)
	encoder.WriteFieldDateTime(1, value)
	encoder.WriteFieldDateTimePtr(2, &value)
	encoder.WriteFieldDateTimePtr(3, nil)
	encoder.WriteFieldDateTimeArray(4, values)
	encoder.WriteEnd()
	decoder := NewDecoder(encoder.Bytes())
	if got := decoder.ReadDateTime(); !got.Equal(value) {
		t.Fatal(got)
	}
	if !decoder.NextField() || decoder.FieldID() != 1 || !decoder.ReadDateTime().Equal(value) {
		t.Fatal("field")
	}
	if !decoder.NextField() || decoder.FieldID() != 2 {
		t.Fatal("nullable field")
	}
	if got := decoder.ReadDateTimePtr(); got == nil || !got.Equal(value) {
		t.Fatal(got)
	}
	if !decoder.NextField() || decoder.FieldID() != 3 || decoder.ReadDateTimePtr() != nil {
		t.Fatal("null")
	}
	if !decoder.NextField() || decoder.FieldID() != 4 {
		t.Fatal("array field")
	}
	if got := decoder.ReadDateTimeArray(); !slices.EqualFunc(got, values, time.Time.Equal) {
		t.Fatal(got)
	}
	if decoder.NextField() || decoder.Finish() != nil {
		t.Fatal(decoder.Err())
	}
}

func TestDateTimeColumnarReadAndSkip(t *testing.T) {
	values := []time.Time{time.Unix(-1, 900_000_000).UTC(), {}, time.Unix(253402300799, 999999999).UTC()}
	for _, nullable := range []bool{false, true} {
		var writer ColumnarWriter
		writer.SetCount(3)
		if nullable {
			writer.WriteColumnDateTimePtr(1, []*time.Time{&values[0], nil, &values[2]})
		} else {
			writer.WriteColumnDateTime(1, values)
		}
		wire, err := writer.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		reader := NewColumnarReader(wire)
		if !reader.NextColumn() {
			t.Fatal(reader.Err())
		}
		if nullable {
			got := reader.ReadColumnDateTimePtr()
			if len(got) != 3 || got[0] == nil || !got[0].Equal(values[0]) || got[1] != nil || got[2] == nil || !got[2].Equal(values[2]) {
				t.Fatal(got)
			}
		} else if got := reader.ReadColumnDateTime(); !slices.EqualFunc(got, values, time.Time.Equal) {
			t.Fatal(got)
		}
		if reader.NextColumn() || reader.Err() != nil || reader.Offset() != len(wire) {
			t.Fatal(reader.Err())
		}
		reader = NewColumnarReader(wire)
		reader.NextColumn()
		if nullable {
			reader.SkipColumnDateTimePtr()
		} else {
			reader.SkipColumnDateTime()
		}
		if reader.NextColumn() || reader.Err() != nil || reader.Offset() != len(wire) {
			t.Fatal(reader.Err())
		}
	}
}

func TestDateTimeArrayBoundsAndErrors(t *testing.T) {
	for _, wire := range [][]byte{{2, 0, 0}, {1, 0, 128}, {1, 0, 128, 148, 235, 220, 3}, {128}, {255, 255, 255, 255, 127}} {
		decoder := NewDecoder(wire)
		if got := decoder.ReadDateTimeArray(); got != nil || decoder.Err() == nil {
			t.Fatalf("accepted %x: %v", wire, got)
		}
	}
	decoder := NewDecoder([]byte{0})
	if got := decoder.ReadDateTimeArray(); len(got) != 0 || decoder.Finish() != nil {
		t.Fatal(got, decoder.Err())
	}
	prior := errors.New("prior error")
	decoder = NewDecoder([]byte{0, 0})
	decoder.Fail(prior)
	decoder.ReadDateTime()
	decoder.ReadDateTimePtr()
	if decoder.ReadDateTimeArray() != nil || decoder.Err() != prior || decoder.Offset() != 0 {
		t.Fatal("lost prior error")
	}
}

func TestDateTimeSkipValidationAndAllocations(t *testing.T) {
	for _, kind := range []FieldSkipType{SkipDateTime, SkipNullDateTime} {
		for _, bad := range invalidDateTimeValues() {
			wire := bytes.Clone(bad)
			if kind == SkipNullDateTime {
				wire = append([]byte{1}, wire...)
			}
			decoder := NewDecoder(wire)
			decoder.SkipValue(kind)
			if decoder.Err() == nil {
				t.Fatalf("skip accepted %x", wire)
			}
		}
	}
	value := time.Unix(-1, 999999999).UTC()
	encoder := Encoder{buf: make([]byte, 0, 64)}
	valid := true
	if n := testing.AllocsPerRun(100, func() {
		encoder.Reset()
		encoder.WriteDateTime(value)
		decoder := NewDecoder(encoder.Bytes())
		decoder.SkipValue(SkipDateTime)
		valid = valid && decoder.Finish() == nil
	}); n != 0 {
		t.Fatalf("scalar/skip allocated %v", n)
	}
	if !valid {
		t.Fatal("invalid scalar skip")
	}
	decoder := NewDecoder([]byte{0})
	decoder.SkipValue(SkipNullDateTime)
	if decoder.Finish() != nil {
		t.Fatal(decoder.Err())
	}
}

func TestDateTimeColumnFailureDoesNotReturnPartialValues(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, bad := range invalidDateTimeValues() {
			wire := testutil.Bytes(t)(AppendDateTime(nil, time.Unix(42, 1)))
			if nullable {
				wire = append(append([]byte{1}, wire...), 1)
			}
			wire = append(wire, bad...)
			reader := ColumnarReader{buf: wire, count: 2}
			if nullable {
				if reader.ReadColumnDateTimePtr() != nil {
					t.Fatal("partial nullable values")
				}
			} else if reader.ReadColumnDateTime() != nil {
				t.Fatal("partial values")
			}
			if reader.Err() == nil {
				t.Fatalf("column accepted %x", wire)
			}
			reader = ColumnarReader{buf: wire, count: 2}
			if nullable {
				reader.SkipColumnDateTimePtr()
			} else {
				reader.SkipColumnDateTime()
			}
			if reader.Err() == nil {
				t.Fatalf("skip accepted %x", wire)
			}
		}
		reader := ColumnarReader{buf: []byte{0}, count: 10_000}
		if nullable {
			reader.ReadColumnDateTimePtr()
		} else {
			reader.ReadColumnDateTime()
		}
		if !errors.Is(reader.Err(), ErrInsufficientElementBytes) {
			t.Fatal(reader.Err())
		}
		reader = ColumnarReader{buf: []byte{0}, count: 10_000}
		if nullable {
			reader.SkipColumnDateTimePtr()
		} else {
			reader.SkipColumnDateTime()
		}
		if !errors.Is(reader.Err(), ErrInsufficientElementBytes) {
			t.Fatal(reader.Err())
		}
	}
}

func TestDateTimePreservesExistingErrorsAndEmptyColumns(t *testing.T) {
	prior := errors.New("prior error")
	decoder := NewDecoder([]byte{1, 0, 0})
	decoder.Fail(prior)
	decoder.SkipDateTime()
	decoder.SkipNullableDateTime()
	if decoder.Err() != prior || decoder.Offset() != 0 {
		t.Fatal("overwritten error")
	}
	for _, nullable := range []bool{false, true} {
		reader := ColumnarReader{buf: []byte{0, 0}, count: 1, err: prior}
		if nullable {
			reader.ReadColumnDateTimePtr()
			reader.SkipColumnDateTimePtr()
		} else {
			reader.ReadColumnDateTime()
			reader.SkipColumnDateTime()
		}
		if reader.Err() != prior || reader.Offset() != 0 {
			t.Fatal("overwritten column error")
		}
		var writer ColumnarWriter
		if nullable {
			writer.WriteColumnDateTimePtr(1, nil)
		} else {
			writer.WriteColumnDateTime(1, nil)
		}
		wire, err := writer.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		reader = *NewColumnarReader(wire)
		if reader.NextColumn() || reader.Err() != nil || reader.Offset() != len(wire) {
			t.Fatal(reader.Err())
		}
		reader = ColumnarReader{}
		if nullable {
			if len(reader.ReadColumnDateTimePtr()) != 0 {
				t.Fatal("nonempty column")
			}
		} else if len(reader.ReadColumnDateTime()) != 0 {
			t.Fatal("nonempty column")
		}
		if reader.Err() != nil || reader.Offset() != 0 {
			t.Fatal(reader.Err())
		}
	}
}

func FuzzDateTimePair(f *testing.F) {
	for _, wire := range append(invalidDateTimeValues(), []byte{1, 255, 147, 235, 220, 3}, []byte{0, 0}) {
		f.Add(wire, 0)
	}
	f.Fuzz(func(t *testing.T, wire []byte, off int) {
		value, size := ReadDateTime(wire, off)
		if size == 0 {
			return
		}
		if off < 0 || off > len(wire) || size > len(wire)-off {
			t.Fatal("invalid consumed boundary")
		}
		seconds, n := ReadSvarint(wire, off)
		nanos, nn := ReadVarint(wire, off+n)
		if n <= 0 || nn <= 0 || n+nn != size || nanos >= 1_000_000_000 || value.Unix() != seconds || uint64(value.Nanosecond()) != nanos {
			t.Fatal("lost exact components")
		}
		canonical := testutil.Bytes(t)(AppendDateTime(nil, value))
		other, count := ReadDateTime(canonical, 0)
		if count != len(canonical) || !other.Equal(value) {
			t.Fatal("round trip mismatch")
		}
	})
}
