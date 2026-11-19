package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

func canonicalSealedSchema(t *testing.T) *Schema {
	t.Helper()
	raw, err := os.ReadFile("../codec/testdata/sealed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema *Schema `json:"schema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Schema.Prepare(); err != nil {
		t.Fatal(err)
	}
	return fixture.Schema
}

func TestSealedSchemaUsesSharedCanonicalMetadata(t *testing.T) {
	raw, err := os.ReadFile("../codec/testdata/sealed-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema *Schema `json:"schema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	s := fixture.Schema
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	if FieldSealed != 12 || FieldSealed.String() != "Sealed" {
		t.Fatal("sealed changed existing wire kind numbering")
	}
	s.InferTypeUsage()
	for _, name := range []string{"Outcome", "EmptyOutcome"} {
		if s.Sealeds[name] == nil || s.Sealeds[name].Usage != TypeUsageInputOutput {
			t.Fatal("lost sealed API usage", name)
		}
		if s.LookupModel(name) != nil {
			t.Fatal("a sum was published as an ordinary model", name)
		}
	}
}

func TestRegisterSealedOwnsVariantFields(t *testing.T) {
	s := New()
	declaration := &Sealed{Name: "Outcome", Variants: []Variant{{ID: 7, Name: "Value", Fields: []Param{{ID: 19, Name: "value", Type: FieldInt}}}}}
	if err := s.RegisterSealed(declaration); err != nil {
		t.Fatal(err)
	}
	declaration.Variants[0].Name = "Edited"
	declaration.Variants[0].Fields[0].Name = "edited"
	if s.Sealeds["Outcome"].Variants[0].Name != "Value" || s.Sealeds["Outcome"].Variants[0].Fields[0].Name != "value" {
		t.Fatal("registration borrowed mutable input")
	}
}

func TestSealedSchemaRejectsMalformedMetadata(t *testing.T) {
	for _, source := range []string{
		`{"sealeds":{"Outcome":null}}`,
		`{"sealeds":{"Outcome":{"name":"Wrong","variants":[]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":0,"name":"Value","fields":[]}]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":1,"name":"Value","fields":[]},{"id":1,"name":"Other","fields":[]}]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":1,"name":"Value","fields":[{"id":1,"name":"x","type":"Int"},{"id":1,"name":"y","type":"String"}]}]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":1,"name":"Value","fields":[{"id":1,"name":"x","type":"Sealed","typeName":"Missing"}]}]}}}`,
		`{"types":{"Value":{"name":"Value","fields":[{"id":1,"name":"x","type":"Sealed","typeName":"Missing"}]}},"sealeds":{}}`,
	} {
		t.Run(source, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(source), &s); err != nil {
				t.Fatal(err)
			}
			if err := s.Prepare(); err == nil || !strings.Contains(err.Error(), "schema:") {
				t.Fatal("malformed sealed metadata passed registration", err)
			}
		})
	}
}

func TestSealedSchemaConvertsCanonicalRows(t *testing.T) {
	s := canonicalSealedSchema(t)
	for _, sample := range []struct {
		json string
		wire []byte
	}{
		{`{"Success":{"value":7}}`, []byte{1, 4, 0, 1, 14, 0}},
		{`{"Pending":{}}`, []byte{3, 2, 0, 0}},
	} {
		wire, err := s.JSONToBinary("Outcome", []byte(sample.json))
		if err != nil || !bytes.Equal(wire, sample.wire) {
			t.Fatal("wrong sealed input frame", wire, err)
		}
		out, err := s.SealedToJSON(nil, wire, "Outcome")
		if err != nil || string(out) != sample.json {
			t.Fatal("wrong sealed output shape", string(out), err)
		}
	}
	out, err := s.SealedToJSON(nil, []byte{1, 2, 0, 0}, "Outcome")
	if err != nil || string(out) != `{"Success":{}}` {
		t.Fatal("tag-only output fabricated a payload", string(out), err)
	}
}

