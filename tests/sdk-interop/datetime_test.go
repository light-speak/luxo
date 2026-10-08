package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

type dateTimeFixtureCorpus struct {
	DateTimeCases []struct {
		Input, Seconds, Canonical string
		Nanoseconds               int
	}
	DateTimeBoundaryCases []struct {
		Input     string
		Canonical *string
	}
}

func readDateTimeFixtureCorpus(t *testing.T) dateTimeFixtureCorpus {
	t.Helper()
	raw, err := os.ReadFile("pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus dateTimeFixtureCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.DateTimeCases) == 0 || len(corpus.DateTimeBoundaryCases) == 0 {
		t.Fatal("missing DateTime corpus")
	}
	return corpus
}

func TestFixtureDateTimeNanoseconds(t *testing.T) {
	t.Chdir("../..")
	corpus := readDateTimeFixtureCorpus(t)
	for _, gateway := range []bool{false, true} {
		err := withFixtureMode(gateway, func(endpoint, _ string) error {
			request := func(body string) (int, []byte, error) { return fixtureJSONRequest(endpoint, body) }
			if err := verifyFixtureTimestamps(request, corpus); err != nil {
				return fmt.Errorf("HTTP: %w", err)
			}
			return withFixtureWSJSONRequests(t.Context(), endpoint, func(request fixtureJSONRequester) error {
				if err := verifyFixtureTimestamps(request, corpus); err != nil {
					return fmt.Errorf("WebSocket: %w", err)
				}
				return nil
			})
		})
		if err != nil {
			t.Fatalf("gateway=%t: %v", gateway, err)
		}
	}
}

func verifyFixtureTimestamps(request fixtureJSONRequester, corpus dateTimeFixtureCorpus) error {
	for _, tc := range corpus.DateTimeCases {
		parsed, err := time.Parse(time.RFC3339Nano, tc.Input)
		if err != nil || strconv.FormatInt(parsed.Unix(), 10) != tc.Seconds || parsed.Nanosecond() != tc.Nanoseconds || parsed.UTC().Format(time.RFC3339Nano) != tc.Canonical {
			return fmt.Errorf("invalid DateTime golden %s: %v", tc.Input, err)
		}
		if err := verifyFixtureTimestamp(request, tc.Input, tc.Canonical); err != nil {
			return err
		}
	}
	for _, tc := range corpus.DateTimeBoundaryCases {
		if tc.Canonical != nil {
			if err := verifyFixtureTimestamp(request, tc.Input, *tc.Canonical); err != nil {
				return err
			}
			continue
		}
		status, body, err := request(`{"$api":"echoTimestamp","value":` + strconv.Quote(tc.Input) + `}`)
		if err != nil {
			return err
		}
		if status != 400 {
			return fmt.Errorf("invalid DateTime %q returned %d %s", tc.Input, status, body)
		}
		if err := verifyFixtureTimestamp(request, "1969-12-31T23:59:59.999999999Z", "1969-12-31T23:59:59.999999999Z"); err != nil {
			return fmt.Errorf("request after invalid timestamp: %w", err)
		}
	}
	return nil
}

func verifyFixtureTimestamp(request fixtureJSONRequester, input, canonical string) error {
	status, body, err := request(`{"$api":"echoTimestamp","value":` + strconv.Quote(input) + `}`)
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
