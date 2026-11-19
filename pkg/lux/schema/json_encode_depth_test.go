package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func jsonDepthSchema(asType bool) *Schema {
	s := New()
	fields := []Field{
		{ID: 1, Name: "value", Type: FieldInt},
		{ID: 2, Name: "child", Type: FieldModel, TypeName: "Node", Nullable: true, Relation: true},
		{ID: 3, Name: "children", Type: FieldModel, TypeName: "Node", IsList: true, Nullable: true, Relation: true},
		{ID: 4, Name: "payload", Type: FieldJSON, Nullable: true},
		{ID: 5, Name: "jsons", Type: FieldJSON, IsList: true, Nullable: true},
	}
	if asType {
		s.RegisterType(&TypeDecl{Name: "Node", Fields: fields})
	} else {
		s.RegisterModel(&Model{Name: "Node", Fields: fields})
	}
	return s
}

func jsonDepthInput(depth int, lists bool, generic int) string {
	raw := `{"value":1,"payload":` + strings.Repeat("[", generic) + "0" + strings.Repeat("]", generic) + "}"
	for level := 1; level < depth; level++ {
		if lists {
			raw = `{"value":1,"children":[` + raw + "]}"
		} else {
			raw = `{"value":1,"child":` + raw + "}"
		}
	}
	return raw
}

func TestJSONToBinaryIndependentDepthBudgets(t *testing.T) {
	for _, asType := range []bool{false, true} {
		for _, lists := range []bool{false, true} {
			for _, depth := range []int{1, 63, 64, 65, 128} {
				for _, generic := range []int{0, 128} {
					t.Run(fmt.Sprintf("type%t/lists%t/model%d/json%d", asType, lists, depth, generic), func(t *testing.T) {
						wire, err := jsonDepthSchema(asType).JSONToBinary("Node", json.RawMessage(jsonDepthInput(depth, lists, generic)))
						if depth <= 64 {
							if err != nil || len(wire) == 0 {
								t.Fatal("legal independent budgets rejected", err)
							}
						} else if !errors.Is(err, codec.ErrModelDepth) || wire != nil {
							t.Fatal("model limit64 not enforced atomically", err)
						}
					})
				}
			}
		}
	}
}

func TestJSONToBinaryAllJSONOccurrencesBounded(t *testing.T) {
	for _, asType := range []bool{false, true} {
		for _, field := range []string{"payload", "jsons", "value"} {
			for _, depth := range []int{128, 129} {
				for _, duplicate := range []bool{false, true} {
					if field == "value" && !duplicate {
						continue // Only overwritten scalar shapes are valid business input.
					}
					t.Run(fmt.Sprintf("type%t/%s/json%d/duplicate%t", asType, field, depth, duplicate), func(t *testing.T) {
						raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
						last := "0"
						if field == "jsons" {
							raw = "[" + raw + "]"
							last = "[]"
						}
						body := `{"` + field + `":` + raw
						if duplicate {
							body += `,"` + field + `":` + last
						}
						wire, err := jsonDepthSchema(asType).JSONToBinary("Node", json.RawMessage(body+"}"))
						if (err == nil) != (depth == 128) || err != nil && wire != nil {
							t.Fatal("JSON occurrence budget violated", err)
						}
					})
				}
			}
		}
	}
}

func TestJSONToBinaryDiscardedModelResources(t *testing.T) {
	s := jsonDepthSchema(false)
	for _, depth := range []int{64, 65} {
		for _, lists := range []bool{false, true} {
			child := jsonDepthInput(depth-1, lists, 128)
			field, last := "child", "null"
			if lists {
				field, child, last = "children", "["+child+"]", "[]"
			}
			wire, err := s.JSONToBinary("Node", json.RawMessage(`{"`+field+`":`+child+`,"`+field+`":`+last+"}"))
			if (err == nil) != (depth == 64) || err != nil && wire != nil {
				t.Errorf("discarded model%d list%t: %v", depth, lists, err)
			}
		}
	}
	for _, raw := range []string{`{"child":{},"child":null}`, `{"value":"ignored","value":1}`, `{"children":[{}],"children":[]}`} {
		if _, err := s.JSONToBinary("Node", json.RawMessage(raw)); err != nil {
			t.Fatal("discarded business validation changed last wins", err)
		}
	}
}

