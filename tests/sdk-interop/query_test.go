package main

import (
	"github.com/light-speak/luxo/internal/testutil"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// No database is configured: rejection must happen in the generated CRUD
// handler before its model client is accessed, including through a real RPC hop.
func TestFixtureStrictQueryBoundary(t *testing.T) {
	t.Chdir("../..")
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%t", gateway), func(t *testing.T) {
			if err := withFixtureMode(gateway, func(endpoint, _ string) error {
				contract := queryFixtureSchema(t, endpoint)
				for _, binary := range []bool{false, true} {
					for _, filter := range []api.Filter{
						{Field: "missing", Operator: "eq", Value: "1"},
						{Field: "state", Operator: "eq", Value: "FUTURE"},
						{Field: "state", Operator: "gt", Value: "OPEN"},
						{Field: "active", Operator: "eq", Value: "truue"},
						{Field: "score", Operator: "eq", Value: "NaN"},
						{Field: "count", Operator: "eq", Value: "bad"},
					} {
						checkQueryFixture(t, endpoint, contract, binary, map[string]any{"$filters": []api.Filter{filter}})
					}
					for _, field := range []string{"missing", "id"} {
						checkQueryFixture(t, endpoint, contract, binary, map[string]any{"$sorters": []api.Sorter{{Field: field, Order: "asc"}}})
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func queryFixtureSchema(t *testing.T, endpoint string) *schema.Schema {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint+"?$schema", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Introspection-Key", "sdk-interop")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result schema.Schema
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result.APIs["listQueryProbes"] == nil || result.Models["QueryProbe"] == nil {
		t.Fatal("query fixture schema missing")
	}
	return &result
}

func checkQueryFixture(t *testing.T, endpoint string, contract *schema.Schema, binary bool, params map[string]any) {
	t.Helper()
	var body []byte
	var err error
	if binary {
		mask := testutil.Bytes(t)(codec.AppendSelectionMask(nil, testutil.Bytes(t)(codec.FieldMaskSet(nil, contract.Models["QueryProbe"].Fields[0].ID)), nil))
		body, err = api.EncodeBinaryRequest(contract.APIs["listQueryProbes"].ID, mask, params, nil)
	} else {
		params["$api"] = "listQueryProbes"
		params["$select"] = "id"
		body, err = json.Marshal(params)
	}
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	content := "application/json"
	if binary {
		content = "application/x-luxo"
		req.Header.Set("X-Luxo-Mode", "binary")
	}
	req.Header.Set("Content-Type", content)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 400 {
		t.Fatalf("binary=%v query=%v: HTTP %d %s", binary, params, response.StatusCode, data)
	}
}
