package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

type apiProtocolFixture struct {
	Version  int `json:"version"`
	Requests struct {
		AllTypes string `json:"allTypes"`
		Nullable string `json:"nullable"`
		Selected string `json:"selected"`
	} `json:"requests"`
	ErrorEnvelope string `json:"errorEnvelope"`
	Frames        struct {
		CallRequest      string `json:"callRequest"`
		SubscribeSuccess string `json:"subscribeSuccess"`
	} `json:"frames"`
}

func loadAPIProtocolFixture(t *testing.T) apiProtocolFixture {
	t.Helper()
	data, err := os.ReadFile("../codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture apiProtocolFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 {
		t.Fatalf("fixture version = %d, want 1", fixture.Version)
	}
	return fixture
}

func decodeAPIProtocolHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProtocolConformanceRequests(t *testing.T) {
	fixture := loadAPIProtocolFixture(t)
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{name: "all types", got: encodeProtocolFixtureAllTypes(t), want: fixture.Requests.AllTypes},
		{name: "nullable", got: encodeProtocolFixtureNullable(t), want: fixture.Requests.Nullable},
		{name: "recursive selection", got: encodeProtocolFixtureSelection(t), want: fixture.Requests.Selected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hex.EncodeToString(test.got); got != test.want {
				t.Fatalf("wire = %s, want %s", got, test.want)
			}
		})
	}
}

func encodeProtocolFixtureAllTypes(t *testing.T) []byte {
	t.Helper()
	meta := []ParamMeta{
		{Name: "integer", Type: "Int", FieldID: 1}, {Name: "duration", Type: "Duration", FieldID: 2},
		{Name: "float", Type: "Float", FieldID: 3}, {Name: "text", Type: "String", FieldID: 4},
		{Name: "status", Type: "Enum", FieldID: 5}, {Name: "decimal", Type: "Decimal", FieldID: 6},
		{Name: "active", Type: "Boolean", FieldID: 7}, {Name: "createdAt", Type: "DateTime", FieldID: 8},
		{Name: "id", Type: "UUID", FieldID: 9}, {Name: "blob", Type: "Bytes", FieldID: 10},
		{Name: "metadata", Type: "JSON", FieldID: 11}, {Name: "tags", Type: "String", FieldID: 12, IsList: true},
	}
	params := map[string]any{
		"integer": int64(-3), "duration": int64(9), "float": 1.25, "text": "Luxo世界",
		"status": "OPEN", "decimal": "12.50", "active": true, "createdAt": "1970-01-01T00:01:00Z",
		"id": "01234567-89ab-cdef-0123-456789abcdef", "blob": []byte{0, 0xff},
		"metadata": json.RawMessage(`{"ok":true}`), "tags": []string{"a", "世界"},
	}
	data, err := EncodeBinaryRequest(300, nil, params, meta)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeProtocolFixtureNullable(t *testing.T) []byte {
	t.Helper()
	data, err := EncodeBinaryRequest(9, nil, map[string]any{"nickname": nil, "age": int64(42)}, []ParamMeta{
		{Name: "nickname", Type: "String", FieldID: 1, Nullable: true},
		{Name: "age", Type: "Int", FieldID: 2, Nullable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeProtocolFixtureSelection(t *testing.T) []byte {
	t.Helper()
	child := codec.AppendSelectionMask(nil, codec.FieldMaskSet(nil, 2), nil)
	fields := codec.FieldMaskSet(nil, 1)
	fields = codec.FieldMaskSet(fields, 3)
	mask := codec.AppendSelectionMask(nil, fields, []codec.SelectionMaskChild{{FieldID: 3, Mask: child}})
	data, err := EncodeBinaryRequest(7, mask, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProtocolConformanceErrorAndFrames(t *testing.T) {
	fixture := loadAPIProtocolFixture(t)
	errorWire := appendBinaryError(nil, wireError{
		Code: 400, Name: "BadRequest", Message: "bad", TraceID: "t", Data: []byte(`{}`), Cause: "c",
	})
	if got := hex.EncodeToString(errorWire); got != fixture.ErrorEnvelope {
		t.Fatalf("error wire = %s, want %s", got, fixture.ErrorEnvelope)
	}
	decoded, err := DecodeBinaryError(decodeAPIProtocolHex(t, fixture.ErrorEnvelope), 500)
	if err != nil || decoded.Code != 400 || decoded.Name != "BadRequest" || decoded.TraceID != "t" {
		t.Fatalf("decoded error = %+v, error = %v", decoded, err)
	}

	call := []byte{BinaryFrameCallRequest}
	call = codec.AppendVarint(call, 253)
	call = append(call, 7, 0, 0)
	if !bytes.Equal(call, decodeAPIProtocolHex(t, fixture.Frames.CallRequest)) {
		t.Fatalf("call frame = %x", call)
	}
	ack := codec.AppendVarint([]byte{BinaryFrameSubscribeSuccess}, 253)
	if !bytes.Equal(ack, decodeAPIProtocolHex(t, fixture.Frames.SubscribeSuccess)) {
		t.Fatalf("subscription acknowledgement = %x", ack)
	}
}
