package codec

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

type codecProtocolFixture struct {
	Version    int `json:"version"`
	Primitives struct {
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
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("columnar wire = %x, want %x", writer.Bytes(), want)
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
