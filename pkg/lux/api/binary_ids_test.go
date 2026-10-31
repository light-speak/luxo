package api

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinaryRequestRejectsIncompleteIdentifierHeaders(t *testing.T) {
	registry := NewAPIRegistry()
	registry.Register("read", 1)
	for _, item := range []struct {
		name, message string
		body          []byte
	}{
		{"api varint", "invalid API ID varint", []byte{0x80}},
		{"mask length", "invalid field mask length", []byte{1}},
		{"mask body", "field mask exceeds body", []byte{1, 3, 0, 0}},
		{"parameter varint", "param field ID varint overflow", append([]byte{1, 0}, bytes.Repeat([]byte{0xff}, 10)...)},
		{"parameter truncation", "truncated param field ID", []byte{1, 0, 0x80}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if req, err := registry.ParseBinaryRequest(item.body); req != nil || err == nil || !strings.Contains(err.Error(), item.message) {
				t.Fatalf("request=%v, error=%v; want %s", req, err, item.message)
			}
		})
	}
}

func TestBinaryRequestRejectsOutOfRangeRouteIDs(t *testing.T) {
	for _, id := range []uint64{0, codec.MaxAPIID + 1, 1<<32 | 1, math.MaxUint64} {
		registry := NewAPIRegistry()
		registry.Register("read", 1)
		// Even corrupted startup metadata must not make an invalid wire ID
		// routable. This bypass stays private to the boundary regression test.
		registry.idToName[int(id)] = "invalid"
		body := append(codec.AppendVarint(nil, id), 0, 0)
		if req, err := registry.ParseBinaryRequest(body); err == nil || req != nil || !strings.Contains(err.Error(), "outside 1..") {
			t.Fatalf("invalid route ID %d was accepted", id)
		}
		// A wide wire ID can alias a valid Go int on 32-bit targets. The public
		// encoder accepts an int, so only test values still invalid after conversion.
		encodedID := int(id)
		if encodedID > 0 && uint64(encodedID) <= codec.MaxAPIID {
			continue
		}
		if body, err := EncodeBinaryRequest(encodedID, nil, nil, nil); err == nil || body != nil {
			t.Fatalf("invalid route ID %d was encoded", id)
		}
	}
}

func TestBinaryRequestRejectsWideParameterIDsBeforeNarrowing(t *testing.T) {
	for _, id := range []uint64{codec.SortersFieldID + 1, 1<<32 | 1, math.MaxUint64} {
		registry := NewAPIRegistry()
		registry.Register("read", 1)
		registry.RegisterParams("read", []ParamMeta{{Name: "value", Type: "Int", FieldID: 1}})
		registry.paramOrder["read"][0].FieldID = int(id)
		body := codec.AppendVarint([]byte{1, 0}, id)
		body = append(codec.AppendSvarint(body, 7), 0)
		if req, err := registry.ParseBinaryRequest(body); err == nil || req != nil || !strings.Contains(err.Error(), "exceeds protocol limit") {
			t.Fatalf("wide parameter ID %d was narrowed or accepted", id)
		}
	}
}

func TestBinaryRequestRejectsInvalidOmittedParameterIDs(t *testing.T) {
	for _, id := range []int{-1, 0, codec.FiltersFieldID, codec.SortersFieldID} {
		meta := []ParamMeta{{Name: "optional", Type: "Int", FieldID: id}}
		if body, err := EncodeBinaryRequest(1, nil, nil, meta); err == nil || body != nil {
			t.Fatalf("invalid omitted parameter ID %d accepted", id)
		}
	}
}

func TestBinaryRequestClassifiedMaxima(t *testing.T) {
	registry := NewAPIRegistry()
	registry.Register("read", codec.MaxAPIID)
	meta := []ParamMeta{{Name: "value", Type: "Int", FieldID: codec.MaxParameterID}}
	registry.RegisterParams("read", meta)
	body, err := EncodeBinaryRequest(codec.MaxAPIID, nil, map[string]any{"value": int64(7)}, meta)
	if err != nil {
		t.Fatal(err)
	}
	req, err := registry.ParseBinaryRequest(body)
	if err != nil || req == nil || req.API != "read" {
		t.Fatalf("valid maximum rejected: %v", err)
	}
	value, err := req.ParamInt("value")
	if err != nil || value != 7 {
		t.Fatalf("parameter value changed: %d / %v", value, err)
	}
}
