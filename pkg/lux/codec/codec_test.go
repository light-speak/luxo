package codec

import (
	"math"
	"testing"
)

// --- Wire-level tests ---

func TestVarint(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 255, 256, 16383, 16384, math.MaxUint64}
	for _, c := range cases {
		buf := AppendVarint(nil, c)
		got, n := ReadVarint(buf, 0)
		if n == 0 {
			t.Fatalf("ReadVarint failed for %d", c)
		}
		if got != c {
			t.Fatalf("varint %d: got %d", c, got)
		}
		if n != len(buf) {
			t.Fatalf("varint %d: consumed %d of %d bytes", c, n, len(buf))
		}
	}
}

func TestSvarint(t *testing.T) {
	cases := []int64{0, 1, -1, 127, -128, 255, -256, math.MaxInt64, math.MinInt64}
	for _, c := range cases {
		buf := AppendSvarint(nil, c)
		got, n := ReadSvarint(buf, 0)
		if n == 0 {
			t.Fatalf("ReadSvarint failed for %d", c)
		}
		if got != c {
			t.Fatalf("svarint %d: got %d", c, got)
		}
	}
}

func TestFixed64(t *testing.T) {
	cases := []float64{0, 1.0, -1.0, 3.14, math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, c := range cases {
		buf := AppendFixed64(nil, c)
		got, n := ReadFixed64(buf, 0)
		if n != 8 {
			t.Fatalf("fixed64 consumed %d bytes", n)
		}
		if got != c {
			t.Fatalf("fixed64 %f: got %f", c, got)
		}
	}
}

func TestString(t *testing.T) {
	cases := []string{"", "hello", "你好世界", "a string with spaces and special chars: !@#$%"}
	for _, c := range cases {
		buf := AppendString(nil, c)
		got, n := ReadString(buf, 0)
		if n == 0 {
			t.Fatalf("ReadString failed for %q", c)
		}
		if got != c {
			t.Fatalf("string %q: got %q", c, got)
		}
	}
}

func TestBool(t *testing.T) {
	for _, c := range []bool{true, false} {
		buf := AppendBool(nil, c)
		got, n := ReadBool(buf, 0)
		if n == 0 {
			t.Fatal("ReadBool failed")
		}
		if got != c {
			t.Fatalf("bool %v: got %v", c, got)
		}
	}
}

func TestNullable(t *testing.T) {
	// null
	buf := AppendNull(nil)
	present, n := ReadNullable(buf, 0)
	if n != 1 || present {
		t.Fatal("expected null")
	}

	// present
	buf = AppendPresent(nil)
	present, n = ReadNullable(buf, 0)
	if n != 1 || !present {
		t.Fatal("expected present")
	}
}

// --- Encoder/Decoder round-trip ---

func TestEncoderDecoderRoundTrip(t *testing.T) {
	// Simulate: TaskAssignedEvent { TaskId: 42, AssigneeId: 7 }
	var enc Encoder
	enc.WriteFieldInt(1, 42)
	enc.WriteFieldInt(2, 7)
	enc.WriteEnd()

	data := enc.Bytes()
	t.Logf("encoded %d bytes (JSON would be ~40)", len(data))

	dec := NewDecoder(data)
	var taskId, assigneeId int64
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			taskId = dec.ReadInt()
		case 2:
			assigneeId = dec.ReadInt()
		}
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if taskId != 42 {
		t.Fatalf("taskId: got %d", taskId)
	}
	if assigneeId != 7 {
		t.Fatalf("assigneeId: got %d", assigneeId)
	}
}

func TestEncoderDecoderMixed(t *testing.T) {
	// Simulate: UserCreated { Id: 1, Name: "alice", Email: "alice@test.com", Score: 99.5, Active: true }
	var enc Encoder
	enc.WriteFieldInt(1, 1)
	enc.WriteFieldString(2, "alice")
	enc.WriteFieldString(3, "alice@test.com")
	enc.WriteFieldFloat(4, 99.5)
	enc.WriteFieldBool(5, true)
	enc.WriteEnd()

	data := enc.Bytes()
	t.Logf("encoded %d bytes", len(data))

	dec := NewDecoder(data)
	var id int64
	var name, email string
	var score float64
	var active bool

	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			id = dec.ReadInt()
		case 2:
			name = dec.ReadString()
		case 3:
			email = dec.ReadString()
		case 4:
			score = dec.ReadFloat()
		case 5:
			active = dec.ReadBool()
		}
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if id != 1 || name != "alice" || email != "alice@test.com" || score != 99.5 || !active {
		t.Fatalf("got: id=%d name=%s email=%s score=%f active=%v", id, name, email, score, active)
	}
}

