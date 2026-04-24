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

// --- Field Mask ---

func TestFieldMaskHas(t *testing.T) {
	mask := FieldMaskSet(nil, 1)
	mask = FieldMaskSet(mask, 3)
	mask = FieldMaskSet(mask, 9)

	if !FieldMaskHas(mask, 1) {
		t.Error("should have field 1")
	}
	if FieldMaskHas(mask, 2) {
		t.Error("should not have field 2")
	}
	if !FieldMaskHas(mask, 3) {
		t.Error("should have field 3")
	}
	if !FieldMaskHas(mask, 9) {
		t.Error("should have field 9")
	}
}

func TestFieldMaskNilIsAll(t *testing.T) {
	if !FieldMaskHas(nil, 1) || !FieldMaskHas(nil, 999) {
		t.Error("nil mask should include all fields")
	}
}

func TestFieldMaskAll(t *testing.T) {
	mask := FieldMaskAll(12)
	for i := 1; i <= 12; i++ {
		if !FieldMaskHas(mask, i) {
			t.Errorf("FieldMaskAll(12) should have field %d", i)
		}
	}
}

// --- Array encoding ---

func TestIntArrayRoundTrip(t *testing.T) {
	var enc Encoder
	enc.WriteFieldIntArray(1, []int64{10, 20, 30, -1, 0})
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	if dec.FieldID() != 1 {
		t.Fatalf("field = %d", dec.FieldID())
	}
	got := dec.ReadIntArray()
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if len(got) != 5 || got[0] != 10 || got[3] != -1 {
		t.Fatalf("got %v", got)
	}
}

func TestStringArrayRoundTrip(t *testing.T) {
	var enc Encoder
	enc.WriteFieldStringArray(1, []string{"hello", "world", ""})
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	got := dec.ReadStringArray()
	if len(got) != 3 || got[0] != "hello" || got[2] != "" {
		t.Fatalf("got %v", got)
	}
}

func TestFloatArrayRoundTrip(t *testing.T) {
	var enc Encoder
	enc.WriteFieldFloatArray(1, []float64{1.1, 2.2, 3.3})
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	got := dec.ReadFloatArray()
	if len(got) != 3 || got[0] != 1.1 {
		t.Fatalf("got %v", got)
	}
}

func TestEmptyArray(t *testing.T) {
	var enc Encoder
	enc.WriteFieldIntArray(1, []int64{})
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	got := dec.ReadIntArray()
	if len(got) != 0 {
		t.Fatalf("empty array should have 0 elements, got %d", len(got))
	}
}

// --- Columnar encoding ---

func TestColumnarSingleRecord(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(1)
	w.WriteColumnInt(1, []int64{42})
	w.WriteColumnString(2, []string{"alice"})
	w.WriteColumnBool(3, []bool{true})
	data := w.Bytes()

	t.Logf("single record columnar: %d bytes", len(data))

	r := NewColumnarReader(data)
	if r.Count() != 1 {
		t.Fatalf("count = %d", r.Count())
	}

	r.NextColumn()
	if r.FieldID() != 1 {
		t.Fatalf("field = %d", r.FieldID())
	}
	ids := r.ReadColumnInt()
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("ids = %v", ids)
	}

	r.NextColumn()
	names := r.ReadColumnString()
	if len(names) != 1 || names[0] != "alice" {
		t.Fatalf("names = %v", names)
	}

	r.NextColumn()
	bools := r.ReadColumnBool()
	if len(bools) != 1 || !bools[0] {
		t.Fatalf("bools = %v", bools)
	}

	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

func TestColumnarList(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnInt(1, []int64{1, 2, 3})
	w.WriteColumnString(2, []string{"alice", "bob", "carol"})
	w.WriteColumnFloat(3, []float64{10.5, 20.5, 30.5})
	data := w.Bytes()

	t.Logf("3 records columnar: %d bytes", len(data))

	r := NewColumnarReader(data)
	if r.Count() != 3 {
		t.Fatalf("count = %d", r.Count())
	}

	r.NextColumn()
	ids := r.ReadColumnInt()
	if len(ids) != 3 || ids[2] != 3 {
		t.Fatalf("ids = %v", ids)
	}

	r.NextColumn()
	names := r.ReadColumnString()
	if len(names) != 3 || names[1] != "bob" {
		t.Fatalf("names = %v", names)
	}

	r.NextColumn()
	scores := r.ReadColumnFloat()
	if len(scores) != 3 || scores[0] != 10.5 {
		t.Fatalf("scores = %v", scores)
	}
}

