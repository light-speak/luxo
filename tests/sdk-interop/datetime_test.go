package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestFixtureDateTimeNanoseconds(t *testing.T) {
	t.Chdir("../..")
	raw, err := os.ReadFile("pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		DateTimeCases []struct {
			Input, Seconds, Canonical string
			Nanoseconds               int
		}
		DateTimeBoundaryCases []struct {
			Input     string
			Canonical *string
		}
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.DateTimeCases) == 0 {
		t.Fatal("missing DateTime corpus")
	}
	for _, gateway := range []bool{false, true} {
		err := withFixtureMode(gateway, func(endpoint, _ string) error {
			for _, tc := range corpus.DateTimeCases {
				parsed, err := time.Parse(time.RFC3339Nano, tc.Input)
				if err != nil || strconv.FormatInt(parsed.Unix(), 10) != tc.Seconds || parsed.Nanosecond() != tc.Nanoseconds || parsed.UTC().Format(time.RFC3339Nano) != tc.Canonical {
					return fmt.Errorf("invalid DateTime golden %s: %v", tc.Input, err)
				}
				if err := verifyFixtureTimestamp(endpoint, tc.Input, tc.Canonical); err != nil {
					return err
				}
			}
			for _, tc := range corpus.DateTimeBoundaryCases {
				if tc.Canonical != nil {
					if err := verifyFixtureTimestamp(endpoint, tc.Input, *tc.Canonical); err != nil {
						return err
					}
				} else {
					status, body, err := fixtureJSONRequest(endpoint, `{"$api":"echoTimestamp","value":`+strconv.Quote(tc.Input)+`}`)
					if err != nil {
						return err
					}
					if status != 400 {
						return fmt.Errorf("invalid DateTime %q returned %d %s", tc.Input, status, body)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("gateway=%t: %v", gateway, err)
		}
	}
}

func verifyFixtureTimestamp(endpoint, input, canonical string) error {
	status, body, err := fixtureJSONRequest(endpoint, `{"$api":"echoTimestamp","value":`+strconv.Quote(input)+`}`)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("DateTime %s returned %d %s", input, status, body)
	}
	var response struct{ Data string }
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Data != canonical {
		return fmt.Errorf("DateTime %s returned %d %s, want %s", input, status, body, canonical)
	}
	return nil
}
