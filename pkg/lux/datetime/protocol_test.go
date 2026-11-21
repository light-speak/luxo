package datetime

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestParseRFC3339SharedBoundaries(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Input     string
			Canonical *string
		} `json:"dateTimeBoundaryCases"`
	}
	data, err := os.ReadFile("../codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 34 {
		t.Fatal("missing shared cases")
	}
	for _, tc := range fixture.Cases {
		parsed, err := ParseRFC3339(tc.Input)
		if tc.Canonical == nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted %q: %v", tc.Input, parsed)
			}
		} else if err != nil || parsed.Format(time.RFC3339Nano) != *tc.Canonical {
			t.Fatal(tc.Input, parsed, err)
		}
	}
}

func TestParseRFC3339ShapeRejections(t *testing.T) {
	for _, input := range []string{
		"", "2000-01-01T00:00:00.123", "2000-01-01T00:00:00.1234567890+00:00",
		"2000-01-01t00:00:00+00:00", "2000-01-01T00:00:00Z",
	} {
		for length := range len(input) {
			if _, err := ParseRFC3339(input[:length]); err == nil {
				t.Fatalf("accepted prefix %q", input[:length])
			}
		}
	}
	const valid = "2000-02-29T12:34:56.123456789+01:00"
	for index := range len(valid) {
		value := []byte(valid)
		value[index] = '!'
		if _, err := ParseRFC3339(string(value)); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if _, err := ParseRFC3339("2000-01-01t00:00:00+00:00"); err != nil {
		t.Fatal(err)
	}
}

func FuzzParseRFC3339(f *testing.F) {
	for _, input := range []string{"0000-01-01T00:00:00Z", "9999-12-31T23:59:59.999999999Z", "2000-02-29t00:00:00+01:00", "2000-01-01T00:00:00.1234567890Z"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := ParseRFC3339(input)
		if err != nil {
			return
		}
		if !ValidSeconds(value.Unix()) || value.Location() != time.UTC {
			t.Fatal(value)
		}
		again, err := ParseRFC3339(value.Format(time.RFC3339Nano))
		if err != nil || !again.Equal(value) {
			t.Fatal(value, again, err)
		}
	})
}
