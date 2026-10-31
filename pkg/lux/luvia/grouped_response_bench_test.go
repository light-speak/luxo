package luvia

import (
	"math"
	"strconv"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var groupedBenchmarkResult [][]byte

// Historical code is retained only for reproducibility, not wire compatibility.
// Its concatenated-row list is not equivalent to the current canonical payload.
func BenchmarkHistoricalGroupedResponse(b *testing.B) {
	response := groupedResponseFixture(16, 2)
	b.ReportAllocs()
	for b.Loop() {
		groupedBenchmarkResult = parseGroupedResponseBaseline(response, true)
	}
}

var resolvedValidationResult bool

type resolvedValidationCase struct {
	name         string
	rows, fields int
	list         bool
}

var resolvedValidationCases = []resolvedValidationCase{
	{"object-2", 1, 2, false}, {"list-small-128x16", 128, 16, true}, {"list-wide-128x16", 128, 16, true},
}

func resolvedValidationFixture(t testing.TB, test resolvedValidationCase) ([][]byte, ExtendStep, *schema.Schema) {
	t.Helper()
	registry := schema.New()
	model := &schema.Model{Name: "Child"}
	for id := 1; id <= test.fields; id++ {
		model.Fields = append(model.Fields, schema.Field{ID: id, Name: "field" + strconv.Itoa(id), Type: schema.FieldInt})
	}
	if err := registry.RegisterModel(model); err != nil {
		t.Fatal(err)
	}
	body := []byte{0}
	if test.list {
		w := &codec.ColumnarWriter{}
		w.SetCount(test.rows)
		values := make([]int64, test.rows)
		for i := range values {
			values[i] = math.MaxInt64 - int64(i)
			if test.name == "list-small-128x16" {
				values[i] = int64(i % 32)
			}
		}
		for id := 1; id <= test.fields; id++ {
			w.WriteColumnInt(id, values)
		}
		body = testutil.Bytes(t)(w.Bytes())
	} else {
		for id := 1; id <= test.fields; id++ {
			body = codec.AppendSvarint(codec.AppendVarint(body, uint64(id)), math.MaxInt64)
		}
		body = append(body, 0)
	}
	return [][]byte{codec.AppendBytes(nil, body)}, ExtendStep{ModelName: "Child", IsList: test.list}, registry
}

func TestResolvedValidationBenchmarkFixtures(t *testing.T) {
	for _, test := range resolvedValidationCases {
		t.Run(test.name, func(t *testing.T) {
			values, step, registry := resolvedValidationFixture(t, test)
			for _, validate := range []func([][]byte, ExtendStep, *schema.Schema) bool{
				validateResolvedPerValue, ValidateResolvedFields,
			} {
				if !validate(values, step, registry) {
					t.Fatal("invalid benchmark fixture")
				}
				truncated := [][]byte{values[0][:len(values[0])-1]}
				if validate(truncated, step, registry) {
					t.Fatal("truncated benchmark fixture was accepted")
				}
			}
		})
	}
}

func BenchmarkValidateResolvedFields(b *testing.B) {
	for _, test := range resolvedValidationCases {
		values, step, registry := resolvedValidationFixture(b, test)
		for _, implementation := range []struct {
			name     string
			validate func([][]byte, ExtendStep, *schema.Schema) bool
		}{
			{"per-value", validateResolvedPerValue}, {"column-check", ValidateResolvedFields},
		} {
			b.Run(test.name+"/"+implementation.name, func(b *testing.B) {
				if !implementation.validate(values, step, registry) {
					b.Fatal("invalid benchmark fixture")
				}
				b.ReportAllocs()
				for b.Loop() {
					resolvedValidationResult = implementation.validate(values, step, registry)
				}
			})
		}
	}
}

func BenchmarkGroupedResponse(b *testing.B) {
	for _, test := range []struct {
		name        string
		keys, items int
	}{
		{"empty", 1, 0}, {"one", 1, 1}, {"batch", 16, 2}, {"wide", 1, 128},
	} {
		response := resolvedResponseFixture(b, test.keys, test.items, false)
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				groupedBenchmarkResult = ParseGroupedResponse(response, false)
			}
		})
	}
}
func groupedResponseItem() []byte {
	item := codec.AppendSvarint([]byte{0, 1}, math.MaxInt64)
	item = codec.AppendBytes(append(item, 2), []byte("精度 café"))
	return append(item, 0)
}

