package luvia

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

var primaryKeysBenchmarkSink BinaryKeys

func BenchmarkPrimaryKeys(b *testing.B) {
	for _, test := range []struct {
		name         string
		rows, fields int
		columnar     bool
	}{
		{"row-small", 1, 2, false}, {"row-wide", 1, 16, false},
		{"column-small", 1, 2, true}, {"column-batch", 16, 16, true},
		{"column-many", 128, 16, true},
	} {
		for _, keyLast := range []bool{false, true} {
			data, model, registry := primaryKeysBenchmarkFixture(test.rows, test.fields, test.columnar, keyLast)
			for _, baseline := range primaryKeysBenchmarkOrder() {
				name := fmt.Sprintf("%s/keyLast=%v/baseline=%v", test.name, keyLast, baseline)
				b.Run(name, func(b *testing.B) {
					benchmarkPrimaryKeyExtraction(b, data, model, registry, test.columnar, baseline)
				})
			}
		}
	}
}

func benchmarkPrimaryKeyExtraction(b *testing.B, data []byte, model *schema.Model, registry *schema.Schema, columnar, baseline bool) {
	key := model.PrimaryKeyField()
	extract := func(old bool) (BinaryKeys, bool) {
		if columnar {
			if old {
				return ExtractPrimaryKeyColumnBaseline(data, key, model)
			}
			return ExtractPrimaryKeyColumn(data, key, model)
		}
		if old {
			return ExtractPrimaryKeysBaseline(data, key, model, registry)
		}
		return ExtractPrimaryKeys(data, key, model, registry)
	}
	before, oldOK := extract(true)
	after, newOK := extract(false)
	if !oldOK || !newOK || before.count != after.count || !bytes.Equal(before.values, after.values) {
		b.Fatal("benchmark implementations disagree on exact key bytes")
	}
	b.ReportAllocs()
	for b.Loop() {
		primaryKeysBenchmarkSink, _ = extract(baseline)
	}
}

func primaryKeysBenchmarkOrder() []bool {
	if os.Getenv("LUXO_KEYS_BENCH_REVERSE") == "1" {
		return []bool{false, true}
	}
	return []bool{true, false}
}

func primaryKeysBenchmarkFixture(rows, fields int, columnar, keyLast bool) ([]byte, *schema.Model, *schema.Schema) {
	registry := schema.New()
	model := &schema.Model{Name: "Parent"}
	var order []int
	for id := 1; id <= fields; id++ {
		model.Fields = append(model.Fields, schema.Field{ID: id, Name: fmt.Sprintf("field%d", id), Type: schema.FieldInt, PrimaryKey: id == 1})
		order = append(order, id)
	}
	registry.RegisterModel(model)
	if keyLast {
		order = append(order[1:], 1)
	}
	data := []byte{0}
	if columnar {
		data = codec.AppendVarint(nil, uint64(rows))
		data = append(data, 0)
	}
	for _, id := range order {
		data = codec.AppendVarint(data, uint64(id))
		for row := range rows {
			value := int64(42)
			if id == 1 {
				value = 9223372036854775807 - int64(row)
			}
			data = codec.AppendSvarint(data, value)
		}
	}
	return append(data, 0), model, registry
}
