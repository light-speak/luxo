package luvia

import (
	"bytes"
	"math"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestGroupedResponseRejectsIncompleteEnvelope(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		response := resolvedResponseFixture(t, 2, 1, nullable)
		for length := range len(response) {
			if got := ParseGroupedResponse(response[:length], nullable); got != nil {
				t.Fatalf("accepted %d/%d bytes", length, len(response))
			}
		}
		for _, extra := range [][]byte{{0}, {1}, {0xff, 0xff}} {
			if ParseGroupedResponse(append(bytes.Clone(response), extra...), nullable) != nil {
				t.Fatal("accepted trailing bytes")
			}
		}
	}
}

func TestGroupedResponseCountsAreBoundedBeforeAllocation(t *testing.T) {
	for _, count := range []uint64{codec.MaxArrayElements + 1, 1 << 32, 1 << 63, math.MaxUint64} {
		for _, nullable := range []bool{false, true} {
			if ParseGroupedResponse(codec.AppendVarint(nil, count), nullable) != nil {
				t.Fatal("unbounded key count")
			}
			response := []byte{1}
			if nullable {
				response = append(response, 1)
			}
			response = codec.AppendVarint(response, count)
			if ParseGroupedResponse(response, nullable) != nil {
				t.Fatal("length exceeds response")
			}
		}
	}
	response := codec.AppendVarint(nil, codec.MaxArrayElements)
	if allocations := testing.AllocsPerRun(100, func() {
		if ParseGroupedResponse(response, false) != nil {
			t.Fatal("missing groups accepted")
		}
	}); allocations != 0 {
		t.Fatalf("allocated before count validation: %g", allocations)
	}
}

func TestGroupedResponsePreservesExactBlobs(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		for _, items := range []int{0, 1, 2, 127, 128, 16384} {
			response := resolvedResponseFixture(t, 2, items, nullable)
			before := bytes.Clone(response)
			blobs := ParseGroupedResponse(response, nullable)
			if len(blobs) != 2 || !bytes.Equal(response, before) {
				t.Fatal("changed input or group count")
			}
			want := resolvedValueFixture(t, items, nullable)
			for _, blob := range blobs {
				if !bytes.Equal(blob, want) {
					t.Fatal("changed canonical value bytes")
				}
			}
		}
		if blobs := ParseGroupedResponse([]byte{0}, nullable); blobs == nil || len(blobs) != 0 {
			t.Fatal("zero groups confused with malformed response")
		}
	}
}

func TestGroupedResponseUsesOnlyIndexAllocation(t *testing.T) {
	for _, items := range []int{0, 1, 2, 128} {
		response := resolvedResponseFixture(t, 2, items, true)
		allocations := testing.AllocsPerRun(100, func() {
			if len(ParseGroupedResponse(response, true)) != 2 {
				t.Fatal("invalid fixture")
			}
		})
		if allocations != 1 {
			t.Fatalf("expected one group index allocation, got %g", allocations)
		}
	}
}

func TestGroupedResponseAcceptsMaximumKeyCount(t *testing.T) {
	response := codec.AppendVarint(nil, codec.MaxArrayElements)
	response = append(response, make([]byte, codec.MaxArrayElements)...)
	if result := ParseGroupedResponse(response, true); len(result) != codec.MaxArrayElements {
		t.Fatal("rejected maximum null-group count")
	}
}

func TestGroupedResponseRejectsEmptyRequiredPayload(t *testing.T) {
	for _, data := range [][]byte{{1, 0}, {1, 0x80}, {1, 1}, {1, 0x80, 0}} {
		if ParseGroupedResponse(data, false) != nil {
			t.Fatalf("accepted invalid required value: %x", data)
		}
	}
}

func resolvedResponseFixture(t testing.TB, keys, items int, nullable bool) []byte {
	response := codec.AppendVarint(nil, uint64(keys))
	value := resolvedValueFixture(t, items, nullable)
	for range keys {
		response = append(response, value...)
	}
	return response
}

func resolvedValueFixture(t testing.TB, items int, nullable bool) []byte {
	var writer codec.ColumnarWriter
	writer.SetCount(items)
	if items != 0 {
		values := make([]int64, items)
		for i := range values {
			values[i] = math.MaxInt64 - int64(i)
		}
		writer.WriteColumnInt(1, values)
	}
	var value []byte
	if nullable {
		value = append(value, 1)
	}
	return codec.AppendBytes(value, testutil.Bytes(t)(writer.Bytes()))
}
