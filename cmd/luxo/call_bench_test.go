package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkCLIJSONInference(b *testing.B) {
	for _, raw := range []string{"42", "1.25", "true", "hello"} {
		before, _ := json.Marshal(previousCLIInference(raw))
		after, _ := json.Marshal(inferType(raw))
		if string(before) != string(after) {
			b.Fatal("non-equivalent benchmark input")
		}
		for _, side := range []struct {
			name  string
			parse func(string) any
		}{
			{"before", previousCLIInference}, {"after", inferType},
		} {
			b.Run(raw+"/"+side.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					data, err := json.Marshal(side.parse(raw))
					if err != nil || len(data) == 0 {
						b.Fatal("invalid benchmark output", err)
					}
				}
			})
		}
	}
}

// Frozen CLI inference before preserving complete JSON tokens.
func previousCLIInference(value string) any {
	var integer int64
	if _, err := fmt.Sscanf(value, "%d", &integer); err == nil && fmt.Sprintf("%d", integer) == value {
		return integer
	}
	var floating float64
	if _, err := fmt.Sscanf(value, "%g", &floating); err == nil {
		return floating
	}
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	return value
}
