//go:build !race

package codec

import (
	"bytes"
	"testing"
)

// The standard JSON validator pools parser state; race deliberately discards
// sync.Pool entries. Only this warm allocation assertion excludes instrumentation;
// all correctness, aliasing and malformed-input tests also run under race.
func TestJSONWarmAllocation(t *testing.T) {
	raw := []byte(`{"n":123456789012345678901234567890,"f":1e-9999}`)
	encoder := Encoder{buf: make([]byte, 0, 4096)}
	if allocations := testing.AllocsPerRun(100, func() {
		encoder.Reset()
		if err := encoder.WriteJSON(raw); err != nil {
			t.Fatal(err)
		}
		decoder := NewDecoder(encoder.Bytes())
		if !bytes.Equal(decoder.ReadJSON(), raw) || decoder.Finish() != nil {
			t.Fatal("roundtrip failed")
		}
	}); allocations != 0 {
		t.Fatalf("warm raw JSON roundtrip allocated: %g", allocations)
	}
}
