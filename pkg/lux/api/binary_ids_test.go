package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestBinarySelectionDepthWithoutSchema(t *testing.T) {
	registry := NewAPIRegistry()
	if err := registry.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{1, 31, 32, 33, 1024} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			mask, wire := binarySelectionDepthFixture(depth)
			req, parseErr := registry.ParseBinaryRequest(wire)
			encoded, encodeErr := EncodeBinaryRequest(1, mask, nil, nil)
			if depth > 32 {
				if parseErr == nil || req != nil {
					t.Error("inbound over-depth selection accepted without schema")
				}
				if encodeErr == nil || encoded != nil {
					t.Error("outbound over-depth selection encoded")
				}
			} else if parseErr != nil || encodeErr != nil || req == nil || req.API != "read" || !bytes.Equal(encoded, wire) {
				t.Fatalf("valid depth %d changed: parse=%v encode=%v", depth, parseErr, encodeErr)
			}
		})
	}
}

func TestBinaryHTTPSelectionDepthWithoutSchema(t *testing.T) {
	router := NewRouter()
	if err := router.Registry.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	router.Handle("read", func(_ context.Context, req *Request) error {
		calls.Add(1)
		req.Buf.B = append(req.Buf.B, 1)
		return nil
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 2 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	for _, depth := range []int{32, 33, 1024, 32} {
		_, wire := binarySelectionDepthFixture(depth)
		before := calls.Load()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/luvia", bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Luxo-Mode", "binary")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read=%v close=%v", readErr, closeErr)
		}
		if depth > 32 {
			if response.StatusCode != http.StatusBadRequest || calls.Load() != before {
				t.Fatalf("depth %d reached handler: status=%d calls=%d", depth, response.StatusCode, calls.Load()-before)
			}
		} else if response.StatusCode != http.StatusOK || calls.Load() != before+1 || !bytes.Equal(body, []byte{1}) {
			t.Fatalf("depth %d failed: status=%d calls=%d body=%x", depth, response.StatusCode, calls.Load()-before, body)
		}
	}
}

// Keep inbound bad-wire fixtures independent of EncodeBinaryRequest.
func binarySelectionDepthFixture(depth int) (mask, wire []byte) {
	mask = []byte{1, 1}
	for level := 1; level < depth; level++ {
		node := codec.AppendVarint([]byte{1, 1, 1}, uint64(len(mask)))
		mask = append(node, mask...)
	}
	wire = codec.AppendVarint([]byte{1}, uint64(len(mask)))
	wire = append(append(wire, mask...), 0)
	return mask, wire
}

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