func TestColumnarNullable(t *testing.T) {
	v1 := int64(10)
	v3 := int64(30)
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnIntPtr(1, []*int64{&v1, nil, &v3})
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnIntPtr()
	if len(vals) != 3 {
		t.Fatalf("len = %d", len(vals))
	}
	if vals[0] == nil || *vals[0] != 10 {
		t.Fatal("vals[0] should be 10")
	}
	if vals[1] != nil {
		t.Fatal("vals[1] should be nil")
	}
	if vals[2] == nil || *vals[2] != 30 {
		t.Fatal("vals[2] should be 30")
	}
}

func TestColumnarVsRow(t *testing.T) {
	// Compare sizes: 10 users with 3 fields each
	type user struct {
		id    int64
		name  string
		score float64
	}
	users := []user{
		{1, "alice", 10.5}, {2, "bob", 20.5}, {3, "carol", 30.5},
		{4, "dave", 40.5}, {5, "eve", 50.5}, {6, "frank", 60.5},
		{7, "grace", 70.5}, {8, "hank", 80.5}, {9, "ivy", 90.5},
		{10, "jack", 100.5},
	}

	// Row encoding
	var rowBuf []byte
	rowBuf = AppendVarint(rowBuf, uint64(len(users))) // count
	for _, u := range users {
		var enc Encoder
		enc.WriteFieldInt(1, u.id)
		enc.WriteFieldString(2, u.name)
		enc.WriteFieldFloat(3, u.score)
		enc.WriteEnd()
		rowBuf = append(rowBuf, enc.Bytes()...)
	}

	// Columnar encoding
	var col ColumnarWriter
	col.SetCount(len(users))
	ids := make([]int64, len(users))
	names := make([]string, len(users))
	scores := make([]float64, len(users))
	for i, u := range users {
		ids[i] = u.id
		names[i] = u.name
		scores[i] = u.score
	}
	col.WriteColumnInt(1, ids)
	col.WriteColumnString(2, names)
	col.WriteColumnFloat(3, scores)
	colBuf := col.Bytes()

	t.Logf("10 users row: %d bytes, columnar: %d bytes, saving: %d%%",
		len(rowBuf), len(colBuf), (len(rowBuf)-len(colBuf))*100/len(rowBuf))
}

func BenchmarkColumnarWrite10(b *testing.B) {
	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	names := []string{"alice", "bob", "carol", "dave", "eve", "frank", "grace", "hank", "ivy", "jack"}
	scores := []float64{10.5, 20.5, 30.5, 40.5, 50.5, 60.5, 70.5, 80.5, 90.5, 100.5}

	var w ColumnarWriter
	b.ReportAllocs()
	for b.Loop() {
		w.Reset()
		w.SetCount(10)
		w.WriteColumnInt(1, ids)
		w.WriteColumnString(2, names)
		w.WriteColumnFloat(3, scores)
		_ = w.Bytes()
	}
}

// --- Additional coverage tests ---

func TestAppendBytesAndReadBytes(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{1, 2, 3},
		[]byte("hello world"),
	}
	for _, c := range cases {
		buf := AppendBytes(nil, c)
		got, n := ReadBytes(buf, 0)
		if n == 0 && len(c) > 0 {
			t.Fatalf("ReadBytes failed for %v", c)
		}
		if len(got) != len(c) {
			t.Fatalf("ReadBytes %v: got len %d", c, len(got))
		}
	}
}

func TestReadBytesShortBuf(t *testing.T) {
	// length says 100 but buf only has a few bytes
	buf := AppendVarint(nil, 100)
	buf = append(buf, 1, 2, 3)
	got, n := ReadBytes(buf, 0)
	if n != 0 || got != nil {
		t.Fatal("should fail on short buf")
	}
}