func schemaDepthResponse(depth int, lists bool, generic int) []byte {
	enc := codec.NewEncoder(nil)
	enc.WriteVarint(0)
	enc.WriteFieldInt(1, 1)
	if depth > 1 {
		child := schemaDepthResponse(depth-1, lists, generic)
		field := 2
		if lists {
			field, child = 3, append([]byte{1}, child...)
		}
		enc.WriteFieldHeader(field)
		enc.WritePresent()
		enc.WriteBytes(child)
	} else {
		enc.WriteFieldHeader(4)
		enc.WritePresent()
		enc.WriteString(strings.Repeat("[", generic) + "0" + strings.Repeat("]", generic))
	}
	enc.WriteEnd()
	return enc.Bytes()
}

func TestBinaryJSONConvertersIndependentModelDepth(t *testing.T) {
	for _, asType := range []bool{false, true} {
		for _, lists := range []bool{false, true} {
			for _, depth := range []int{1, 63, 64, 65, 128} {
				s := jsonDepthSchema(asType)
				model := s.LookupModel("Node")
				row := schemaDepthResponse(depth, lists, 128)
				column := append([]byte{1}, row...)
				page := append([]byte(nil), column...)
				for range 3 {
					page = codec.AppendSvarint(page, 1)
				}
				for _, entry := range []struct {
					name string
					read func([]byte) ([]byte, error)
				}{
					{"row", func(dst []byte) ([]byte, error) { return BinaryToJSON(dst, row, model, s) }},
					{"column", func(dst []byte) ([]byte, error) { return BinaryListToJSON(dst, column, model, s) }},
					{"page", func(dst []byte) ([]byte, error) { return BinaryPaginatedListToJSON(dst, page, model, s) }},
				} {
					t.Run(fmt.Sprintf("type%t/lists%t/model%d/%s", asType, lists, depth, entry.name), func(t *testing.T) {
						got, err := entry.read([]byte("prefix"))
						if depth <= 64 {
							if err != nil || len(got) <= len("prefix") || !json.Valid(got[len("prefix"):]) {
								t.Fatal("legal independent model/JSON budget rejected", err)
							}
						} else if !errors.Is(err, codec.ErrModelDepth) || string(got) != "prefix" {
							t.Fatal("structured response limit64 not enforced atomically", err)
						}
					})
				}
			}
		}
	}
}

func TestJSONToBinaryDiscardedMetadataAndShapes(t *testing.T) {
	s := jsonDepthSchema(false)
	s.Models["Node"].Fields[1].TypeName = "Unregistered"
	for _, raw := range []string{
		`{"child":{"anything":1},"child":null}`,
		`{"value":[],"value":1}`,
		`{"children":{},"children":[]}`,
	} {
		if _, err := s.JSONToBinary("Node", []byte(raw)); err != nil {
			t.Fatal("discarded value applied business validation", err)
		}
	}
	s = jsonDepthSchema(false)
	if _, err := s.JSONToBinary("Node", []byte(`{"child":{"unknown":0},"child":null}`)); err != nil {
		t.Fatal("discarded unknown member applied business validation", err)
	}
	for _, leaf := range []string{`{"child":null}`, `{"children":[]}`, `{}`} {
		for _, depth := range []int{64, 65} {
			raw := leaf
			for level := 1; level < depth; level++ {
				raw = `{"child":` + raw + `}`
			}
			_, err := s.JSONToBinary("Node", []byte(raw))
			if (err == nil) != (depth == 64) {
				t.Fatalf("empty terminal model%d %s: %v", depth, leaf, err)
			}
		}
	}
	// Escaped duplicates still name the same canonical field; numeric precision is unchanged.
	wire, err := s.JSONToBinary("Node", []byte(`{"value":"ignored","\u0076alue":9007199254740993}`))
	dec := codec.NewDecoder(wire)
	dec.SkipArenaHeader()
	if err != nil || !dec.NextField() || dec.ReadInt() != 9007199254740993 || dec.NextField() || dec.Finish() != nil {
		t.Fatal("canonical duplicate or exact integer changed", err)
	}
}