func TestSealedSchemaRejectsMalformedFramesAndInputs(t *testing.T) {
	s := canonicalSealedSchema(t)
	for _, source := range []string{
		`{}`, `null`, `{"Missing":{}}`, `{"Success":{}}`,
		`{"Success":{"value":null}}`, `{"Pending":{"x":1}}`,
		`{"Pending":{},"Pending":{}}`, `{"Pending":{},"Success":{"value":7}}`,
	} {
		if _, err := s.JSONToBinary("Outcome", []byte(source)); err == nil {
			t.Fatal("invalid input passed", source)
		}
	}
	for _, wire := range [][]byte{{}, {99, 2, 0, 0}, {0, 2, 0, 0}, {1, 4, 0, 1, 14}, {1, 4, 0, 1, 14, 0, 3, 2, 0, 0}} {
		prefix := []byte("unchanged")
		out, err := s.SealedToJSON(prefix, wire, "Outcome")
		if err == nil || !bytes.Equal(out, prefix) {
			t.Fatal("bad output escaped or changed destination", wire, out, err)
		}
	}
	if _, err := s.JSONToBinary("EmptyOutcome", []byte(`{}`)); err == nil {
		t.Fatal("uninhabited sum acquired an input")
	}
	if _, err := s.SealedToJSON(nil, []byte{1, 2, 0, 0}, "EmptyOutcome"); err == nil {
		t.Fatal("uninhabited sum acquired an output")
	}
}

func TestSealedSchemaChecksEveryObjectDepth(t *testing.T) {
	s := canonicalSealedSchema(t)
	for _, count := range []int{31, 32, 33} {
		raw := sealedJSONDepthValue(count)
		wire, err := s.JSONToBinary("Outcome", []byte(raw))
		if count <= 32 {
			if err != nil || len(wire) == 0 {
				t.Fatal("valid tag and payload object boundary", count, err)
			}
		} else if !errors.Is(err, codec.ErrModelDepth) || wire != nil {
			t.Fatal("present sum exceeded the object budget", count, err)
		}
	}
	for _, field := range []string{"child", "history"} {
		for _, count := range []int{31, 32} {
			value := sealedJSONDepthValue(count)
			if field == "history" {
				value = "[" + value + "]"
			}
			raw := `{"Nested":{"` + field + `":` + value + `,"` + field + `":null}}`
			wire, err := s.JSONToBinary("Outcome", []byte(raw))
			if count == 31 {
				if err != nil || len(wire) == 0 {
					t.Fatal("valid discarded resource boundary", field, err)
				}
			} else if !errors.Is(err, codec.ErrModelDepth) || wire != nil {
				t.Fatal("discarded sum bypassed the object budget", field, err)
			}
		}
	}
}

func sealedJSONDepthValue(count int) string {
	value := `{"Pending":{}}`
	for level := 1; level < count; level++ {
		value = `{"Nested":{"child":` + value + `}}`
	}
	return value
}

func TestSealedSelectionDependenciesRespectTagOnlyLeaves(t *testing.T) {
	s := dependencyTestSchema()
	if err := s.RegisterSealed(&Sealed{Name: "Outcome", Variants: []Variant{
		{ID: 1, Name: "Value", Fields: []Param{{ID: 1, Name: "user", Type: FieldModel, TypeName: "User"}}},
		{ID: 2, Name: "Nested", Fields: []Param{{ID: 1, Name: "child", Type: FieldSealed, TypeName: "Outcome", Nullable: true}}},
		{ID: 3, Name: "Pending"},
	}}); err != nil {
		t.Fatal(err)
	}
	s.RegisterType(&TypeDecl{Name: "Envelope", Fields: []Field{{ID: 1, Name: "outcome", Type: FieldSealed, TypeName: "Outcome"}}})
	for _, sample := range []struct{ root, selected string }{
		{"Outcome", "Value{user{postCount}}"},
		{"Outcome", "Nested{child{Value{user{postCount}}}}"},
		{"Envelope", "outcome{Value{user{postCount}}}"},
	} {
		plan, err := s.PrepareSelectionDependencies(sample.root)
		if err != nil {
			t.Fatal(sample.root, err)
		}
		fields, err := selection.Parse(sample.selected)
		if err != nil {
			t.Fatal(err)
		}
		got, err := plan.AppendSources(nil, fields)
		if err != nil || !slices.Equal(got, []ComputedSource{{Model: "Post", Module: "content"}}) {
			t.Fatal("selected sealed dependency", sample, got, err)
		}
	}
	plan, err := s.PrepareSelectionDependencies("Outcome")
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"Value", "Pending", "Nested", "Nested{child{Value}}"} {
		fields, err := selection.Parse(selected)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := plan.AppendSources(nil, fields); err != nil || len(got) != 0 {
			t.Fatal("case leaf loaded payload dependencies", selected, got, err)
		}
	}
	all, err := plan.AppendSources(nil, nil)
	if err != nil || !slices.Equal(all, []ComputedSource{{Model: "User", Module: "accounts"}, {Model: "Post", Module: "content"}}) {
		t.Fatal("omitted selection lost active payload dependencies", all, err)
	}
	ordinary, err := s.PrepareSelectionDependencies("Envelope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ordinary.AppendSources(nil, nil); err == nil {
		t.Fatal("ordinary DTO no longer requires its explicit root selection")
	}
}