func TestReadBytesInvalidVarint(t *testing.T) {
	got, n := ReadBytes(nil, 0)
	if n != 0 || got != nil {
		t.Fatal("should fail on empty buf")
	}
}

func TestVarintOverflow(t *testing.T) {
	// Build a varint with >10 continuation bytes to trigger overflow
	buf := make([]byte, 12)
	for i := range buf {
		buf[i] = 0x80 // all continuation
	}
	_, n := ReadVarint(buf, 0)
	if n != -1 {
		t.Fatalf("expected overflow (n=-1), got n=%d", n)
	}
}

func TestReadSvarintOverflow(t *testing.T) {
	// Trigger overflow branch in ReadSvarint via varint overflow
	buf := make([]byte, 12)
	for i := range buf {
		buf[i] = 0x80
	}
	v, n := ReadSvarint(buf, 0)
	if n != 0 || v != 0 {
		t.Fatalf("expected (0,0) for overflow, got (%d,%d)", v, n)
	}
}

func TestReadBoolShort(t *testing.T) {
	_, n := ReadBool(nil, 0)
	if n != 0 {
		t.Fatal("should fail on nil buf")
	}
}

func TestEncoderWriteFieldBytes(t *testing.T) {
	var enc Encoder
	enc.WriteFieldBytes(1, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	if dec.FieldID() != 1 {
		t.Fatalf("field = %d", dec.FieldID())
	}
	got := dec.ReadBytes()
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if len(got) != 4 || got[0] != 0xDE || got[3] != 0xEF {
		t.Fatalf("got %v", got)
	}
}

func TestEncoderWriteFieldFloatPtr(t *testing.T) {
	f := 3.14
	var enc Encoder
	enc.WriteFieldFloatPtr(1, &f)
	enc.WriteFieldFloatPtr(2, nil)
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	var f1, f2 *float64
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			f1 = dec.ReadFloatPtr()
		case 2:
			f2 = dec.ReadFloatPtr()
		}
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if f1 == nil || *f1 != 3.14 {
		t.Fatalf("f1 = %v", f1)
	}
	if f2 != nil {
		t.Fatalf("f2 should be nil, got %v", *f2)
	}
}

func TestEncoderWriteFieldBoolPtr(t *testing.T) {
	b := true
	var enc Encoder
	enc.WriteFieldBoolPtr(1, &b)
	enc.WriteFieldBoolPtr(2, nil)
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	var b1, b2 *bool
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			b1 = dec.ReadBoolPtr()
		case 2:
			b2 = dec.ReadBoolPtr()
		}
	}
	if dec.Err() != nil {
		t.Fatal(dec.Err())
	}
	if b1 == nil || !*b1 {
		t.Fatalf("b1 = %v", b1)
	}
	if b2 != nil {
		t.Fatalf("b2 should be nil, got %v", *b2)
	}
}

func TestDecoderReadBytesError(t *testing.T) {
	// Decoder at end of buffer trying to read bytes
	dec := NewDecoder([]byte{0x01}) // field 1
	dec.NextField()
	got := dec.ReadBytes()
	if got != nil {
		t.Fatal("should return nil on truncated data")
	}
	if dec.Err() == nil {
		t.Fatal("should set error")
	}
}

func TestDecoderReadIntError(t *testing.T) {
	dec := NewDecoder([]byte{0x01}) // field 1, no value
	dec.NextField()
	v := dec.ReadInt()
	if v != 0 || dec.Err() == nil {
		t.Fatal("should error on truncated int")
	}
}

func TestDecoderReadFloatError(t *testing.T) {
	dec := NewDecoder([]byte{0x01, 0x01, 0x02}) // field 1, short float
	dec.NextField()
	v := dec.ReadFloat()
	if v != 0 || dec.Err() == nil {
		t.Fatal("should error on truncated float")
	}
}

