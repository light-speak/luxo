package luvia

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func FuzzGroupedResponseEnvelope(f *testing.F) {
	for _, data := range [][]byte{
		nil, {0}, {1}, {1, 0}, {1, 1, 0x80}, {1, 0x80},
		resolvedResponseFixture(f, 1, 1, false), resolvedResponseFixture(f, 2, 2, true),
		codec.AppendVarint(nil, ^uint64(0)),
		codec.AppendVarint([]byte{1}, codec.MaxArrayElements+1),
	} {
		f.Add(data, false)
		f.Add(data, true)
	}
	// Large numeric counts are covered without allocating their claimed size.
	f.Fuzz(func(t *testing.T, data []byte, nullable bool) {
		if len(data) > 4096 {
			return
		}
		before := bytes.Clone(data)
		got := ParseGroupedResponse(data, nullable)
		want := referenceGroupedResponse(data, nullable)
		if !bytes.Equal(data, before) || (got == nil) != (want == nil) || len(got) != len(want) {
			t.Fatalf("envelope disagreement for %x (list=%v)", data, nullable)
		}
		for i := range got {
			if (got[i] == nil) != (want[i] == nil) || !bytes.Equal(got[i], want[i]) {
				t.Fatalf("blob %d changed for %x (list=%v)", i, data, nullable)
			}
		}
	})
}

func FuzzResolvedColumnTraversal(f *testing.F) {
	for _, data := range [][]byte{nil, {0, 0, 0}, {1, 0, 1, 2, 0}, {1, 0, 1, 0x80}, resolvedValueFixture(f, 2, false)} {
		f.Add(data)
	}
	registry := schema.New()
	registry.RegisterModel(&schema.Model{Name: "Child", Fields: []schema.Field{
		{ID: 1, Type: schema.FieldInt}, {ID: 2, Type: schema.FieldFloat}, {ID: 3, Type: schema.FieldBool},
		{ID: 4, Type: schema.FieldString}, {ID: 5, Type: schema.FieldUUID}, {ID: 6, Type: schema.FieldBool, Nullable: true},
		{ID: 7, Type: schema.FieldInt, IsList: true}, {ID: 8, Type: schema.FieldInt, Nullable: true},
	}})
	step := ExtendStep{ModelName: "Child", IsList: true}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			return
		}
		before := bytes.Clone(data)
		values := [][]byte{codec.AppendBytes(nil, data)}
		if got, want := ValidateResolvedFields(values, step, registry), validateResolvedPerValue(values, step, registry); got != want {
			t.Fatalf("column checks disagree for %x: %v/%v", data, got, want)
		}
		if !bytes.Equal(data, before) {
			t.Fatal("validation mutated input")
		}
	})
}

// The reference uses the standard library reader, independently of codec helpers.
func referenceGroupedResponse(data []byte, nullable bool) [][]byte {
	reader := bytes.NewReader(data)
	keys, err := binary.ReadUvarint(reader)
	if err != nil || keys > codec.MaxArrayElements || keys > uint64(reader.Len()) {
		return nil
	}
	result := make([][]byte, int(keys))
	for i := range result {
		start := len(data) - reader.Len()
		if nullable {
			present, err := reader.ReadByte()
			if err != nil || present > 1 {
				return nil
			}
			if present == 0 {
				result[i] = data[start : start+1]
				continue
			}
		}
		length, err := binary.ReadUvarint(reader)
		if err != nil || length == 0 || length > uint64(reader.Len()) {
			return nil
		}
		if _, err := reader.Seek(int64(length), io.SeekCurrent); err != nil {
			return nil
		}
		result[i] = data[start : len(data)-reader.Len()]
	}
	if reader.Len() != 0 {
		return nil
	}
	return result
}