func TestBinaryJSONConvertersDiscardedResources(t *testing.T) {
	s := jsonDepthSchema(false)
	for _, field := range []int{2, 3, 4, 5} {
		for _, over := range []bool{false, true} {
			t.Run(fmt.Sprintf("field%d/over%t", field, over), func(t *testing.T) {
				var blob []byte
				if field <= 3 {
					depth := 63
					if over {
						depth++
					}
					blob = schemaDepthResponse(depth, field == 3, 128)
					if field == 3 {
						blob = append([]byte{1}, blob...)
					}
				} else {
					depth := 128
					if over {
						depth++
					}
					blob = []byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth))
					if field == 5 {
						e := codec.NewEncoder(nil)
						e.WriteVarint(1)
						e.WriteBytes(blob)
						blob = e.Bytes()
					}
				}
				e := codec.NewEncoder(nil)
				e.WriteVarint(0)
				e.WriteFieldHeader(field)
				e.WritePresent()
				if field == 5 {
					// Row scalar lists are inline; columnar cells have an outer byte frame.
					e = codec.NewEncoder(append(e.Bytes(), blob...))
				} else {
					e.WriteBytes(blob)
				}
				e.WriteFieldHeader(field)
				e.WriteNull()
				e.WriteEnd()
				row := e.Bytes()
				column := append([]byte{1}, row...)
				if field == 5 {
					c := codec.NewEncoder(nil)
					c.WriteVarint(1)
					c.WriteVarint(0)
					c.WriteFieldHeader(field)
					c.WritePresent()
					c.WriteBytes(blob)
					c.WriteFieldHeader(field)
					c.WriteNull()
					c.WriteEnd()
					column = c.Bytes()
				}
				page := append([]byte(nil), column...)
				for range 3 {
					page = codec.AppendSvarint(page, 1)
				}
				for _, data := range []struct {
					wire []byte
					read func([]byte, []byte, *Model, ...*Schema) ([]byte, error)
				}{{row, BinaryToJSON}, {column, BinaryListToJSON}, {page, BinaryPaginatedListToJSON}} {
					got, err := data.read([]byte("prefix"), data.wire, s.Models["Node"], s)
					if over {
						if err == nil || string(got) != "prefix" || field <= 3 && !errors.Is(err, codec.ErrModelDepth) {
							t.Fatal("overwritten resource hidden or partial output exposed", err)
						}
					} else if err != nil || !json.Valid(got[len("prefix"):]) {
						t.Fatal("legal old value rejected", err)
					}
				}
			})
		}
	}
}

func TestBinaryJSONConvertersEmptyAtDepthLimit(t *testing.T) {
	s := jsonDepthSchema(false)
	for _, field := range []int{2, 3} {
		for _, present := range []bool{false, true} {
			for _, depth := range []int{64, 65} {
				e := codec.NewEncoder(nil)
				e.WriteVarint(0)
				e.WriteFieldHeader(field)
				if present {
					e.WritePresent()
					empty := []byte{0, 0}
					if field == 3 {
						empty = []byte{0, 0, 0}
					}
					e.WriteBytes(empty) // Empty object or zero-count columnar list.
				} else {
					e.WriteNull()
				}
				e.WriteEnd()
				wire := e.Bytes()
				for level := 1; level < depth; level++ {
					e := codec.NewEncoder(nil)
					e.WriteVarint(0)
					e.WriteFieldHeader(2)
					e.WritePresent()
					e.WriteBytes(wire)
					e.WriteEnd()
					wire = e.Bytes()
				}
				got, err := BinaryToJSON([]byte("prefix"), wire, s.Models["Node"], s)
				wantError := depth > 64 || field == 2 && present
				if wantError {
					if !errors.Is(err, codec.ErrModelDepth) || string(got) != "prefix" {
						t.Fatalf("field%d/present%t/model%d: %v", field, present, depth, err)
					}
				} else if err != nil || !json.Valid(got[len("prefix"):]) {
					t.Fatal("null or empty list charged as an object", err)
				}
			}
		}
	}
}
