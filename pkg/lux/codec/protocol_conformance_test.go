package codec

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"testing"
)

type codecProtocolFixture struct {
	Version    int `json:"version"`
	Int64Cases []struct {
		Value string `json:"value"`
		Wire  string `json:"wire"`
	} `json:"int64Cases"`
	InvalidInt64 []string `json:"invalidInt64"`
	Primitives   struct {
		Varint300         string `json:"varint300"`
		SvarintNegative42 string `json:"svarintNegative42"`
		Fixed64OnePoint25 string `json:"fixed64OnePoint25"`
		Booleans          string `json:"booleans"`
		UTF8String        string `json:"utf8String"`
		Bytes             string `json:"bytes"`
		UUID              string `json:"uuid"`
		IntArray          string `json:"intArray"`
		NullableNull      string `json:"nullableNull"`
		NullableString    string `json:"nullableString"`
	} `json:"primitives"`
	Columnar string `json:"columnar"`
}

func loadCodecProtocolFixture(t *testing.T) codecProtocolFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture codecProtocolFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 {
		t.Fatalf("fixture version = %d, want 1", fixture.Version)
	}
	return fixture
}

func protocolFixtureBytes(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProtocolConformanceInt64(t *testing.T) {
	fixture := loadCodecProtocolFixture(t)
	for _, item := range fixture.Int64Cases {
		t.Run(item.Value, func(t *testing.T) {
			value, err := strconv.ParseInt(item.Value, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			var encoder Encoder
			encoder.WriteInt(value)
			if got := hex.EncodeToString(encoder.Bytes()); got != item.Wire {
				t.Fatalf("wire = %s, want %s", got, item.Wire)
			}
			wire := protocolFixtureBytes(t, item.Wire)
			decoder := NewDecoder(wire)
			if got := decoder.ReadInt(); got != value || decoder.Err() != nil {
				t.Fatalf("value = %d, error = %v", got, decoder.Err())
			}
			column := NewColumnarReader(append(append([]byte{1, 0, 1}, wire...), 0))
			if !column.NextColumn() || !slices.Equal(column.ReadColumnInt(), []int64{value}) || column.Err() != nil {
				t.Fatalf("invalid int64 column: %v", column.Err())
			}
			checkProtocolInt64Containers(t, wire, value)
		})
	}
}

func TestProtocolConformanceInvalidInt64(t *testing.T) {
	fixture := loadCodecProtocolFixture(t)
	for _, wire := range fixture.InvalidInt64 {
		t.Run("invalid/"+wire, func(t *testing.T) {
			data := protocolFixtureBytes(t, wire)
			decoder := NewDecoder(data)
			decoder.ReadInt()
			if decoder.Err() == nil {
				t.Fatal("accepted invalid int64")
			}
			column := NewColumnarReader(append([]byte{1, 0, 1}, data...))
			column.NextColumn()
			column.ReadColumnInt()
			if column.Err() == nil {
				t.Fatal("accepted invalid int64 column")
			}
		})
	}
}

func checkProtocolInt64Containers(t *testing.T, wire []byte, value int64) {
	t.Helper()
	data := append([]byte{1}, wire...)
	decoder := NewDecoder(data)
	if got := decoder.ReadIntPtr(); got == nil || *got != value || decoder.Err() != nil {
		t.Fatalf("invalid nullable int64: %v", decoder.Err())
	}
	decoder = NewDecoder(data)
	if got := decoder.ReadIntArray(); !slices.Equal(got, []int64{value}) || decoder.Err() != nil {
		t.Fatalf("invalid int64 array: %v", decoder.Err())
	}
	column := NewColumnarReader(append([]byte{1, 0, 1}, data...))
	column.NextColumn()
	got := column.ReadColumnIntPtr()
	if len(got) != 1 || got[0] == nil || *got[0] != value || column.Err() != nil {
		t.Fatalf("invalid nullable int64 column: %v", column.Err())
	}
}

func TestVarintTenthByteMatchesStandardLibrary(t *testing.T) {
	for last := range 256 {
		wire := append(bytes.Repeat([]byte{0xff}, 9), byte(last))
		want, wantN := binary.Uvarint(wire)
		got, n := ReadVarint(wire, 0)
		if got != want || (wantN > 0 && n != wantN) || (wantN < 0 && n >= 0) {
			t.Fatalf("last byte %x: value/count = %d/%d, want %d/%d", last, got, n, want, wantN)
		}
	}
}

func FuzzProtocolVarint(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add(bytes.Repeat([]byte{0x80}, 10))
	f.Add(append(bytes.Repeat([]byte{0xff}, 9), 1))
	f.Add(append(bytes.Repeat([]byte{0xff}, 9), 2))
	f.Fuzz(func(t *testing.T, wire []byte) {
		want, wantN := binary.Uvarint(wire)
		got, n := ReadVarint(wire, 0)
		// The standard library reports ten continuation bytes as truncated;
		// Luxo rejects them as overflow. Both must reject without consuming.
		if (wantN > 0 && (got != want || n != wantN)) || (wantN <= 0 && n > 0) {
			t.Fatalf("wire %x: value/count = %d/%d, want %d/%d", wire, got, n, want, wantN)
		}
	})
}

func BenchmarkProtocolInt64(b *testing.B) {
	for _, value := range []int64{0, 127, 1 << 32, 1<<53 + 1, 1<<63 - 1, -1 << 63} {
		b.Run(strconv.FormatInt(value, 10), func(b *testing.B) {
			wire := AppendSvarint(nil, value)
			b.ReportAllocs()
			for b.Loop() {
				if got, n := ReadSvarint(wire, 0); got != value || n != len(wire) {
					b.Fatal("incorrect int64 decode")
				}
			}
		})
	}
}

func TestProtocolConformancePrimitiveEncodings(t *testing.T) {
	fixture := loadCodecProtocolFixture(t)
	tests := []struct {
		name  string
		want  string
		write func(*Encoder)
	}{
		{name: "varint", want: fixture.Primitives.Varint300, write: func(e *Encoder) { e.WriteVarint(300) }},
		{name: "svarint", want: fixture.Primitives.SvarintNegative42, write: func(e *Encoder) { e.WriteInt(-42) }},
		{name: "fixed64", want: fixture.Primitives.Fixed64OnePoint25, write: func(e *Encoder) { e.WriteFloat(1.25) }},
		{name: "booleans", want: fixture.Primitives.Booleans, write: func(e *Encoder) { e.WriteBool(true); e.WriteBool(false) }},
		{name: "utf8 string", want: fixture.Primitives.UTF8String, write: func(e *Encoder) { e.WriteString("Luxo世界") }},
		{name: "bytes", want: fixture.Primitives.Bytes, write: func(e *Encoder) { e.WriteBytes([]byte{0, 0xff, 0x10}) }},
		{name: "uuid", want: fixture.Primitives.UUID, write: writeProtocolFixtureUUID},
		{name: "int array", want: fixture.Primitives.IntArray, write: writeProtocolFixtureIntArray},
		{name: "nullable null", want: fixture.Primitives.NullableNull, write: func(e *Encoder) { e.WriteNull() }},
		{name: "nullable string", want: fixture.Primitives.NullableString, write: func(e *Encoder) { e.WritePresent(); e.WriteString("Luxo") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var encoder Encoder
			test.write(&encoder)
			if got := hex.EncodeToString(encoder.Bytes()); got != test.want {
				t.Fatalf("wire = %s, want %s", got, test.want)
			}
		})
	}
}

func writeProtocolFixtureUUID(encoder *Encoder) {
	encoder.WriteUUID([16]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef})
}

func writeProtocolFixtureIntArray(encoder *Encoder) {
	encoder.WriteArrayHeader(3)
	encoder.WriteInt(-3)
	encoder.WriteInt(0)
	encoder.WriteInt(9)
}

func TestProtocolConformanceColumnarEncoding(t *testing.T) {
	fixture := loadCodecProtocolFixture(t)
	trueValue, falseValue := true, false
	var writer ColumnarWriter
	writer.SetCount(3)
	writer.WriteColumnInt(1, []int64{-3, 0, 9})
	writer.WriteColumnString(2, []string{"a", "世界", ""})
	writer.WriteColumnBoolPtr(3, []*bool{&trueValue, nil, &falseValue})
	want := protocolFixtureBytes(t, fixture.Columnar)
	data, err := writer.Bytes()
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("columnar wire = %x, %v; want %x", data, err, want)
	}

	reader := NewColumnarReader(want)
	if reader.Count() != 3 || reader.ArenaSize() != 7 {
		t.Fatalf("columnar header = count %d, arena %d", reader.Count(), reader.ArenaSize())
	}
	if !reader.NextColumn() || reader.FieldID() != 1 || !slices.Equal(reader.ReadColumnInt(), []int64{-3, 0, 9}) {
		t.Fatal("invalid integer column")
	}
	if !reader.NextColumn() || reader.FieldID() != 2 || !slices.Equal(reader.ReadColumnString(), []string{"a", "世界", ""}) {
		t.Fatal("invalid string column")
	}
	if !reader.NextColumn() || reader.FieldID() != 3 {
		t.Fatal("missing nullable boolean column")
	}
	values := reader.ReadColumnBoolPtr()
	if len(values) != 3 || values[0] == nil || !*values[0] || values[1] != nil || values[2] == nil || *values[2] {
		t.Fatalf("nullable boolean column = %v", values)
	}
	if reader.NextColumn() || reader.Err() != nil {
		t.Fatalf("columnar trailer error = %v", reader.Err())
	}
}