func TestDecoderReadStringError(t *testing.T) {
	// field 1 + varint length says 100 but no data
	buf := []byte{0x01}
	buf = AppendVarint(buf, 100)
	dec := NewDecoder(buf)
	dec.NextField()
	v := dec.ReadString()
	if v != "" || dec.Err() == nil {
		t.Fatal("should error on truncated string")
	}
}

func TestDecoderReadBoolError(t *testing.T) {
	dec := NewDecoder([]byte{0x01}) // field 1, no value
	dec.NextField()
	v := dec.ReadBool()
	if v != false || dec.Err() == nil {
		t.Fatal("should error on truncated bool")
	}
}

func TestDecoderSkipField(t *testing.T) {
	var enc Encoder
	enc.WriteFieldInt(1, 42)
	enc.WriteEnd()

	dec := NewDecoder(enc.Bytes())
	dec.NextField()
	dec.SkipField()
	if dec.Err() == nil {
		t.Fatal("SkipField should set error")
	}
}

func TestDecoderNextFieldOverflow(t *testing.T) {
	// Create a varint that overflows to trigger the n<0 branch
	buf := make([]byte, 12)
	for i := range buf {
		buf[i] = 0x80
	}
	dec := NewDecoder(buf)
	if dec.NextField() {
		t.Fatal("should not advance on overflow")
	}
	if dec.Err() == nil {
		t.Fatal("should set overflow error")
	}
}

func TestDecoderNextFieldTruncated(t *testing.T) {
	// Test the truncated varint error branch (n==0 but not at buf end)
	// Actually, for n==0 at buf end, that's already covered by empty test.
	// We need n==0 with data in buffer that's all continuation bytes but incomplete
	buf := []byte{0x80} // single continuation byte, incomplete varint
	dec := NewDecoder(buf)
	if dec.NextField() {
		t.Fatal("should not advance on truncated varint")
	}
	if dec.Err() == nil {
		t.Fatal("should set truncated error")
	}
}

func TestDecoderReadIntPtrNullableError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	// now off is past buffer, ReadIntPtr calls ReadNullable on empty
	v := dec.ReadIntPtr()
	if v != nil || dec.Err() == nil {
		t.Fatal("should error on truncated nullable")
	}
}

func TestDecoderReadFloatPtrNullableError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	v := dec.ReadFloatPtr()
	if v != nil || dec.Err() == nil {
		t.Fatal("should error on truncated nullable")
	}
}

func TestDecoderReadBoolPtrNullableError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	v := dec.ReadBoolPtr()
	if v != nil || dec.Err() == nil {
		t.Fatal("should error on truncated nullable")
	}
}

func TestDecoderReadStringPtrNullableError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	v := dec.ReadStringPtr()
	if v != nil || dec.Err() == nil {
		t.Fatal("should error on truncated nullable")
	}
}

func TestDecoderArrayHeaderError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	got := dec.ReadIntArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error on truncated array header")
	}
}

func TestDecoderStringArrayHeaderError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	got := dec.ReadStringArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error")
	}
}

func TestDecoderFloatArrayHeaderError(t *testing.T) {
	dec := NewDecoder([]byte{0x01})
	dec.NextField()
	got := dec.ReadFloatArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error")
	}
}

func TestDecoderIntArrayItemError(t *testing.T) {
	// array header says 5 items, but no item data
	buf := []byte{0x01} // field 1
	buf = AppendVarint(buf, 5)
	dec := NewDecoder(buf)
	dec.NextField()
	got := dec.ReadIntArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error on truncated array items")
	}
}

func TestDecoderStringArrayItemError(t *testing.T) {
	buf := []byte{0x01}
	buf = AppendVarint(buf, 5) // 5 items, no data
	dec := NewDecoder(buf)
	dec.NextField()
	got := dec.ReadStringArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error on truncated string array items")
	}
}

func TestDecoderFloatArrayItemError(t *testing.T) {
	buf := []byte{0x01}
	buf = AppendVarint(buf, 5) // 5 items, no data
	dec := NewDecoder(buf)
	dec.NextField()
	got := dec.ReadFloatArray()
	if got != nil || dec.Err() == nil {
		t.Fatal("should error on truncated float array items")
	}
}

