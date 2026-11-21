package schema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func TestJSONErrorContextCompatibility(t *testing.T) {
	s := jsonDepthSchema(false)
	for _, lists := range []bool{false, true} {
		_, err := s.JSONToBinary("Node", []byte(jsonDepthInput(65, lists, 0)))
		prefix := "Node.child: "
		if lists {
			prefix = "Node.children: element 0: "
		}
		want := strings.Repeat(prefix, 64) + codec.ErrModelDepth.Error()
		if err == nil || err.Error() != want || !errors.Is(err, codec.ErrModelDepth) {
			t.Fatalf("changed model-depth error: %v", err)
		}
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				for range 8 {
					if err.Error() != want {
						t.Error("concurrent error text changed")
					}
				}
			})
		}
		workers.Wait()
	}
	_, err := s.JSONToBinary("Node", []byte(`{"value":9223372036854775808}`))
	cause := new(strconv.NumError)
	if !errors.As(err, &cause) || !errors.Is(err, strconv.ErrRange) || cause.Num != "9223372036854775808" {
		t.Fatal("typed numeric cause lost", err)
	}
	if want := `Node.value: expected integer: strconv.ParseInt: parsing "9223372036854775808": value out of range`; err.Error() != want {
		t.Fatal("numeric context changed", err)
	}
	_, err = s.JSONToBinary("Node", []byte(`{"child":{"value":9223372036854775808}}`))
	if !errors.As(err, &cause) || !errors.Is(err, strconv.ErrRange) || !strings.HasPrefix(err.Error(), "Node.child: Node.value: expected integer:") {
		t.Fatal("nested diagnostic lost its typed numeric cause", err)
	}
	s = newJSONEncodingSchema()
	for _, sample := range []struct{ raw, want string }{
		{`{"name":1}`, `Input: name must be a string`},
		{`{"address":{"city":1}}`, `Input.address: Address: city must be a string`},
		{`{"addresses":[{}, {"city":1}]}`, `Input.addresses: element 1: Address: city must be a string`},
		{`{"ids":[` + strings.Repeat("0,", 127) + `null]}`, `Input.ids: element 127 must not be null`},
		{`{"ids":[` + strings.Repeat("0,", 127) + `"x"]}`, `Input.ids: element 127: expected integer: expected a signed 64-bit JSON integer`},
	} {
		_, err := s.JSONToBinary("Input", []byte(sample.raw))
		if err == nil || err.Error() != sample.want {
			t.Fatalf("%s: got %v want %s", sample.raw, err, sample.want)
		}
	}
	// Different metadata names and an array index must remain visible verbatim.
	s = New()
	if err := s.RegisterModel(&Model{Name: "根.型", Fields: []Field{{ID: 1, Name: "配列", Type: FieldModel, TypeName: "子", IsList: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterType(&TypeDecl{Name: "子", Fields: []Field{{ID: 1, Name: "値", Type: FieldInt}}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.JSONToBinary("根.型", []byte(`{"配列":[{"値":null}]}`))
	if err == nil || err.Error() != fmt.Sprint("根.型.配列: element 0: 子.値: must not be null") {
		t.Fatal("metadata context changed", err)
	}
}

type schemaErrorInput struct {
	name string
	raw  []byte
}

func schemaErrorInputs() []schemaErrorInput {
	deepBusiness := `{"value":"x"}`
	for range 63 {
		deepBusiness = `{"child":` + deepBusiness + `}`
	}
	return []schemaErrorInput{
		{"model65", []byte(jsonDepthInput(65, false, 0))},
		{"listModel65", []byte(jsonDepthInput(65, true, 0))},
		{"deepJSON129", []byte(jsonDepthInput(64, false, 129))},
		{"json129", []byte(jsonDepthInput(1, false, 129))},
		{"oldModel65", []byte(`{"child":` + jsonDepthInput(64, true, 128) + `,"child":null}`)},
		{"business", []byte(`{"value":"x"}`)},
		{"deepBusiness", []byte(deepBusiness)},
		{"syntax", []byte(`{"value":`)},
	}
}

func TestJSONErrorRepeatedReadsPreserveAllocationBehavior(t *testing.T) {
	for _, lists := range []bool{false, true} {
		_, err := jsonDepthSchema(false).JSONToBinary("Node", []byte(jsonDepthInput(65, lists, 0)))
		if err == nil {
			t.Fatal("missing error")
		}
		want := err.Error()
		allocations := testing.AllocsPerRun(16, func() {
			if err.Error() != want {
				t.Fatal("message changed")
			}
		})
		if allocations != 0 {
			t.Fatalf("repeated Error() added allocations: %g", allocations)
		}
	}
}
