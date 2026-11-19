package jsonutil

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckJSONDepthBudget(t *testing.T) {
	for _, containers := range [][2]string{{"[", "]"}, {`{"x":`, "}"}} {
		for _, depth := range []int{0, 128, 129} {
			raw := []byte(strings.Repeat(containers[0], depth) + "0" + strings.Repeat(containers[1], depth))
			err := CheckJSONDepth(raw)
			if depth <= 128 && err != nil || depth > 128 && !errors.Is(err, errJSONDepth) {
				t.Fatalf("depth=%d: %v", depth, err)
			}
		}
	}
	for _, raw := range [][]byte{nil, []byte(`"` + strings.Repeat("[", 1000) + `"`), []byte(" true ")} {
		if err := CheckJSONDepth(raw); err != nil {
			t.Fatal(err)
		}
	}
}
