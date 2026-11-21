package main

import (
	"github.com/light-speak/luxo/internal/testutil"

	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

type enumFixtureSchema struct {
	APIs map[string]struct {
		ID     int
		Params []struct{ ID int }
	}
	Types map[string]struct{ Fields []struct{ ID int } }
}

type enumFixtureRequest struct {
	shape, api, body string
	object, list     bool
}

// Every server entry must reject values absent from its own schema.
func TestFixtureRejectsUndeclaredEnums(t *testing.T) {
	t.Chdir("../..")
	previous := fixtureSchema
	t.Cleanup(func() { fixtureSchema = previous })
	fixtureSchema += `
type EnumProbe { value: FixtureState }
api echoEnumProbe(input: EnumProbe): EnumProbe { return input }
api echoEnumProbes(inputs: [EnumProbe]): [EnumProbe] { return inputs }
`
	for _, gateway := range []bool{false, true} {
		t.Run(fmt.Sprintf("gateway=%t", gateway), func(t *testing.T) {
			err := withFixtureMode(gateway, func(endpoint, _ string) error {
				schema := loadEnumFixtureSchema(t, endpoint)
				for _, value := range []string{"OPEN", "CLOSED", "FUTURE", "", "open"} {
					quoted := strconv.Quote(value)
					requests := []enumFixtureRequest{
						{"scalar", "echoFixtureState", `{"$api":"echoFixtureState","value":` + quoted + `}`, false, false},
						{"list", "echoFixtureStates", `{"$api":"echoFixtureStates","values":[` + quoted + `]}`, false, true},
						{"object", "echoEnumProbe", `{"$api":"echoEnumProbe","input":{"value":` + quoted + `},"$select":"value"}`, true, false},
						{"objects", "echoEnumProbes", `{"$api":"echoEnumProbes","inputs":[{"value":` + quoted + `}],"$select":"value"}`, true, true},
					}
					for _, request := range requests {
						for _, binary := range []bool{false, true} {
							status, err := sendEnumFixture(t, endpoint, schema, request, value, binary)
							if err != nil {
								return err
							}
							want := http.StatusBadRequest
							if value == "OPEN" || value == "CLOSED" {
								want = http.StatusOK
							}
							if status != want {
								t.Errorf("value=%q shape=%s binary=%t: status=%d, want=%d", value, request.shape, binary, status, want)
							}
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func loadEnumFixtureSchema(t *testing.T, endpoint string) enumFixtureSchema {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint+"?$schema", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Introspection-Key", "sdk-interop")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var schema enumFixtureSchema
	if err := json.NewDecoder(response.Body).Decode(&schema); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(schema.Types["EnumProbe"].Fields) != 1 {
		t.Fatal("invalid fixture schema")
	}
	return schema
}

func sendEnumFixture(t *testing.T, endpoint string, schema enumFixtureSchema, request enumFixtureRequest, value string, binary bool) (int, error) {
	t.Helper()
	if !binary {
		status, body, err := fixtureJSONRequest(endpoint, request.body)
		t.Logf("JSON value=%s shape=%s status=%d response=%s", value, request.shape, status, body)
		return status, err
	}
	definition := schema.APIs[request.api]
	if len(definition.Params) != 1 || definition.ID <= 0 {
		t.Fatal("invalid fixture API")
	}
	var enc codec.Encoder
	enc.WriteFieldHeader(definition.Params[0].ID)
	if request.list {
		enc.WriteArrayHeader(1)
	}
	var mask []byte
	if request.object {
		id := schema.Types["EnumProbe"].Fields[0].ID
		message := codec.AppendVarint(nil, uint64(len(value)))
		message = codec.AppendVarint(message, uint64(id))
		message, err := codec.AppendString(message, value)
		if err != nil {
			t.Fatal(err)
		}
		enc.WriteBytes(append(message, 0))
		mask = testutil.Bytes(t)(codec.AppendSelectionMask(nil, testutil.Bytes(t)(codec.FieldMaskSet(nil, id)), nil))
	} else if err := enc.WriteString(value); err != nil {
		t.Fatal(err)
	}
	body := codec.AppendVarint(nil, uint64(definition.ID))
	body = codec.AppendVarint(body, uint64(len(mask)))
	body = append(body, mask...)
	body = append(body, enc.Bytes()...)
	body = append(body, 0)
	client := &http.Client{Timeout: 5 * time.Second}
	httpRequest, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	httpRequest.Header.Set("Content-Type", "application/x-luxo")
	httpRequest.Header.Set("X-Luxo-Mode", "binary")
	response, err := client.Do(httpRequest)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == 200 && response.Header.Get("X-Luxo-Mode") != "binary" {
		t.Fatal("fixture did not use Binary response mode")
	}
	data, err := io.ReadAll(response.Body)
	t.Logf("Binary value=%s shape=%s status=%d response=%x", value, request.shape, response.StatusCode, data)
	return response.StatusCode, err
}