func TestEncoderDecoderNullable(t *testing.T) {
	val := int64(42)
	var enc Encoder
	enc.WriteFieldIntPtr(1, &val)   // present
	enc.WriteFieldIntPtr(2, nil)    // null
	enc.WriteFieldStringPtr(3, nil) // null string
	s := "hello"
	enc.WriteFieldStringPtr(4, &s) // present string
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	var f1, f2 *int64
	var f3, f4 *string
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			f1 = dec.ReadIntPtr()
		case 2:
			f2 = dec.ReadIntPtr()
		case 3:
			f3 = dec.ReadStringPtr()
		case 4:
			f4 = dec.ReadStringPtr()
		}
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if f1 == nil || *f1 != 42 {
		t.Fatalf("f1: got %v", f1)
	}
	if f2 != nil {
		t.Fatalf("f2: should be nil, got %v", *f2)
	}
	if f3 != nil {
		t.Fatalf("f3: should be nil")
	}
	if f4 == nil || *f4 != "hello" {
		t.Fatalf("f4: got %v", f4)
	}
}

func TestEncoderReset(t *testing.T) {
	var enc Encoder
	enc.WriteFieldInt(1, 100)
	enc.WriteEnd()
	first := len(enc.Bytes())

	enc.Reset()
	enc.WriteFieldInt(1, 200)
	enc.WriteEnd()
	second := len(enc.Bytes())

	if first != second {
		t.Fatalf("sizes should match: %d vs %d", first, second)
	}

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	if dec.ReadInt() != 200 {
		t.Fatal("should read reset value")
	}
}

func TestDecoderEmpty(t *testing.T) {
	dec := NewDecoder([]byte{0x00}) // just end marker
	if dec.NextField() {
		t.Fatal("empty message should have no fields")
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
}

func TestDecoderInvalidData(t *testing.T) {
	dec := NewDecoder([]byte{})
	if dec.NextField() {
		t.Fatal("should not read from empty buf")
	}
}

func TestVarintEdgeCases(t *testing.T) {
	// Read from empty
	_, n := ReadVarint(nil, 0)
	if n != 0 {
		t.Fatal("should fail on nil")
	}
	// Read from offset beyond buf
	_, n = ReadVarint([]byte{1}, 5)
	if n != 0 {
		t.Fatal("should fail on out of bounds")
	}
}

func TestFixed64Short(t *testing.T) {
	_, n := ReadFixed64([]byte{1, 2, 3}, 0)
	if n != 0 {
		t.Fatal("should fail on short buf")
	}
}

func TestNullableOutOfBounds(t *testing.T) {
	_, n := ReadNullable([]byte{}, 0)
	if n != 0 {
		t.Fatal("should fail on empty buf")
	}
}

// --- Benchmarks ---

func BenchmarkEncodeEvent(b *testing.B) {
	var enc Encoder
	b.ReportAllocs()
	for b.Loop() {
		enc.Reset()
		enc.WriteFieldInt(1, 42)
		enc.WriteFieldInt(2, 7)
		enc.WriteFieldString(3, "task_assigned")
		enc.WriteEnd()
		_ = enc.Bytes()
	}
}

func BenchmarkDecodeEvent(b *testing.B) {
	var enc Encoder
	enc.WriteFieldInt(1, 42)
	enc.WriteFieldInt(2, 7)
	enc.WriteFieldString(3, "task_assigned")
	enc.WriteEnd()
	data := append([]byte{}, enc.Bytes()...)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dec := NewDecoder(data)
		for dec.NextField() {
			switch dec.FieldID() {
			case 1:
				_ = dec.ReadInt()
			case 2:
				_ = dec.ReadInt()
			case 3:
				_ = dec.ReadString()
			}
		}
	}
}