func groupedResponseFixture(keys, items int) []byte {
	response := codec.AppendVarint(nil, uint64(keys))
	item := groupedResponseItem()
	for range keys {
		response = codec.AppendVarint(response, uint64(items))
		for range items {
			response = codec.AppendBytes(response, item)
		}
	}
	return response
}

// This frozen pre-fix implementation is used only with valid benchmark inputs.
// It is intentionally not safe for malformed or unbounded wire counts.
func parseGroupedResponseBaseline(resp []byte, isList bool) [][]byte {
	if len(resp) == 0 {
		return nil
	}
	keyCount, n := codec.ReadVarint(resp, 0)
	if n <= 0 {
		return nil
	}
	off := n

	blobs := make([][]byte, keyCount)
	for i := 0; i < int(keyCount); i++ {
		itemCount, cn := codec.ReadVarint(resp, off)
		if cn <= 0 {
			return blobs
		}
		off += cn

		if isList {
			// Collect all items for this key into a list blob:
			// [count varint][item1 raw bytes][item2 raw bytes]...
			var listBuf []byte
			listBuf = codec.AppendArrayHeader(listBuf, int(itemCount))
			for j := 0; j < int(itemCount); j++ {
				item, rn := codec.ReadBytes(resp, off)
				if rn == 0 {
					return blobs
				}
				off += rn
				listBuf = append(listBuf, item...)
			}
			blobs[i] = listBuf
		} else {
			if itemCount == 0 {
				blobs[i] = nil
			} else {
				item, rn := codec.ReadBytes(resp, off)
				if rn == 0 {
					return blobs
				}
				off += rn
				blobs[i] = item
				// Skip remaining items (shouldn't exist for single field)
				for j := 1; j < int(itemCount); j++ {
					_, rn := codec.ReadBytes(resp, off)
					if rn == 0 {
						return blobs
					}
					off += rn
				}
			}
		}
	}
	return blobs
}

// Frozen per-value traversal: identical canonical wire, only primitive-column traversal differs.
func validateResolvedPerValue(blobs [][]byte, step ExtendStep, registry *schema.Schema) bool {
	if registry == nil {
		return false
	}
	fields, ok := nestedResponseFields(registry, step.ModelName)
	if !ok {
		return false
	}
	for _, blob := range blobs {
		dec := codec.NewDecoder(blob)
		if step.Nullable && !dec.ReadBool() {
			if dec.Finish() != nil {
				return false
			}
			continue
		}
		body := dec.ReadBytes()
		if dec.Finish() != nil || !skipModelResponsePerValue(body, fields, registry, step.IsList, 0) {
			return false
		}
	}
	return true
}

func skipModelResponsePerValue(body []byte, fields []schema.Field, registry *schema.Schema, columnar bool, depth int) bool {
	count := 1
	if columnar {
		header := codec.NewColumnarReader(body)
		if header.Err() != nil {
			return false
		}
		count = header.Count()
		body = body[header.Offset():]
	}
	dec := codec.NewDecoder(body)
	if !columnar {
		dec.ReadArenaSize()
	}
	for dec.NextField() {
		field := responseFieldByID(fields, dec.FieldID())
		if field == nil {
			return false
		}
		for range count {
			if !skipResponseField(dec, field, registry, columnar, depth) {
				return false
			}
		}
	}
	return dec.Finish() == nil
}
