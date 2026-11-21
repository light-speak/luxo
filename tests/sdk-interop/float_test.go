package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestFixtureRejectsBusinessFloatOverflow(t *testing.T) {
	t.Chdir("../..")
	for _, gateway := range []bool{false, true} {
		err := withFixtureMode(gateway, func(endpoint, _ string) error {
			for _, apiName := range []string{"scaleFloat", "scaleFloatList"} {
				for _, value := range []string{"1.7976931348623157e308", "-1.7976931348623157e308"} {
					request := `{"$api":"` + apiName + `","value":` + value + `,"factor":2}`
					status, body, err := fixtureJSONRequest(endpoint, request)
					if err != nil {
						return err
					}
					var result struct {
						Error   string
						Code    int
						Message string
						Cause   string
						Data    json.RawMessage
					}
					if err := json.Unmarshal(body, &result); err != nil {
						return err
					}
					if status != 500 || result.Code != 500 || result.Error != "Internal" || result.Message != "error.internal" || result.Cause != "" || len(result.Data) != 0 {
						return fmt.Errorf("%s overflow became %d %s", apiName, status, body)
					}
				}
			}
			return verifyGatewayEcho(endpoint, "echoInteger", "9223372036854775807")
		})
		if err != nil {
			t.Fatalf("gateway=%t: %v", gateway, err)
		}
	}
}
