//go:build !race

package rpc

import "testing"

// Race deliberately discards pool entries. Allocation gates measure warm
// reuse; the functional and ownership regressions also run with race.
func TestRPCErrorDataReaderAllocations(t *testing.T) {
	data := ownRPCErrorData([]byte(rpcPrecisionJSON), []byte(`{"label":"精度 café"}`))
	for _, tc := range []struct {
		field string
		limit float64
	}{
		{"label", 2}, {"number", 5}, {"items", 5}, {"absent", 5},
	} {
		t.Run(tc.field, func(t *testing.T) {
			dst := make([]byte, 0, 1024)
			allocs := testing.AllocsPerRun(100, func() { dst, _ = data.AppendText(dst[:0], tc.field) })
			if allocs > tc.limit {
				t.Fatalf("warm lookup allocated %v times, want <= %v", allocs, tc.limit)
			}
		})
	}
}
