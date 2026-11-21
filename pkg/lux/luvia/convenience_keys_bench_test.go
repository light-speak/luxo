package luvia

import (
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"slices"
	"testing"
)

// Exact pre-fix bodies are retained only for valid-input local comparisons.
func extractIDBeforeParentValidation(primary []byte, idFieldID int, fieldTypes map[int]codec.FieldSkipType) (int64, bool) {
	if len(primary) == 0 {
		return 0, false
	}
	dec := codec.NewDecoder(primary)
	dec.SkipArenaHeader()
	for dec.NextField() {
		fid := dec.FieldID()
		if fid == idFieldID {
			return dec.ReadInt(), dec.Err() == nil
		}
		// Skip this field based on its known type
		st, ok := fieldTypes[fid]
		if !ok {
			return 0, false // unknown field, can't skip
		}
		skipField(dec, st)
		if dec.Err() != nil {
			return 0, false
		}
	}
	return 0, false
}

func extractIDColumnBeforeParentValidation(data []byte, idFieldID int, model *schema.Model) []int64 {
	r := codec.NewColumnarReader(data)
	if r.Err() != nil || r.Count() == 0 {
		return nil
	}
	for r.NextColumn() {
		if r.FieldID() == idFieldID {
			return r.ReadColumnInt()
		}
		// Skip this column using schema type info
		f := model.FieldByID(r.FieldID())
		if f == nil {
			return nil // unknown column, can't skip
		}
		skipColumnarColumn(r, f)
		if r.Err() != nil {
			return nil
		}
	}
	return nil
}

var convenienceRowKey int64
var convenienceRowOK bool
var convenienceColumnKeys []int64

func BenchmarkConvenienceKeyParentValidation(b *testing.B) {
	for _, sample := range []struct {
		name         string
		rows, fields int
		columnar     bool
	}{
		{"row-small", 1, 2, false}, {"row-wide", 1, 16, false},
		{"column-small", 1, 2, true}, {"column-batch", 16, 16, true},
		{"column-many", 128, 16, true},
	} {
		for _, keyLast := range []bool{false, true} {
			data, model, _ := primaryKeysBenchmarkFixture(sample.rows, sample.fields, sample.columnar, keyLast)
			for _, before := range []bool{true, false} {
				version := "current"
				if before {
					version = "before"
				}
				b.Run(fmt.Sprintf("%s/keyLast=%v/%s", sample.name, keyLast, version), func(b *testing.B) {
					benchmarkConvenienceKeys(b, data, model, sample.columnar, before)
				})
			}
		}
	}
}

func benchmarkConvenienceKeys(b *testing.B, data []byte, model *schema.Model, columnar, before bool) {
	if columnar {
		old := extractIDColumnBeforeParentValidation(data, 1, model)
		current := ExtractIDColumn(data, 1, model)
		if len(old) == 0 || !slices.Equal(old, current) {
			b.Fatal("column implementations disagree on valid input")
		}
		b.ReportAllocs()
		for b.Loop() {
			if before {
				convenienceColumnKeys = extractIDColumnBeforeParentValidation(data, 1, model)
			} else {
				convenienceColumnKeys = ExtractIDColumn(data, 1, model)
			}
		}
		return
	}
	types := make(map[int]codec.FieldSkipType, len(model.Fields))
	for _, field := range model.Fields {
		types[field.ID] = codec.SkipVarint
	}
	old, oldOK := extractIDBeforeParentValidation(data, 1, types)
	current, ok := ExtractID(data, 1, types)
	if !oldOK || !ok || old != current {
		b.Fatal("row implementations disagree on valid input")
	}
	b.ReportAllocs()
	for b.Loop() {
		if before {
			convenienceRowKey, convenienceRowOK = extractIDBeforeParentValidation(data, 1, types)
		} else {
			convenienceRowKey, convenienceRowOK = ExtractID(data, 1, types)
		}
	}
}