func TestSealedPublicRegistrationBoundaries(t *testing.T) {
	for _, declaration := range []*Sealed{
		nil, {},
		{Name: "Broken", Variants: []Variant{{ID: 1, Name: "Value", Fields: []Param{{ID: 1, Name: "value", Type: FieldType(999)}}}}},
	} {
		s := New()
		if err := s.RegisterSealed(declaration); err == nil || len(s.Sealeds) != 0 {
			t.Fatal("invalid public declaration changed schema", declaration, err)
		}
	}
	s := New()
	if err := s.RegisterModel(&Model{Name: "Person", Fields: []Field{{ID: 1, Name: "id", Type: FieldInt}}}); err != nil {
		t.Fatal(err)
	}
	if s.SelectionModel("Person") != s.LookupModel("Person") {
		t.Fatal("ordinary lookup changed without sums")
	}
	if err := s.RegisterSealed(&Sealed{Name: "Outcome", Variants: []Variant{{ID: 1, Name: "Ready"}}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Person", "Outcome", "Outcome.Ready"} {
		if s.SelectionModel(name) == nil {
			t.Fatal("missing selection directory", name)
		}
	}
	for _, name := range []string{"Outcome.Missing", "Missing.Ready"} {
		if s.SelectionModel(name) != nil {
			t.Fatal("unknown private case acquired a directory", name)
		}
	}
}

func TestSealedPublicPrepareRejectsOpenGraphs(t *testing.T) {
	for _, source := range []string{
		`{"models":{"Outcome":{"name":"Outcome","fields":[]}},"sealeds":{"Outcome":{"name":"Outcome","variants":[]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":1,"name":"Value","fields":[{"id":1,"name":"x","type":"Model","typeName":"Missing"}]}]}}}`,
		`{"sealeds":{"Outcome":{"name":"Outcome","variants":[{"id":1,"name":"Value","fields":[{"id":1,"name":"x","type":"Enum","typeName":"Missing"}]}]}}}`,
		`{"models":{"Record":{"name":"Record","fields":[{"id":1,"name":"x","type":"Sealed","typeName":"Missing"}]}}}`,
		`{"apis":{"write":{"id":1,"name":"write","params":[{"id":1,"name":"x","type":"Sealed","typeName":"Missing"}]}}}`,
	} {
		t.Run(source, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(source), &s); err != nil {
				t.Fatal(err)
			}
			if err := s.Prepare(); err == nil || !strings.Contains(err.Error(), "schema:") {
				t.Fatal("open graph passed startup", err)
			}
		})
	}
}

func TestSealedPublicListConversionPreservesDestination(t *testing.T) {
	s := canonicalSealedSchema(t)
	var valid codec.Encoder
	valid.WriteVarint(2)
	valid.WriteBytes([]byte{3, 2, 0, 0})
	valid.WriteBytes([]byte{1, 4, 0, 1, 14, 0})
	if out, err := s.SealedListToJSON([]byte("prefix:"), valid.Bytes(), "Outcome"); err != nil || string(out) != `prefix:[{"Pending":{}},{"Success":{"value":7}}]` {
		t.Fatal("list framing/order changed", string(out), err)
	}
	if out, err := s.SealedListToJSON(nil, []byte{0}, "EmptyOutcome"); err != nil || string(out) != "[]" {
		t.Fatal("uninhabited list cannot be empty", string(out), err)
	}
	for _, sample := range []struct {
		name string
		wire []byte
	}{
		{"Missing", []byte{0}}, {"Outcome", nil}, {"Outcome", []byte{2, 0}},
		{"Outcome", []byte{1, 2, 1}}, {"Outcome", []byte{1, 4, 99, 2, 0, 0}},
		{"Outcome", append(slices.Clone(valid.Bytes()), 0)},
	} {
		prefix := []byte("unchanged")
		out, err := s.SealedListToJSON(prefix, sample.wire, sample.name)
		if err == nil || !bytes.Equal(out, prefix) {
			t.Fatal("invalid list escaped or changed destination", sample, string(out), err)
		}
	}
}