func TestDecoderNextFieldWithExistingError(t *testing.T) {
	dec := NewDecoder([]byte{0x01, 0x00})
	dec.NextField()
	dec.SkipField() // sets error
	// NextField should return false when err is set
	if dec.NextField() {
		t.Fatal("should not advance when error is set")
	}
}

// --- ColumnarWriter Reset ---

func TestColumnarWriterReset(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(2)
	w.WriteColumnInt(1, []int64{10, 20})
	_ = w.Bytes()

	w.Reset()
	w.SetCount(1)
	w.WriteColumnInt(1, []int64{42})
	data := w.Bytes()

	r := NewColumnarReader(data)
	if r.Count() != 1 {
		t.Fatalf("count = %d after reset, want 1", r.Count())
	}
	r.NextColumn()
	vals := r.ReadColumnInt()
	if len(vals) != 1 || vals[0] != 42 {
		t.Fatalf("got %v", vals)
	}
}

// --- Columnar nullable ptr columns ---

func TestColumnarFloatPtrWrite(t *testing.T) {
	f1 := 1.5
	f3 := 3.5
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnFloatPtr(1, []*float64{&f1, nil, &f3})
	data := w.Bytes()

	// Verify we can read the data (reader for FloatPtr doesn't exist, verify no panic)
	r := NewColumnarReader(data)
	if r.Count() != 3 {
		t.Fatalf("count = %d", r.Count())
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

func TestColumnarStringPtr(t *testing.T) {
	s1 := "hello"
	s3 := "world"
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnStringPtr(1, []*string{&s1, nil, &s3})
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnStringPtr()
	if len(vals) != 3 {
		t.Fatalf("len = %d", len(vals))
	}
	if vals[0] == nil || *vals[0] != "hello" {
		t.Fatal("vals[0]")
	}
	if vals[1] != nil {
		t.Fatal("vals[1] should be nil")
	}
	if vals[2] == nil || *vals[2] != "world" {
		t.Fatal("vals[2]")
	}
}

func TestColumnarBoolPtrWrite(t *testing.T) {
	b1 := true
	b3 := false
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnBoolPtr(1, []*bool{&b1, nil, &b3})
	data := w.Bytes()

	// Verify write produces data without error
	r := NewColumnarReader(data)
	if r.Count() != 3 {
		t.Fatalf("count = %d", r.Count())
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
}

// --- ColumnarReader error branches ---

func TestColumnarReaderInvalidHeader(t *testing.T) {
	r := NewColumnarReader(nil)
	if r.Err() == nil {
		t.Fatal("should error on nil data")
	}
}

func TestColumnarReaderIntTruncated(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnInt(1, []int64{1}) // only 1 value, but count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnInt()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error on truncated column")
	}
}

func TestColumnarReaderFloatTruncated(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnFloat(1, []float64{1.0}) // only 1, count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnFloat()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error")
	}
}

func TestColumnarReaderStringTruncated(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnString(1, []string{"a"}) // only 1, count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnString()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error")
	}
}

func TestColumnarReaderBoolTruncated(t *testing.T) {
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnBool(1, []bool{true}) // only 1, count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnBool()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error")
	}
}

func TestColumnarReaderIntPtrTruncatedNullable(t *testing.T) {
	// count=3 but only 1 value worth of data
	v := int64(1)
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnIntPtr(1, []*int64{&v}) // only 1, count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnIntPtr()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error")
	}
}

func TestColumnarReaderStringPtrTruncated(t *testing.T) {
	s := "a"
	var w ColumnarWriter
	w.SetCount(3)
	w.WriteColumnStringPtr(1, []*string{&s}) // only 1, count=3
	data := w.Bytes()

	r := NewColumnarReader(data)
	r.NextColumn()
	vals := r.ReadColumnStringPtr()
	if vals != nil || r.Err() == nil {
		t.Fatal("should error")
	}
}

func TestColumnarReaderNextColumnWithError(t *testing.T) {
	r := NewColumnarReader(nil)
	// error already set
	if r.NextColumn() {
		t.Fatal("should not advance with error")
	}
}

// --- FieldMask edge cases ---

