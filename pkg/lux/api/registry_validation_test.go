package api

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestRegistryRequestBenchmarkParity(t *testing.T) {
	for _, width := range []int{1, 16} {
		s, names, params := registryBenchmarkCatalog(t, 16, width, 0)
		r := NewAPIRegistry()
		if err := r.RegisterSchemaAPIs(s); err != nil {
			t.Fatal(err)
		}
		values := make(map[string]any, width)
		jsonReq := &Request{API: names[0], Params: make(map[string]json.RawMessage, width)}
		for i, param := range params[0] {
			values[param.Name] = int64(i + 1)
			jsonReq.Params[param.Name] = strconv.AppendInt(nil, int64(i+1), 10)
		}
		body, err := EncodeBinaryRequest(1, nil, values, params[0])
		if err != nil {
			t.Fatal(err)
		}
		var binaryReq Request
		if err := r.ParseBinaryRequestInto(body, &binaryReq); err != nil {
			t.Fatal(err)
		}
		if err := r.prepareJSONRequest(jsonReq); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, jsonReq.BinaryRequest()) {
			t.Fatal("benchmark JSON/Binary normalization differs")
		}
		for _, request := range []*Request{&binaryReq, jsonReq} {
			for i, param := range params[0] {
				if got, err := request.ParamInt(param.Name); err != nil || got != int64(i+1) {
					t.Fatalf("benchmark changed %s: %d / %v", param.Name, got, err)
				}
			}
		}
	}
}

