package luvia

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestSkipResponseNestedFrames(t *testing.T) {
	registry := schema.New()
	registry.RegisterType(&schema.TypeDecl{Name: "Child", Fields: []schema.Field{{ID: 1, Type: schema.FieldInt}}})
	for _, list := range []bool{false, true} {
		field := &schema.Field{Type: schema.FieldModel, TypeName: "Child", Relation: true, Nullable: true, IsList: list}
		body := codec.AppendSvarint([]byte{0, 1}, 9223372036854775807)
		body = append(body, 0)
		if list {
			body = append([]byte{1}, body...)
		}
		valid := codec.AppendBytes([]byte{1}, body)
		t.Run(fmt.Sprintf("list=%v", list), func(t *testing.T) {
			if !skipNestedRowField(codec.NewDecoder(valid), field, registry, 1) {
				t.Fatal("valid nested frame rejected")
			}
			for end := range len(valid) {
				if skipNestedRowField(codec.NewDecoder(valid[:end]), field, registry, 1) {
					t.Fatalf("truncated frame accepted at %d", end)
				}
			}
			for _, bad := range [][]byte{nil, body[:len(body)-1], append(bytes.Clone(body), 0), {0xff}} {
				if skipNestedRowField(codec.NewDecoder(codec.AppendBytes([]byte{1}, bad)), field, registry, 1) {
					t.Fatalf("invalid nested body accepted: %x", bad)
				}
			}
			allocs := testing.AllocsPerRun(100, func() {
				if !skipNestedRowField(codec.NewDecoder(valid), field, registry, 1) {
					t.Fatal("valid frame rejected during allocation check")
				}
			})
			if allocs != 0 {
				t.Fatalf("nested declaration lookup allocated %v times", allocs)
			}
		})
	}
}

func TestSkipResponseDepthBound(t *testing.T) {
	registry := schema.New()
	field := schema.Field{ID: 1, Type: schema.FieldModel, TypeName: "Node", Relation: true}
	registry.RegisterModel(&schema.Model{Name: "Node", Fields: []schema.Field{field}})
	body := []byte{0, 0}
	for depth := 1; depth <= maxKeyResponseDepth; depth++ {
		frame := codec.AppendBytes(nil, body)
		ok := skipNestedRowField(codec.NewDecoder(frame), &field, registry, 1)
		if ok != (depth < maxKeyResponseDepth) {
			t.Fatalf("depth %d accepted=%v", depth, ok)
		}
		body = append(codec.AppendBytes([]byte{0, 1}, body), 0)
	}
}

func TestSkipResponseScalarLists(t *testing.T) {
	for _, kind := range []schema.FieldType{schema.FieldInt, schema.FieldBool} {
		for _, columnar := range []bool{false, true} {
			field := &schema.Field{Type: kind, IsList: true, Nullable: true}
			for _, value := range [][]byte{{0}, {1, 0}, {1, 1, 1}, {1, 2, 0, 1}} {
				encoded := bytes.Clone(value)
				if columnar && value[0] != 0 {
					encoded = codec.AppendBytes([]byte{1}, value[1:])
				}
				dec := codec.NewDecoder(encoded)
				if !skipResponseField(dec, field, nil, columnar, 0) || dec.Finish() != nil {
					t.Fatalf("valid scalar list rejected: %v/%v/%x", kind, columnar, encoded)
				}
			}
			for _, bad := range [][]byte{nil, {2}, {1}, {1, 4, 0}, {1, 1, 0x80}} {
				if skipResponseField(codec.NewDecoder(bad), field, nil, columnar, 0) {
					t.Fatalf("bad scalar list accepted: %v/%v/%x", kind, columnar, bad)
				}
			}
		}
	}
	for _, bad := range [][]byte{{1, 2}, {1, 0xff}, {0x80}} {
		if skipScalarResponseList(codec.NewDecoder(bad), &schema.Field{Type: schema.FieldBool}) {
			t.Fatalf("invalid bool array accepted: %x", bad)
		}
	}
}

func TestSkipResponseBooleanAndNestedColumnArray(t *testing.T) {
	field := &schema.Field{Type: schema.FieldBool, Nullable: true}
	for _, data := range [][]byte{{0}, {1, 0}, {1, 1}} {
		if !skipResponseField(codec.NewDecoder(data), field, nil, false, 0) {
			t.Fatalf("valid nullable boolean rejected: %x", data)
		}
	}
	for _, data := range [][]byte{nil, {2}, {1}, {1, 2}} {
		if skipResponseField(codec.NewDecoder(data), field, nil, false, 0) {
			t.Fatalf("invalid nullable boolean accepted: %x", data)
		}
	}
	fields := []schema.Field{{ID: 1, Type: schema.FieldInt, IsList: true}}
	valid := codec.AppendBytes([]byte{1, 0, 1}, []byte{1, 2})
	if !skipModelResponse(append(valid, 0), fields, nil, true, 1) {
		t.Fatal("columnar scalar-array child rejected")
	}
	bad := codec.AppendBytes([]byte{1, 0, 1}, []byte{1, 2, 0})
	if skipModelResponse(append(bad, 0), fields, nil, true, 1) {
		t.Fatal("trailing scalar-array data accepted")
	}
}

func TestSkipResponseColumnarCountUsesColumnLimit(t *testing.T) {
	for _, count := range []uint64{codec.MaxArrayElements + 1, codec.MaxColumnarRecords, codec.MaxColumnarRecords + 1} {
		body := append(codec.AppendVarint(nil, count), 0, 1)
		body = append(body, make([]byte, count)...)
		body = append(body, 0)
		fields := []schema.Field{{ID: 1, Type: schema.FieldBool}}
		if ok := skipModelResponse(body, fields, nil, true, 1); ok != (count <= codec.MaxColumnarRecords) {
			t.Fatalf("columnar count %d accepted=%v", count, ok)
		}
	}
}

func TestSkipResponseColumnarBodyValidation(t *testing.T) {
	fields := []schema.Field{{ID: 1, Type: schema.FieldInt}, {ID: 2, Type: schema.FieldModel, TypeName: "Child", Relation: true}}
	registry := schema.New()
	registry.RegisterModel(&schema.Model{Name: "Child", Fields: fields[:1]})
	valid := codec.AppendBytes([]byte{1, 0, 1, 2, 2}, []byte{0, 1, 2, 0})
	valid = append(valid, 0)
	if !skipModelResponse(valid, fields, registry, true, 0) {
		t.Fatal("valid scalar and relation columns rejected")
	}
	for end := 0; end < len(valid); end++ {
		if skipModelResponse(valid[:end], fields, registry, true, 0) {
			t.Fatalf("accepted truncation at %d", end)
		}
	}
	for _, bad := range [][]byte{
		append(bytes.Clone(valid), 0),
		{1, 0, 1, 0x80},
		{1, 0, 99, 2, 0},
		append(codec.AppendBytes([]byte{1, 0, 2}, []byte{0, 1, 0x80}), 0),
		append(codec.AppendBytes([]byte{1, 0, 2}, []byte{0, 99, 2, 0}), 0),
	} {
		if skipModelResponse(bad, fields, registry, true, 0) {
			t.Fatalf("accepted malformed columns: %x", bad)
		}
	}
}
