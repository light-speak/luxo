package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"testing"
)

type numericFixtureCase struct {
	Token              string
	Int64, Float64Bits *string
}

type numericFixtureRequest struct {
	name, body, field string
	rows, array       bool
	count             int
	float             bool
}

func TestFixtureRawJSONNumericCorpus(t *testing.T) {
	t.Chdir("../..")
	cases := readNumericFixtureCases(t)
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%t", gateway), func(t *testing.T) {
			err := withFixtureMode(gateway, func(endpoint, _ string) error {
				t.Run("http", func(t *testing.T) {
					verifyNumericFixtureCases(t, func(body string) (int, []byte, error) {
						return fixtureJSONRequest(endpoint, body)
					}, cases)
				})
				t.Run("websocket", func(t *testing.T) {
					verify := func(request fixtureJSONRequester) error {
						verifyNumericFixtureCases(t, request, cases)
						return nil
					}
					if err := withFixtureWSJSONRequests(t.Context(), endpoint, verify); err != nil {
						t.Fatal(err)
					}
				})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readNumericFixtureCases(t *testing.T) []numericFixtureCase {
	t.Helper()
	raw, err := os.ReadFile("pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct{ JSONNumberConversions []numericFixtureCase }
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.JSONNumberConversions) == 0 {
		t.Fatal("missing numeric conversion corpus")
	}
	return corpus.JSONNumberConversions
}

func verifyNumericFixtureCases(t *testing.T, request fixtureJSONRequester, cases []numericFixtureCase) {
	t.Helper()
	for _, value := range cases {
		for _, shape := range numericFixtureRequests(value.Token) {
			t.Run(shape.name+"/"+value.Token, func(t *testing.T) {
				status, body, err := request(shape.body)
				if err != nil {
					t.Fatal(err)
				}
				want := value.Int64
				if shape.float {
					want = value.Float64Bits
				}
				verifyNumericFixtureResponse(t, status, body, shape, want)
				if want == nil {
					// Rejection must not prevent a following call; WS reuses the same socket.
					status, body, err = request(`{"$api":"echoInteger","value":1}`)
					if err != nil {
						t.Fatal(err)
					}
					one := "1"
					verifyNumericFixtureResponse(t, status, body, numericFixtureRequest{count: 1}, &one)
				}
			})
		}
	}
}

func numericFixtureRequests(token string) []numericFixtureRequest {
	precision := `"input":{"value":` + token + `,"duration":0,"optional":null,"values":[],"label":"numeric","ignored":0},"$select":"value"`
	floats := `"input":{"floats":[` + token + `],"tail":7},"$select":"floats"`
	return []numericFixtureRequest{
		{name: "Int", body: `{"$api":"echoInteger","value":` + token + `}`, count: 1},
		{name: "Duration", body: `{"$api":"echoDuration","value":` + token + `}`, count: 1},
		{name: "IntList", body: `{"$api":"echoIntegers","values":[` + token + `]}`, array: true, count: 1},
		{name: "IntObject", body: `{"$api":"echoPrecision",` + precision + `}`, field: "value", count: 1},
		{name: "IntObjects", body: `{"$api":"listPrecision",` + precision + `}`, field: "value", rows: true, count: 2},
		{name: "Float", body: `{"$api":"scaleFloat","value":` + token + `,"factor":1}`, float: true, count: 1},
		{name: "FloatList", body: `{"$api":"scaleFloatList","value":` + token + `,"factor":1}`, float: true, array: true, count: 2},
		{name: "FloatObject", body: `{"$api":"echoScalarArrays",` + floats + `}`, float: true, field: "floats", array: true, count: 1},
		{name: "FloatObjects", body: `{"$api":"listScalarArrays",` + floats + `}`, float: true, field: "floats", rows: true, array: true, count: 2},
	}
}

func verifyNumericFixtureResponse(t *testing.T, status int, body []byte, shape numericFixtureRequest, want *string) {
	t.Helper()
	var response struct {
		Data           json.RawMessage
		Error, TraceID string
		Code           int
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if status != http.StatusBadRequest || response.Error != "BadRequest" || response.Code != 400 || response.TraceID == "" || len(response.Data) != 0 {
			t.Fatalf("invalid numeric input did not produce a clean BadRequest: %d %s", status, body)
		}
		return
	}
	if status != http.StatusOK || response.Error != "" || len(response.Data) == 0 {
		t.Fatalf("valid numeric input failed: %d %s", status, body)
	}
	values := numericFixtureValues(t, response.Data, shape)
	if len(values) != shape.count {
		t.Fatalf("got %d values, want %d: %s", len(values), shape.count, body)
	}
	for _, raw := range values {
		if !shape.float {
			if string(raw) != *want {
				t.Fatalf("integer = %s, want %s", raw, *want)
			}
			continue
		}
		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || fmt.Sprintf("%016x", math.Float64bits(value)) != *want {
			t.Fatalf("Float = %s (bits %016x), want bits %s: %v", raw, math.Float64bits(value), *want, err)
		}
	}
}

func numericFixtureValues(t *testing.T, data json.RawMessage, shape numericFixtureRequest) []json.RawMessage {
	t.Helper()
	rows := []json.RawMessage{data}
	if shape.rows {
		if err := json.Unmarshal(data, &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want two", len(rows))
		}
	}
	var values []json.RawMessage
	for _, row := range rows {
		if shape.field != "" {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(row, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 1 || fields[shape.field] == nil {
				t.Fatalf("unexpected selected fields: %s", row)
			}
			row = fields[shape.field]
		}
		if shape.array {
			var items []json.RawMessage
			if err := json.Unmarshal(row, &items); err != nil {
				t.Fatal(err)
			}
			values = append(values, items...)
		} else {
			values = append(values, row)
		}
	}
	return values
}