func TestSealedPublicOutputDepthAndPayloadErrors(t *testing.T) {
	s := canonicalSealedSchema(t)
	for _, count := range []int{32, 33} {
		prefix := []byte("prefix:")
		out, err := s.SealedToJSON(prefix, sealedOutputDepthFrame(count), "Outcome")
		if count == 32 {
			if err != nil || string(out) != "prefix:"+sealedJSONDepthValue(count) {
				t.Fatal("valid binary object boundary", string(out), err)
			}
		} else if !errors.Is(err, codec.ErrModelDepth) || !bytes.Equal(out, prefix) {
			t.Fatal("deep binary value escaped or changed destination", err)
		}
	}
	for _, sample := range []struct {
		name string
		wire []byte
	}{{"Missing", []byte{3, 2, 0, 0}}, {"Outcome", []byte{1, 0}}} {
		prefix := []byte("unchanged")
		if out, err := s.SealedToJSON(prefix, sample.wire, sample.name); err == nil || !bytes.Equal(out, prefix) {
			t.Fatal("unknown type or malformed payload escaped", string(out), err)
		}
	}
}

func sealedOutputDepthFrame(count int) []byte {
	if count == 1 {
		return []byte{3, 2, 0, 0}
	}
	var payload, value codec.Encoder
	payload.WriteVarint(0)
	payload.WriteFieldHeader(1)
	payload.WritePresent()
	payload.WriteBytes(sealedOutputDepthFrame(count - 1))
	payload.WriteEnd()
	value.WriteVarint(4)
	value.WriteBytes(payload.Bytes())
	return value.Bytes()
}

func TestSealedPublicNestedFieldsAndSchemaRequirement(t *testing.T) {
	s := canonicalSealedSchema(t)
	fields := []Field{
		{ID: 1, Name: "value", Type: FieldSealed, TypeName: "Outcome", Nullable: true},
		{ID: 2, Name: "values", Type: FieldSealed, TypeName: "Outcome", IsList: true},
		{ID: 3, Name: "absent", Type: FieldSealed, TypeName: "Outcome", Nullable: true},
	}
	if err := s.RegisterType(&TypeDecl{Name: "Envelope", Fields: fields}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	raw := `{"value":{"Pending":{}},"values":[{"Pending":{}},{"Success":{"value":7}}],"absent":null}`
	wire, err := s.JSONToBinary("Envelope", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	model := s.LookupModel("Envelope")
	if out, err := BinaryToJSON(nil, wire, model, s); err != nil || string(out) != raw {
		t.Fatal("nested sealed fields lost presence/list framing", string(out), err)
	}
	prefix := []byte("unchanged")
	if out, err := BinaryToJSON(prefix, wire, model); err == nil || !bytes.Equal(out, prefix) {
		t.Fatal("present sealed field did not require schema", string(out), err)
	}
	if out, err := BinaryToJSON(prefix, []byte{0, 1, 1, 4, 3}, model, s); err == nil || !bytes.Equal(out, prefix) {
		t.Fatal("truncated sealed child escaped", string(out), err)
	}
	nullWire, err := s.JSONToBinary("Envelope", []byte(`{"value":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := BinaryToJSON(nil, nullWire, model); err != nil || string(out) != `{"value":null}` {
		t.Fatal("null incorrectly required child metadata", string(out), err)
	}
}

func TestSealedPublicDiscardedValuesKeepResourceChecks(t *testing.T) {
	s := canonicalSealedSchema(t)
	for _, discarded := range []string{
		`42`, `{"Future":{"nested":[]}}`, `{"Pending":42}`,
		`{"Pending":{"unknown":{"nested":[]}}}`,
	} {
		raw := `{"Nested":{"child":` + discarded + `,"child":null}}`
		wire, err := s.JSONToBinary("Outcome", []byte(raw))
		if err != nil || len(wire) == 0 {
			t.Fatal("discarded value received business decoding", discarded, err)
		}
	}
	raw := `{"Nested":{"child":{"Pending":{"unknown":` + strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129) + `}},"child":null}}`
	if wire, err := s.JSONToBinary("Outcome", []byte(raw)); err == nil || wire != nil {
		t.Fatal("discarded unknown field bypassed JSON depth limit", err)
	}
}

func TestSealedPublicDirectMetadataRejectsMissingChild(t *testing.T) {
	s := New()
	if err := s.RegisterType(&TypeDecl{Name: "Envelope", Fields: []Field{
		{ID: 1, Name: "value", Type: FieldSealed, TypeName: "Missing"},
	}}); err != nil {
		t.Fatal(err)
	}
	// Direct registered metadata is supported; unresolved children still fail.
	wire, err := s.JSONToBinary("Envelope", []byte(`{"value":{"Pending":{}}}`))
	if err == nil || !strings.Contains(err.Error(), `unknown sealed type "Missing"`) || wire != nil {
		t.Fatal("unresolved direct metadata escaped or produced partial output", wire, err)
	}
	if err := s.Prepare(); err == nil || !strings.Contains(err.Error(), `unknown sealed reference "Missing"`) {
		t.Fatal("startup accepted the same unresolved child", err)
	}
}
