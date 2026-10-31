package api

import (
	"encoding/json"
	"slices"
	"testing"
)

type testEnum string

func TestParamEnumArray(t *testing.T) {
	for _, binary := range []bool{false, true} {
		req := &Request{Params: map[string]json.RawMessage{"v": []byte(`["OPEN","CLOSED"]`)}}
		if binary {
			req.SetBinaryParams([]string{"v"}, 1)
			req.SetParamSlot(0, []string{"OPEN", "CLOSED"})
		}
		got, err := ParamEnumArray[testEnum](req, "v")
		if err != nil || !slices.Equal(got, []testEnum{"OPEN", "CLOSED"}) {
			t.Fatalf("binary=%t: %v / %v", binary, got, err)
		}
		if binary {
			value, _ := req.findParam("v")
			value.([]string)[0] = "OTHER"
		} else {
			clear(req.Params["v"])
		}
		if got[0] != "OPEN" {
			t.Fatal("result aliases writable request storage")
		}
		if got, err := ParamEnumArray[testEnum](req, "missing"); err == nil || got != nil {
			t.Fatalf("missing: %v / %v", got, err)
		}
	}
	for _, raw := range []string{"null", "7", `[null]`, `["OPEN",7]`, `["\uD800"]`, `["OPEN"]false`} {
		req := &Request{Params: map[string]json.RawMessage{"v": []byte(raw)}}
		if got, err := ParamEnumArray[testEnum](req, "v"); err == nil || got != nil {
			t.Errorf("invalid %s: %v / %v", raw, got, err)
		}
	}
	for _, value := range []any{nil, int64(1), []int64{1}} {
		req := &Request{}
		req.SetBinaryParams([]string{"v"}, 1)
		req.SetParamSlot(0, value)
		if got, err := ParamEnumArray[testEnum](req, "v"); err == nil || got != nil {
			t.Errorf("invalid binary %v: %v / %v", value, got, err)
		}
	}
}
