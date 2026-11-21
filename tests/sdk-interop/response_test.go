package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestFixtureErrorAndPagination(t *testing.T) {
	t.Chdir("../..")
	err := withFixture(func(endpoint, _ string) error {
		status, body, err := fixtureJSONRequest(endpoint, `{"$api":"rejectPrecision","value":9223372036854775807}`)
		if err != nil {
			return err
		}
		var rejected struct {
			Error, Message, TraceID string
			Code                    int
			Data                    struct {
				Value int64
				Label string
			}
		}
		if err := json.Unmarshal(body, &rejected); err != nil {
			return err
		}
		if status != 422 || rejected.Code != 422 || rejected.Error != "PrecisionRejected" ||
			rejected.Message != "error.precision_rejected" || rejected.TraceID == "" ||
			rejected.Data.Value != math.MaxInt64 || rejected.Data.Label != "精度 café" {
			return fmt.Errorf("unexpected error response: %d %s", status, body)
		}
		if err := verifyFixturePage(endpoint); err != nil {
			return err
		}
		return verifyFixtureListEffects(endpoint)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFixtureRequiredJSONParameters(t *testing.T) {
	t.Chdir("../..")
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%t", gateway), func(t *testing.T) {
			err := withFixtureMode(gateway, func(endpoint, _ string) error {
				return verifyRequiredJSONParameters(func(body string) (int, []byte, error) {
					return fixtureJSONRequest(endpoint, body)
				})
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFixtureRequiredWSJSONParameters(t *testing.T) {
	t.Chdir("../..")
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%t", gateway), func(t *testing.T) {
			if err := withFixtureMode(gateway, func(endpoint, _ string) error {
				return verifyWSJSONParameters(t.Context(), endpoint)
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func verifyWSJSONParameters(parent context.Context, endpoint string) error {
	return withFixtureWSJSONRequests(parent, endpoint, verifyRequiredJSONParameters)
}

type fixtureJSONRequester func(string) (int, []byte, error)

func withFixtureWSJSONRequests(parent context.Context, endpoint string, use func(fixtureJSONRequester) error) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(endpoint, "http"), nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	var sequence uint64
	return use(func(body string) (int, []byte, error) {
		sequence++
		frame := `{"$id":` + strconv.FormatUint(sequence, 10) + "," + body[1:]
		if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
			return 0, nil, err
		}
		kind, response, err := conn.Read(ctx)
		if err != nil {
			return 0, nil, err
		}
		var result struct {
			ID    uint64 `json:"$id"`
			Code  int
			Error string
		}
		if err := json.Unmarshal(response, &result); err != nil {
			return 0, nil, err
		}
		if kind != websocket.MessageText || result.ID != sequence {
			return 0, nil, fmt.Errorf("unexpected WebSocket response: %d %s", kind, response)
		}
		if result.Error == "" {
			return http.StatusOK, response, nil
		}
		return result.Code, response, nil
	})
}

func verifyRequiredJSONParameters(request fixtureJSONRequester) error {
	for _, item := range []struct{ api, param, value string }{
		{"echoInteger", "value", "null"}, {"echoString", "value", "null"},
		{"echoBoolean", "value", "null"}, {"echoTimestamp", "value", "null"},
		{"echoTimestamp", "value", "0"}, {"echoTimestamp", "value", "9007199254740993"},
		{"echoTimestamps", "values", "[0]"}, {"echoTimestamps", "values", "[9007199254740993]"},
		{"echoIntegers", "values", "null"}, {"echoIntegers", "values", "[1,null]"},
		{"echoStrings", "values", "null"}, {"echoStrings", "values", `["ok",null]`},
		{"echoUUIDs", "values", "null"}, {"echoUUIDs", "values", "[null]"},
		{"echoBytes", "value", "null"}, {"echoBytes", "value", "[1,2]"},
		{"echoBytes", "value", "[]"}, {"echoByteLists", "values", "[null]"},
		{"echoByteLists", "values", `[[1,2]]`}, {"echoByteLists", "values", `["AQI=",null]`},
		{"echoScalarArrays", "input", `{"bytes":[[1,2]]}`},
	} {
		status, body, err := request(jsonParameterFixtureBody(item.api, item.param, item.value))
		if err != nil {
			return err
		}
		var rejected struct{ Error string }
		if err := json.Unmarshal(body, &rejected); err != nil {
			return err
		}
		if status != http.StatusBadRequest || rejected.Error != "BadRequest" {
			return fmt.Errorf("%s accepted %s or returned the wrong error: %d %s", item.api, item.value, status, body)
		}
	}
	return verifyValidJSONParameters(request)
}

func verifyValidJSONParameters(request func(string) (int, []byte, error)) error {
	for _, item := range []struct{ api, param, value string }{
		{"echoInteger", "value", "9223372036854775807"}, {"echoInteger", "value", "-9223372036854775808"},
		{"echoString", "value", `"東京"`}, {"echoBoolean", "value", "false"},
		{"echoTimestamp", "value", `"1969-12-31T23:59:59Z"`},
		{"echoTimestamps", "values", `["1969-12-31T23:59:59Z","2026-09-17T01:02:03Z"]`},
		{"echoIntegers", "values", "[]"}, {"echoIntegers", "values", "[9223372036854775807,-9223372036854775808]"},
		{"echoStrings", "values", "[]"}, {"echoStrings", "values", `["東京",""]`},
		{"echoUUIDs", "values", "[]"}, {"echoUUIDs", "values", `["550e8400-e29b-41d4-a716-446655440000"]`},
		{"echoBytes", "value", `""`}, {"echoBytes", "value", `"AH+A/w=="`},
		{"echoByteLists", "values", `[]`}, {"echoByteLists", "values", `["AH+A/w==",""]`},
		{"echoScalarArrays", "input", `{"bytes":["AH+A/w==",""],"jsons":null}`},
		{"echoScalarArrays", "input", `{"bytes":null,"jsons":[null,123456789012345678901234567890,0.123456789012345678901]}`},
	} {
		status, body, err := request(jsonParameterFixtureBody(item.api, item.param, item.value))
		if err != nil {
			return err
		}
		var response struct{ Data json.RawMessage }
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		if status != http.StatusOK || string(response.Data) != item.value {
			return fmt.Errorf("%s changed %s: %d %s", item.api, item.value, status, body)
		}
	}
	return nil
}

func jsonParameterFixtureBody(api, param, value string) string {
	selection := ""
	if api == "echoScalarArrays" {
		selection = `,"$select":"bytes,jsons"`
	}
	return `{"$api":"` + api + `","` + param + `":` + value + selection + `}`
}

func TestFixtureJSONErrorRetainsNumberTokens(t *testing.T) {
	t.Chdir("../..")
	data, err := os.ReadFile("pkg/lux/codec/testdata/protocol-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct{ JSONNumberTokens []string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.JSONNumberTokens) == 0 {
		t.Fatal("missing numeric corpus")
	}
	for _, gateway := range []bool{false, true} {
		err = withFixtureMode(gateway, func(endpoint, _ string) error {
			for _, token := range corpus.JSONNumberTokens {
				value := `{"n":` + token + `,"items":[` + token + `,null],"text":"精度 café"}`
				if err := verifyFixtureJSONError(endpoint, value); err != nil {
					return fmt.Errorf("token %s: %w", token, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func verifyFixtureJSONError(endpoint, value string) error {
	status, body, err := fixtureJSONRequest(endpoint, `{"$api":"rejectJSON","value":`+value+`}`)
	if err != nil {
		return err
	}
	var response struct {
		Error, Message, TraceID string
		Code                    int
		Data                    struct{ Value json.RawMessage }
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if status != 422 || response.Code != 422 || response.Error != "JSONRejected" ||
		response.Message != "error.json_rejected" || response.TraceID == "" || string(response.Data.Value) != value {
		return fmt.Errorf("unexpected JSON error: %d %s", status, body)
	}
	return nil
}

func verifyFixturePage(endpoint string) error {
	status, body, err := fixtureJSONRequest(endpoint,
		`{"$api":"browsePrecision","$select":"id, value","value":9223372036854775807,"page":9223372036854775807}`)
	if err != nil {
		return err
	}
	var response struct {
		Data struct {
			Items                 []json.RawMessage
			Total, Page, PageSize int64
		}
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	page := response.Data
	if status != 200 || len(page.Items) != 0 || page.Total != 3 || page.Page != math.MaxInt64 || page.PageSize != 2 {
		return fmt.Errorf("unexpected pagination response: %d %s", status, body)
	}
	return nil
}

func verifyFixtureListEffects(endpoint string) error {
	status, body, err := fixtureJSONRequest(endpoint, `{"$api":"checkListEffects"}`)
	if err != nil {
		return err
	}
	var response struct{ Data []int64 }
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if status != 200 || fmt.Sprint(response.Data) != "[1 2 3 3]" {
		return fmt.Errorf("list elements executed more than once: %d %s", status, body)
	}
	return nil
}

func fixtureJSONRequest(endpoint, body string) (int, []byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}