func TestRegistryRejectsConflictingRoutesAtomically(t *testing.T) {
	r := NewAPIRegistry()
	if err := r.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name string
		id   int64
	}{
		{"", 2}, {"zero", 0}, {"negative", -1}, {"wide", codec.MaxAPIID + 1}, {"other", 1}, {"read", 2},
	} {
		// Register accepts a host int; unrepresentable wire IDs are tested at decoding.
		if int64(int(item.id)) != item.id {
			continue
		}
		if err := r.Register(item.name, int(item.id)); err == nil {
			t.Fatalf("accepted %+v", item)
		}
		if len(r.idToName) != 1 || len(r.nameToID) != 1 || r.idToName[1] != "read" || r.nameToID["read"] != 1 {
			t.Fatal("failed registration changed routes")
		}
	}
	if err := r.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("maximum", codec.MaxAPIID); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryOwnsParametersAndRejectsConflicts(t *testing.T) {
	r := NewAPIRegistry()
	want := []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}}
	input := slices.Clone(want)
	if err := r.RegisterParams("read", input); err != nil {
		t.Fatal(err)
	}
	input[0].Name = "changed"
	if !slices.Equal(r.ParamOrder("read"), want) {
		t.Fatal("registry borrowed input")
	}
	for _, invalid := range [][]ParamMeta{
		{{Name: "id", FieldID: 0}}, {{Name: "id", FieldID: -1}}, {{Name: "id", FieldID: codec.FiltersFieldID}},
		{{Name: "id", FieldID: codec.SortersFieldID}}, {{Name: "", FieldID: 1}},
		{{Name: "id", FieldID: 1}, {Name: "other", FieldID: 1}},
		{{Name: "id", FieldID: 1}, {Name: "id", FieldID: 2}},
		{{Name: "id", Type: "String", FieldID: 1}}, nil,
	} {
		if err := r.RegisterParams("read", invalid); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
		if !slices.Equal(r.ParamOrder("read"), want) || !slices.Equal(r.ParamNames("read"), []string{"id"}) {
			t.Fatal("failed registration changed parameters")
		}
	}
	if err := r.RegisterParams("", nil); err == nil {
		t.Fatal("accepted empty API name")
	}
	if err := r.RegisterParams("read", want); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterParams("max", []ParamMeta{{Name: "id", Type: "Int", FieldID: codec.MaxParameterID}}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySchemaRegistrationIsAtomic(t *testing.T) {
	r := NewAPIRegistry()
	old := schema.New()
	old.APIs["read"] = &schema.API{Name: "read", ID: 1}
	if err := r.RegisterSchemaAPIs(old); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*schema.Schema{
		nil,
		{APIs: map[string]*schema.API{"nil": nil}},
		{APIs: map[string]*schema.API{"alias": {Name: "different", ID: 2}}},
		{APIs: map[string]*schema.API{"valid": {Name: "valid", ID: 2}, "bad": {Name: "bad", ID: 0}}},
		{APIs: map[string]*schema.API{"other": {Name: "other", ID: 1}}},
		{APIs: map[string]*schema.API{"bad": {Name: "bad", ID: 3, Params: []schema.Param{{Name: "id", ID: 0}}}}},
		{Models: map[string]*schema.Model{"bad": {Name: "bad", Fields: []schema.Field{{Name: "id", ID: 0}}}}},
	} {
		if err := r.RegisterSchemaAPIs(invalid); err == nil {
			t.Fatal("accepted invalid schema")
		}
		if r.schema != old || len(r.nameToID) != 1 || r.nameToID["read"] != 1 || len(r.paramOrder) != 1 {
			t.Fatal("failed schema registration changed state")
		}
	}
	if err := r.RegisterSchemaAPIs(old); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryOwnsConvertedSchemaParameters(t *testing.T) {
	r := NewAPIRegistry()
	s := schema.New()
	s.APIs["read"] = &schema.API{Name: "read", ID: 1, Params: []schema.Param{{Name: "id", ID: 1, Type: schema.FieldInt, TypeName: "Int"}}}
	if err := r.RegisterSchemaAPIs(s); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(r.ParamOrder("read"))
	s.APIs["read"].Params[0] = schema.Param{Name: "changed", ID: 2, Type: schema.FieldString}
	if !slices.Equal(r.ParamOrder("read"), want) || !slices.Equal(r.ParamNames("read"), []string{"id"}) {
		t.Fatal("registry aliases the schema parameter declaration")
	}
}

func TestRegistryRejectsInvalidFreshParameterLayouts(t *testing.T) {
	for _, params := range [][]ParamMeta{
		{{Name: "id", FieldID: 0}}, {{Name: "id", FieldID: -1}},
		{{Name: "id", FieldID: codec.FiltersFieldID}}, {{Name: "id", FieldID: codec.SortersFieldID}},
		{{Name: "", FieldID: 1}},
		{{Name: "id", FieldID: 1}, {Name: "id", FieldID: 2}},
		{{Name: "id", FieldID: 1}, {Name: "other", FieldID: 1}},
	} {
		r := NewAPIRegistry()
		if err := r.RegisterParams("read", params); err == nil || len(r.paramOrder) != 0 || len(r.paramNames) != 0 {
			t.Fatalf("accepted invalid layout or partially installed it: %+v / %v", params, err)
		}
	}
}

func TestRegistryScalarMetadataIsEquivalentAcrossGeneratedSources(t *testing.T) {
	for _, explicitFirst := range []bool{false, true} {
		r := NewAPIRegistry()
		a := []ParamMeta{{Name: "id", Type: "Int", FieldID: 1}}
		b := slices.Clone(a)
		b[0].TypeName = "Int"
		if explicitFirst {
			a, b = b, a
		}
		if err := r.RegisterParams("read", a); err != nil {
			t.Fatal(err)
		}
		if err := r.RegisterParams("read", b); err != nil {
			t.Fatal(err)
		}
		b[0].TypeName = "Different"
		if err := r.RegisterParams("read", b); err == nil {
			t.Fatal("accepted a different named type")
		}
	}
	for _, kind := range []string{"Enum", "Model"} {
		r := NewAPIRegistry()
		if err := r.RegisterParams("read", []ParamMeta{{Name: "value", Type: kind, TypeName: kind, FieldID: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := r.RegisterParams("read", []ParamMeta{{Name: "value", Type: kind, FieldID: 1}}); err == nil {
			t.Fatal("erased a named type")
		}
	}
}

func TestRegistryParameterIdentityDoesNotDependOnDeclarationOrder(t *testing.T) {
	r := NewAPIRegistry()
	first := []ParamMeta{{Name: "a", Type: "Int", FieldID: 2}, {Name: "b", Type: "String", FieldID: 1}}
	if err := r.RegisterParams("read", first); err != nil {
		t.Fatal(err)
	}
	reordered := []ParamMeta{first[1], first[0]}
	if err := r.RegisterParams("read", reordered); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.ParamOrder("read"), first) {
		t.Fatal("idempotent registration replaced slot order")
	}
	for _, invalid := range [][]ParamMeta{
		{first[0], first[0]},
		{first[1], {Name: "different", Type: "Int", FieldID: 2}},
	} {
		if err := r.RegisterParams("read", invalid); err == nil {
			t.Fatal("accepted a changed parameter set")
		}
	}
}

func TestRegistrySetSchemaRejectsInvalidReplacement(t *testing.T) {
	r := NewAPIRegistry()
	old := schema.New()
	if err := r.SetSchema(old); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*schema.Schema{
		nil,
		{APIs: map[string]*schema.API{"read": {Name: "read", ID: 0}}},
		{Types: map[string]*schema.TypeDecl{"Item": {Name: "Item", Fields: []schema.Field{{Name: "id", ID: 0}}}}},
	} {
		if err := r.SetSchema(invalid); err == nil {
			t.Fatal("accepted invalid schema replacement")
		}
		if r.schema != old {
			t.Fatal("failed replacement changed schema")
		}
	}
	next := schema.New()
	next.Types["Item"] = &schema.TypeDecl{Name: "Item", Fields: []schema.Field{{Name: "id", ID: 1}}}
	if err := r.SetSchema(next); err != nil {
		t.Fatal(err)
	}
	if r.schema != next || next.LookupModel("Item") == nil {
		t.Fatal("replacement did not prepare type metadata")
	}
}

func TestRouterPreparationRejectsDirectlyModifiedSchema(t *testing.T) {
	router := NewRouter()
	router.Schema.APIs["read"] = &schema.API{Name: "read", ID: 0}
	if err := router.Prepare(); err == nil || router.prepared {
		t.Fatalf("invalid metadata published: %v", err)
	}
	router.Schema.APIs["read"].ID = 1
	if err := router.Prepare(); err != nil || !router.prepared {
		t.Fatalf("corrected metadata could not be prepared: %v", err)
	}
}
