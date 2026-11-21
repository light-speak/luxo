//go:build !race

package i18n

import (
	"math"
	"testing"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// Race instrumentation deliberately discards sync.Pool entries. Allocation
// gates run without instrumentation; the functional tests still run with race.
func TestSubstituteAllocations(t *testing.T) {
	var data Data = luxerrors.ResourceError{Resource: "User", ID: luxerrors.IntID(math.MaxInt64)}
	for _, tc := range []struct {
		template string
		want     string
		allocs   float64
	}{
		{"Resource not found", "Resource not found", 0},
		{"${resource}:${id}:${missing}", "User:9223372036854775807:${missing}", 1},
		{"${missing}", "${missing}", 0},
		{"${unclosed", "${unclosed", 0},
	} {
		t.Run(tc.template, func(t *testing.T) {
			var output string
			allocs := testing.AllocsPerRun(100, func() { output = substitute(tc.template, data) })
			if output != tc.want {
				t.Fatalf("output = %q, want %q", output, tc.want)
			}
			if allocs > tc.allocs {
				t.Fatalf("allocs = %v, want <= %v after warmup", allocs, tc.allocs)
			}
		})
	}
}