func TestFieldMaskSetInvalid(t *testing.T) {
	mask := FieldMaskSet(nil, 0)
	if mask != nil {
		t.Error("fieldID 0 should not set anything")
	}
	mask = FieldMaskSet(nil, -1)
	if mask != nil {
		t.Error("negative fieldID should not set anything")
	}
}

func TestFieldMaskHasInvalid(t *testing.T) {
	mask := FieldMaskSet(nil, 1)
	if FieldMaskHas(mask, 0) {
		t.Error("fieldID 0 should not match")
	}
	if FieldMaskHas(mask, -1) {
		t.Error("negative fieldID should not match")
	}
}

func TestFieldMaskHasBeyondLen(t *testing.T) {
	mask := FieldMaskSet(nil, 1) // 1 byte
	if FieldMaskHas(mask, 100) {
		t.Error("field beyond mask length should not match")
	}
}

func TestFieldMaskAllZero(t *testing.T) {
	mask := FieldMaskAll(0)
	if mask != nil {
		t.Error("FieldMaskAll(0) should return nil")
	}
	mask = FieldMaskAll(-1)
	if mask != nil {
		t.Error("FieldMaskAll(-1) should return nil")
	}
}

// --- Columnar: NextColumn with id==0 (end marker) ---

func TestColumnarReaderNextColumnEndMarker(t *testing.T) {
	// Build minimal columnar: count=1, then field ID=0 (end marker)
	var buf []byte
	buf = AppendVarint(buf, 1) // count=1
	buf = AppendVarint(buf, 0) // field ID=0 → end
	r := NewColumnarReader(buf)
	if r.NextColumn() {
		t.Fatal("field ID 0 should signal end of columns")
	}
}

// --- Columnar: ReadColumnIntPtr truncated value after present ---

func TestColumnarReaderIntPtrTruncatedValue(t *testing.T) {
	// count=1, write present marker (0x01) but no int value after
	var buf []byte
	buf = AppendVarint(buf, 1) // count=1
	buf = AppendVarint(buf, 1) // field ID=1
	buf = AppendPresent(buf)   // present marker
	// No int value follows → truncated

	r := NewColumnarReader(buf)
	r.NextColumn()
	vals := r.ReadColumnIntPtr()
	if vals != nil {
		t.Fatal("should return nil on truncated int value")
	}
	if r.Err() == nil {
		t.Fatal("should set error on truncated int value")
	}
}

// --- Columnar: ReadColumnStringPtr truncated value after present ---

func TestColumnarReaderStringPtrTruncatedValue(t *testing.T) {
	// count=1, write present marker but string length says 100, no data
	var buf []byte
	buf = AppendVarint(buf, 1) // count=1
	buf = AppendVarint(buf, 1) // field ID=1
	buf = AppendPresent(buf)   // present marker
	// String length says 100 but no data
	buf = AppendVarint(buf, 100)

	r := NewColumnarReader(buf)
	r.NextColumn()
	vals := r.ReadColumnStringPtr()
	if vals != nil {
		t.Fatal("should return nil on truncated string value")
	}
	if r.Err() == nil {
		t.Fatal("should set error on truncated string value")
	}
}

func BenchmarkRowWrite10(b *testing.B) {
	type user struct {
		id    int64
		name  string
		score float64
	}
	users := []user{
		{1, "alice", 10.5}, {2, "bob", 20.5}, {3, "carol", 30.5},
		{4, "dave", 40.5}, {5, "eve", 50.5}, {6, "frank", 60.5},
		{7, "grace", 70.5}, {8, "hank", 80.5}, {9, "ivy", 90.5},
		{10, "jack", 100.5},
	}
	b.ReportAllocs()
	for b.Loop() {
		var buf []byte
		buf = AppendVarint(buf, uint64(len(users)))
		for _, u := range users {
			var enc Encoder
			enc.WriteFieldInt(1, u.id)
			enc.WriteFieldString(2, u.name)
			enc.WriteFieldFloat(3, u.score)
			enc.WriteEnd()
			buf = append(buf, enc.Bytes()...)
		}
	}
}
