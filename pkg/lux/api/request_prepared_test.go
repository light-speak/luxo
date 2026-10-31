package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func preparedParamRegistry() *APIRegistry {
	s := schema.New()
	s.RegisterType(&schema.TypeDecl{Name: "Input", Fields: []schema.Field{
		{ID: 1, Name: "name", Type: schema.FieldString},
	}})
	s.RegisterAPI(&schema.API{ID: 42, Name: "create", Params: []schema.Param{
		{ID: 9, Name: "input", Type: schema.FieldModel, TypeName: "Input", Nullable: true, HasDefault: true},
		{ID: 2, Name: "count", Type: schema.FieldInt},
		{ID: 7, Name: "inputs", Type: schema.FieldModel, TypeName: "Input", IsList: true, Nullable: true, HasDefault: true},
	}})
	r := NewAPIRegistry()
	r.RegisterSchemaAPIs(s)
	return r
}

func TestPreparedJSONStructuredParams(t *testing.T) {
	r := preparedParamRegistry()
	req := &Request{API: "create", Params: map[string]json.RawMessage{
		"input": []byte(`{"name":"東京"}`), "count": []byte(`42`), "inputs": []byte(`[{"name":"東京"}]`),
	}}
	if err := r.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	canonical, err := r.ParseBinaryRequest(req.BinaryRequest())
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := canonical.ParamMessage("input", false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, present, err := req.ParamMessage("input", false, false)
	if err != nil || !present || !bytes.Equal(got, want) {
		t.Fatalf("prepared input: %x, %t, %v; want %x", got, present, err, want)
	}
	list, present, err := req.ParamMessageArray("inputs", false, false)
	if err != nil || !present || len(list) != 1 || !bytes.Equal(list[0], want) {
		t.Fatalf("prepared inputs: %x, %t, %v", list, present, err)
	}
	if req.BinaryMode || !req.HasParam("count") || req.HasParam("missing") {
		t.Fatal("structured cache changed JSON mode or scalar presence")
	}
	if got, err := req.ParamInt("count"); err != nil || got != 42 {
		t.Fatalf("scalar after preparation: %d, %v", got, err)
	}
	clear(req.Params["input"])
	if !bytes.Equal(got, want) {
		t.Fatal("canonical message borrowed mutable JSON input")
	}
}

func TestPreparedJSONStructuredParamLifecycle(t *testing.T) {
	r := preparedParamRegistry()
	req := &Request{API: "create", Params: map[string]json.RawMessage{
		"input": []byte(`{"name":"old"}`), "count": []byte(`42`), "inputs": []byte(`[]`),
	}}
	if err := r.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	if list, present, err := req.ParamMessageArray("inputs", true, true); err != nil || !present || list == nil || len(list) != 0 {
		t.Fatalf("empty list: %v, %t, %v", list, present, err)
	}
	req.Params["input"] = []byte(`null`)
	req.Params["inputs"] = []byte(`null`)
	if err := r.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	if !req.ParamIsNull("input") || !req.ParamIsNull("inputs") {
		t.Fatal("explicit null lost")
	}
	if value, present, err := req.ParamMessage("input", true, true); err != nil || !present || value != nil {
		t.Fatalf("explicit null: %v, %t, %v", value, present, err)
	}
	delete(req.Params, "input")
	if err := r.prepareJSONRequest(req); err != nil {
		t.Fatal(err)
	}
	if value, present, err := req.ParamMessage("input", true, true); err != nil || present || value != nil {
		t.Fatalf("omitted retained old cache: %v, %t, %v", value, present, err)
	}
	req.Params["input"] = []byte(`{"name":true}`)
	if err := r.prepareJSONRequest(req); err == nil {
		t.Fatal("invalid typed field accepted")
	}
	if req.BinaryRequest() != nil || req.BinaryParams() != nil {
		t.Fatal("failed preparation retained a prior valid request")
	}
	if value, present, err := req.ParamMessage("inputs", true, true); err != nil || present || value != nil {
		t.Fatalf("failed preparation retained a partial cache: %v, %t, %v", value, present, err)
	}
}

func TestPreparedJSONClearsCacheOnLaterFailures(t *testing.T) {
	for _, failure := range []string{"scalar", "missing", "filters", "sorters", "unregistered"} {
		t.Run(failure, func(t *testing.T) {
			r := preparedParamRegistry()
			req := &Request{API: "create", Params: map[string]json.RawMessage{
				"input": []byte(`{"name":"old"}`), "count": []byte(`42`),
			}}
			if err := r.prepareJSONRequest(req); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "scalar":
				req.Params["count"] = []byte(`true`)
			case "missing":
				delete(req.Params, "count")
			case "filters":
				req.Filters = []Filter{{Field: "name", Operator: "invalid"}}
			case "sorters":
				req.Sorters = []Sorter{{Field: "name", Order: "invalid"}}
			case "unregistered":
				req.API = "unregistered"
			}
			if err := r.prepareJSONRequest(req); (err == nil) != (failure == "unregistered") {
				t.Fatalf("prepare %s: %v", failure, err)
			}
			if req.BinaryRequest() != nil || req.BinaryParams() != nil || req.paramNames != nil || req.paramCount != 0 || req.paramSet != 0 || req.paramNull != 0 {
				t.Fatal("preparation retained canonical state")
			}
			for _, value := range req.paramSlots {
				if value != nil {
					t.Fatal("preparation retained an owned message")
				}
			}
		})
	}
}

func TestPreparedJSONParamSlotLimit(t *testing.T) {
	for _, count := range []int{16, 17} {
		s := schema.New()
		s.RegisterType(&schema.TypeDecl{Name: "Input", Fields: []schema.Field{{ID: 1, Name: "id", Type: schema.FieldInt}}})
		definition := &schema.API{ID: 1, Name: "create"}
		req := &Request{API: "create", Params: make(map[string]json.RawMessage)}
		for index := range count {
			name := fmt.Sprintf("input%d", index)
			definition.Params = append(definition.Params, schema.Param{ID: index + 1, Name: name, Type: schema.FieldModel, TypeName: "Input"})
			req.Params[name] = []byte(`{"id":42}`)
		}
		s.RegisterAPI(definition)
		r := NewAPIRegistry()
		r.RegisterSchemaAPIs(s)
		err := r.prepareJSONRequest(req)
		if count == 17 {
			if err == nil || !strings.Contains(err.Error(), "too many params") || req.paramNames != nil {
				t.Fatalf("slot limit: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if value, present, err := req.ParamMessage("input15", false, false); err != nil || !present || len(value) == 0 {
			t.Fatalf("last slot: %x %t %v", value, present, err)
		}
	}
}
